package main

import (
	"io"

	"github.com/jakechampion/lang/internal/caps"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/gates"
)

// runCapabilities implements `fern -capabilities FILE.fern`: load the
// entry exactly as a compile would (fern.toml packages, workspaces,
// vendored deps, literate entries all resolve through loadEntry),
// type-check it (so method calls are rewritten to their hoisted
// names), and print the per-package capability report computed by
// internal/caps. Report mode only — the report itself never enforces
// (grants are enforced on the compile/-check/-interp paths via
// gates.Capabilities); see docs/PACKAGE-CAPABILITIES-BRIEF.md (#5361).
func runCapabilities(srcPath string, w io.Writer) error {
	e, err := loadEntry(srcPath)
	if err != nil {
		return err
	}
	prog := e.prog
	if err := constfold.Fold(prog, nil); err != nil {
		return e.format(err)
	}
	if _, err := checker.Check(prog); err != nil {
		return e.format(err)
	}
	rows := caps.Analyze(prog, gates.PackageResolver(srcPath))
	_, err = io.WriteString(w, caps.Format(rows))
	return err
}
