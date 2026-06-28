package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnv(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	contents := "" +
		"# a comment\n" +
		"\n" +
		"FOO=bar\n" +
		`QUOTED="baz qux"` + "\n" +
		"SINGLE='hi'\n" +
		"PREEXISTING=fromfile\n" +
		"NOEQUALSIGN\n"
	if err := os.WriteFile(envFile, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	// A pre-existing env var must NOT be overwritten by the file.
	t.Setenv("PREEXISTING", "fromenv")
	// Ensure the others are unset going in.
	os.Unsetenv("FOO")
	os.Unsetenv("QUOTED")
	os.Unsetenv("SINGLE")
	t.Cleanup(func() {
		os.Unsetenv("FOO")
		os.Unsetenv("QUOTED")
		os.Unsetenv("SINGLE")
	})

	if err := LoadDotEnv(envFile); err != nil {
		t.Fatalf("LoadDotEnv: %v", err)
	}

	cases := map[string]string{
		"FOO":         "bar",
		"QUOTED":      "baz qux",
		"SINGLE":      "hi",
		"PREEXISTING": "fromenv", // unchanged
	}
	for k, want := range cases {
		if got := os.Getenv(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func TestLoadDotEnvMissingFileIsOK(t *testing.T) {
	if err := LoadDotEnv(filepath.Join(t.TempDir(), "nope.env")); err != nil {
		t.Errorf("missing .env should not error, got %v", err)
	}
}

func TestResolveSecret(t *testing.T) {
	t.Setenv("MY_TOKEN", "sk-secret")

	tests := []struct {
		name    string
		ref     string
		want    string
		wantErr bool
	}{
		{"empty", "", "", false},
		{"literal passthrough", "plain-value", "plain-value", false},
		{"env present", "env:MY_TOKEN", "sk-secret", false},
		{"env missing", "env:NOT_SET_XYZ", "", true},
		{"keychain unimplemented", "keychain:foo", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveSecret(tt.ref)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ResolveSecret(%q) err = %v, wantErr %v", tt.ref, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("ResolveSecret(%q) = %q, want %q", tt.ref, got, tt.want)
			}
		})
	}
}

func TestIsSecretRef(t *testing.T) {
	cases := map[string]bool{
		"env:X":      true,
		"keychain:X": true,
		"literal":    false,
		"":           false,
	}
	for in, want := range cases {
		if got := IsSecretRef(in); got != want {
			t.Errorf("IsSecretRef(%q) = %v, want %v", in, got, want)
		}
	}
}
