# Audio ingestion via local STT — a voice memo becomes a citable source (design)

**Status:** approved 2026-08-21 (all three decisions Jandro-picked-recommended:
**whisper.cpp as a detected binary** first, with Apple Speech later behind the
same seam; **a flagged `00-Inbox` note**, never a failure, when no provider is
configured; **a config duration cap** that refuses past it).
**FR-212, FR-213; ADR-042.** Graduates `docs/19` **B1**.

**No migration.** Schema stays `0007`. No new automation — built-ins stay **26**.

## The idea

A voice memo or a downloaded recording is the largest untapped personal input,
and the pipeline already has the shape for it. `KindImage` (ADR-035) proved the
pattern: an extension-classified local file, a provider seam that returns nil
when off, and an unchanged `enrich → chunk → embed` tail. Audio is the same
shape with a different provider — **the transcript is just text**, so citations,
search, redaction and the `sources` row all work with no new code downstream.

## FR-212 — `KindAudio`, config, validation, doctor

### Classification

An `audioExts` map beside `imageExts` in `internal/ingestion/input.go`:
`.m4a .mp3 .wav .aac .flac .ogg .opus`, producing `KindAudio` from
`ClassifyInput`.

`KindAudio` joins the `AllowLocalFiles` guard at `pipeline.go:107` alongside
`KindFile/KindPDF/KindImage`. This matters: without it the `knowledge_ingest`
MCP tool could transcribe arbitrary host audio, and that tool exists to be
callable by a model.

### Config

```yaml
ingestion:
  stt:
    mode: "off"          # off | whisper:<model>
    max_minutes: 120     # refuse recordings longer than this
    binary: ""           # optional explicit path; empty = look up "whisper" on PATH
```

`off` is the default, so the feature ships disabled and a fresh install
transcribes nothing. `validateSTT` runs beside `validateVision` in the
per-profile loop and refuses: a mode that is neither `off` nor
`whisper:<model>`; an empty model after the prefix; and `max_minutes` outside
1–1440.

### Doctor

An `stt` check: **off** when the mode is off; **OK** naming the resolved binary
and model; **warn** when a mode is configured but the binary is not found, with
`Fix: "install whisper.cpp and put its binary on PATH, or set
ingestion.stt.binary"`. It carries a `Fix`, so `self-check` (FR-207) files it
to the review queue automatically.

## FR-213 — The seam, the whisper provider, and the pipeline stage

### The seam

```go
// internal/ingestion
type Transcript struct {
	Text     string
	Duration time.Duration
	Model    string
}

type STT interface {
	// Probe reports a recording's duration WITHOUT transcribing it, so the
	// duration cap can refuse before any CPU is spent. A provider that cannot
	// tell cheaply returns (0, nil), meaning "unknown — proceed"; only a real
	// failure returns an error.
	Probe(ctx context.Context, path string) (time.Duration, error)
	Transcribe(ctx context.Context, path string) (Transcript, error)
}

// STTFor returns nil, nil when transcription is off — the VisionFor contract.
func STTFor(cfg config.IngestionConfig, goos string) (STT, error)
```

Modelled on `VisionFor` (`internal/ingestion/vision.go:30`) down to the
`nil, nil`-when-off convention and errors that name the fix. The `whisper`
implementation resolves its binary the way OCR resolves tesseract: a missing
binary is a construction error the caller degrades on, never a panic.

Apple Speech will land behind this same seam later, exactly as ADR-038 filled
ADR-035's Apple slot — which is why the interface takes a path and returns
text rather than exposing anything whisper-specific.

### The pipeline stage

`read()` gains a `KindAudio` arm:

1. **Size guard first.** `sttMaxBytes` (a Go const, 500 MB) checked by
   `os.Stat` before anything is opened — the mechanical guard, knowable
   without probing.

   **The two caps are deliberately not aligned, and the byte cap bites first
   for lossless audio.** 120 minutes of `.wav` is roughly 1.2 GB, so it is
   refused on size well before the duration cap applies; 120 minutes of `.m4a`
   is around 60 MB and is governed by duration alone. That is the intended
   behaviour — the byte cap protects memory regardless of format, the duration
   cap protects time — but it means a user who hits the size limit sees a
   size-worded refusal on a recording shorter than `max_minutes`, so the
   flagged note must say which cap refused it.
2. **Duration guard.** `STT.Probe` reports the duration without transcribing;
   refuse past `stt.max_minutes` with `ErrTooLong`. Probing separately is the
   point — reading the duration out of the finished `Transcript` would mean
   the CPU was already spent, which is what the cap exists to prevent. A
   provider returning `(0, nil)` means "unknown", and the run proceeds under
   the byte cap alone.
3. **Transcribe.** The provider returns a `Transcript`.
4. **Return the text as the document body.**

**Everything downstream is unchanged.** Redaction (NFR-06) applies to the
transcript before it is persisted or could reach a model; enrichment, chunking,
embedding and the `sources` row all treat it as ordinary text. This is the
whole reason the slice is small.

**Audio bytes never flow downstream.** `read()` returns text; the bytes go only
to the archive step. Stated explicitly because getting it wrong is precisely
how audio ends up in a `chunks` row.

### Archiving — the one place the image pattern does NOT transfer

`attachmentPath(hash, srcPath)` (`pipeline.go:557`) and `AttachmentsDir` are
reused **unchanged** — already content-hash-keyed and extension-preserving.

The image path's *copy* cannot be reused. `pipeline.go:334` does
`p.Vault.Create(attachmentPath(...), string(img))`: the whole file, held in
memory, converted to a string. Fine for a screenshot; untenable for an hour of
`.wav` (~600 MB, doubled by the string conversion), and it routes binary
through the API meant for text notes.

So this slice adds **`vault.CopyFile(destRel, srcPath string) error`** — an
`io.Copy` into the vault, beside the existing wikilink-safe writers, creating
parent directories and refusing to overwrite. The image path is deliberately
**left alone**: it works, and changing it is not this slice's business.

### Two non-fatal refusals

Both reuse H1's shipped precedent — `ErrNoCaptions` → `writeCapturedNote`
(`pipeline.go:116`), which archives the original and writes a flagged
`00-Inbox` note:

- **`ErrNoSTT`** — no provider configured. The file is archived, the note says
  transcription is not configured, the run **succeeds**. "Zero model calls when
  no provider" stays literally true, and a user who drops a voice memo before
  setting STT up loses nothing.
- **`ErrTooLong`** — past the duration cap. Same treatment, with the duration
  and the cap in the note.

Neither is a failure. A recorded failure per dropped audio file would make the
capture automation and watch-folders noisy for anyone who has not configured
STT — which is everyone, by default.

## Out of scope

- **Diarisation.** Speaker labels change the note's structure *and* the
  extraction that reads it (B2). A different feature, as `docs/19` suspected.
- **Apple Speech**, **streaming/real-time transcription**, and **B2's action
  extraction**, which rides this seam but is its own slice.
- **Migrating the image path to `CopyFile`.** It works; leave it.
- **Transcribing audio from URLs.** The media/caption path (H1) already covers
  remote media; this is local files only.

## Verification

**Unit — classification:** every extension in `audioExts` produces `KindAudio`;
an unknown extension still produces `KindFile`; a URL is unaffected; and
`KindAudio` is refused when `AllowLocalFiles` is false (the agent-driven path).

**Unit — config:** `validateSTT` accepts `off` and `whisper:base`; refuses an
unknown mode, an empty model after the prefix, and `max_minutes` of 0 and 1441.

**Unit — the seam:** `STTFor` returns `nil, nil` for `off`; returns an error
naming the fix when the binary is absent; and returns a working provider when
a fake binary path is supplied.

**Unit — the pipeline, with a fake STT:** a transcript flows through the
unchanged tail (assert a `sources` row, chunks, and the note body containing
the transcript); a redaction rule rewrites the transcript before persistence;
the audio is archived at `attachments/<hash>.<ext>` and **its bytes appear in
no chunk row** — the assertion that catches the mistake this design exists to
avoid; `ErrNoSTT` and `ErrTooLong` each produce a flagged `00-Inbox` note and
a successful run; the size guard refuses before opening the file.

**Unit — `vault.CopyFile`:** copies bytes exactly; creates parent directories;
refuses to overwrite an existing file; refuses a path outside the vault
(the `safeAbs` rule every vault writer follows).

**Live smoke** in an isolated env (scratch `AXON_HOME`, dashboard port 7799 —
**never 7777**; isolate `vault_path` and `data_dir` too, not just `AXON_HOME`;
`mkdir -p` the data dir or SQLite fails with `unable to open database file
(14)`; `cd` back to the repo root by absolute path after `cd web && npm run
build`; run `go test -race ./...` before pushing, since CI does): a real short
recording ingested with whisper installed — note written, transcript
searchable, audio archived; the same file with `stt.mode: off` producing the
flagged note and a successful run; a file over the cap refused the same way;
`axon doctor` showing the `stt` check in all three states; and `self-check`
filing the warning when the binary is missing.

## Docs to update on completion

`docs/03` (FR-212/FR-213 rows), `docs/02` (ADR-042 planned → built at the cut),
`docs/04` (the `ingestion.stt` config reference),
`docs/05-component-knowledge-ingestion.md` (audio as a fourth local-file kind),
`docs/GUIDE.md` (a short "ingest a voice memo" section),
`axon.config.example.yaml` (a commented `stt` block), `docs/19` B1 (shipped,
with both open decisions resolved and the archiving finding recorded), and
`CHANGELOG.md` `[Unreleased]`.
