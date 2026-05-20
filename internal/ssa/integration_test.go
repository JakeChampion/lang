package ssa_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/ssa"
)

// liftNamed parses + checks + lowers `src`, then lifts the
// single IR function with a Name matching `wantSuffix` (the
// test's named target — avoids dragging the entire auto-
// prelude through the lift, which would conflate test-vs-
// prelude bugs in any failure). Returns the lifted SSA Func.
func liftNamed(t *testing.T, src, wantSuffix string) *ssa.Func {
	t.Helper()
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
		if !strings.HasSuffix(fn.Name, wantSuffix) {
			continue
		}
		f, err := ssa.LiftFromIR(fn)
		if err != nil {
			t.Fatalf("LiftFromIR(%s): %v", fn.Name, err)
		}
		if err := ssa.Verify(f); err != nil {
			t.Fatalf("Verify(%s) after lift: %v", fn.Name, err)
		}
		return f
	}
	t.Fatalf("no IR func matched suffix %q", wantSuffix)
	return nil
}

// TestIntegrationSimpleAdd — end-to-end pipeline on a trivial
// add function: parser → checker → ir.LowerWith → LiftFromIR →
// Verify → Optimize → Verify. Confirms the pieces compose.
func TestIntegrationSimpleAdd(t *testing.T) {
	src := `function add(a: i32, b: i32): i32 { return a + b; }`
	add := liftNamed(t, src, "add")
	iters := ssa.Optimize(add)
	if iters < 1 {
		t.Errorf("Optimize iters = %d, want >= 1", iters)
	}
	if err := ssa.Verify(add); err != nil {
		t.Fatalf("Verify after Optimize: %v", err)
	}
}

// TestIntegrationIfElse — exercises the OpIf lift path via
// real source. Confirms the dominance-frontier + phi-merge
// machinery handles a checker-emitted shape.
func TestIntegrationIfElse(t *testing.T) {
	src := `
		function abs(n: i32): i32 {
			if (n < 0) {
				return 0 - n;
			} else {
				return n;
			}
		}
	`
	f := liftNamed(t, src, "abs")
	ssa.Optimize(f)
	if err := ssa.Verify(f); err != nil {
		t.Fatalf("Verify after Optimize: %v", err)
	}
}

// TestIntegrationLoop — counted loop via while.
func TestIntegrationLoop(t *testing.T) {
	src := `
		function sum(n: i32): i32 {
			var total: i32 = 0;
			var i: i32 = 0;
			while (i < n) {
				total = total + i;
				i = i + 1;
			}
			return total;
		}
	`
	f := liftNamed(t, src, "sum")
	ssa.Optimize(f)
	if err := ssa.Verify(f); err != nil {
		t.Fatalf("Verify after Optimize: %v", err)
	}
}

// TestIntegrationStats — Optimize on a real program should
// reduce op count for a constant-arithmetic chain.
func TestIntegrationStats(t *testing.T) {
	src := `function main(): i32 { return 1 + 2 + 3; }`
	main := liftNamed(t, src, "main")
	before := main.Stats()
	ssa.Optimize(main)
	after := main.Stats()
	if after.Ops >= before.Ops {
		t.Errorf("Optimize didn't reduce ops: before=%s after=%s", before, after)
	}
}
