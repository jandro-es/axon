package config

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// validateNotify enforces the ADR-041 rules. Runs at profile level in
// Config.Validate, beside validateWatchFolders.
//
// It deliberately does NOT check event kinds against a known list. Kinds are
// assembled three ways — literals at the emitter, passed as a parameter, and
// built at runtime from user input ("review." + action) — so a static list is
// correct only until the next emitter lands, and a stale list refusing valid
// config would be a worse failure than the typo it prevents. The `notify`
// doctor check warns about unrecognised kinds instead.
func validateNotify(p Profile) error {
	n := p.Notify
	raw := strings.TrimSpace(n.URL)
	if raw == "" && len(n.Events) == 0 {
		return nil // off, the default
	}
	// A half-configured notifier is silent, and silence is indistinguishable
	// from working — so refuse both halves of the mistake loudly.
	if raw == "" {
		return fmt.Errorf("notify.events is set but notify.url is empty — a notifier with no destination is silent")
	}
	if len(n.Events) == 0 {
		return fmt.Errorf("notify.url is set but notify.events is empty — a notifier with no subscribed kinds never fires")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("notify.url %q is not a valid absolute url", n.URL)
	}
	// Scheme before host: file:///x parses with an empty Host, and "not a
	// valid absolute url" would be a misleading way to say "wrong scheme".
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("notify.url %q: scheme must be http or https (got %q)", n.URL, u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("notify.url %q is not a valid absolute url", n.URL)
	}
	if u.Scheme == "http" && !isLocalHostname(u.Hostname()) {
		return fmt.Errorf("notify.url %q must use https for a public host — plaintext would push vault activity in the clear", n.URL)
	}
	for _, k := range n.Events {
		if strings.TrimSpace(k) == "" {
			return fmt.Errorf("notify.events contains an empty entry")
		}
	}
	return nil
}

// isLocalHostname reports whether a host is loopback or a private address —
// the self-hosted-ntfy case, where plaintext is acceptable because the traffic
// never leaves the machine or the LAN.
func isLocalHostname(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".local") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())
}
