package ssa_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/ssa"
)

// TestLiftAllPreludeFunctions lifts every function in the
// lowered IR of a minimal main program (which pulls in the
// auto-prelude). Each function must lift without an error
// and verify cleanly. Acts as a broad robustness test —
// catches any IR shape the lift mis-handles.
func TestLiftAllPreludeFunctions(t *testing.T) {
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

	for _, fn := range irProg.Funcs {
		t.Run(fn.Name, func(t *testing.T) {
			f, err := ssa.LiftFromIR(fn)
			if err != nil {
				t.Errorf("LiftFromIR: %v", err)
				return
			}
			if err := ssa.Verify(f); err != nil {
				t.Errorf("Verify: %v", err)
				return
			}
		})
	}
}
