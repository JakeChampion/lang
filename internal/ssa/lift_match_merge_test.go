package ssa_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/monomorph"
	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/ssa"
)

// A match whose arms produce different values via a result slot must lift to
// VALID SSA — the join needs a phi over the arm values. A merge that gives up
// and picks one arm's value when an unreachable predecessor edge leaves the
// result slot undefined produces `ret v` for a value defined on only one
// path (ssa.Verify: "uses vN before its def dominates").
func TestLiftMatchOptionJoinPhi(t *testing.T) {
	src := `
		function half(n: i32): Option[i32] {
			if (n % 2 == 0) { return Some(n / 2); }
			return None;
		}
		function main(): i32 {
			return match (half(7)) { Some(v) => v, None => 99 };
		}
	`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	irProg, err := ir.LowerWith(prog, info, 8)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	for _, fn := range irProg.Funcs {
		f, err := ssa.LiftFromIR(fn)
		if err != nil {
			t.Fatalf("lift %s: %v", fn.Name, err)
		}
		// The contract of this fix is well-formed SSA: the match join now carries
		// a phi over the arm result values (filled with the entry undef on the
		// unreachable impossible-arm edge), instead of `ret`ing a value defined
		// on only one path. (End-to-end *execution* of Option additionally needs
		// the IR's load/store bit-width carried through the lift — the boxes pack
		// i32 fields at 4-byte offsets, which the width-agnostic 8-byte memory
		// path overruns — a separate follow-up.)
		if err := ssa.Verify(f); err != nil {
			t.Fatalf("Verify(%s): %v", fn.Name, err)
		}
	}
}
