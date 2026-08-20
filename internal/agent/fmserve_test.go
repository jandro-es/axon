package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The exact response shape captured live from `fm serve` on macOS 27.0.
const fmServeCaptured = `{"model":"system","id":"chatcmpl-E4C3BD2F","choices":[{"index":0,"message":{"refusal":null,"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens_details":{"cached_tokens":0},"total_tokens":63,"completion_tokens":3,"completion_tokens_details":{"reasoning_tokens":0},"prompt_tokens":60},"object":"chat.completion","created":1787248075}`

// fmServeTestServer serves handler on a unix socket and returns a supervisor
// already pointed at it whose Ensure is a no-op (the "child" is the server).
// shortSocketPath returns a socket path under the 104-byte macOS sun_path
// limit — t.TempDir() paths routinely exceed it and bind fails EINVAL.
func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "fm")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "fm.sock")
}

func fmServeTestServer(t *testing.T, handler http.HandlerFunc) *FMSupervisor {
	t.Helper()
	socket := shortSocketPath(t)
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(handler)
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)

	// The test server IS the child: mark it running so Ensure early-returns
	// (a cold Ensure would unlink the live socket before "spawning").
	sup := &FMSupervisor{socket: socket, healthTimeout: time.Second, health: fmHealth, proc: fakeProc{alive: true}}
	return sup
}

type fakeProc struct{ alive bool }

func (p fakeProc) Alive() bool { return p.alive }
func (fakeProc) Stop()         {}

func TestFMServeRunMapsCapturedResponse(t *testing.T) {
	var got fmChatRequest
	sup := fmServeTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(fmServeCaptured))
	})
	f := NewFMServe(sup)
	resp, err := f.Run(context.Background(), Request{
		Operation: "test", Model: "apple:system", System: "sys", Prompt: "Reply with exactly: ok",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "system" {
		t.Errorf("endpoint model = %q, want bare variant %q", got.Model, "system")
	}
	if got.Stream {
		t.Error("must request non-streaming")
	}
	if len(got.Messages) != 2 || got.Messages[0].Role != "system" {
		t.Errorf("messages = %+v", got.Messages)
	}
	if resp.Text != "ok" {
		t.Errorf("text = %q", resp.Text)
	}
	if resp.Model != "apple:system" {
		t.Errorf("ledger model = %q, want the AXON ref", resp.Model)
	}
	if resp.Usage.InputTokens != 60 || resp.Usage.OutputTokens != 3 || resp.Usage.CacheRead != 0 {
		t.Errorf("usage = %+v, want measured 60/3", resp.Usage)
	}
}

func TestFMServeRunSurfacesErrors(t *testing.T) {
	sup := fmServeTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"message":"Private Cloud Compute is not available in this context."}}`))
	})
	f := NewFMServe(sup)
	_, err := f.Run(context.Background(), Request{Model: "apple:pcc", Prompt: "hi"})
	if err == nil || !strings.Contains(err.Error(), "status 503") {
		t.Fatalf("want a surfaced 503, got %v", err)
	}
}

func TestFMSupervisorLifecycle(t *testing.T) {
	socket := shortSocketPath(t)
	healthy := false
	spawned := 0
	var latest *togglableProc
	sup := &FMSupervisor{
		socket: socket, healthTimeout: 300 * time.Millisecond,
		health: func(context.Context, string) error {
			if healthy {
				return nil
			}
			return errors.New("not yet")
		},
	}
	sup.spawn = func() (fmProc, error) { spawned++; latest = &togglableProc{alive: true}; return latest, nil }

	// Unhealthy forever → Ensure fails after the window.
	if err := sup.Ensure(context.Background()); err == nil {
		t.Fatal("want error while never healthy")
	}
	// Healthy → Ensure succeeds, second Ensure reuses (no respawn).
	healthy = true
	if err := sup.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := sup.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if spawned != 2 {
		t.Fatalf("spawned = %d, want 2 (failed attempt + one live)", spawned)
	}
	// Child dies → next Ensure respawns.
	latest.alive = false
	if err := sup.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if spawned != 3 {
		t.Fatalf("spawned = %d, want 3 after a death", spawned)
	}
	sup.Stop()
}

type togglableProc struct{ alive bool }

func (p *togglableProc) Alive() bool { return p.alive }
func (p *togglableProc) Stop()       { p.alive = false }

func TestFMSocketPathIsShortStableAndDistinct(t *testing.T) {
	deep := "/Users/someone/very/deep/nested/path/to/.axon/profiles/personal/data/dir/that/keeps/going"
	a := FMSocketPath(deep)
	if len(a) > 100 {
		t.Fatalf("socket path %q is %d bytes — must stay under the 104-byte sun_path limit", a, len(a))
	}
	if a != FMSocketPath(deep) {
		t.Error("must be stable for the same data dir")
	}
	if a == FMSocketPath("/other/data/dir") {
		t.Error("must differ per data dir")
	}
}

func TestFMSupervisorRejectsOverlongSocket(t *testing.T) {
	sup := &FMSupervisor{
		socket:        filepath.Join("/tmp", strings.Repeat("x", 120), "fm.sock"),
		healthTimeout: 100 * time.Millisecond,
		health:        func(context.Context, string) error { return errors.New("unreached") },
	}
	sup.spawn = func() (fmProc, error) { t.Fatal("must not spawn on an overlong socket path"); return nil, nil }
	if err := sup.Ensure(context.Background()); err == nil || !strings.Contains(err.Error(), "104") {
		t.Fatalf("want an actionable sun_path error, got %v", err)
	}
}
