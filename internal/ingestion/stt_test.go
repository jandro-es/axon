package ingestion

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/config"
)

func TestSTTForOffReturnsNilNil(t *testing.T) {
	got, err := STTFor(config.IngestionConfig{}, "darwin")
	if err != nil || got != nil {
		t.Fatalf("omitted stt must be (nil, nil), got (%v, %v)", got, err)
	}
	got, err = STTFor(config.IngestionConfig{STT: config.STTConfig{Mode: "off"}}, "linux")
	if err != nil || got != nil {
		t.Fatalf("explicit off must be (nil, nil), got (%v, %v)", got, err)
	}
}

func TestSTTForMissingBinaryIsAnActionableError(t *testing.T) {
	cfg := config.IngestionConfig{STT: config.STTConfig{
		Mode: "whisper:base", Binary: "/nonexistent/whisper"}}
	_, err := STTFor(cfg, "darwin")
	if err == nil {
		t.Fatal("a missing binary must be an error the caller can degrade on")
	}
	if !strings.Contains(err.Error(), "whisper") {
		t.Fatalf("the error must name what is missing: %v", err)
	}
}

func TestSTTForBuildsWhisperProvider(t *testing.T) {
	// A binary that exists is enough to construct; nothing is executed here.
	cfg := config.IngestionConfig{STT: config.STTConfig{
		Mode: "whisper:base", Binary: "/bin/echo"}}
	got, err := STTFor(cfg, "darwin")
	if err != nil {
		t.Fatalf("construct: %v", err)
	}
	if got == nil {
		t.Fatal("a configured provider must not be nil")
	}
}

// Probe must not transcribe: a provider that cannot tell the duration cheaply
// reports (0, nil) meaning "unknown, proceed" rather than erroring.
func TestWhisperProbeUnknownIsNotAnError(t *testing.T) {
	w := &whisperSTT{bin: "/bin/echo", model: "base"}
	d, err := w.Probe(context.Background(), "/nonexistent/audio.m4a")
	if err != nil {
		t.Fatalf("an unknown duration must not be an error: %v", err)
	}
	if d != 0 {
		t.Fatalf("unknown duration must be 0, got %v", d)
	}
}

// whisper.cpp installs as "whisper-cli" (Homebrew, and upstream since it
// renamed from "main"). Defaulting to "whisper" meant a standard install was
// never found — caught only when whisper was actually installed.
func TestSTTForFindsWhisperCLIByName(t *testing.T) {
	orig := sttLookPath
	defer func() { sttLookPath = orig }()
	var tried []string
	sttLookPath = func(name string) (string, error) {
		tried = append(tried, name)
		if name == "whisper-cli" {
			return "/opt/homebrew/bin/whisper-cli", nil
		}
		return "", errors.New("not found")
	}
	got, err := STTFor(config.IngestionConfig{
		STT: config.STTConfig{Mode: "whisper:base"}}, "darwin")
	if err != nil {
		t.Fatalf("a whisper-cli install must be found: %v (tried %v)", err, tried)
	}
	w, ok := got.(*whisperSTT)
	if !ok || w.bin != "/opt/homebrew/bin/whisper-cli" {
		t.Fatalf("resolved binary = %+v, want the whisper-cli path", got)
	}
	if len(tried) == 0 || tried[0] != "whisper-cli" {
		t.Fatalf("whisper-cli must be tried first, got %v", tried)
	}
}

// --output-txt makes whisper.cpp write "<input>.txt" NEXT TO THE SOURCE, which
// for a watched folder would mean writing into the owner's directory — ADR-040
// forbids that. The transcript comes from stdout instead.
func TestWhisperArgsNeverWriteBesideTheSource(t *testing.T) {
	w := &whisperSTT{bin: "/bin/echo", model: "base"}
	args := w.args("/tmp/memo.m4a")
	for _, a := range args {
		if strings.Contains(a, "output") {
			t.Fatalf("no output-file flag may be passed (it writes beside the source): %v", args)
		}
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"-m", "base", "-f", "/tmp/memo.m4a", "--no-timestamps", "--no-prints"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q: %v", want, args)
		}
	}
}
