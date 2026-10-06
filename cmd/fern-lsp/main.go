// cmd/fern-lsp is the stdio entry point for the Fern Language Server
// Protocol implementation. Editors / IDEs spawn this binary and speak
// LSP over stdin/stdout per the spec; the actual server logic lives
// in internal/tools/lsp so the wasm playground can drive it in-process.
//
// It advertises full-document sync and publishes `fern -check`'s
// diagnostics on every change; docs/LSP-INTEGRATION-PLAN.md lists the
// cursor features it serves.
package main

import (
	"fmt"
	"os"

	"github.com/jakechampion/lang/internal/tools/lsp"
)

func main() {
	s := lsp.NewServer()
	// Editors give us file:// URIs against real filesystem paths,
	// so route multi-file programs through modload — cross-module
	// imports type-check + resolve to-definition for the user.
	// The wasm wrapper leaves this off because the browser has
	// no sibling files to read.
	s.EnableWorkspace()
	if err := s.Serve(os.Stdin, os.Stdout); err != nil {
		// Write to stderr so we don't corrupt the LSP wire format
		// on stdout. The editor will see a non-zero exit and most
		// surface the stderr text in its language-server log.
		fmt.Fprintln(os.Stderr, "fern-lsp:", err)
		os.Exit(2)
	}
	os.Exit(s.ExitCode())
}
