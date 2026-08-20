# docs/superpowers — design specs of record

`specs/` holds the per-slice design documents written during the 1.1 → 1.3 build
cycles (2026-07-02 → 2026-07-10). **They are load-bearing**: ADRs in
`docs/02-architecture.md` cite them as the design of record — do not delete or
renumber them.

A sibling `plans/` directory used to hold the step-by-step execution plans for
those same slices. Every plan was executed to completion and the directory was
deleted on 2026-08-20 (`git log -- docs/superpowers/plans` recovers them). If
you find a reference to a plan file, the work it describes shipped; the spec in
`specs/` plus the code are the surviving artefacts.
