package main

import (
	"io"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/check/constfold"
	"github.com/jakechampion/lang/internal/pkg/caps"
	"github.com/jakechampion/lang/internal/tools/gates"
)

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
	constfold.PruneDeadArms(prog)
	rows, _ := gates.CapabilityRows(srcPath, prog)
	_, err = io.WriteString(w, caps.Format(rows))
	return err
}
