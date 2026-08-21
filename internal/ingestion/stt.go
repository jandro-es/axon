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
	bin := strings.TrimSpace(s.Binary)
	if bin == "" {
		resolved, err := sttLookPath("whisper")
		if err != nil {
			return nil, fmt.Errorf("ingestion.stt: %q needs the whisper binary on PATH (not found) — install whisper.cpp, set ingestion.stt.binary, or use off", mode)
		}
		bin = resolved
	} else if _, err := sttLookPath(bin); err != nil {
		return nil, fmt.Errorf("ingestion.stt: whisper binary %q not executable: %w", bin, err)
	}
	return &whisperSTT{bin: bin, model: model}, nil
}

// whisperSTT shells out to whisper.cpp.
//
// UNVERIFIED: the flags below are whisper.cpp's common CLI shape, but builds
// differ and no test executes the binary — it was not installed when this
// landed. The first person to run this against a real whisper should check
// `whisper --help` and correct them; nothing else in the slice depends on
// their exact form.
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

func (w *whisperSTT) Transcribe(ctx context.Context, path string) (Transcript, error) {
	var out, errBuf bytes.Buffer
	cmd := exec.CommandContext(ctx, w.bin, "-m", w.model, "-f", path, "--output-txt", "--no-timestamps")
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
