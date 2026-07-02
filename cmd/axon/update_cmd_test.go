package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/selfupdate"
)

// fakeReleaseServer serves a latest release for THIS platform.
func fakeReleaseServer(t *testing.T, version string, binary []byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var srv *httptest.Server
	name := selfupdate.AssetName(version, runtime.GOOS, runtime.GOARCH)
	sum := sha256.Sum256(binary)
	checksums := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), name)
	mux.HandleFunc("/repos/jandro-es/axon/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"tag_name":"v%s","assets":[
			{"name":%q,"browser_download_url":"%s/dl/bin"},
			{"name":"checksums.txt","browser_download_url":"%s/dl/sums"}]}`,
			version, name, srv.URL, srv.URL)
	})
	mux.HandleFunc("/dl/bin", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(binary) })
	mux.HandleFunc("/dl/sums", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, checksums) })
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestUpdateCheckOnlyReportsAvailability(t *testing.T) {
	t.Setenv("AXON_HOME", t.TempDir()) // isolate the update cache
	srv := fakeReleaseServer(t, "999.0.0", []byte("bin"))
	t.Setenv("AXON_UPDATE_BASE_URL", srv.URL)

	out, err := run(t, "update", "--check-only")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	// A dev build is never "older" than a release, so check-only must report
	// up-to-date (never nag unversioned builds) — and the cache must be written.
	if !strings.Contains(out, "up to date") {
		t.Errorf("dev build must never be nagged:\n%s", out)
	}
	if _, err := os.Stat(updateCachePath()); err != nil {
		t.Errorf("update cache not written: %v", err)
	}
}

func TestUpdateJSONShape(t *testing.T) {
	t.Setenv("AXON_HOME", t.TempDir())
	srv := fakeReleaseServer(t, "999.0.0", []byte("bin"))
	t.Setenv("AXON_UPDATE_BASE_URL", srv.URL)

	out, err := run(t, "update", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{`"latest": "999.0.0"`, `"update_available"`, `"current"`} {
		if !strings.Contains(out, want) {
			t.Errorf("json missing %s:\n%s", want, out)
		}
	}
}

func TestDoctorUpdateCheckReadsCacheOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AXON_HOME", home)
	// No server configured: doctor must not need the network.
	writeUpdateCache("999.0.0")
	c := updateAvailabilityCheck()
	// Running build is a dev build → IsNewer is false → OK.
	if c.Status != "ok" || !strings.Contains(c.Detail, "999.0.0") {
		t.Errorf("check = %+v", c)
	}
}
