package ingestion

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/jandro-es/axon/internal/config"
)

// ErrNoSTT, ErrTooLong and ErrTooLarge are non-fatal: each routes to the
// flagged-00-Inbox path, so an audio file that cannot be transcribed is
// archived and recorded rather than failing the run (ADR-042).
var (
	ErrNoSTT    = errors.New("no speech-to-text provider configured")
	ErrTooLong  = errors.New("recording is longer than ingestion.stt.max_minutes")
	ErrTooLarge = errors.New("recording exceeds the size cap")
)

// sttMaxBytes is the mechanical guard, checked before the file is opened.
// Deliberately NOT aligned with max_minutes: it protects memory regardless of
// format, so lossless audio hits it first.
const sttMaxBytes = 500 << 20 // 500 MB

// Transcript is what an STT provider returns. Duration is informational once
// transcription has happened; the duration CAP is enforced earlier, via Probe.
type Transcript struct {
	Text     string
	Duration time.Duration
	Model    string
}

// STT transcribes a local audio file (FR-213, ADR-042).
type STT interface {
	// Probe reports a recording's duration WITHOUT transcribing it, so the
	// duration cap can refuse before any CPU is spent. A provider that cannot
	// tell cheaply returns (0, nil) — "unknown, proceed"; only a real failure
	// returns an error.
	Probe(ctx context.Context, path string) (time.Duration, error)
	Transcribe(ctx context.Context, path string) (Transcript, error)
}

// whisperBinaryNames are tried in order when ingestion.stt.binary is unset.
// "whisper-cli" is what whisper.cpp actually installs as; "whisper" is kept as
// a fallback for a hand-built or symlinked binary.
var whisperBinaryNames = []string{"whisper-cli", "whisper"}

// ResolveWhisperBinary returns the executable to run for a configured
// ingestion.stt.binary (empty = search the known names).
//
// Exported because the doctor `stt` check must resolve it EXACTLY as STTFor
// does — a check that looks for a different binary than the pipeline runs
// reports a healthy install as broken, or worse, the reverse.
func ResolveWhisperBinary(configured string) (string, error) {
	if b := strings.TrimSpace(configured); b != "" {
		resolved, err := sttLookPath(b)
		if err != nil {
			return "", fmt.Errorf("whisper binary %q not executable: %w", b, err)
		}
		return resolved, nil
	}
	var lastErr error
	for _, candidate := range whisperBinaryNames {
		if resolved, err := sttLookPath(candidate); err == nil {
			return resolved, nil
		} else {
			lastErr = err
		}
	}
	return "", fmt.Errorf("whisper.cpp not on PATH — looked for %s (%v)", strings.Join(whisperBinaryNames, ", "), lastErr)
}

// sttLookPath is indirected so tests can stub binary discovery, matching the
// vision/OCR seams.
var sttLookPath = exec.LookPath

// STTFor builds the configured provider, or (nil, nil) when transcription is
// off — the VisionFor contract. A missing binary is an actionable error the
// caller degrades on, never a panic.
func STTFor(cfg config.IngestionConfig, goos string) (STT, error) {
	s := cfg.STT
	mode := s.ModeOr()
	if mode == "off" {
		return nil, nil
	}
	model := s.WhisperModel()
	if model == "" {
		return nil, fmt.Errorf("ingestion.stt: mode %q names no model — use whisper:<model>", mode)
	}
	bin, err := ResolveWhisperBinary(s.Binary)
	if err != nil {
		return nil, fmt.Errorf("ingestion.stt: %q: %w — install it (brew install whisper-cpp), set ingestion.stt.binary, or use off", mode, err)
	}
	return &whisperSTT{bin: bin, model: model}, nil
}

// whisperSTT shells out to whisper.cpp.
//
// Verified against whisper.cpp 1.9.2 (Homebrew) on 2026-08-21 with a real
// recording: the transcript arrives on stdout, and the run leaves no files
// behind. `--output-txt` is deliberately NOT used — it writes a sibling
// "<input>.txt" next to the source, which for a watched folder would mean
// writing into the owner's directory (ADR-040 forbids that). `--no-prints`
// keeps the model-loading chatter off stdout.
type whisperSTT struct {
	bin   string
	model string
}

// Probe reports the duration without transcribing. whisper.cpp has no
// universal duration-only flag, so this is deliberately "unknown": the byte
// cap remains the guard for such files, and returning (0, nil) is the seam's
// documented way to say so. A provider that can answer cheaply — Apple Speech,
// or a build with a probe flag — should override this.
func (w *whisperSTT) Probe(ctx context.Context, path string) (time.Duration, error) {
	return 0, nil
}

// args builds the whisper.cpp invocation. Extracted so a test can assert no
// output-file flag creeps back in without executing the binary.
func (w *whisperSTT) args(path string) []string {
	return []string{"-m", w.model, "-f", path, "--no-timestamps", "--no-prints"}
}

func (w *whisperSTT) Transcribe(ctx context.Context, path string) (Transcript, error) {
	var out, errBuf bytes.Buffer
	cmd := exec.CommandContext(ctx, w.bin, w.args(path)...)
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(errBuf.String())
		if len(detail) > 300 {
			detail = detail[:300]
		}
		return Transcript{}, fmt.Errorf("whisper: %w (%s)", err, detail)
	}
	return Transcript{Text: strings.TrimSpace(out.String()), Model: w.model}, nil
}
