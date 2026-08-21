# Audio ingestion via local STT Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make a local audio file a searchable, citable source note by transcribing it with a local STT provider.

**Architecture:** Four layers, all following shipped patterns. `internal/ingestion` gains `KindAudio` (the `KindImage` pattern), an `STT` seam (the `VisionFor`/`OCRFor` pattern), and one `read()` arm whose output is *text* — so the entire enrich→chunk→embed tail is untouched. `internal/vault` gains a streaming `CopyFile`, because the image path's archive-by-string cannot hold an hour of audio. `internal/config` gains the `stt` block and its validator; `internal/core` gains a doctor check.

**Tech Stack:** Go 1.26+, standard library only. `whisper` is a detected external binary — no Go dependency.

**Spec:** `docs/superpowers/specs/2026-08-21-stt-ingestion-design.md`

## Global Constraints

- **FR IDs:** FR-212 (kind, config, validation, doctor), FR-213 (seam, whisper provider, pipeline stage, both refusals). **ADR-042** covers the decision; do not write another ADR.
- **No migration.** Schema stays `0007`. **No new automation** — built-ins stay 26; `registry_test.go`, `seeds_test.go` and `internal/mcp/tools_more_test.go` must not need touching.
- **No new Go dependency.** whisper is an external binary detected on PATH.
- **Off by default.** `ingestion.stt.mode: "off"` is the default on both profiles; a fresh install transcribes nothing.
- **`KindAudio` must join the `AllowLocalFiles` guard** (`pipeline.go:107`). Without it the `knowledge_ingest` MCP tool — which exists to be called by a model — could transcribe arbitrary host audio.
- **The transcript is the document; audio bytes never flow downstream.** `read()` returns text. Bytes go only to the archive step. Getting this wrong is how audio ends up in a `chunks` row.
- **Two non-fatal refusals:** `ErrNoSTT` (no provider) and `ErrTooLong` (past the cap) both route to the flagged-`00-Inbox` path and return a **successful** run. Never a failure — every dropped audio file becoming a recorded failure would make capture and watch-folders noisy for everyone who has not configured STT, which is everyone by default.
- **Two caps, deliberately unaligned:** `sttMaxBytes` (Go const, 500 MB) checked by `os.Stat` before opening; `stt.max_minutes` (config, default 120) checked via `STT.Probe` before transcribing. The byte cap bites first for lossless audio, so **the flagged note must say which cap refused it**.
- **Reuse `attachmentPath` and `AttachmentsDir` unchanged.** Do **not** migrate the image path to `CopyFile` — it works, and it is not this slice's business.
- **Go hygiene:** `gofmt`/`goimports` clean, `go vet` and `golangci-lint` green, errors wrapped with `%w`, `context.Context` propagated. Cleanup-path calls returning errors need `_ =` or errcheck fails.
- **Run `go test -race ./...` before pushing** — CI runs the race detector; the local gate does not.
- **Never bind port 7777**; smoke uses 7799 and isolates `vault_path` and `data_dir`, not just `AXON_HOME`.

---

### Task 1: `KindAudio` classification + the AllowLocalFiles guard (FR-212)

**Files:**
- Modify: `internal/ingestion/input.go` (the `InputKind` consts ~line 12, `audioExts` beside `imageExts` ~line 28, the `ClassifyInput` switch ~line 54)
- Modify: `internal/ingestion/pipeline.go:107` (the guard's case list)
- Test: `internal/ingestion/input_test.go` (append)

**Interfaces:**
- Produces: `ingestion.KindAudio InputKind = "audio"` and `audioExts`. Every later task branches on `KindAudio`.

- [ ] **Step 1: Write the failing test**

Append to `internal/ingestion/input_test.go`:

```go
func TestClassifyInputAudio(t *testing.T) {
	for _, ext := range []string{".m4a", ".mp3", ".wav", ".aac", ".flac", ".ogg", ".opus"} {
		in := ClassifyInput("/tmp/recording"+ext, nil, false)
		if in.Kind != KindAudio {
			t.Errorf("%s classified as %q, want audio", ext, in.Kind)
		}
	}
	// Case-insensitive, like imageExts.
	if got := ClassifyInput("/tmp/Recording.M4A", nil, false); got.Kind != KindAudio {
		t.Errorf("uppercase extension classified as %q, want audio", got.Kind)
	}
	// Unrelated extensions are unaffected.
	if got := ClassifyInput("/tmp/notes.txt", nil, false); got.Kind != KindFile {
		t.Errorf("txt classified as %q, want file", got.Kind)
	}
	if got := ClassifyInput("/tmp/scan.png", nil, false); got.Kind != KindImage {
		t.Errorf("png classified as %q, want image", got.Kind)
	}
	// A URL is still a URL even with an audio-looking path.
	if got := ClassifyInput("https://example.com/ep.mp3", nil, false); got.Kind != KindURL {
		t.Errorf("http url classified as %q, want url", got.Kind)
	}
}
```

Check how `imageExts` handles case first — `grep -n "ToLower\|filepathExt" internal/ingestion/input.go` — and match it rather than adding a second convention.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ingestion/ -run TestClassifyInputAudio -v`
Expected: FAIL — `undefined: KindAudio`.

- [ ] **Step 3: Implement**

In `internal/ingestion/input.go`, add the const beside the others:

```go
	KindAudio InputKind = "audio"
```

Add the extension set beside `imageExts`:

```go
// audioExts are the lowercase extensions classified as KindAudio (FR-212).
var audioExts = map[string]bool{
	".m4a": true, ".mp3": true, ".wav": true, ".aac": true,
	".flac": true, ".ogg": true, ".opus": true,
}
```

Add the switch arm in `ClassifyInput`, after the image case:

```go
	case audioExts[ext]:
		kind = KindAudio
```

- [ ] **Step 4: Add `KindAudio` to the AllowLocalFiles guard**

In `internal/ingestion/pipeline.go:107`, extend the case list:

```go
	case KindFile, KindPDF, KindImage, KindAudio:
		if !opts.AllowLocalFiles {
			return res, fmt.Errorf("local-file ingestion of %q is not permitted on this path (agent-driven ingestion is URL-only)", arg)
		}
```

Add a test asserting that guard, since it is the security-relevant half:

```go
func TestAudioIngestRefusedWithoutAllowLocalFiles(t *testing.T) {
	p := &Pipeline{}
	_, err := p.Ingest(context.Background(), "/tmp/secret.m4a", IngestOptions{AllowLocalFiles: false})
	if err == nil || !strings.Contains(err.Error(), "not permitted") {
		t.Fatalf("agent-driven audio ingestion must be refused, got %v", err)
	}
}
```

If `Pipeline{}` with no fields panics before reaching the guard, construct it the way the package's existing pipeline tests do (`grep -n "Pipeline{" internal/ingestion/*_test.go | head -3`) — the assertion must exercise the guard, not a nil dereference.

- [ ] **Step 5: Run tests and commit**

Run: `go test ./internal/ingestion/ && gofmt -l internal/ingestion`
Expected: PASS, silent.

```bash
git add internal/ingestion/input.go internal/ingestion/input_test.go internal/ingestion/pipeline.go
git commit -m "feat(ingestion): KindAudio classification behind the AllowLocalFiles guard (FR-212)"
```

---

### Task 2: The `stt` config block, validator and doctor check (FR-212)

**Files:**
- Modify: `internal/config/types.go` (`STTConfig`, and an `STT` field on `IngestionConfig` beside `Vision` ~line 201)
- Create: `internal/config/stt.go` (accessors + `validateSTT`)
- Modify: `internal/config/load.go` (call it beside `validateVision`)
- Modify: `internal/core/doctor.go` (the check, appended beside the other ingestion checks)
- Test: `internal/config/stt_test.go`, `internal/core/doctor_test.go` (append)

**Interfaces:**
- Produces: `config.STTConfig{Mode, Binary string; MaxMinutes int}`, `IngestionConfig.STT STTConfig`, `(STTConfig) ModeOr() string`, `(STTConfig) MaxMinutesOr() int`, `validateSTT(cfg IngestionConfig) error`, and `core.sttCheck(p config.Profile) Check`. Task 4 reads the config.

- [ ] **Step 1: Write the failing test**

Create `internal/config/stt_test.go`:

```go
package config

import (
	"strings"
	"testing"
)

func TestValidateSTT(t *testing.T) {
	ok := func(mode string, mins int) IngestionConfig {
		return IngestionConfig{STT: STTConfig{Mode: mode, MaxMinutes: mins}}
	}
	// Off (explicit and by omission) is valid and is the default.
	if err := validateSTT(IngestionConfig{}); err != nil {
		t.Fatalf("omitted stt must be valid: %v", err)
	}
	if err := validateSTT(ok("off", 0)); err != nil {
		t.Fatalf("off must be valid: %v", err)
	}
	if err := validateSTT(ok("whisper:base", 120)); err != nil {
		t.Fatalf("whisper:base must be valid: %v", err)
	}

	cases := []struct{ name, mode, want string }{
		{"unknown provider", "vosk:small", "off or whisper:"},
		{"bare whisper", "whisper", "off or whisper:"},
		{"empty model", "whisper:", "model"},
	}
	for _, c := range cases {
		err := validateSTT(ok(c.mode, 60))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want error containing %q, got %v", c.name, c.want, err)
		}
	}
	for _, mins := range []int{-1, 1441} {
		if err := validateSTT(ok("whisper:base", mins)); err == nil {
			t.Errorf("max_minutes %d must be refused", mins)
		}
	}
}

func TestSTTDefaults(t *testing.T) {
	if got := (STTConfig{}).ModeOr(); got != "off" {
		t.Errorf("default mode = %q, want off", got)
	}
	if got := (STTConfig{}).MaxMinutesOr(); got != 120 {
		t.Errorf("default max_minutes = %d, want 120", got)
	}
	if got := (STTConfig{MaxMinutes: 30}).MaxMinutesOr(); got != 30 {
		t.Errorf("explicit max_minutes = %d, want 30", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run 'TestValidateSTT|TestSTTDefaults' -v`
Expected: FAIL — `undefined: STTConfig`.

- [ ] **Step 3: Add the types and validator**

In `internal/config/types.go`, beside the `Vision` field on `IngestionConfig`:

```go
	// STT configures local speech-to-text for KindAudio ingestion (FR-212,
	// ADR-042). Off by default on both profiles.
	STT STTConfig `yaml:"stt,omitempty"`
```

Create `internal/config/stt.go`:

```go
package config

import (
	"fmt"
	"strings"
)

// STTConfig configures local speech-to-text (FR-212, ADR-042).
type STTConfig struct {
	// Mode is "off" (default) or "whisper:<model>".
	Mode string `yaml:"mode,omitempty"`
	// Binary optionally pins the executable; empty looks up "whisper" on PATH,
	// the OCR/vision helper convention.
	Binary string `yaml:"binary,omitempty"`
	// MaxMinutes refuses recordings longer than this (default 120). Config
	// rather than a Go const because transcription speed varies enormously by
	// machine and model, so the right ceiling genuinely differs per install.
	MaxMinutes int `yaml:"max_minutes,omitempty"`
}

// ModeOr returns the configured mode, defaulting to off.
func (s STTConfig) ModeOr() string {
	if strings.TrimSpace(s.Mode) == "" {
		return "off"
	}
	return s.Mode
}

// MaxMinutesOr returns the duration cap, defaulting to 120.
func (s STTConfig) MaxMinutesOr() int {
	if s.MaxMinutes == 0 {
		return 120
	}
	return s.MaxMinutes
}

// WhisperModel returns the model named by a "whisper:<model>" mode, or "".
func (s STTConfig) WhisperModel() string {
	mode := s.ModeOr()
	if !strings.HasPrefix(mode, "whisper:") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(mode, "whisper:"))
}

// validateSTT applies the ADR-042 rules, run beside validateVision.
func validateSTT(cfg IngestionConfig) error {
	s := cfg.STT
	mode := s.ModeOr()
	if mode != "off" {
		if !strings.HasPrefix(mode, "whisper:") {
			return fmt.Errorf("ingestion.stt.mode must be off or whisper:<model> (got %q)", s.Mode)
		}
		if s.WhisperModel() == "" {
			return fmt.Errorf("ingestion.stt.mode %q names no model — use whisper:<model>, e.g. whisper:base", s.Mode)
		}
	}
	if s.MaxMinutes < 0 || s.MaxMinutes > 1440 {
		return fmt.Errorf("ingestion.stt.max_minutes must be 0–1440 (got %d; 0 means the default 120)", s.MaxMinutes)
	}
	return nil
}
```

Wire it into `internal/config/load.go` beside the `validateVision` call:

```go
		if err := validateSTT(p.Ingestion); err != nil {
			return fmt.Errorf("config validation failed: profile %q: %w", name, err)
		}
```

Check `validateVision`'s exact call shape first (`grep -n "validateVision" internal/config/load.go`) and match it — it may take the whole `Profile`.

- [ ] **Step 4: Add the doctor check**

Append the test to `internal/core/doctor_test.go`:

```go
func TestSTTCheck(t *testing.T) {
	off := sttCheck(config.Profile{})
	if off.Status != StatusOK || !strings.Contains(off.Detail, "off") {
		t.Fatalf("default should read as off: %+v", off)
	}
	if off.Fix != "" {
		t.Fatalf("an off check has nothing to fix: %+v", off)
	}

	// Configured but the binary is absent: warn, with a Fix so self-check
	// (FR-207) files it.
	missing := config.Profile{Ingestion: config.IngestionConfig{
		STT: config.STTConfig{Mode: "whisper:base", Binary: "/nonexistent/whisper"}}}
	warn := sttCheck(missing)
	if warn.Status != StatusWarn {
		t.Fatalf("a missing binary must warn: %+v", warn)
	}
	if warn.Fix == "" {
		t.Fatal("the warning must carry a Fix")
	}
	if !strings.Contains(warn.Detail, "base") {
		t.Fatalf("the detail should name the model: %+v", warn)
	}
}
```

Add to `internal/core/doctor.go`:

```go
// sttCheck reports on local speech-to-text (FR-212). The warn path carries a
// Fix, so self-check (FR-207) files it.
func sttCheck(p config.Profile) Check {
	const name = "stt"
	s := p.Ingestion.STT
	if s.ModeOr() == "off" {
		return Check{Name: name, Status: StatusOK,
			Detail: "off (set ingestion.stt.mode to whisper:<model> to transcribe audio files)"}
	}
	bin := s.Binary
	if strings.TrimSpace(bin) == "" {
		bin = "whisper"
	}
	resolved, err := lookPath(bin)
	if err != nil {
		return Check{Name: name, Status: StatusWarn,
			Detail: fmt.Sprintf("stt is %s but %q was not found — audio files will be captured untranscribed", s.ModeOr(), bin),
			Fix:    "install whisper.cpp and put its binary on PATH, or set ingestion.stt.binary"}
	}
	return Check{Name: name, Status: StatusOK,
		Detail: fmt.Sprintf("%s ready (%s), max %d min", s.ModeOr(), resolved, s.MaxMinutesOr())}
}
```

`lookPath` is the package's existing indirected `exec.LookPath` (`doctor.go:68`) — use it, so tests can stub discovery. Append `checks = append(checks, sttCheck(p))` beside the other profile-scoped ingestion checks.

- [ ] **Step 5: Run tests and commit**

Run: `go test ./internal/config/ ./internal/core/ && gofmt -l internal/config internal/core && go vet ./internal/config/ ./internal/core/`
Expected: PASS, silent, clean.

```bash
git add internal/config/types.go internal/config/stt.go internal/config/stt_test.go internal/config/load.go internal/core/doctor.go internal/core/doctor_test.go
git commit -m "feat(config): ingestion.stt block, validator and doctor check (FR-212)"
```

---

### Task 3: `vault.CopyFile` — streaming archive (FR-213)

**Files:**
- Create: `internal/vault/copy.go`
- Test: `internal/vault/copy_test.go`

**Interfaces:**
- Consumes: `(*FS).safeAbs` and `checkNoSymlinkEscape` (`internal/vault/fs.go:62,84`) — every vault writer goes through them.
- Produces: `func (v *FS) CopyFile(destRel, srcPath string) error`. Task 5 calls it.

- [ ] **Step 1: Write the failing test**

Create `internal/vault/copy_test.go`:

```go
package vault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCopyFile(t *testing.T) {
	vaultDir := t.TempDir()
	v := NewFS(vaultDir)
	src := filepath.Join(t.TempDir(), "recording.m4a")
	payload := []byte("not really audio, but bytes are bytes")
	if err := os.WriteFile(src, payload, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := v.CopyFile("03-Resources/Knowledge/attachments/abc.m4a", src); err != nil {
		t.Fatalf("copy: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(vaultDir, "03-Resources/Knowledge/attachments/abc.m4a"))
	if err != nil {
		t.Fatalf("destination not written: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("bytes differ: %q", got)
	}
	// The source is untouched — a copy, never a move.
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("source must survive: %v", err)
	}
}

func TestCopyFileRefusesOverwrite(t *testing.T) {
	v := NewFS(t.TempDir())
	src := filepath.Join(t.TempDir(), "a.m4a")
	if err := os.WriteFile(src, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := v.CopyFile("attachments/a.m4a", src); err != nil {
		t.Fatal(err)
	}
	if err := v.CopyFile("attachments/a.m4a", src); err == nil {
		t.Fatal("overwriting an existing attachment must be refused")
	}
}

func TestCopyFileRefusesEscape(t *testing.T) {
	v := NewFS(t.TempDir())
	src := filepath.Join(t.TempDir(), "a.m4a")
	if err := os.WriteFile(src, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"../outside.m4a", "/etc/evil.m4a"} {
		if err := v.CopyFile(bad, src); err == nil {
			t.Errorf("destination %q must be refused", bad)
		}
	}
}

func TestCopyFileMissingSource(t *testing.T) {
	v := NewFS(t.TempDir())
	err := v.CopyFile("attachments/a.m4a", filepath.Join(t.TempDir(), "nope.m4a"))
	if err == nil || !strings.Contains(err.Error(), "source") {
		t.Fatalf("a missing source must be a clear error, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/vault/ -run TestCopyFile -v`
Expected: FAIL — `v.CopyFile undefined`.

- [ ] **Step 3: Implement**

Create `internal/vault/copy.go`:

```go
package vault

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// CopyFile streams srcPath into the vault at destRel (FR-213, ADR-042).
//
// It exists because the image archive path holds a whole file in memory as a
// string (`Vault.Create(path, string(bytes))`) — fine for a screenshot,
// untenable for an hour of .wav. Binary attachments stream; text notes keep
// using Create.
//
// Copies, never moves: the owner's file stays where it is. Refuses to
// overwrite, so a content-hash collision can never silently replace an
// existing attachment. destRel goes through the same safeAbs/symlink guards
// as every other vault writer.
func (v *FS) CopyFile(destRel, srcPath string) error {
	abs, err := v.safeAbs(destRel)
	if err != nil {
		return err
	}
	if err := v.checkNoSymlinkEscape(abs); err != nil {
		return err
	}
	in, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("copy into vault: source %q: %w", filepath.Base(srcPath), err)
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fmt.Errorf("copy into vault: create %q: %w", destRel, err)
	}
	// O_EXCL: refuse to overwrite an existing attachment.
	out, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("copy into vault: destination %q: %w", destRel, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(abs)
		return fmt.Errorf("copy into vault: %q: %w", destRel, err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(abs)
		return fmt.Errorf("copy into vault: close %q: %w", destRel, err)
	}
	return nil
}
```

`checkNoSymlinkEscape` may take different arguments — check `grep -n "func (v \*FS) checkNoSymlinkEscape" internal/vault/fs.go` and match. If `safeAbs` already covers the symlink case for a not-yet-existing path, drop the second call rather than inventing a use for it.

- [ ] **Step 4: Run tests and commit**

Run: `go test ./internal/vault/ -v 2>&1 | tail -15 && gofmt -l internal/vault`
Expected: PASS for the whole package, silent.

```bash
git add internal/vault/copy.go internal/vault/copy_test.go
git commit -m "feat(vault): CopyFile streams binary attachments into the vault (FR-213)"
```

---

### Task 4: The STT seam and the whisper provider (FR-213)

**Files:**
- Create: `internal/ingestion/stt.go`
- Test: `internal/ingestion/stt_test.go`

**Interfaces:**
- Consumes: `config.IngestionConfig` and the `STTConfig` accessors (Task 2).
- Produces: `ingestion.Transcript{Text string; Duration time.Duration; Model string}`, `ingestion.STT` interface with `Probe(ctx, path) (time.Duration, error)` and `Transcribe(ctx, path) (Transcript, error)`, and `ingestion.STTFor(cfg config.IngestionConfig, goos string) (STT, error)` returning `nil, nil` when off. Task 5 wires it into the pipeline.

- [ ] **Step 1: Write the failing test**

Create `internal/ingestion/stt_test.go`:

```go
package ingestion

import (
	"context"
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ingestion/ -run 'TestSTTFor|TestWhisperProbe' -v`
Expected: FAIL — `undefined: STTFor`.

- [ ] **Step 3: Implement**

Read `internal/ingestion/vision.go` first and mirror its structure — this file should look like its sibling, not like a new invention.

Create `internal/ingestion/stt.go`:

```go
package ingestion

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/jandro-es/axon/internal/config"
)

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
type whisperSTT struct {
	bin   string
	model string
}

// Probe asks whisper for the duration without transcribing. whisper.cpp has no
// universal duration-only flag, so this is best-effort: anything it cannot
// determine is reported as unknown (0, nil) and the byte cap remains the only
// guard for that file.
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
```

**Verify the whisper invocation before trusting it.** The flags above are the common whisper.cpp CLI shape, but they vary by build. If `whisper` is installed, run `whisper --help` and adjust; if it is not, leave the flags and say so in the commit message so the smoke task checks them rather than assuming.

- [ ] **Step 4: Run tests and commit**

Run: `go test ./internal/ingestion/ -run 'TestSTTFor|TestWhisperProbe' -v && gofmt -l internal/ingestion && go vet ./internal/ingestion/`
Expected: PASS, silent, clean.

```bash
git add internal/ingestion/stt.go internal/ingestion/stt_test.go
git commit -m "feat(ingestion): STT seam and whisper provider (FR-213)"
```

---

### Task 5: The pipeline stage and the two refusals (FR-213)

**Files:**
- Modify: `internal/ingestion/pipeline.go` — the `Pipeline` struct (add `STT`), `read()`, the archive step (~line 333), `writeCapturedNote` (~line 513, parameterised)
- Modify: `cmd/axon/deps.go` (build the provider into the pipeline, beside `OCRFor`/`VisionFor`)
- Test: `internal/ingestion/pipeline_test.go` (append)

**Interfaces:**
- Consumes: `STT`, `Transcript`, `STTFor` (Task 4); `vault.CopyFile` (Task 3); `KindAudio` (Task 1); `config.STTConfig` accessors (Task 2).
- Produces: `ErrNoSTT`, `ErrTooLong`, `ErrTooLarge`, and `Pipeline.STT`. Nothing later depends on them.

- [ ] **Step 1: Parameterise `writeCapturedNote`**

It currently hard-codes `"no captions available"` and the `00-Inbox/media-` prefix, so it cannot serve audio as-is. Change the signature:

```go
// writeCapturedNote archives the source and writes a flagged 00-Inbox note —
// the shipped "arrived but could not be processed" path. reason is recorded in
// the note and the result; prefix names the note file ("media", "audio").
func (p *Pipeline) writeCapturedNote(in Input, reason, prefix string) (IngestResult, error) {
```

Inside, replace the hard-coded string and prefix with the parameters, and update its one existing caller (`pipeline.go:117`) to pass `"no captions available", "media"` so H1's behaviour is byte-identical. Run `go test ./internal/ingestion/` — the existing caption test must still pass unchanged.

- [ ] **Step 2: Write the failing tests**

Append to `internal/ingestion/pipeline_test.go`. Check how existing pipeline tests construct a `Pipeline` and a fake vault, and follow that exactly:

```go
type fakeSTT struct {
	text     string
	duration time.Duration
	err      error
}

func (f fakeSTT) Probe(context.Context, string) (time.Duration, error) { return f.duration, nil }
func (f fakeSTT) Transcribe(context.Context, string) (Transcript, error) {
	if f.err != nil {
		return Transcript{}, f.err
	}
	return Transcript{Text: f.text, Duration: f.duration, Model: "fake"}, nil
}

// The core promise: a transcript flows through the UNCHANGED tail, and the
// audio bytes reach the attachment and nothing else.
func TestIngestAudioTranscribesAndArchives(t *testing.T) {
	p, v := newTestPipeline(t) // match the package's existing helper
	p.STT = fakeSTT{text: "quantum entanglement and flux pinning", duration: time.Minute}
	src := writeTempFile(t, "memo.m4a", "fake audio bytes")

	res, err := p.Ingest(context.Background(), src, IngestOptions{AllowLocalFiles: true})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if res.Status != "ok" {
		t.Fatalf("status = %q, want ok", res.Status)
	}
	note, err := v.Read(context.Background(), res.NotePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note.Body, "flux pinning") {
		t.Fatalf("the transcript must be the note body:\n%s", note.Body)
	}
	if strings.Contains(note.Body, "fake audio bytes") {
		t.Fatal("audio bytes must never reach the note — only the transcript does")
	}
	if res.Chunks == 0 {
		t.Fatal("the transcript must be chunked like any other text")
	}
	// The audio is archived, and the attachment is the ONLY place the bytes go.
	if !strings.Contains(note.Body, "attachments/") {
		t.Fatalf("the note should reference the archived audio:\n%s", note.Body)
	}
}

func TestIngestAudioWithoutProviderCapturesInsteadOfFailing(t *testing.T) {
	p, v := newTestPipeline(t)
	p.STT = nil // transcription off
	src := writeTempFile(t, "memo.m4a", "fake audio bytes")

	res, err := p.Ingest(context.Background(), src, IngestOptions{AllowLocalFiles: true})
	if err != nil {
		t.Fatalf("no provider must NOT be a failure: %v", err)
	}
	if res.Status != "captured" {
		t.Fatalf("status = %q, want captured", res.Status)
	}
	if !strings.HasPrefix(res.NotePath, "00-Inbox/audio-") {
		t.Fatalf("want a flagged 00-Inbox audio note, got %q", res.NotePath)
	}
	note, _ := v.Read(context.Background(), res.NotePath)
	if !strings.Contains(strings.ToLower(note.Body), "transcri") {
		t.Fatalf("the note must say why it was not transcribed:\n%s", note.Body)
	}
}

func TestIngestAudioOverDurationCapIsCaptured(t *testing.T) {
	p, v := newTestPipeline(t)
	p.Ingestion.STT.MaxMinutes = 1
	p.STT = fakeSTT{text: "should never be used", duration: 90 * time.Minute}
	src := writeTempFile(t, "long.m4a", "fake audio bytes")

	res, err := p.Ingest(context.Background(), src, IngestOptions{AllowLocalFiles: true})
	if err != nil {
		t.Fatalf("over-cap must NOT be a failure: %v", err)
	}
	if res.Status != "captured" {
		t.Fatalf("status = %q, want captured", res.Status)
	}
	note, _ := v.Read(context.Background(), res.NotePath)
	// The note must say WHICH cap refused it — the byte and duration caps are
	// deliberately unaligned, so "too big" and "too long" are different facts.
	if !strings.Contains(note.Body, "90") && !strings.Contains(strings.ToLower(note.Body), "long") {
		t.Fatalf("the note must name the duration and the cap:\n%s", note.Body)
	}
}
```

`p.Ingestion` assumes the pipeline holds the ingestion config; if it does not, thread `MaxMinutes` however the pipeline already reads config (`grep -n "Ingestion\|MediaHosts" internal/ingestion/pipeline.go | head -5`) and adjust.

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/ingestion/ -run TestIngestAudio -v`
Expected: FAIL — audio is classified but not handled, so it falls through to the generic file path.

- [ ] **Step 4: Implement**

Add to the `Pipeline` struct: `STT STT` and, if not already present, whatever field carries `config.IngestionConfig`.

Add the sentinel errors beside `ErrNoCaptions`:

```go
// ErrNoSTT and ErrTooLong/ErrTooLarge are non-fatal: each routes to the
// flagged-00-Inbox path, so an audio file that cannot be transcribed is
// archived and recorded rather than failing the run (ADR-042).
var (
	ErrNoSTT    = errors.New("no speech-to-text provider configured")
	ErrTooLong  = errors.New("recording exceeds ingestion.stt.max_minutes")
	ErrTooLarge = errors.New("recording exceeds the size cap")
)

// sttMaxBytes is the mechanical guard, checked before the file is opened.
// Deliberately not aligned with max_minutes: it protects memory regardless of
// format, so lossless audio hits it first.
const sttMaxBytes = 500 << 20 // 500 MB
```

Add the `KindAudio` arm to `read()`:

```go
	case KindAudio:
		if p.STT == nil {
			return nil, ErrNoSTT
		}
		st, err := os.Stat(in.Path)
		if err != nil {
			return nil, fmt.Errorf("read audio %q: %w", in.Path, err)
		}
		if st.Size() > sttMaxBytes {
			return nil, fmt.Errorf("%w: %d MB", ErrTooLarge, st.Size()>>20)
		}
		if d, perr := p.STT.Probe(ctx, in.Path); perr == nil && d > 0 {
			if cap := time.Duration(p.sttMaxMinutes()) * time.Minute; d > cap {
				return nil, fmt.Errorf("%w: %s exceeds %s", ErrTooLong, d.Round(time.Second), cap)
			}
		}
		tr, err := p.STT.Transcribe(ctx, in.Path)
		if err != nil {
			return nil, fmt.Errorf("transcribe %q: %w", filepath.Base(in.Path), err)
		}
		return &Document{URL: "file://" + in.Path, Body: []byte(tr.Text), FetchedAt: time.Now().UTC()}, nil
```

Extend the `Ingest` error branch that already handles `ErrNoCaptions` (`pipeline.go:116`):

```go
	if err != nil {
		if in.Kind == KindMedia && errors.Is(err, ErrNoCaptions) {
			return p.writeCapturedNote(in, "no captions available", "media")
		}
		if in.Kind == KindAudio {
			switch {
			case errors.Is(err, ErrNoSTT):
				return p.writeCapturedNote(in, "no speech-to-text provider configured — set ingestion.stt.mode", "audio")
			case errors.Is(err, ErrTooLong), errors.Is(err, ErrTooLarge):
				return p.writeCapturedNote(in, err.Error(), "audio")
			}
		}
		return res, err
	}
```

Archive the audio beside the image archive (~line 333), using the streaming copy:

```go
	// Archive the source audio by streaming — never as a string, which would
	// hold the whole recording in memory (ADR-042).
	if in.Kind == KindAudio {
		if err := p.Vault.CopyFile(attachmentPath(hash, in.Path), in.Path); err != nil {
			return err
		}
	}
```

`writeCapturedNote` must also archive the audio for the refusal paths — check whether it already archives for media and extend it the same way, so a captured audio file is never lost.

Finally add the helper:

```go
func (p *Pipeline) sttMaxMinutes() int { return p.Ingestion.STT.MaxMinutesOr() }
```

- [ ] **Step 5: Wire the provider in the daemon**

In `cmd/axon/deps.go`, beside `OCRFor`/`VisionFor`:

```go
	stt, _ := ingestion.STTFor(d.profile.Ingestion, runtime.GOOS) // off/misconfig → nil; doctor surfaces it
```

and add `STT: stt` to the `ingestion.Pipeline{...}` literal. The discarded error matches the OCR/vision convention exactly — the doctor `stt` check is what tells the owner.

- [ ] **Step 6: Full gate and commit**

```bash
go build ./... && go test -race ./... && golangci-lint run
git add internal/ingestion/ cmd/axon/deps.go
git commit -m "feat(ingestion): transcribe KindAudio; capture instead of failing when it cannot (FR-213)"
```

Expected: whole suite green, 0 lint issues, and **the built-in automation count untouched**.

---

### Task 6: Documentation

**Files:** `docs/03-requirements.md`, `docs/02-architecture.md` (ADR-042 planned → built), `docs/04-data-model-and-config.md`, `docs/05-component-knowledge-ingestion.md`, `docs/GUIDE.md`, `axon.config.example.yaml`, `docs/19-roadmap-second-brain.md`, `CHANGELOG.md`, `CLAUDE.md`

- [ ] **Step 1: FR rows**

After the FR-211 row in `docs/03-requirements.md`, add a section and two rows:

```markdown
### Audio ingestion via local STT (docs/19 B1) *(built 2026-08-21)*

FR-212…FR-213 trace to **ADR-042** and graduate `docs/19` B1; spec in
`docs/superpowers/specs/2026-08-21-stt-ingestion-design.md`. No migration, no
new automation.

| ID | Pri | Requirement |
|----|-----|-------------|
| FR-212 | M | **`KindAudio`, the `stt` config block, validation and doctor (ADR-042).** `.m4a .mp3 .wav .aac .flac .ogg .opus` classify as `KindAudio` and join the `AllowLocalFiles` guard, so the `knowledge_ingest` MCP tool — which exists to be called by a model — cannot transcribe arbitrary host audio. `ingestion.stt` carries `mode` (`off` default, or `whisper:<model>`), an optional `binary` override, and `max_minutes` (default 120; config rather than a Go const because transcription speed varies enormously by machine and model). `validateSTT` runs beside `validateVision` and refuses an unknown provider, a mode naming no model, and `max_minutes` outside 0–1440. An `stt` doctor check reports off / ready-with-binary-and-cap / a warn when the binary is missing, carrying a `Fix` so `self-check` (FR-207) files it. |
| FR-213 | M | **The STT seam, the whisper provider, and the pipeline stage (ADR-042).** `ingestion.STT` has `Probe` (duration without transcribing, so the cap refuses before CPU is spent; `(0, nil)` means unknown) and `Transcribe`; `STTFor` returns `nil, nil` when off — the `VisionFor` contract — so Apple Speech can land later behind the same seam. `read()`'s `KindAudio` arm checks `sttMaxBytes` (Go const, 500 MB, before opening) then the duration cap, then returns the **transcript as the document body** — the whole enrich→chunk→embed tail is unchanged, so redaction, citations and search work with no new code, and **audio bytes never reach a `chunks` row**. The source is archived via the new streaming `vault.CopyFile`, because the image path's `Create(path, string(bytes))` holds a whole file in memory. Two non-fatal refusals — `ErrNoSTT` and `ErrTooLong`/`ErrTooLarge` — route to `writeCapturedNote` (now parameterised by reason and filename prefix), archiving the audio and writing a flagged `00-Inbox` note with a **successful** run: a recorded failure per dropped audio file would make capture and watch-folders noisy for everyone who has not configured STT. |
```

- [ ] **Step 2: Flip ADR-042 to built**

Change `*(accepted — planned)*` to `*(accepted — built)*`.

- [ ] **Step 3: Config reference and example**

Document `ingestion.stt` in `docs/04-data-model-and-config.md`. In `axon.config.example.yaml`, inside the `ingestion:` block:

```yaml
      # stt:                                  # local speech-to-text (ADR-042)
      #   mode: "whisper:base"                # off (default) | whisper:<model>
      #   max_minutes: 120                    # refuse longer recordings
      #   binary: ""                          # optional path; empty = "whisper" on PATH
      # Off by default. Audio files (.m4a .mp3 .wav .aac .flac .ogg .opus) are
      # transcribed locally and the transcript becomes an ordinary source note;
      # the recording is archived under Knowledge/attachments. With stt off, or
      # a recording over the cap, the file is archived and a flagged 00-Inbox
      # note records why — never a failure.
```

Verify: `go run ./cmd/axon config validate --config axon.config.example.yaml`.

- [ ] **Step 4: Component docs, GUIDE, roadmap**

- `docs/05-component-knowledge-ingestion.md`: audio as a fourth local-file kind in the entry-points list added by the watch-folders slice.
- `docs/GUIDE.md`: a short "Ingest a voice memo" section — install whisper.cpp, set two keys, `axon ingest memo.m4a`, note the caps and the archived original.
- `docs/19-roadmap-second-brain.md` B1 → shipped, resolving all three open decisions (**whisper.cpp first**, Apple Speech later behind the same seam; **diarisation out**, as the entry suspected; **a config duration cap**) and recording the archiving finding — that the image pattern's archive-by-string does not transfer, so this added `vault.CopyFile`. Update the sequencing sketch, which names B1 as the biggest remaining unlock, and note that **B2 (meeting notes with action extraction) is now unblocked**.

- [ ] **Step 5: CHANGELOG and CLAUDE.md**

`CHANGELOG.md` under `[Unreleased]` → `### Added`:

```markdown
- **Voice memos become searchable notes.** (FR-212, FR-213, **ADR-042**; no
  schema change.) Point `ingestion.stt.mode` at a local whisper.cpp model and
  `axon ingest memo.m4a` — or a recording dropped in a watched folder —
  transcribes on your machine and becomes an ordinary source note: searchable,
  citable, redacted like any other text. The recording is archived beside it.
  Off by default; with STT off, or a recording past the duration cap, the file
  is archived and a flagged inbox note records why, rather than failing.
```

`CLAUDE.md`: FR range → `FR-01…FR-213`, ADR range → `ADR-001…042`, plus a line recording the slice.

- [ ] **Step 6: Final gate and commit**

```bash
gofmt -l . && go build ./... && go test -race ./... && golangci-lint run
git add -A && git commit -m "docs: FR-212/FR-213, ADR-042 built, stt config reference"
```

---

### Task 7: Live smoke

**Files:** `<scratchpad>/stt-smoke/` (throwaway)

- [ ] **Step 1: Establish whether whisper is available**

```bash
command -v whisper && whisper --help 2>&1 | head -20
```

**This determines what the smoke can prove.** If whisper is present, verify Task 4's flags against its actual `--help` and fix them if they differ — that is the one part of this slice no unit test covers. If it is absent, say so plainly in the completion report and smoke only the paths that do not need it (classification, the off-path capture note, doctor's warn state, config validation). **Do not report the transcription path as verified if whisper was never run.**

- [ ] **Step 2: Build and isolate**

```bash
cd /Users/jandro/Projects/axon/web && npm run build
cd /Users/jandro/Projects/axon
S=/private/tmp/claude-501/-Users-jandro-Projects-axon/2535b695-9eab-42af-be3a-0a30892551fc/scratchpad/stt-smoke
mkdir -p "$S/home/profiles/personal" "$S/vault"
go build -o "$S/axon" ./cmd/axon
cp axon.config.example.yaml "$S/home/config.yaml"
sed -i '' 's/port: 7777/port: 7799/g' "$S/home/config.yaml"
grep -rn 7777 "$S" && echo "STOP: 7777 present" || echo "port clean"
```

`cd` back to the repo root by absolute path before `go build`. Point `vault_path` at `$S/vault` and `data_dir` at `$S/home/profiles/personal`.

- [ ] **Step 3: Smoke the off path (always possible)**

With `stt.mode` absent, ingest any small audio-extension file:

```bash
export AXON_HOME="$S/home"
printf 'not audio' > "$S/memo.m4a"
"$S/axon" ingest "$S/memo.m4a" --config "$S/home/config.yaml"
ls "$S/vault/00-Inbox/"                 # expect a flagged audio-*.md note
find "$S/vault" -path "*attachments*"   # expect the archived original
"$S/axon" doctor --config "$S/home/config.yaml" | grep -i stt   # expect "off"
```

Verify the run **succeeded** — this is the "never a failure" promise.

- [ ] **Step 4: Smoke the transcription path (only if whisper is installed)**

Set `stt.mode: "whisper:<a model you have>"`, ingest a genuine short recording, and confirm: a source note whose body is the transcript, the transcript searchable via `axon search`, the recording archived under `attachments/`, and **no audio bytes in the database** —

```bash
sqlite3 "$S/home/profiles/personal/db.sqlite" "SELECT COUNT(*) FROM chunks WHERE text LIKE '%RIFF%' OR text LIKE '%ftyp%';"
```

Expected `0`. Grep-style checks on the vault alone are not sufficient here; check the DB.

- [ ] **Step 5: Smoke the caps and doctor's warn state**

Set `max_minutes: 1` against a longer recording (or a fake `Probe` if whisper cannot report duration) and confirm the flagged note names the cap. Point `stt.binary` at a nonexistent path and confirm `axon doctor` warns with its `Fix`, and that `axon run self-check` files it.

- [ ] **Step 6: Record and clean up**

Delete `$S`. State explicitly in the completion report which paths were verified with a real whisper and which were not.

---

## Self-Review

**Spec coverage:** classification and the `AllowLocalFiles` guard → Task 1; config, validator, doctor → Task 2; `vault.CopyFile` and why the image path cannot be reused → Task 3; the seam with `Probe`, and the whisper provider → Task 4; the pipeline arm, both caps, both non-fatal refusals, the parameterised `writeCapturedNote`, the archive, and daemon wiring → Task 5; FR rows, ADR flip, config reference, GUIDE, roadmap, CHANGELOG → Task 6; smoke including the DB assertion that audio never reaches a chunk row → Task 7. Out-of-scope items (diarisation, Apple Speech, streaming, B2, migrating the image path, URL audio) appear in no task, correctly.

**Type consistency:** `KindAudio` (Task 1) branched on in Tasks 4–5. `config.STTConfig{Mode, Binary, MaxMinutes}` with `ModeOr`/`MaxMinutesOr`/`WhisperModel` (Task 2) read in Tasks 2, 4, 5. `vault.CopyFile(destRel, srcPath string) error` (Task 3) called once, in Task 5. `STT` with `Probe`/`Transcribe`, `Transcript{Text, Duration, Model}`, `STTFor(cfg, goos) (STT, error)` (Task 4) used in Task 5 and the daemon wiring. `ErrNoSTT`/`ErrTooLong`/`ErrTooLarge` and `sttMaxBytes` declared once each in Task 5.

**Three fragile points, flagged rather than hidden.** (1) **The whisper CLI flags in Task 4 are unverified** — they are the common whisper.cpp shape, but builds differ, and no unit test exercises them because nothing is executed. Task 4 says to check `--help` if the binary is present; Task 7 says to fix them there and to *not* claim the transcription path is verified if whisper was never run. (2) **`writeCapturedNote` changes signature** and has an existing caller whose behaviour must stay byte-identical — Task 5 Step 1 does that first, alone, so a regression there is obvious. (3) **Several test helpers are assumed** (`newTestPipeline`, `writeTempFile`, `p.Ingestion`); each step says to check the package's existing helpers and match them rather than inventing parallel ones.
