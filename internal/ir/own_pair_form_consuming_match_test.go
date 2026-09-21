package ir_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// A consuming (`own`-param) match arm whose body returns a same-enum
// construction is reuse-paired, and the pairing moves the arm's box release —
// the uniqueness test, the shallow free, and the shared-branch payload
// retains — into the reuse token that construction emits. A PAIR-FORM return
// emits no token, so the whole release went missing: the box leaked and the
// payload binding stayed an uncounted alias, which is how `.with` came to
// write through a box another binding still named (#9901).
//
// Asserted on the op stream rather than only at runtime because the pairing is
// what decides this, and the runtime symptom is whichever of "wrong answer" or
// "leak" the program happens to expose. The e2e differential covers the answer.
const ownPairFormConsumingMatch = `enum Box { Full(i32[]), Empty }

@noinline
function put(own b: Box, i: i32, x: i32): Box {
    match (b) { Full(xs) => { return Full(xs.with(i, x)); }, Empty => { return Empty; } }
}

function main(): i32 {
    var b: Box = Full([1, 2, 3]);
    b = put(b, 0, 9);
    match (b) { Full(xs) => { return xs[0]; }, Empty => { return 1; } }
}
`

// armBeforeFirst returns the ops of `name` up to its first matching op, which
// for a pair-form consuming traversal is the arm's own extent: the Full arm
// returns through OpMakeSomeI32, so everything before it belongs to that arm.
func opsBefore(fn *ir.Func, stop func(ir.Op) bool) []ir.Op {
	for i, op := range fn.Ops {
		if stop(op) {
			return fn.Ops[:i]
		}
	}
	return fn.Ops
}

func TestOwnPairFormConsumingMatchArmReleasesItsBox(t *testing.T) {
	prog := lowerForTest(t, ownPairFormConsumingMatch)
	var put *ir.Func
	for _, f := range prog.Funcs {
		if f.Name == "put" {
			put = f
		}
	}
	if put == nil {
		t.Fatal("no lowered `put`")
	}
	// The arm ends at the pair-form return, which is also the proof this
	// function lowers the shape the bug needs: no box construction at all.
	arm := opsBefore(put, func(op ir.Op) bool { return op.Kind == ir.OpMakeSomeI32 })
	if len(arm) == len(put.Ops) {
		t.Fatal("`put` does not return the pair form — the case this pins no longer reproduces")
	}
	var uniq, inc, alloc int
	for _, op := range arm {
		switch {
		case op.Kind == ir.OpRcIsUnique:
			uniq++
		case op.Kind == ir.OpRcInc:
			inc++
		case op.Kind == ir.OpCallDirect && op.Str == "__alloc_reuse":
			alloc++
		}
	}
	if alloc != 0 {
		t.Errorf("arm emits %d __alloc_reuse — a pair-form return builds no box to reuse", alloc)
	}
	// One test for the box release (free on unique, dec on shared) and one for
	// the `.with` copy-on-write, plus the retain of the moved-out payload on
	// the shared branch. Before the fix the arm had only the `.with` test and
	// no retain at all.
	if uniq < 2 {
		t.Errorf("arm emits %d rc.is_unique, want at least 2 (the box release and the `.with`)", uniq)
	}
	if inc == 0 {
		t.Error("arm emits no rc.inc — the payload binding is an uncounted alias of a box that may be shared")
	}
}
