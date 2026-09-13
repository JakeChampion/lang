package main

import (
	"io"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/monomorph"
	"github.com/jakechampion/lang/internal/treeshake"
)

// runAppendReport implements `fern -append-report FILE.fern`: load and
// type-check the entry exactly as a compile would, lower it to IR, and
// print what emitArrayPush decided at each `.append` (#6992).
//
// Report mode only — the same lowering a build runs, with the emitted code
// thrown away.
func runAppendReport(srcPath string, w io.Writer) error {
	e, err := loadEntry(srcPath)
	if err != nil {
		return err
	}
	prog := e.prog
	// The report lowers as the default -target (arm64-linux) would, so
	// target_os() / target_arch() fold to its two halves and a program
	// branching on either still lowers.
	if err := constfold.FoldWith(prog, constfold.Inputs{TargetOS: "linux", TargetArch: "arm64"}); err != nil {
		return e.format(err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		return e.format(err)
	}
	// The same two passes a build runs before lowering: a method call on a
	// generic receiver is an indirect call until monomorph resolves it, and
	// the shake keeps the report to the functions a binary would carry.
	if err := monomorph.Run(prog, info); err != nil {
		return e.format(err)
	}
	treeshake.Run(prog, info, treeshake.DropImplMethods(info)...)
	irProg, err := ir.LowerWith(prog, info, 8)
	if err != nil {
		return e.format(err)
	}
	_, err = io.WriteString(w, ir.FormatAppendSites(irProg))
	return err
}
