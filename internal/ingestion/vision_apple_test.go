package ingestion

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/config"
)

// captureRun records the invocation and the temp image's bytes at call time
// (the file must exist while fm runs and be gone afterwards).
type captureRun struct {
	bin      string
	args     []string
	imgAtRun []byte
	imgPath  string
	stdout   string
	stderr   string
	err      error
}

func (c *captureRun) run(_ context.Context, bin string, args ...string) ([]byte, []byte, error) {
	c.bin = bin
	c.args = args
	for i, a := range args {
		if a == "--image" && i+1 < len(args) {
			c.imgPath = args[i+1]
			c.imgAtRun, _ = os.ReadFile(args[i+1])
		}
	}
	return []byte(c.stdout), []byte(c.stderr), c.err
}

func hasFlagPair(args []string, flag, val string) bool {
	for i, a := range args {
		if a == flag && i+1 < len(args) && args[i+1] == val {
			return true
		}
	}
	return false
}

func TestAppleVisionDescribeOnDevice(t *testing.T) {
	cr := &captureRun{stdout: "\x1b[38;2;1;2;3mThe text says AXON.\x1b[0m\n"}
	v := NewAppleVision("/usr/bin/fm", "system")
	v.run = cr.run

	got, err := v.Describe(context.Background(), []byte("png-bytes"), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if got != "The text says AXON." {
		t.Errorf("description = %q (ANSI must be stripped)", got)
	}
	if cr.bin != "/usr/bin/fm" || len(cr.args) == 0 || cr.args[0] != "respond" {
		t.Errorf("invocation = %s %v", cr.bin, cr.args)
	}
	if string(cr.imgAtRun) != "png-bytes" || !strings.HasSuffix(cr.imgPath, ".png") {
		t.Errorf("image file at run: path=%q bytes=%q", cr.imgPath, cr.imgAtRun)
	}
	if _, err := os.Stat(cr.imgPath); !os.IsNotExist(err) {
		t.Errorf("temp image %q must be removed after the call", cr.imgPath)
	}
	if !hasFlagPair(cr.args, "--text", visionPrompt) {
		t.Error("must pass the NFR-05 vision prompt via --text")
	}
	for _, a := range cr.args {
		if a == "--model" {
			t.Error("on-device variant must not pass --model")
		}
	}
	if v.Name() != "apple" {
		t.Errorf("Name() = %q", v.Name())
	}
}

func TestAppleVisionPCCVariant(t *testing.T) {
	cr := &captureRun{stdout: "ok"}
	v := NewAppleVision("/usr/bin/fm", "pcc")
	v.run = cr.run
	if _, err := v.Describe(context.Background(), []byte("x"), "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	if !hasFlagPair(cr.args, "--model", "pcc") {
		t.Errorf("pcc variant must pass --model pcc, args = %v", cr.args)
	}
	if v.Name() != "apple:pcc" {
		t.Errorf("Name() = %q", v.Name())
	}
}

func TestAppleVisionErrorSurfacesStrippedOutput(t *testing.T) {
	cr := &captureRun{stderr: "Error: \x1b[38;2;255;107;128mPrivate Cloud Compute is not available in this context.\x1b[0m", err: errors.New("exit status 1")}
	v := NewAppleVision("/usr/bin/fm", "pcc")
	v.run = cr.run
	_, err := v.Describe(context.Background(), []byte("x"), "image/png")
	if err == nil || !strings.Contains(err.Error(), "Private Cloud Compute") {
		t.Fatalf("want the stripped fm error surfaced, got %v", err)
	}
	if strings.Contains(err.Error(), "\x1b") {
		t.Error("error carries raw ANSI")
	}
	if _, serr := os.Stat(cr.imgPath); !os.IsNotExist(serr) {
		t.Error("temp image must be removed on the error path too")
	}
}

func TestVisionForAppleModes(t *testing.T) {
	orig := fmLookPath
	defer func() { fmLookPath = orig }()

	fmLookPath = func(string) (string, error) { return "/usr/bin/fm", nil }
	for mode, wantName := range map[string]string{"apple": "apple", "apple:pcc": "apple:pcc"} {
		v, err := VisionFor(config.IngestionConfig{Vision: mode}, "darwin")
		if err != nil || v == nil || v.Name() != wantName {
			t.Errorf("VisionFor(%q) = %v, %v; want provider %q", mode, v, err, wantName)
		}
	}

	fmLookPath = func(string) (string, error) { return "", errors.New("not found") }
	if _, err := VisionFor(config.IngestionConfig{Vision: "apple"}, "darwin"); err == nil || !strings.Contains(err.Error(), "fm") {
		t.Errorf("apple without fm must error actionably, got %v", err)
	}
	if _, err := VisionFor(config.IngestionConfig{Vision: "apple"}, "linux"); err == nil {
		t.Error("apple off macOS must error")
	}
	if _, err := VisionFor(config.IngestionConfig{Vision: "apple:weird"}, "darwin"); err == nil {
		t.Error("unknown apple variant must error")
	}
}
