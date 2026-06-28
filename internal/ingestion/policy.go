package ingestion

import (
	"fmt"
	"net"
	"strings"

	"github.com/jandro-es/axon/internal/config"
)

// PolicyError is returned when a URL is refused by the profile policy before any
// network access happens (NFR-05). It names the host and the reason.
type PolicyError struct {
	Host   string
	Reason string
}

func (e *PolicyError) Error() string {
	return fmt.Sprintf("ingest denied for host %q: %s", e.Host, e.Reason)
}

// CheckIngestPolicy enforces the profile's ingestion egress controls for a host,
// to be called BEFORE fetching (the work profile is deny-by-default). Precedence:
//
//  1. An explicit (non-wildcard) ingest_domains_allow match always permits.
//  2. Otherwise an ingest_domains_deny match (including "*") refuses.
//  3. Otherwise a "*" in ingest_domains_allow permits; a non-empty allowlist
//     without the host refuses (deny-by-default when an allowlist is set).
//  4. The egress_allowlist is then applied the same way as a network backstop,
//     unless it contains "*" or the host was an explicit ingest allow.
func CheckIngestPolicy(p config.PolicyConfig, host string) error {
	if host == "" {
		return &PolicyError{Host: host, Reason: "empty host"}
	}
	// Always refuse link-local literal IPs (cloud metadata 169.254.169.254,
	// fe80::/10) regardless of the allowlist — never a legitimate ingest target,
	// and the classic SSRF pivot.
	if ip := net.ParseIP(host); ip != nil && ip.IsLinkLocalUnicast() {
		return &PolicyError{Host: host, Reason: "link-local address refused (SSRF guard)"}
	}
	explicitAllow := matchesAnyExact(p.IngestDomainsAllow, host)

	if !explicitAllow {
		if matchesAny(p.IngestDomainsDeny, host) {
			return &PolicyError{Host: host, Reason: "matched ingest_domains_deny"}
		}
		if !hasWildcard(p.IngestDomainsAllow) && len(p.IngestDomainsAllow) > 0 {
			return &PolicyError{Host: host, Reason: "not in ingest_domains_allow"}
		}
	}

	// Network-level backstop: egress allowlist. An explicit ingest allow implies
	// intended egress and bypasses it; a "*" allowlist permits everything.
	if !explicitAllow && len(p.EgressAllowlist) > 0 && !hasWildcard(p.EgressAllowlist) {
		if !matchesAny(p.EgressAllowlist, host) {
			return &PolicyError{Host: host, Reason: "not in egress_allowlist"}
		}
	}
	return nil
}

// matchesDomain reports whether host equals pattern or is a subdomain of it.
// "*" matches anything.
func matchesDomain(pattern, host string) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	host = strings.ToLower(host)
	if pattern == "*" {
		return true
	}
	return host == pattern || strings.HasSuffix(host, "."+pattern)
}

func matchesAny(patterns []string, host string) bool {
	for _, p := range patterns {
		if matchesDomain(p, host) {
			return true
		}
	}
	return false
}

// matchesAnyExact is like matchesAny but ignores the "*" wildcard, so it answers
// "is there an explicit (named) allow entry for this host?".
func matchesAnyExact(patterns []string, host string) bool {
	for _, p := range patterns {
		if strings.TrimSpace(p) == "*" {
			continue
		}
		if matchesDomain(p, host) {
			return true
		}
	}
	return false
}

func hasWildcard(patterns []string) bool {
	for _, p := range patterns {
		if strings.TrimSpace(p) == "*" {
			return true
		}
	}
	return false
}
