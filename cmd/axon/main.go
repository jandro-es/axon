// Command axon is the single entrypoint binary for AXON: the CLI that wires the
// daemon, MCP server, ingestion and observability. Phase 0 ships the skeleton —
// `config validate` and `doctor` are real; the rest are stubs.
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "axon: "+err.Error())
		os.Exit(1)
	}
}
