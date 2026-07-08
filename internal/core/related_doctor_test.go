package core

import (
	"testing"

	"github.com/jandro-es/axon/internal/config"
)

func TestRelatedCheckReportsDisabled(t *testing.T) {
	off := false
	p := config.Profile{Dashboard: config.DashboardConfig{RelatedEnabled: &off}}
	c := relatedCheck(p, p.Paths())
	if c.Status != StatusOK {
		t.Fatalf("status = %v, want OK", c.Status)
	}
	if c.Name != "related" {
		t.Fatalf("name = %q, want related", c.Name)
	}
}
