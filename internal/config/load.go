package config

import (
	"fmt"
	"os"

	"github.com/go-playground/validator/v10"
	"github.com/goccy/go-yaml"
)

// DefaultConfigFile is the conventional config filename. It lives under the
// AXON home directory (see DefaultConfigPath); pass --config to override.
const DefaultConfigFile = "config.yaml"

// validate is shared and concurrency-safe per the validator docs.
var validate = validator.New()

// Load reads, parses and validates the config file at path. It returns a typed
// Config or a descriptive error. It does NOT resolve secrets or expand paths —
// callers do that explicitly when they need a runnable profile.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}
	return Parse(raw)
}

// Parse parses and validates config bytes. Separated from Load so tests can
// feed bytes directly without touching the filesystem.
func Parse(raw []byte) (*Config, error) {
	var c Config
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Validate runs struct-tag validation across the whole config and then applies
// the cross-field rules that struct tags can't express.
func (c *Config) Validate() error {
	if err := validate.Struct(c); err != nil {
		return fmt.Errorf("config validation failed: %w", err)
	}
	// The active profile must actually exist in the profiles map.
	if _, ok := c.Profiles[c.ActiveProfile]; !ok {
		return fmt.Errorf("active_profile %q is not defined in profiles", c.ActiveProfile)
	}
	// Local-model routing (ADR-015) and capture (ADR-016) rules on every
	// profile's respective blocks.
	for name, p := range c.Profiles {
		if err := validateLocalRouting(p.Models); err != nil {
			return fmt.Errorf("config validation failed: profile %q: %w", name, err)
		}
		if err := validateCapture(p.Capture); err != nil {
			return fmt.Errorf("config validation failed: profile %q: %w", name, err)
		}
	}
	return nil
}
