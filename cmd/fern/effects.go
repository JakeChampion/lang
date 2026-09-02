package main

import (
	"io"

	"github.com/jakechampion/lang/internal/caps"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/effects"
	"github.com/jakechampion/lang/internal/platforms"
)

// runEffects implements `fern -effects FILE.fern`: the per-FUNCTION
// sibling of the per-package `-capabilities` report — the effect-rows
// prototype's measurement instrument (#5320,
// docs/EFFECT-ROWS-BRIEF.md).
//
// It exists to answer the question the deferral of effect rows rests
// on: what would a per-function effect row actually buy in Fern? The
// summary counts — how many functions reach nothing, the row-size
// distribution, how many are effect ORIGINS rather than inheritors,
// and how many are charged an effect only because a function value
// could be carrying anything — are the measurement; the per-function
// lines are how you audit one.
//
// The same call graph is printed under BOTH capability vocabularies,
// because they answer different questions and disagree in ways that
// matter: `authority` (internal/caps) is what a dependency could do to
// you and deliberately excludes stdio; `host` (internal/platforms) is
// what the target must provide and counts `print`. A function pure
// under one is not necessarily pure under the other.
//
// Report mode only: nothing here enforces. The shipped enforcement of
// the same reachability lives elsewhere — E070 per package
// (internal/caps) and E066 per target (internal/platforms).
func runEffects(srcPath string, w io.Writer) error {
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
	g := effects.Build(prog, info.BuiltinNames())
	for _, v := range []struct {
		title string
		table map[string]string
	}{
		{"authority (internal/caps) — what a dependency can reach", caps.BuiltinCaps},
		{"host (internal/platforms) — what the target must provide", platforms.GatedBuiltins()},
	} {
		if _, err := io.WriteString(w, effects.Format(v.title, effects.Report(g, v.table))); err != nil {
			return err
		}
		if _, err := io.WriteString(w, "\n"); err != nil {
			return err
		}
	}
	return nil
}
