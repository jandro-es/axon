package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// Secret reference prefixes used in the YAML (e.g. oauth_token: env:NAME).
const (
	secretPrefixEnv      = "env:"
	secretPrefixKeychain = "keychain:"
)

// LoadDotEnv reads a .env file of KEY=VALUE lines into the process environment.
// Lines that are blank or start with '#' are ignored; surrounding single or
// double quotes are stripped. Existing environment variables are NOT
// overwritten, so a real shell env always wins over the file. A missing file is
// not an error (secrets are optional at this stage); other IO errors are.
func LoadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("open env file %q: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); !exists {
			if err := os.Setenv(key, val); err != nil {
				return fmt.Errorf("set env %q: %w", key, err)
			}
		}
	}
	return scanner.Err()
}

// ResolveSecret resolves a secret reference to its value. A bare value (no
// recognised prefix) is returned as-is so non-secret literals still work. An
// env: reference reads the named environment variable and errors if it is
// unset. keychain: is recognised but not yet implemented (Phase 7).
func ResolveSecret(ref string) (string, error) {
	switch {
	case ref == "":
		return "", nil
	case strings.HasPrefix(ref, secretPrefixEnv):
		name := strings.TrimPrefix(ref, secretPrefixEnv)
		v, ok := os.LookupEnv(name)
		if !ok {
			return "", fmt.Errorf("secret env %q is not set", name)
		}
		return v, nil
	case strings.HasPrefix(ref, secretPrefixKeychain):
		return "", fmt.Errorf("keychain secret resolution is not yet implemented")
	default:
		return ref, nil
	}
}

// IsSecretRef reports whether s looks like a secret reference rather than a
// literal value. Useful for validation and redaction.
func IsSecretRef(s string) bool {
	return strings.HasPrefix(s, secretPrefixEnv) || strings.HasPrefix(s, secretPrefixKeychain)
}
