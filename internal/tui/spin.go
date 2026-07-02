package tui

import (
	"fmt"
	"io"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jandro-es/axon/internal/ui"
)

// Spin runs fn behind a live spinner titled title. In plain mode it prints
// the title, runs fn synchronously, and prints the summary. fn's error is
// returned verbatim either way; the summary line is printed only on success.
func Spin(out io.Writer, title string, fn func() (summary string, err error)) error {
	st := ui.For(out)
	if !Interactive(out) {
		fmt.Fprintf(out, "%s %s\n", st.Cyan(ui.IconArrow), title)
		summary, err := fn()
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s %s\n", st.Green(ui.IconOK), summary)
		return nil
	}

	m := spinModel{title: title, spin: spinner.New(spinner.WithSpinner(spinner.MiniDot))}
	prog := tea.NewProgram(m, tea.WithOutput(out), tea.WithoutSignalHandler())
	go func() {
		summary, err := fn()
		prog.Send(spinDoneMsg{summary: summary, err: err})
	}()
	res, perr := prog.Run()
	if perr != nil {
		return perr
	}
	final := res.(spinModel)
	if final.err != nil {
		return final.err
	}
	fmt.Fprintf(out, "%s %s\n", st.Green(ui.IconOK), final.summary)
	return nil
}

type spinDoneMsg struct {
	summary string
	err     error
}

type spinModel struct {
	title   string
	spin    spinner.Model
	summary string
	err     error
}

func (m spinModel) Init() tea.Cmd { return m.spin.Tick }

func (m spinModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinDoneMsg:
		m.summary, m.err = msg.summary, msg.err
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

func (m spinModel) View() string {
	return fmt.Sprintf("%s %s\n", m.spin.View(), m.title)
}
