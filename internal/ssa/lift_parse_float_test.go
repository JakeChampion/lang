package ssa_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/ssa"
)

// TestLiftPreludeParseFloat — regression test for the parse_float
// phi-dominance bug. parse_float has a sequence of conditional
// while-loops that update local booleans (`saw_int`, `saw_frac`)
// only in some loops, not others. Before the loop-body pre-scan
// (loopBodyWrites), the lift eagerly created a header phi for
// every initialised slot at every OpLoop, producing phis with
// args that didn't dominate their pred when the loop was
// conditionally entered (e.g., the `if (i < n && s[i] == '.')`
// guard around the fraction loop). Now the lift only creates
// header phis for slots actually written in the loop body, so
// the unwritten slots keep their pre-loop merged Value and
// the dominance invariants hold.
func TestLiftPreludeParseFloat(t *testing.T) {
	src := `function main(): i32 { return 0; }`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	irProg, err := ir.LowerWith(prog, info, 8)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}

	var found bool
	for _, fn := range irProg.Funcs {
		if !strings.Contains(fn.Name, "parse_float") {
			continue
		}
		found = true
		f, err := ssa.LiftFromIR(fn)
		if err != nil {
			t.Fatalf("LiftFromIR(%s): %v", fn.Name, err)
		}
		if err := ssa.Verify(f); err != nil {
			t.Fatalf("Verify(%s) after lift: %v", fn.Name, err)
		}
	}
	if !found {
		t.Skip("parse_float not in lowered IR (auto-prelude missing?)")
	}
}
