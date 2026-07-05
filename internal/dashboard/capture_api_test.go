package dashboard

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/events"
	"github.com/jandro-es/axon/internal/vault"
)

func captureTestServer(t *testing.T, enabled bool) (*httptest.Server, *vault.FS, *events.Bus) {
	t.Helper()
	v := vault.NewFS(t.TempDir())
	bus := events.NewBus()
	s := New(Config{
		Profile: "test", Vault: v, Bus: bus, CaptureEnabled: enabled,
	})
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv, v, bus
}

func postCapture(t *testing.T, url string, payload map[string]any, withHeader bool) *http.Response {
	t.Helper()
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", url+"/api/capture", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if withHeader {
		req.Header.Set("X-Axon-Capture", "1")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestCaptureURLHappyPath(t *testing.T) {
	srv, v, _ := captureTestServer(t, true)

	res := postCapture(t, srv.URL, map[string]any{"url": "https://example.com/a", "title": "A"}, true)
	if res.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	var out struct {
		OK   bool   `json:"ok"`
		Path string `json:"path"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if !out.OK || !strings.HasPrefix(out.Path, "00-Inbox/capture-") {
		t.Fatalf("out = %+v", out)
	}
	data, err := os.ReadFile(filepath.Join(v.Root(), filepath.FromSlash(out.Path)))
	if err != nil {
		t.Fatalf("note not written: %v", err)
	}
	if !strings.HasPrefix(string(data), "https://example.com/a\n") {
		t.Fatalf("URL not on its own first line:\n%s", data)
	}
}

func TestCaptureGuardAndToggle(t *testing.T) {
	srv, _, _ := captureTestServer(t, true)
	if res := postCapture(t, srv.URL, map[string]any{"url": "https://x"}, false); res.StatusCode != http.StatusForbidden {
		t.Fatalf("no-header status = %d, want 403", res.StatusCode)
	}
	if res := postCapture(t, srv.URL, map[string]any{}, true); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty-payload status = %d, want 400", res.StatusCode)
	}
	off, _, _ := captureTestServer(t, false)
	if res := postCapture(t, off.URL, map[string]any{"url": "https://x"}, true); res.StatusCode != http.StatusNotFound {
		t.Fatalf("disabled status = %d, want 404", res.StatusCode)
	}
}

func TestCaptureTextOnly(t *testing.T) {
	srv, v, _ := captureTestServer(t, true)
	res := postCapture(t, srv.URL, map[string]any{"text": "a clipped paragraph"}, true)
	if res.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	var out struct {
		Path string `json:"path"`
	}
	_ = json.NewDecoder(res.Body).Decode(&out)
	data, _ := os.ReadFile(filepath.Join(v.Root(), filepath.FromSlash(out.Path)))
	if !strings.Contains(string(data), "a clipped paragraph") {
		t.Fatalf("text missing:\n%s", data)
	}
}

// io is used by the served-page tests added in Task 3.
var _ = io.Discard
