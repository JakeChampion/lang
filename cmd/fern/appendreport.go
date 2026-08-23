package main

import (
	"io"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/ir"
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
	if err := constfold.Fold(prog, nil); err != nil {
		return e.format(err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		return e.format(err)
	}
	irProg, err := ir.LowerWith(prog, info, 8)
	if err != nil {
		return e.format(err)
	}
	_, err = io.WriteString(w, ir.FormatAppendSites(irProg))
	return err
}
