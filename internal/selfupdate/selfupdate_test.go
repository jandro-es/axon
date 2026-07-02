package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestIsNewer(t *testing.T) {
	for _, tc := range []struct {
		current, latest string
		want            bool
	}{
		{"1.2.2", "1.2.3", true},
		{"1.2.3", "1.2.3", false},
		{"1.2.3", "1.2.2", false},
		{"1.9.0", "1.10.0", true},
		{"dev", "9.9.9", false}, // never nag dev builds
		{"", "1.0.0", false},
		{"v1.2.2", "v1.2.3", true}, // leading v tolerated on both sides
		{"1.2.3", "2.0.0-rc.1", true},
	} {
		if got := IsNewer(tc.current, tc.latest); got != tc.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", tc.current, tc.latest, got, tc.want)
		}
	}
}

func TestAssetName(t *testing.T) {
	if got := AssetName("1.2.3", "darwin", "arm64"); got != "axon_1.2.3_darwin_arm64" {
		t.Errorf("AssetName = %q", got)
	}
}

// fakeRelease serves a GitHub-shaped latest-release document plus assets.
func fakeRelease(t *testing.T, version string, binary []byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var srv *httptest.Server
	name := AssetName(version, "darwin", "arm64")
	sum := sha256.Sum256(binary)
	checksums := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), name)

	mux.HandleFunc("/repos/o/r/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"tag_name":"v%s","assets":[
			{"name":%q,"browser_download_url":"%s/dl/%s"},
			{"name":"checksums.txt","browser_download_url":"%s/dl/checksums.txt"}]}`,
			version, name, srv.URL, name, srv.URL)
	})
	mux.HandleFunc("/dl/"+name, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(binary) })
	mux.HandleFunc("/dl/checksums.txt", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, checksums) })
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestCheckLatestAndDownloadVerified(t *testing.T) {
	binary := []byte("#!/bin/sh\necho new-axon\n")
	srv := fakeRelease(t, "9.9.9", binary)

	rel, err := CheckLatest(context.Background(), srv.URL, "o", "r")
	if err != nil {
		t.Fatal(err)
	}
	if rel.Version != "9.9.9" || len(rel.Assets) != 2 {
		t.Fatalf("release = %+v", rel)
	}

	dest := t.TempDir()
	name := AssetName("9.9.9", "darwin", "arm64")
	path, err := DownloadVerified(context.Background(), rel, name, dest)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(binary) {
		t.Error("downloaded binary differs")
	}
}

func TestDownloadVerifiedRejectsBadChecksum(t *testing.T) {
	binary := []byte("legit")
	srv := fakeRelease(t, "1.0.1", binary)
	rel, err := CheckLatest(context.Background(), srv.URL, "o", "r")
	if err != nil {
		t.Fatal(err)
	}
	// Tamper: request the real asset name but the server was built for a
	// different payload under a second name — simulate by corrupting the
	// release map to point the asset at the checksums file itself.
	name := AssetName("1.0.1", "darwin", "arm64")
	rel.Assets[name] = rel.Assets["checksums.txt"]
	if _, err := DownloadVerified(context.Background(), rel, name, t.TempDir()); err == nil {
		t.Error("bad checksum must be rejected")
	}
}

func TestSwap(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "axon")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	newBin := filepath.Join(dir, "axon-new")
	if err := os.WriteFile(newBin, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Swap(target, newBin); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "new" {
		t.Errorf("target = %q, want new", got)
	}
	old, _ := os.ReadFile(target + ".old")
	if string(old) != "old" {
		t.Errorf(".old = %q, want old", old)
	}
	st, _ := os.Stat(target)
	if st.Mode()&0o111 == 0 {
		t.Error("swapped binary must be executable")
	}
}
