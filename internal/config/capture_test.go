package config

import "testing"

func TestCaptureConfigDefaults(t *testing.T) {
	var c CaptureConfig
	if c.EnrichMode() != "heuristic" {
		t.Errorf("EnrichMode = %q, want heuristic", c.EnrichMode())
	}
	if c.Archive() != "04-Archive/Capture" {
		t.Errorf("Archive = %q, want 04-Archive/Capture", c.Archive())
	}
	c = CaptureConfig{Enrich: "claude", ArchiveDir: "04-Archive/Clips"}
	if c.EnrichMode() != "claude" || c.Archive() != "04-Archive/Clips" {
		t.Errorf("overrides not honored: %+v", c)
	}
}

func TestValidateCapture(t *testing.T) {
	tests := []struct {
		name    string
		cfg     CaptureConfig
		wantErr bool
	}{
		{"zero value ok", CaptureConfig{}, false},
		{"heuristic ok", CaptureConfig{Enrich: "heuristic"}, false},
		{"claude ok", CaptureConfig{Enrich: "claude"}, false},
		{"bad enrich", CaptureConfig{Enrich: "gpt"}, true},
		{"absolute archive dir", CaptureConfig{ArchiveDir: "/tmp/x"}, true},
		{"escaping archive dir", CaptureConfig{ArchiveDir: "../outside"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCapture(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
