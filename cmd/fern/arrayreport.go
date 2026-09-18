package main

import (
	"io"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/monomorph"
	"github.com/jakechampion/lang/internal/treeshake"
)

// runArrayReport implements `fern -array-report FILE.fern`: load and
// type-check the entry exactly as a compile would, lower it to IR, and print
// the std/array combinator pipelines the IR recognises (#9730).
//
// Report mode only — the same lowering a build runs, with the emitted code
// thrown away. It reads the RAW lowering rather than the optimised one, like
// `-append-report` beside it: the battery inlines std/array's one-line method
// delegates, so running it first would change which spelling of each call the
// report describes without changing what the program does.
func runArrayReport(srcPath string, w io.Writer) error {
	e, err := loadEntry(srcPath)
	if err != nil {
		return err
	}
	prog := e.prog
	if err := constfold.FoldWith(prog, constfold.Inputs{TargetOS: "linux", TargetArch: "arm64"}); err != nil {
		return e.format(err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		return e.format(err)
	}
	// monomorph because a combinator call on a generic receiver is an
	// indirect call until it resolves, and the recogniser keys on the
	// monomorphised name; the shake keeps the report to what a binary carries.
	if err := monomorph.Run(prog, info); err != nil {
		return e.format(err)
	}
	treeshake.Run(prog, info, treeshake.DropImplMethods(info)...)
	irProg, err := ir.LowerWith(prog, info, 8)
	if err != nil {
		return e.format(err)
	}
	_, err = io.WriteString(w, ir.FormatArrayPipelines(irProg))
	return err
}
