package agent

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// fmProc is the supervised child seam; the real one wraps *exec.Cmd, tests
// substitute an in-process server.
type fmProc interface {
	Alive() bool
	Stop()
}

// FMSupervisor owns the `fm serve --socket` child (ADR-038/FR-193): one
// instance per process, started lazily by the first call that needs it,
// health-probed before first use, restarted on the next Ensure after a death,
// terminated on Stop. Safe for concurrent use.
type FMSupervisor struct {
	socket        string
	healthTimeout time.Duration

	mu   sync.Mutex
	proc fmProc

	// spawn starts the child; injectable so tests need no fm binary.
	spawn func() (fmProc, error)
	// health probes GET /health over the socket; injectable for tests.
	health func(ctx context.Context, socket string) error
}

// NewFMSupervisor builds the real supervisor. bin is the fm binary path
// (from exec.LookPath at wiring time); socket lives in the profile data dir.
func NewFMSupervisor(bin, socket string) *FMSupervisor {
	s := &FMSupervisor{socket: socket, healthTimeout: 10 * time.Second, health: fmHealth}
	s.spawn = func() (fmProc, error) { return startFMServe(bin, socket) }
	return s
}

// Socket is the unix socket path the child listens on.
func (s *FMSupervisor) Socket() string { return s.socket }

// Ensure makes sure a healthy child is listening, starting (or restarting)
// one if needed. It returns once /health answers or the health window closes.
func (s *FMSupervisor) Ensure(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.proc != nil && s.proc.Alive() {
		return nil
	}
	// A dead child leaves a stale socket file that would refuse the bind.
	if s.proc != nil {
		s.proc.Stop()
		s.proc = nil
	}
	_ = os.Remove(s.socket)
	if err := os.MkdirAll(filepath.Dir(s.socket), 0o700); err != nil {
		return fmt.Errorf("fm serve socket dir: %w", err)
	}
	p, err := s.spawn()
	if err != nil {
		return fmt.Errorf("start fm serve: %w", err)
	}
	deadline := time.Now().Add(s.healthTimeout)
	for {
		hctx, cancel := context.WithTimeout(ctx, time.Second)
		err = s.health(hctx, s.socket)
		cancel()
		if err == nil {
			s.proc = p
			return nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			p.Stop()
			return fmt.Errorf("fm serve did not become healthy on %s: %w", s.socket, err)
		}
		if !p.Alive() {
			return fmt.Errorf("fm serve exited before becoming healthy on %s", s.socket)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Stop terminates the child (daemon shutdown / CLI process exit).
func (s *FMSupervisor) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.proc != nil {
		s.proc.Stop()
		s.proc = nil
	}
	_ = os.Remove(s.socket)
}

// execFMProc is the real child handle. Exactly one goroutine (started at
// spawn) calls Wait; everyone else watches the done channel.
type execFMProc struct {
	cmd  *exec.Cmd
	done chan struct{}
}

func (p *execFMProc) Alive() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

func (p *execFMProc) Stop() {
	select {
	case <-p.done:
		return
	default:
	}
	_ = p.cmd.Process.Signal(os.Interrupt)
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		_ = p.cmd.Process.Kill()
		<-p.done
	}
}

func startFMServe(bin, socket string) (fmProc, error) {
	cmd := exec.Command(bin, "serve", "--socket", socket)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p := &execFMProc{cmd: cmd, done: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(p.done) }()
	return p, nil
}

// fmHealth GETs /health over the unix socket.
func fmHealth(ctx context.Context, socket string) error {
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://fm/health", nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fm serve /health: status %d", resp.StatusCode)
	}
	return nil
}
