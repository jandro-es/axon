# H1 — Multimodal ingestion (design)

**Roadmap:** 1.3 Phase H, slice H1 (`docs/17-roadmap-1.3.md`).
**FR:** FR-171, FR-172, FR-173. **ADR:** ADR-035. **Migration:** none.
**Branch:** `feature/multimodal-ingestion`. **Date:** 2026-07-10.

## Purpose

1.3 widens what AXON can *take in*. Today the ingestion pipeline handles URLs,
HTML articles, text files, and PDFs (with an OCR fallback, ADR-026). H1 adds two
new input surfaces, both landing as clean, linked, retrievable Knowledge notes
through the **existing, content-agnostic pipeline tail**:

1. **Images & screenshots** — a local image becomes a note whose text is
   recovered **locally** (OCR first, a local vision model when OCR is sparse),
   with the source image archived into the vault.
2. **Video / podcast captions** — a media URL (YouTube family, or any URL forced
   with `--media`) becomes a transcript note via native captions; a caption-less
   or tooling-absent URL is captured and flagged, never crashes.

Both obey the unchanged constitution: local-first perception, every writer
wikilink-safe, off by default, all-off still useful (S8), the vault rebuilds the
DB and never the reverse (S9), fetched/recovered content is **data, never
instructions** (NFR-05).

## Where this sits in the pipeline

`Pipeline.Ingest` is: `ClassifyInput → policy → read → extract → redact → hash →
enrich → write → persist+embed`. **Everything from `redact` onward is
content-agnostic** and is not touched. All new code lands in `ClassifyInput`
(two new kinds), `read`/`extract` (two new branches), plus two new local
provider seams and their config/doctor/wiring. No new automation, no new MCP
tool, no schema migration.

```
KindImage → ReadFile → extractImage(OCR-first, vision-if-sparse) ┐
KindMedia → captionFetch(yt-dlp) → transcript ─────────────────── ┼→ redact → … → note
(existing) KindURL/KindPDF/KindFile ────────────────────────────┘
```

## 1. Images & screenshots (FR-171, and the vision seam FR-172)

### Input classification
`input.go` gains `KindImage`, classified by a lowercase extension in
`{.png,.jpg,.jpeg,.gif,.webp,.heic,.heif,.tiff,.tif,.bmp}`. Classification order
in `ClassifyInput`: http(s) URL → media-host check (see §2) → `.pdf` → image
extension → plain file. The canonical `URL` identifier stays `file://<path>`
(reuses the existing source-row idempotency by URL).

Reading uses the existing `ReadFile`. The `AllowLocalFiles` guard already refuses
local files on agent-driven paths, so **image ingestion is CLI-only** (the MCP
`knowledge_ingest` path is URL-only); a prompt-injected agent cannot read an
arbitrary host image into the vault and the model. `KindImage` joins
`KindFile`/`KindPDF` in that same policy branch.

### The vision provider seam (FR-172)
A new leaf seam mirroring `embeddings.Provider` and `ingestion.OCR`:

```go
// Vision produces a plain-language description (including transcribed text) of
// an image. Implementations are strictly local (ADR-035); output is content,
// never instructions (NFR-05). A nil Vision means the feature is off.
type Vision interface {
    Describe(ctx context.Context, img []byte, mime string) (string, error)
    Name() string
}
```

- **`OllamaVision`** (`vision_ollama.go`) — posts to Ollama `/api/generate`
  with `{model, prompt, images:[base64], stream:false, format? }`, reads
  `.response`. The prompt asks the model to *transcribe any text verbatim, name
  the app/context if it is a screenshot, and describe key visual elements, as
  plain prose* — and frames the image strictly as data to describe. Bounded
  `http.Client` timeout; injectable `post` seam for tests (mirrors
  `rerank.OllamaReranker`).
- **`apple`** — returns an actionable error: `vision provider "apple" requires
  macOS 27 on-device image input (not yet available) — use ollama:<model> or
  off`. The seam is in place so the Apple FM image tier drops in later with zero
  caller change (ADR-013 helper pattern), matching the roadmap.
- **`VisionFor(cfg config.IngestionConfig, goos string) (Vision, error)`** —
  `off`→`nil`; `ollama:<model>`→`OllamaVision`; `apple`→the not-yet error;
  anything else→a config error. Mirrors `OCRFor`/`RerankerFor` exactly.

Vision is a **local perception primitive**: budget-exempt, **not** routed
through the token-manager chokepoint (ADR-035, an ADR-015 amendment — the same
status OCR and rerank already hold). There is **no Claude vision path in v1**, so
cardinal rule 1 is untouched and no new Claude spend surface appears.

### OCR-first, vision-if-sparse
The `OCR` seam gains an image method so image text recovery reuses the same
provider story rather than a parallel one:

```go
type OCR interface {
    Recognize(ctx, pdf []byte) (string, error)          // existing (PDF)
    RecognizeImage(ctx, img []byte, mime string) (string, error) // new
    Name() string
}
```

- `TesseractOCR.RecognizeImage` writes the bytes to a temp file and reuses the
  existing `tesseractImage` executor (`tesseract <img> stdout`).
- `AppleOCR.RecognizeImage` extends the Swift helper (`ocr_apple_helper.swift`)
  with an **image mode**: when handed an image path it runs
  `VNRecognizeTextRequest` directly on the loaded `CGImage`, skipping the PDFKit
  page-render step (simpler than the PDF path). Same JSON-over-stdout contract.
  `EnsureOCRHelper` recompiles idempotently as today (the sha256 marker changes
  because the source changed).

`extractImage(ctx, img, mime, ocr, vision)` follows the exact shape of
`ocrFallback` (ADR-026):

1. If `ocr != nil`: `text = ocr.RecognizeImage(...)` (best-effort; error →
   treated as empty).
2. If `len(normalize(text)) < minExtractedChars` **and** `vision != nil`:
   `text = vision.Describe(...)` — vision **replaces** the sparse OCR text
   (like `ocrFallback` replaces sparse PDF text). A vision error is returned so
   the run is recorded failed *only if OCR also produced nothing*; if OCR gave
   some text, a vision error is swallowed and the OCR text stands.
3. `Markdown = normalizeMarkdown(text)`; `Title` = first heading of the recovered
   text, else the filename stem.

Both providers absent (or both empty) ⇒ the note is still written with the
filename title and the embedded image reference (see below), just not richly
searchable — **no crash** (acceptance gate).

### Archiving the image (archive-never-delete, S9)
The source image is **copied** — never moved or deleted — into
`03-Resources/Knowledge/attachments/<content-hash>.<ext>` (new const
`AttachmentsDir`). The written note embeds it at the top of the source block via
an Obsidian embed `![[attachments/<hash>.<ext>]]`, so the vault is
self-contained (a `reindex`/fresh clone still has the image) and the user's
original file is untouched. The content hash keys the attachment filename, so a
re-ingest of the same bytes lands on the same path — idempotent, no duplicate
attachments. This is a thin addition to `buildSourceNote`, gated on
`in.Kind == KindImage`.

Idempotency overall is the **existing Stage-6 machinery**: `GetSourceByURL(in.URL)`
+ content-hash compare short-circuits an unchanged re-ingest with no OCR, no
vision, no embed (FR-24/FR-31).

## 2. Video / podcast captions (FR-173)

### Classification
`KindMedia` is chosen when either:
- the URL host is in the media-host set — a built-in youtube family
  (`youtube.com`, `www.youtube.com`, `m.youtube.com`, `music.youtube.com`,
  `youtu.be`, `youtube-nocookie.com`), extensible via `ingestion.media_hosts`; or
- the caller passes the new `axon ingest --media` flag (an
  `IngestOptions.ForceMedia bool`), which routes *any* URL through the caption
  path — covering podcasts, Vimeo, and other caption-bearing links without
  brittle host-sniffing.

`KindMedia` still passes `CheckIngestPolicy(host)` (the egress allowlist) before
any tooling runs — the same gate as `KindURL`. (Note: `yt-dlp`'s own downstream
fetches to CDN hosts are not individually policy-gated; this is the accepted
detected-binary trade-off, identical to `tesseract`/`pdftoppm` shelling out
under ADR-026. Documented in ADR-035.)

### Caption fetch via detected `yt-dlp`
A small injectable seam (`captions.go`), mirroring the tesseract executor seams
(`lookup`/`run` funcs so tests need no binary):

```go
type captionFetcher struct {
    lookup func(string) (string, error)      // exec.LookPath
    run    func(ctx, url, outDir string) ([]string, error) // yt-dlp invocation
}
```

`yt-dlp --skip-download --write-auto-subs --write-subs --sub-format vtt
--sub-langs <configurable, default "en.*"> -o <tmpl> <url>` writes `.vtt`
subtitle files; we pick the best available, strip WebVTT cue timings/markup to
plain text, and hand it to the standard `read`/`extract` result as
`Extracted{Title, Markdown}` (title from `yt-dlp --print title`, or the video id
fallback). The transcript then flows through the **untouched** enrich → chunk →
embed → note tail, producing a cited source note whose `source_url` is the media
URL.

### Caption-less / yt-dlp absent (the degradation gate)
If `yt-dlp` is not on PATH, or it produces no subtitle track (audio podcast with
no captions, captions disabled), the pipeline **does not fail**. It writes a
light flagged capture note to `00-Inbox/` — frontmatter `type: capture`,
`status: captured`, tag `#needs-captions`, body = the URL and a one-line
"⚠ No captions available — transcript pending" — with **zero model calls**, and
returns `IngestResult{Status: "captured", SkippedReason: "no captions available"}`.
Nothing crashes (acceptance gate). Local STT is explicitly out of 1.3 scope; the
flagged note is the hook for a future STT pass.

## Config, doctor, wiring

### Config (`ingestion`, mirrors `ingestion.ocr`)
```yaml
ingestion:
  vision: "off"          # off | "ollama:<vision-model>" | "apple"   (default off, both profiles)
  media_hosts: []        # extra hosts that auto-classify as KindMedia (youtube family is built in)
  caption_langs: "en.*"  # yt-dlp --sub-langs selector (optional; default "en.*")
```
`IngestionConfig` gains `Vision string` (validate
`omitempty` — the concrete `off|apple|ollama:*` shape is validated in
`VisionFor`, matching how `ocr` uses `oneof` but the `ollama:` prefix cannot be a
static `oneof`), `MediaHosts []string`, `CaptionLangs string`, plus a
`VisionMode()` helper (default `"off"`). Seeds added to **both**
`internal/config/starter.go` **and** `axon.config.example.yaml`.

### doctor (advisory, mirror `ocrCheck`/`rerankCheck`)
- `visionCheck`: `off` → ok "off"; `ollama:<m>` → probe Ollama reachable + model
  present; `apple` → "requires macOS 27 (not yet available)".
- `mediaCheck`: reports whether `yt-dlp` is on PATH ("media caption ingestion
  ready" / "yt-dlp not found — media URLs will be captured and flagged").
  Warn-only; media is not otherwise gated.

### Wiring
`Pipeline` gains a `Vision Vision` field (nil = off). `VisionFor(deps.profile
.Ingestion, runtime.GOOS)` is built alongside the existing `OCRFor(...)` at both
pipeline roots (`cmd/axon/deps.go` `buildServices` and `cmd/axon/ingest_cmd.go`),
error ignored (doctor surfaces misconfig — the OCR precedent). The `--media` flag
is added to `ingest_cmd.go`. Every ingest already emits a bus event (dashboard
≤5s, NFR-07); H1 adds no new event kinds (image/media ingests reuse
`ingest.done`/`ingest.skip`; the captured-flag path emits `ingest.done` with
`Status:"captured"`).

## Testing (TDD, table-driven)

- **`input_test.go`**: `KindImage` classification by extension; `KindMedia` by
  host and by `ForceMedia`; precedence (URL > media > pdf > image > file).
- **`vision_test.go`**: `OllamaVision.Describe` against a fake `post` (happy path,
  transport error, empty response); `VisionFor` matrix (off/ollama/apple/garbage
  × goos).
- **`ocr_image_test.go`**: `TesseractOCR.RecognizeImage` via injected executor;
  the Apple image mode is covered by the existing helper-integration test shape
  (real `swiftc` on macOS in the live smoke).
- **`extract_image_test.go`**: OCR-rich → vision skipped; OCR-sparse + vision →
  vision text used; both nil → filename-title note, no error; vision error with
  OCR text present → OCR text stands.
- **`captions_test.go`**: vtt-strip to plain text; `captionFetcher` happy path via
  injected `run`; yt-dlp absent → `ErrNoCaptions`; no subtitle file → captured
  path.
- **`pipeline_test.go`**: image end-to-end into a note with `![[attachments/…]]`
  + idempotent re-ingest (hash skip); media end-to-end into a transcript note;
  caption-less URL → `00-Inbox` flagged capture, `Status:"captured"`, zero model
  calls; agent-driven (`AllowLocalFiles:false`) image → refused.
- **Count-assertion watch:** no new automation and no new MCP tool, so the
  registry/tools count assertions are **untouched**. Extending the `OCR`
  interface breaks the `ocrFake`/any test double — update those to add
  `RecognizeImage`.

## Acceptance gate (from `docs/17-roadmap-1.3.md`)

A screenshot ingests into a retrievable note whose description was produced
**locally**; a YouTube URL with captions yields a cited source note; both
**idempotent by content hash** (re-ingest is a no-op); **vision provider absent ⇒
OCR-only note, no crash**; a caption-less URL is captured and flagged, nothing
crashes. Live-smoked on macOS with real Ollama-vision + real `yt-dlp` in an
isolated `AXON_HOME` (port 7788, never the user's :7777 daemon).

## Decisions (all Jandro-picked 2026-07-10)

1. **One combined slice** (both surfaces, one ADR-035, one merge; per-task commits
   images-first-then-captions).
2. **Vision = local-only perception primitive** (Ollama now + Apple seam later;
   budget-exempt, not chokepoint-routed; no Claude vision path in v1).
3. **OCR-first, vision-if-sparse** (mirrors the ADR-026 PDF fallback; text
   screenshots cost zero vision calls).
4. **Captions via detected `yt-dlp`** (ADR-026 detected-binary precedent;
   caption-less/absent → capture + flag).

Self-decided defaults (flip on request): image OCR **extends the `OCR` seam**
(uniform provider story) rather than a separate image-OCR type; caption-less
media lands in a **flagged `00-Inbox` capture** rather than failing the ingest.

## Non-goals for H1

Cloud vision or cloud STT as any default path; a Claude vision escalation path;
local STT/audio transcription (caption-less → flag only); recording of any kind;
agent-driven image ingestion (CLI-only by the `AllowLocalFiles` guard); a new
MCP tool or automation; PDF-page vision (PDFs keep the ADR-026 OCR path).
