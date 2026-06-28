package config

import (
	"fmt"
	"os"
)

// DefaultProfileName is the built-in fallback when nothing selects a profile.
const DefaultProfileName = "default"

// ResolveProfileName applies the precedence order for choosing which profile is
// active: CLI flag -> AXON_PROFILE env -> config.active_profile -> built-in
// default. The flag wins because it is the most explicit signal.
func (c *Config) ResolveProfileName(flag string) string {
	if flag != "" {
		return flag
	}
	if v := os.Getenv("AXON_PROFILE"); v != "" {
		return v
	}
	if c.ActiveProfile != "" {
		return c.ActiveProfile
	}
	return DefaultProfileName
}

// ResolveProfile returns the resolved profile name and its Profile, honouring
// the precedence in ResolveProfileName. It errors if the chosen profile is not
// defined in the config.
func (c *Config) ResolveProfile(flag string) (string, Profile, error) {
	name := c.ResolveProfileName(flag)
	p, ok := c.Profiles[name]
	if !ok {
		return name, Profile{}, fmt.Errorf("profile %q is not defined in profiles", name)
	}
	return name, p, nil
}
