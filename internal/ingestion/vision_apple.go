package ingestion

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// AppleVision describes images via macOS 27's fm CLI — a bounded per-call
// `fm respond --image` subprocess (the ADR-026 detected-system-tool pattern;
// FR-196). The "system" variant is on-device; the "pcc" variant adds
// `--model pcc` and exists only behind the models.pcc_enabled gate enforced
// at config validation (FR-197 — PCC receives unredacted image bytes, and
// selecting it is always explicit). Still a perception primitive: never
// chokepoint-routed, never ledgered (ADR-035).
type AppleVision struct {
	bin     string
	variant string // "system" (on-device) | "pcc"
	prompt  string
	timeout time.Duration

	// run executes fm; injectable so tests need no binary.
	run func(ctx context.Context, bin string, args ...string) (stdout, stderr []byte, err error)
}

// NewAppleVision constructs the provider. bin is the fm binary path (from
// VisionFor's LookPath); variant is "system" or "pcc".
func NewAppleVision(bin, variant string) *AppleVision {
	return &AppleVision{
		bin:     bin,
		variant: variant,
		prompt:  visionPrompt,
		timeout: 120 * time.Second,
		run:     runVisionFM,
	}
}

// Name identifies the provider in doctor and logs.
func (v *AppleVision) Name() string {
	if v.variant == "pcc" {
		return "apple:pcc"
	}
	return "apple"
}

// Describe writes the image to a private temp file (fm takes paths), runs one
// bounded fm respond, and returns the ANSI-stripped description.
func (v *AppleVision) Describe(ctx context.Context, img []byte, mime string) (string, error) {
	tmp, err := os.CreateTemp("", "axon-vision-*"+visionExt(mime))
	if err != nil {
		return "", fmt.Errorf("apple vision: temp image: %w", err)
	}
	path := tmp.Name()
	defer func() { _ = os.Remove(path) }()
	if err := os.Chmod(path, 0o600); err != nil {
		return "", fmt.Errorf("apple vision: temp image perms: %w", err)
	}
	if _, err := tmp.Write(img); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("apple vision: write temp image: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}

	args := []string{"respond", "--no-stream", "--image", path, "--text", v.prompt}
	if v.variant == "pcc" {
		args = append(args, "--model", "pcc")
	}
	cctx, cancel := context.WithTimeout(ctx, v.timeout)
	defer cancel()
	stdout, stderr, runErr := v.run(cctx, v.bin, args...)
	out := strings.TrimSpace(visionStripANSI(string(stdout)))
	if runErr != nil {
		detail := strings.TrimSpace(visionStripANSI(string(stderr)))
		if detail == "" {
			detail = out
		}
		return "", fmt.Errorf("apple vision (%s): %w: %s", v.Name(), runErr, visionCap(detail))
	}
	if out == "" {
		return "", fmt.Errorf("apple vision (%s): fm returned no description", v.Name())
	}
	return out, nil
}

// visionExt maps a mime type to the file extension fm keys on.
func visionExt(mime string) string {
	switch mime {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/heic", "image/heif":
		return ".heic"
	case "image/tiff":
		return ".tiff"
	case "image/bmp":
		return ".bmp"
	default:
		return ".png"
	}
}

// visionAnsiRe strips CSI sequences (fm styles output, including 24-bit
// colour). Leaf-local: ingestion shares nothing with core.
var visionAnsiRe = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func visionStripANSI(s string) string { return visionAnsiRe.ReplaceAllString(s, "") }

func visionCap(s string) string {
	const cap = 300
	if len(s) > cap {
		return s[:cap] + "… (truncated)"
	}
	return s
}

// runVisionFM is the real subprocess executor (WaitDelay guard as elsewhere).
func runVisionFM(ctx context.Context, bin string, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}
