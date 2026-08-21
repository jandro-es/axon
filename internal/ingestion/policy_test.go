package ingestion

import (
	"testing"

	"github.com/jandro-es/axon/internal/config"
)

func TestCheckEgressPolicy(t *testing.T) {
	if err := CheckEgressPolicy(config.PolicyConfig{EgressAllowlist: []string{"localhost", "*"}}, "ntfy.sh"); err != nil {
		t.Fatalf("wildcard allowlist should permit any host: %v", err)
	}
	if err := CheckEgressPolicy(config.PolicyConfig{EgressAllowlist: []string{"localhost"}}, "ntfy.sh"); err == nil {
		t.Fatal("a strict allowlist must refuse an unlisted host")
	}
	if err := CheckEgressPolicy(config.PolicyConfig{EgressAllowlist: []string{"localhost"}}, "localhost"); err != nil {
		t.Fatalf("a listed host must be permitted: %v", err)
	}
	if err := CheckEgressPolicy(config.PolicyConfig{}, "ntfy.sh"); err != nil {
		t.Fatalf("an empty allowlist must not refuse: %v", err)
	}
	// ADR-041: loopback and private targets are NOT blocked here, unlike
	// ingest — a notify URL comes from config, which no model can write.
	for _, h := range []string{"127.0.0.1", "192.168.1.10", "localhost"} {
		if err := CheckEgressPolicy(config.PolicyConfig{EgressAllowlist: []string{"*"}}, h); err != nil {
			t.Errorf("self-hosted target %q must be permitted: %v", h, err)
		}
	}
}
