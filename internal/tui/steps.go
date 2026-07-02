package tui

import (
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jandro-es/axon/internal/ui"
)

// StepStatus is a live step state. The terminal states mirror
// core.StepStatus values so command code can cast directly.
type StepStatus string

const (
	StatusRunning StepStatus = "running"
	StatusDone    StepStatus = "done"
	StatusAlready StepStatus = "already"
	StatusWarn    StepStatus = "warn"
	StatusFailed  StepStatus = "failed"
)

// Steps renders a live, ordered step list on a TTY. On a non-interactive
// writer it forwards every TERMINAL state to the plain printer instead (never
// "running"), so headless output is exactly what commands print today.
type Steps struct {
	out   io.Writer
	title string
	plain func(name, detail string, st StepStatus)

	live bool
	prog *tea.Program
	done chan struct{}
	mu   sync.Mutex
}

// NewSteps builds a step list writing to out. plain may be nil (terminal
// states are then silently dropped in non-interactive mode — callers that
// need plain output pass their existing printer).
func NewSteps(out io.Writer, title string, plain func(name, detail string, st StepStatus)) *Steps {
	return &Steps{out: out, title: title, plain: plain, live: Interactive(out)}
}

// Start begins rendering. No-op in plain mode.
func (s *Steps) Start() {
	if !s.live {
		return
	}
	m := newStepsModel(s.title)
	s.prog = tea.NewProgram(m, tea.WithOutput(s.out), tea.WithoutSignalHandler())
	s.done = make(chan struct{})
	go func() {
		defer close(s.done)
		_, _ = s.prog.Run()
	}()
}

// Set upserts a step row (keyed by name).
func (s *Steps) Set(name, detail string, st StepStatus) {
	if s.live {
		s.prog.Send(stepMsg{name: name, detail: detail, st: st})
		return
	}
	if st == StatusRunning || s.plain == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.plain(name, detail, st)
}

// Finish stops the live view (if any) and prints the summary line.
func (s *Steps) Finish(summary string) error {
	if s.live {
		s.prog.Send(finishMsg{})
		<-s.done
	}
	st := ui.For(s.out)
	fmt.Fprintf(s.out, "%s %s\n", st.Green(ui.IconSpark), st.Bold(summary))
	return nil
}

// ---- bubbletea model --------------------------------------------------------

type stepRow struct {
	name, detail string
	st           StepStatus
}

type stepMsg struct {
	name, detail string
	st           StepStatus
}

type finishMsg struct{}

type stepsModel struct {
	title string
	rows  []stepRow
	index map[string]int
	spin  spinner.Model
}

func newStepsModel(title string) stepsModel {
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	return stepsModel{title: title, index: map[string]int{}, spin: sp}
}

func (m stepsModel) Init() tea.Cmd { return m.spin.Tick }

func (m stepsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case stepMsg:
		if i, ok := m.index[msg.name]; ok {
			m.rows[i] = stepRow(msg)
		} else {
			m.index[msg.name] = len(m.rows)
			m.rows = append(m.rows, stepRow(msg))
		}
		return m, nil
	case finishMsg:
		return m, tea.Quit
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
	}
	return m, nil
}

var (
	glyphStyles = map[StepStatus]lipgloss.Style{
		StatusDone:    lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
		StatusAlready: lipgloss.NewStyle().Foreground(lipgloss.Color("6")),
		StatusWarn:    lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		StatusFailed:  lipgloss.NewStyle().Foreground(lipgloss.Color("1")),
	}
	nameStyle   = lipgloss.NewStyle().Bold(true)
	detailStyle = lipgloss.NewStyle().Faint(true)
	titleStyle  = lipgloss.NewStyle().Bold(true).Underline(true)
)

func glyphFor(st StepStatus) string {
	switch st {
	case StatusDone:
		return ui.IconOK
	case StatusAlready:
		return ui.IconAlready
	case StatusWarn:
		return ui.IconWarn
	case StatusFailed:
		return ui.IconError
	default:
		return ""
	}
}

func (m stepsModel) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(m.title))
	b.WriteByte('\n')
	for _, r := range m.rows {
		glyph := m.spin.View()
		if r.st != StatusRunning {
			glyph = glyphStyles[r.st].Render(glyphFor(r.st))
		}
		fmt.Fprintf(&b, "  %s %s %s\n", glyph, nameStyle.Render(fmt.Sprintf("%-22s", r.name)), detailStyle.Render(r.detail))
	}
	return b.String()
}
