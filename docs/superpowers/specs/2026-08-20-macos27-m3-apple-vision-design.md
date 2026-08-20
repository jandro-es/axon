# macOS 27 M3 — Apple vision tier (design)

**Status:** approved 2026-08-20. Decisions: transport = per-call
`fm respond --image` subprocess (Jandro-picked-recommended); model = **PCC
allowed under the opt-in** (Jandro's choice, deviating from the on-device-only
recommendation), interpreted explicitly: `ingestion.vision: apple` stays
on-device, a NEW `ingestion.vision: apple:pcc` mode selects PCC and validates
only while `models.pcc_enabled: true` — the switch gates it, the mode chooses
it, nothing changes silently under existing `apple` configs.
**FR-196/FR-197; ADR-038 amended** (PCC may also serve the vision primitive
when explicitly selected). Graduates docs/21 M3; fills ADR-035's `apple` slot.

Live verification this design rests on: `fm respond --image <png> --text
"Transcribe…"` returned an exact transcription + description from a
restricted context, exit 0.

## Components

1. **`ingestion.AppleVision`** (`internal/ingestion/vision_apple.go`) —
   implements `Vision`: writes img bytes to a 0600 temp file (extension from
   mime; fm takes paths), runs `fm respond --image <tmp> --text
   <visionPrompt>` (the same NFR-05 transcribe-then-describe prompt as
   OllamaVision), appends `--model pcc` **only** for the pcc variant,
   bounded 120 s + WaitDelay, ANSI-stripped output, temp file removed on
   every path. Injectable run seam. `Name()` = `apple` / `apple:pcc`.
   Perception primitive: budget-exempt, non-chokepoint (ADR-035 unchanged);
   the PCC variant is quota-advised via the existing doctor plumbing, never
   ledgered.
2. **`VisionFor`** — `apple` / `apple:pcc` resolve to `AppleVision` when
   darwin + `fm` on PATH (package seam `fmLookPath` for tests); otherwise the
   actionable error ("requires macOS 27 with the fm CLI") so wiring falls
   back to OCR-only and doctor reports — the seam's existing contract.
3. **Validation** — `Config.Validate`'s per-profile loop gains
   `validateVision(p)`: `ingestion.vision: apple:pcc` requires
   `models.pcc_enabled: true` (mirrors the tier gate); unknown `apple:<x>`
   vision modes rejected.
4. **Doctor `visionCheck`** — the `apple`/`apple:pcc` arms use `DetectFM`:
   ready → OK; licence-pending → WARN + `sudo fm license`; absent/too-old →
   WARN (images fall back to OCR-only). The pcc arm reminds that PCC is
   context-gated.
5. **Disclosure (FR-197)** — text redaction cannot apply to pixels: PCC
   vision sends **unredacted image bytes** to Apple-operated compute. Stated
   in the ADR amendment, the example config comment, and the GUIDE row.

## Out of scope

fm serve multimodal; changing OllamaVision or the extractImage OCR-first
flow; PCC fallback from a failed on-device describe (a vision error keeps
the OCR/filename path, as today); barcode/ocr fm tools.

## Verification

Unit: AppleVision (prompt shape, pcc flag only on pcc, temp cleanup, ANSI
strip, error surface), VisionFor table, validateVision gate, visionCheck
states. Live smoke: `ingestion.vision: apple` + `axon ingest` of a real
image → note carries the on-device description; `apple:pcc` without opt-in →
validation rejection; with opt-in → real context-unavailable degrade to the
OCR/filename path, no crash.
