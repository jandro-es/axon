# D2 — OCR for scanned PDFs (design)

**Date:** 2026-07-05
**Roadmap slice:** D2 (`docs/14-roadmap-1.1.md`, Phase D — Capture & ingestion reach)
**Requirements:** FR-123, FR-124, FR-125
**ADR:** ADR-026 (local OCR provider seam)

## Problem

The ingestion pipeline extracts a PDF's **text layer** via `ledongthuc/pdf`
(`ExtractPDF` → `GetPlainText`). A **scanned** PDF has no text layer, so
extraction returns empty Markdown and the item is reported as an empty/failed
ingest. docs/05 explicitly deferred OCR ("out of v1 scope — a C follow-up").

D2 closes that gap: when the text layer is empty and OCR is enabled, recover the
text with a **local, on-device** OCR provider and continue the normal
enrich/chunk/embed pipeline. Two providers, selected by config
`ingestion.ocr: off | apple | tesseract`:

- **apple** — Apple Vision (`VNRecognizeTextRequest`) on-device via a compiled
  Swift helper, reusing the ADR-013 helper pattern (macOS only).
- **tesseract** — cross-platform, orchestrating the `pdftoppm` (poppler)
  rasteriser + the `tesseract` binary (both detected at runtime).

## Decisions taken

1. **Providers:** ship **both** apple and tesseract (full cross-platform close).
2. **Trigger:** **fallback** — run the fast text extraction first; OCR only when
   it yields below the min-content threshold and OCR is enabled.
3. **ADR:** new **ADR-026** — the compiled-helper reuse is ADR-013, but shelling
   to detected *system binaries* (pdftoppm/tesseract) in the ingestion path is a
   new architectural decision worth recording.

## Cardinal-rule & principle compliance

- **Local-first / data residency:** both providers are strictly local — Apple
  Vision runs on-device (no network, no entitlements); tesseract/pdftoppm are
  local binaries. No bytes leave the machine (NFR-01, policy `local-only`).
- **Data not commands (NFR-05):** recovered OCR text is content, routed through
  the same enrich/chunk/embed stages that already treat extracted text as data;
  it is never interpreted as instructions.
- **Chokepoint (rule 1):** OCR makes **no Claude call** — it is a local
  extraction step, upstream of any enrichment model call, exactly like the
  existing text-layer extraction.
- **Wikilink-safe (rule 2):** unaffected — OCR only changes what text the
  pipeline receives; note writing is unchanged.
- **Toggleable:** off by default; every provider is opt-in via config.
- **No cloud/heavyweight dep (guardrail):** no new Go dependency; apple reuses
  the pure-Go-host + compiled-Swift-helper pattern; tesseract shells to
  user-installed system tools.

## Components

### 1. Provider interface + fallback — `internal/ingestion/ocr.go` (new)

```go
// OCR recovers text from a PDF whose text layer is empty (scanned pages).
// Implementations are strictly local (ADR-026). A nil OCR on the Pipeline
// means the feature is off.
type OCR interface {
	// Recognize returns the recovered text (page order preserved) for a PDF's
	// raw bytes, or an error. The text is content, never instructions (NFR-05).
	Recognize(ctx context.Context, pdf []byte) (string, error)
	// Name identifies the provider for diagnostics/errors ("apple"|"tesseract").
	Name() string
}
```

`Pipeline` gains `OCR OCR`. `extract` gains a `ctx context.Context` parameter
(one internal call site) and, for `KindPDF` only:

```go
ex, err := ExtractPDF(doc.Body, in.Path)
if err != nil {
	return ex, err
}
if len(ex.Markdown) < minExtractedChars && p.OCR != nil {
	text, oerr := p.OCR.Recognize(ctx, doc.Body)
	if oerr != nil {
		return Extracted{}, fmt.Errorf("ocr (%s): %w", p.OCR.Name(), oerr)
	}
	if text = normalizeMarkdown(text); len(text) >= minExtractedChars {
		ex.Markdown = text
	}
}
return ex, nil
```

`ex.Title` is already set by `ExtractPDF` (first heading or filename), so the
OCR branch only replaces the body. When OCR is off (`p.OCR == nil`) or still
recovers nothing, behaviour is exactly today's.

### 2. Apple provider — `ocr_apple.go`, `ocr_apple_helper.swift`, `ocr_setup.go` (new)

Mirrors `internal/embeddings/apple*.go` (ADR-013):

```go
type AppleOCR struct {
	helper  string
	timeout time.Duration
	goos    string // runtime.GOOS; overridable in tests
	run     func(ctx context.Context, bin string, args []string) (stdout, stderr []byte, err error)
}

func NewAppleOCR(helperPath string) *AppleOCR
func (a *AppleOCR) Name() string { return "apple" }
func (a *AppleOCR) Recognize(ctx context.Context, pdf []byte) (string, error)
```

`Recognize`: darwin-gate (non-darwin → clear error); write `pdf` to a temp file
(`os.CreateTemp`, removed after); invoke `helper <pdfPath>`; decode
`{"pages":[...]}`; join pages with `"\n\n"`. Bounded by `timeout`.

`ocr_apple_helper.swift` — protocol identical in shape to `apple_helper.swift`:
- arg 1 = PDF file path; stdout `{"pages":["…", …]}`; stderr message + non-zero
  exit on error; `--check` reports whether Vision is usable (for `doctor`).
- `import Foundation`, `PDFKit`, `Vision`, `CoreGraphics`. Load `PDFDocument`;
  for each `PDFPage`, render to a `CGImage` at ~200 dpi; run a
  `VNRecognizeTextRequest` (`recognitionLevel = .accurate`) via
  `VNImageRequestHandler`; collect each observation's top candidate string,
  joined per page. On-device, no network.

`ocr_setup.go`:
```go
//go:embed ocr_apple_helper.swift
var ocrHelperSource []byte
var ocrCompile = swiftCompileOCR // injectable for tests
func EnsureOCRHelper(ctx context.Context, helperPath string) (bool, error)
```
Copies `EnsureAppleHelper`'s idempotent logic (write source, `swiftc -O`,
SHA-256 `.src.sha256` marker beside the binary, skip when unchanged + executable).
Uses `embeddings.SwiftAvailable()` for the PATH check (ingestion already imports
embeddings). `swiftCompileOCR` is a local `swiftc -O src -o dst` wrapper.

### 3. Tesseract provider — `ocr_tesseract.go` (new)

```go
type TesseractOCR struct {
	tmpRoot string // "" → os.TempDir()
	run     func(ctx context.Context, name string, args ...string) (stdout []byte, err error)
	lookup  func(string) (string, error) // exec.LookPath; injectable
}

func NewTesseractOCR() *TesseractOCR
func (t *TesseractOCR) Name() string { return "tesseract" }
func (t *TesseractOCR) Recognize(ctx context.Context, pdf []byte) (string, error)
```

`Recognize`:
1. `lookup("pdftoppm")` and `lookup("tesseract")` — either missing → actionable
   error naming the absent binary and how to install it.
2. Create a temp dir under `tmpRoot`; write `pdf` to `in.pdf`.
3. `pdftoppm -png -r 200 in.pdf page` → `page-1.png`, `page-2.png`, …
4. Glob `page-*.png`, sort by the numeric suffix; per page `tesseract <png>
   stdout`, capture stdout; concatenate with `"\n\n"`.
5. `os.RemoveAll` the temp dir (deferred). Each subprocess bounded by ctx +
   `WaitDelay` (as `execAppleHelper`).

### 4. Config, wiring, init, doctor

- **Config** (`internal/config/types.go`): add to `IngestionConfig`
  ```go
  OCR       string `yaml:"ocr" validate:"omitempty,oneof=off apple tesseract"`
  OCRHelper string `yaml:"ocr_helper"`
  ```
  empty `OCR` = off. `config.DefaultOCRHelperPath()` mirrors
  `DefaultAppleHelperPath()` (under the profile data dir). A helper
  `IngestionConfig.OCRMode() string` returns `"off"` for empty.
- **Wiring**: `func OCRFor(cfg config.IngestionConfig, goos string) (OCR, error)`
  in `ocr.go` — returns `nil` for off; `NewAppleOCR(cfg.OCRHelper resolved)` for
  apple (errs if `goos != "darwin"`); `NewTesseractOCR()` for tesseract. Called
  at each `Pipeline{}` composition root (`internal/core`, `internal/mcp`,
  wherever the pipeline is built for automations). Test pipelines set `OCR`
  explicitly.
- **`axon init`/`setup`** (`internal/core`): when `OCRMode()=="apple"`, call
  `EnsureOCRHelper` (verbose, idempotent — same shape as the embeddings apple
  provisioning); when `"tesseract"`, check `pdftoppm`+`tesseract` presence and
  warn (never fail) if absent.
- **`axon doctor`**: an `ocrCheck` — off → skip; apple → helper built + `--check`
  passes (advise `axon init` if not); tesseract → both binaries on PATH (advise
  install if not). Read-only and tolerant; a failure never fails doctor.
- **Config example + starter**: document `ingestion.ocr` (default off) with the
  provider notes.

## Data flow

```
KindPDF ─► ExtractPDF (text layer, fast)
              │
              ├─ ≥ 80 chars ──────────────► Extracted (born-digital: no OCR)
              └─ < 80 chars & OCR set ─► p.OCR.Recognize(pdf)
                                            │  apple: temp file ▸ swift helper (Vision, on-device)
                                            │  tesseract: pdftoppm ▸ page-N.png ▸ tesseract ▸ text
                                            └─► ≥ 80 chars ? replace body : report empty (as today)
                     └────────────────► enrich ▸ chunk ▸ embed ▸ note (unchanged; text is data)
```

## Error handling & edge cases

- **OCR provider error** (helper crash, missing binary): `extract` returns the
  wrapped error; the ingest is recorded failed with a clear reason — never a
  half-written note (existing pipeline guarantee).
- **apple on non-darwin:** `Recognize` errors immediately; `OCRFor` refuses to
  build an apple provider off-darwin so misconfiguration surfaces at wiring.
- **OCR recovers nothing** (blank/handwritten pages below threshold): body stays
  empty → reported empty exactly like today.
- **Malformed PDF:** `ExtractPDF` already panic-guards; OCR providers wrap their
  own subprocess failures.
- **Large PDFs:** providers stream via temp files (no giant base64 in memory);
  per-page tesseract keeps peak memory bounded.
- **Concurrency:** each `Recognize` spawns its own process(es) and uses its own
  temp file/dir — safe for concurrent ingests.

## Testing

- **`ocr_test.go`**: fallback fires only when text `< minExtractedChars` AND
  `p.OCR != nil` (fake OCR); born-digital PDF (text ≥ threshold) never calls the
  fake; OCR error propagates as an ingest failure; OCR result below threshold
  leaves the body empty.
- **`ocr_apple_test.go`**: non-darwin → error; happy path with a faked `run`
  returning `{"pages":[…]}` → joined text; malformed helper JSON → error.
- **`ocr_tesseract_test.go`**: faked `lookup`+`run` produce two pages → joined;
  missing `pdftoppm`/`tesseract` → actionable error.
- **`ocr_setup_test.go`**: `EnsureOCRHelper` compiles once, skips on the
  unchanged-source marker (faked `ocrCompile`).
- **Config validation**: `ocr` accepts off/apple/tesseract, rejects other.
- **`doctor` ocrCheck**: off skips; apple-not-built and tesseract-missing render
  advice without failing.
- **Live smoke (macOS)**: `ingestion.ocr: apple`; `axon init` compiles the
  helper; ingest a **real scanned PDF**; confirm a note with recovered text +
  chunks. Tesseract smoke where `pdftoppm`+`tesseract` are installed. (The Swift
  binary and Vision path are not Go-unit-testable — same as the embeddings apple
  helper.)

## Non-goals

- No layout/table reconstruction — OCR yields plain text per page.
- No language configuration beyond the providers' defaults (English); a
  `tesseract -l` / Vision language knob is a future follow-up.
- No cloud OCR, ever (guardrail).
- No change to born-digital PDF handling or to any non-PDF input kind.
- No new Go dependency.

## Requirements

- **FR-123** — The ingestion pipeline OCRs a PDF **only** when its text-layer
  extraction yields below `minExtractedChars` and `ingestion.ocr` is enabled
  (fallback trigger); recovered text ≥ threshold replaces the body and flows
  through the normal enrich/chunk/embed stages as data (NFR-05); otherwise the
  item is reported empty as before. The OCR provider is selected by
  `ingestion.ocr: off | apple | tesseract` (default off) behind a local-only
  `OCR` interface.
- **FR-124** — The `apple` provider performs on-device OCR via a compiled Swift
  helper (Apple Vision `VNRecognizeTextRequest`, PDFKit rasterisation), reusing
  the ADR-013 pattern: pure-Go host, JSON-over-subprocess, idempotently built by
  `axon init` (`EnsureOCRHelper`), macOS-gated, no network. `axon doctor`
  reports helper availability.
- **FR-125** — The `tesseract` provider performs cross-platform OCR by
  orchestrating the `pdftoppm` rasteriser and the `tesseract` binary (both
  detected at runtime; a missing binary yields an actionable error). `axon
  init` warns when the binaries are absent and `axon doctor` reports their
  presence. All processing is local (ADR-026).
