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

// TestOptimizeAllPreludeFunctions runs the Optimize pipeline
// on every prelude function and re-verifies the result.
// Catches any pass that breaks SSA structural invariants on
// real code shapes. Independent from the lift-only sweep so
// a regression here is distinguishable from a lift regression.
func TestOptimizeAllPreludeFunctions(t *testing.T) {
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
			ssa.Optimize(f)
			if err := ssa.Verify(f); err != nil {
				t.Errorf("Verify after Optimize: %v", err)
				return
			}
		})
	}
}

// TestOptimizeIdempotentOnPrelude — Optimize must converge to a
// fixed point. After one Optimize pass, the function's String()
// form should be stable across additional Optimize calls. A
// regression here means some pass is non-idempotent (e.g.,
// flip-flopping operand order, oscillating between two
// equivalent shapes).
func TestOptimizeIdempotentOnPrelude(t *testing.T) {
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
			ssa.Optimize(f)
			first := f.String()
			iters := ssa.Optimize(f)
			second := f.String()
			if iters != 1 {
				t.Errorf("second Optimize iters = %d, want 1 (already converged)", iters)
			}
			if first != second {
				t.Errorf("Optimize not idempotent: first call result differs from second")
			}
		})
	}
}
