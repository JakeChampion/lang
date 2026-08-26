package arm64ssa

import (
	"strings"
	"testing"

	x86 "github.com/jakechampion/lang/internal/codegen/x86_64ssa"
)

// A call delivers its result straight into the destination register, skipping
// the staging scratch — but only while the restores cannot overwrite that
// register. The allocator never produces the overlapping case (a result and a
// value live across the same call cannot share a register), so this exercises
// the guard directly rather than waiting for an allocation that should not
// exist.
func TestCallResultStagingDependsOnTheSaveSet(t *testing.T) {
	const numAlloc = DefaultNumAlloc
	scratch := numAlloc + 3 // what emitFuncBody passes: p.NumRegFile - 1

	render := func(saveRegs []int) string {
		t.Helper()
		lines, err := callLines(x86.Inst{
			Op:          x86.Call,
			Callee:      "callee",
			Dst:         3, // x3
			SaveRegs:    saveRegs,
			SaveRegsSet: true,
		}, numAlloc, scratch, 0)
		if err != nil {
			t.Fatalf("callLines: %v", err)
		}
		return strings.Join(lines, "\n")
	}

	staging := "mov " + xreg(scratch) + ", x0"

	t.Run("destination outside the save set is written directly", func(t *testing.T) {
		got := render([]int{4})
		if strings.Contains(got, staging) {
			t.Errorf("result staged through %s even though it is not restored:\n%s", xreg(scratch), got)
		}
		if !strings.Contains(got, "mov x3, x0") {
			t.Errorf("result not delivered straight into x3:\n%s", got)
		}
	})

	t.Run("destination inside the save set is staged", func(t *testing.T) {
		got := render([]int{3})
		if !strings.Contains(got, staging) {
			t.Errorf("result written before a restore of the same register would clobber it,"+
				" so it must stage through %s:\n%s", xreg(scratch), got)
		}
		if !strings.Contains(got, "mov x3, "+xreg(scratch)) {
			t.Errorf("staged result never placed into x3:\n%s", got)
		}
		if strings.Index(got, "ldr x3,") > strings.Index(got, "mov x3, "+xreg(scratch)) {
			t.Errorf("x3 restored AFTER the result was placed into it — the restore wins:\n%s", got)
		}
	})
}

// A pair-returning call delivers x0/x1 as a parallel copy, so a destination
// that is itself x0 or x1 cannot clobber the other half before it is read.
func TestCallPairDeliversBothResultsWithoutClobber(t *testing.T) {
	const numAlloc = DefaultNumAlloc
	lines, err := callLines(x86.Inst{
		Op:          x86.CallPair,
		Callee:      "callee",
		Dst:         1, // x1 — the payload's own return register
		Dst2:        0, // x0 — the tag's
		SaveRegsSet: true,
	}, numAlloc, numAlloc+3, 0)
	if err != nil {
		t.Fatalf("callLines: %v", err)
	}
	got := strings.Join(lines, "\n")
	// Written naively as two movs this is `mov x1, x0` then `mov x0, x1`, which
	// leaves both halves holding the tag. resolveRegMoves has to break the cycle.
	if strings.Contains(got, "mov x1, x0\n\tmov x0, x1") || strings.Contains(got, "mov x1, x0\nmov x0, x1") {
		t.Errorf("swapped pair destinations rendered as two movs, losing the payload:\n%s", got)
	}
	if !strings.Contains(got, "eor") {
		t.Errorf("expected the eor swap for the x0/x1 cycle:\n%s", got)
	}
}
