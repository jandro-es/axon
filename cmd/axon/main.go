// Command axon is the single entrypoint binary for AXON: the CLI that wires the
// daemon, MCP server, ingestion and observability. Phase 0 ships the skeleton —
// `config validate` and `doctor` are real; the rest are stubs.
package main

import (
	"os"

	"github.com/jandro-es/axon/internal/ui"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		ui.FprintError(os.Stderr, err)
		os.Exit(1)
	}
}
