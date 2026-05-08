package codegen

import (
	"strings"
	"testing"
)

func TestPushPopSameReg(t *testing.T) {
	got := peephole("\tpush {r0}\n\tpop {r0}\n\tbl putchar")
	if !strings.Contains(got, "bl putchar") {
		t.Fatalf("missing bl putchar: %q", got)
	}
	if strings.Contains(got, "push {r0}") || strings.Contains(got, "pop {r0}") {
		t.Errorf("push/pop should be removed:\n%s", got)
	}
}

func TestPushPopFoldsToMov(t *testing.T) {
	got := peephole("\tpush {r0}\n\tpop {r2}")
	if !strings.Contains(got, "mov r2, r0") {
		t.Errorf("expected mov r2, r0, got:\n%s", got)
	}
	if strings.Contains(got, "push") || strings.Contains(got, "pop") {
		t.Errorf("push/pop should be gone:\n%s", got)
	}
}

func TestBranchToNextLineDropped(t *testing.T) {
	// `ldr =.Lend` keeps the label alive (it's a reference
	// that's not a branch, so it doesn't get next-line-elided).
	// The trailing `b .Lend` is adjacent to the label and gets
	// dropped.
	in := "\tldr r1, =.Lend\n\tb .Lend\n.Lend:\n\tmov sp, fp"
	got := peephole(in)
	if strings.Contains(got, "\tb .Lend") {
		t.Errorf("branch should be removed:\n%s", got)
	}
	if !strings.Contains(got, ".Lend:") {
		t.Errorf("label should remain (referenced by ldr =):\n%s", got)
	}
}

func TestStoreLoadSameDropsLoad(t *testing.T) {
	in := "\tstr r0, [fp, #-4]\n\tldr r0, [fp, #-4]"
	got := peephole(in)
	if strings.Contains(got, "ldr") {
		t.Errorf("ldr should be removed:\n%s", got)
	}
	if !strings.Contains(got, "str r0, [fp, #-4]") {
		t.Errorf("str should remain:\n%s", got)
	}
}

func TestStoreLoadDifferentAddrUntouched(t *testing.T) {
	in := "\tstr r0, [fp, #-4]\n\tldr r0, [fp, #-8]"
	got := peephole(in)
	if !strings.Contains(got, "ldr r0, [fp, #-8]") {
		t.Errorf("different-addr ldr should be kept:\n%s", got)
	}
}

func TestSelfMovRemoved(t *testing.T) {
	got := peephole("\tmov r0, r0\n\tbx lr")
	if strings.Contains(got, "mov r0, r0") {
		t.Errorf("self-mov not removed:\n%s", got)
	}
}

func TestLabelBetweenBlocksFusion(t *testing.T) {
	// A label between str and ldr means we cannot drop the ldr — other
	// code may branch to the label and skip the str. Add an external
	// branch into the label so the dead-label sweep keeps it alive.
	in := "\tb .Lother\n\tstr r0, [fp, #-4]\n.Lother:\n\tldr r0, [fp, #-4]"
	got := peephole(in)
	if !strings.Contains(got, "ldr r0, [fp, #-4]") {
		t.Errorf("ldr after label was unsafely removed:\n%s", got)
	}
}

// The cmpPop / fcmpPop materialise + branch sequence collapses
// to a single conditional branch that uses the flags already
// set by the preceding cmp / vcmp.
func TestCmpBranchFusionEqElseBranch(t *testing.T) {
	in := strings.Join([]string{
		"\tcmp r1, r0",
		"\tmoveq r0, #1",
		"\tmovne r0, #0",
		"\tcmp r0, #0",
		"\tbeq .LifElse",
	}, "\n")
	got := peephole(in)
	if !strings.Contains(got, "bne .LifElse") {
		t.Errorf("expected bne .LifElse, got:\n%s", got)
	}
	if strings.Contains(got, "moveq r0, #1") || strings.Contains(got, "movne r0, #0") {
		t.Errorf("mov pair should be folded away:\n%s", got)
	}
	if strings.Count(got, "cmp") != 1 {
		t.Errorf("only the original cmp should remain; got:\n%s", got)
	}
}

func TestCmpBranchFusionLtBrIf(t *testing.T) {
	in := strings.Join([]string{
		"\tcmp r1, r0",
		"\tmovlt r0, #1",
		"\tmovge r0, #0",
		"\tcmp r0, #0",
		"\tbne .Lloop",
	}, "\n")
	got := peephole(in)
	if !strings.Contains(got, "blt .Lloop") {
		t.Errorf("expected blt .Lloop, got:\n%s", got)
	}
}

func TestCmpBranchFusionFloatCondCodes(t *testing.T) {
	// fcmpPop emits the same materialise pair after the
	// vcmp.f32 + vmrs sequence — peephole shape matches.
	in := strings.Join([]string{
		"\tvcmp.f32 s1, s0",
		"\tvmrs APSR_nzcv, FPSCR",
		"\tmovmi r0, #1",
		"\tmovpl r0, #0",
		"\tcmp r0, #0",
		"\tbeq .LifElse",
	}, "\n")
	got := peephole(in)
	if !strings.Contains(got, "bpl .LifElse") {
		t.Errorf("expected bpl .LifElse, got:\n%s", got)
	}
}

// Conditional branch to the very next instruction is dead, just
// like the unconditional case — the fallthrough target is the
// same regardless of the branch outcome.
func TestConditionalBranchToNextLineDropped(t *testing.T) {
	// `ldr =.Lnext` keeps the label alive past the
	// dead-label sweep.
	in := "\tldr r1, =.Lnext\n\tbne .Lnext\n.Lnext:\n\tmov sp, fp"
	got := peephole(in)
	if strings.Contains(got, "bne .Lnext") {
		t.Errorf("conditional branch to next line should be removed:\n%s", got)
	}
	if !strings.Contains(got, ".Lnext:") {
		t.Errorf("label should remain (referenced by ldr =):\n%s", got)
	}
}

// The const-imm peephole covers the data-processing ops and
// the shifts that the IR's binary lowerings emit. Each test
// asserts the fold drops the const-load and rewrites the op
// to use an immediate.
func TestArithImmFold_AddSubAndOrrEor(t *testing.T) {
	cases := []struct {
		mnemonic string
		imm      int
	}{
		{"add", 5},
		{"sub", 12},
		{"and", 255},
		{"orr", 16},
		{"eor", 7},
	}
	for _, tc := range cases {
		in := strings.Join([]string{
			"\tldr r0, =" + itoa(tc.imm),
			"\tpop {r1}",
			"\t" + tc.mnemonic + " r0, r1, r0",
		}, "\n")
		got := peephole(in)
		want := tc.mnemonic + " r0, r1, #" + itoa(tc.imm)
		if !strings.Contains(got, want) {
			t.Errorf("%s: expected %q, got:\n%s", tc.mnemonic, want, got)
		}
		if strings.Contains(got, "ldr r0, =") {
			t.Errorf("%s: const-load should be elided:\n%s", tc.mnemonic, got)
		}
	}
}

// Shifts use a 0..31 immediate window (the full encoding). The
// fold uses the same shape as the data-processing ops.
func TestArithImmFold_Shifts(t *testing.T) {
	for _, mn := range []string{"lsl", "asr"} {
		in := strings.Join([]string{
			"\tldr r0, =4",
			"\tpop {r1}",
			"\t" + mn + " r0, r1, r0",
		}, "\n")
		got := peephole(in)
		want := mn + " r0, r1, #4"
		if !strings.Contains(got, want) {
			t.Errorf("%s: expected %q, got:\n%s", mn, want, got)
		}
	}
}

// Shift counts > 31 don't fold — the encoding window is 0..31
// and out-of-range values aren't valid as immediate operands
// to `lsl` / `asr`.
func TestArithImmFold_ShiftsOutOfRangeNotFolded(t *testing.T) {
	in := strings.Join([]string{
		"\tldr r0, =40",
		"\tpop {r1}",
		"\tlsl r0, r1, r0",
	}, "\n")
	got := peephole(in)
	if !strings.Contains(got, "ldr r0, =40") {
		t.Errorf("shift count 40 must not fold (out of range):\n%s", got)
	}
}

// Address-mode sink: `add rD, rB, #N ; ldr rD, [rD]` collapses
// to `ldr rD, [rB, #N]` — the load overwrites rD, so the
// add's only consumer is gone.
func TestAddrModeSinkAddImm(t *testing.T) {
	in := strings.Join([]string{
		"\tadd r0, r1, #4",
		"\tldr r0, [r0]",
	}, "\n")
	got := peephole(in)
	if !strings.Contains(got, "ldr r0, [r1, #4]") {
		t.Errorf("expected `ldr r0, [r1, #4]`, got:\n%s", got)
	}
	if strings.Contains(got, "add r0, r1, #4") {
		t.Errorf("add should be elided:\n%s", got)
	}
}

// `sub` with a positive immediate becomes a negated offset in
// the addressing mode.
func TestAddrModeSinkSubBecomesNegOffset(t *testing.T) {
	in := strings.Join([]string{
		"\tsub r0, r1, #4",
		"\tldr r0, [r0]",
	}, "\n")
	got := peephole(in)
	if !strings.Contains(got, "ldr r0, [r1, #-4]") {
		t.Errorf("expected `ldr r0, [r1, #-4]`, got:\n%s", got)
	}
}

// ldrb shares the same fold shape as ldr.
func TestAddrModeSinkLdrb(t *testing.T) {
	in := strings.Join([]string{
		"\tadd r0, r1, #2",
		"\tldrb r0, [r0]",
	}, "\n")
	got := peephole(in)
	if !strings.Contains(got, "ldrb r0, [r1, #2]") {
		t.Errorf("expected `ldrb r0, [r1, #2]`, got:\n%s", got)
	}
}

// When the load destination differs from the add's destination,
// the add result might still be live, so the fold is skipped.
func TestAddrModeSinkSkipsWhenDstDiffers(t *testing.T) {
	in := strings.Join([]string{
		"\tadd r2, r1, #4",
		"\tldr r0, [r2]",
	}, "\n")
	got := peephole(in)
	if !strings.Contains(got, "add r2, r1, #4") {
		t.Errorf("add should remain (r2 may still be live):\n%s", got)
	}
}

// If-merge stack-elim: when both arms push r0 and the
// consumer is `pop {r0}`, the round-trip through the stack
// is pure overhead — both arms already wrote r0 directly.
// Drop the two pushes and the consumer pop.
func TestIfMergeStackElimWithPopConsumer(t *testing.T) {
	in := strings.Join([]string{
		"\tldr r1, =.LifElse",
		"\tldr r2, =.LifEnd", // keep both labels alive
		"\tldr r0, =1",
		"\tpush {r0}", // then-arm push
		"\tb .LifEnd",
		".LifElse:",
		"\tldr r0, =0",
		"\tpush {r0}", // else-arm push
		".LifEnd:",
		"\tpop {r0}", // merge consumer
	}, "\n")
	got := peephole(in)
	if strings.Contains(got, "push {r0}") {
		t.Errorf("if-merge pushes should be elided:\n%s", got)
	}
	if strings.Contains(got, "pop {r0}") {
		t.Errorf("merge consumer pop should be elided:\n%s", got)
	}
}

// Same elim works when the consumer is `add sp, sp, #4` (the
// IR's OpDrop), since dropping the value matches dropping
// the pushes.
func TestIfMergeStackElimWithDropConsumer(t *testing.T) {
	in := strings.Join([]string{
		"\tldr r1, =.LifElse",
		"\tldr r2, =.LifEnd",
		"\tldr r0, =1",
		"\tpush {r0}",
		"\tb .LifEnd",
		".LifElse:",
		"\tldr r0, =0",
		"\tpush {r0}",
		".LifEnd:",
		"\tadd sp, sp, #4", // OpDrop
	}, "\n")
	got := peephole(in)
	if strings.Contains(got, "push {r0}") {
		t.Errorf("pushes should be elided:\n%s", got)
	}
	if strings.Contains(got, "add sp, sp, #4") {
		t.Errorf("OpDrop should be elided:\n%s", got)
	}
}

// When the consumer is something other than `pop {r0}` /
// `add sp, sp, #4` (e.g. `pop {r1}` for a binPop's lhs),
// the elim doesn't fire — the pop reads from a specific
// stack offset, which would be wrong without the pushes.
func TestIfMergeStackElim_BinPopConsumerSkipped(t *testing.T) {
	in := strings.Join([]string{
		"\tldr r1, =.LifElse",
		"\tldr r2, =.LifEnd",
		"\tldr r0, =1",
		"\tpush {r0}",
		"\tb .LifEnd",
		".LifElse:",
		"\tldr r0, =0",
		"\tpush {r0}",
		".LifEnd:",
		"\tpop {r1}", // wrong consumer
	}, "\n")
	got := peephole(in)
	if !strings.Contains(got, "push {r0}") {
		t.Errorf("pushes must remain (consumer reads r1):\n%s", got)
	}
}

// Two adjacent label declarations merge into one. References
// to the dropped label get rewritten to the kept label.
func TestAdjacentLabelMergeRewritesReferences(t *testing.T) {
	in := strings.Join([]string{
		"\tb .Lsecond",
		".Lfirst:",
		".Lsecond:", // adjacent — merges with .Lfirst
		"\tldr r0, =.Lsecond",
		"\tbx lr",
	}, "\n")
	got := peephole(in)
	if strings.Contains(got, ".Lsecond") {
		t.Errorf(".Lsecond references should be rewritten to .Lfirst:\n%s", got)
	}
	if !strings.Contains(got, ".Lfirst") {
		t.Errorf(".Lfirst must remain:\n%s", got)
	}
}

// Branch threading: `b L1` where `L1: b L2` rewrites the
// outer branch to target `L2` directly, skipping the
// trampoline label. Tested at the helper level so we see
// the threading output without the rest of the peephole
// cascade further collapsing the result.
func TestBranchThreadingSkipsTrampoline(t *testing.T) {
	in := strings.Join([]string{
		"\tb .Lthrough",
		".Lpre:",
		"\tldr r0, =1",
		".Lthrough:",
		"\tb .Ltarget",
		".Lpost:",
		"\tldr r1, =2",
		".Ltarget:",
		"\tbx lr",
	}, "\n")
	got := threadBranches(in)
	if !strings.Contains(got, "\tb .Ltarget\n") {
		t.Errorf("outer `b .Lthrough` should be threaded to `b .Ltarget`:\n%s", got)
	}
}

// Pop-pair fusion: `pop {r0} ; pop {r1}` (sequential regs)
// becomes `pop {r0, r1}` — one ldm instead of two ldrs.
func TestPopFusionAdjacentRegisters(t *testing.T) {
	in := "\tpop {r0}\n\tpop {r1}\n\tcmp r0, r1"
	got := peephole(in)
	if !strings.Contains(got, "pop {r0, r1}") {
		t.Errorf("expected fused `pop {r0, r1}`, got:\n%s", got)
	}
	if strings.Contains(got, "\tpop {r0}\n\tpop {r1}") {
		t.Errorf("separate pops should be gone:\n%s", got)
	}
}

// Pop-pair fusion only fires when the registers are in
// increasing order — `pop {r1} ; pop {r0}` would put the
// wrong values in each register if merged.
func TestPopFusion_BackwardOrderUntouched(t *testing.T) {
	in := "\tpop {r1}\n\tpop {r0}\n\tcmp r1, r0"
	got := peephole(in)
	if !strings.Contains(got, "\tpop {r1}\n\tpop {r0}") {
		t.Errorf("backward-order pops must not fuse:\n%s", got)
	}
}

// Local labels (`.L*`) with no incoming references get dropped
// — common after branch inversion / next-line elision leaves
// behind a trampoline label that nothing branches to.
func TestDeadLabelDropped(t *testing.T) {
	in := strings.Join([]string{
		"\tcmp r1, #1",
		"\tbne .Llive",
		".Ldead:", // unreferenced — should disappear
		"\tldr r0, =42",
		".Llive:",
		"\tbx lr",
	}, "\n")
	got := peephole(in)
	if strings.Contains(got, ".Ldead:") {
		t.Errorf("unreferenced .Ldead: should be dropped:\n%s", got)
	}
	if !strings.Contains(got, ".Llive:") {
		t.Errorf("referenced .Llive: must remain:\n%s", got)
	}
}

// Externally-visible symbols (no `.L` prefix) are untouched
// even if no internal reference appears in this snippet —
// callers from other compilation units can still target them.
func TestDeadLabelKeepsGlobalSymbols(t *testing.T) {
	in := "main:\n\tbx lr"
	got := peephole(in)
	if !strings.Contains(got, "main:") {
		t.Errorf("global symbol must remain:\n%s", got)
	}
}

// Code between an unconditional branch and the next label is
// unreachable; the peephole drops it. `ldr =.Lend` is a non-
// branch reference that keeps the label alive past the
// dead-label sweep.
func TestUnreachableAfterBranchDropped(t *testing.T) {
	in := strings.Join([]string{
		"\tldr r2, =.Lend",
		"\tb .Lend",
		"\tldr r0, =42", // dead — between b and the next label
		"\tmov r1, r3",  // also dead
		".Lend:",
		"\tbx lr",
	}, "\n")
	got := peephole(in)
	if strings.Contains(got, "ldr r0, =42") || strings.Contains(got, "mov r1, r3") {
		t.Errorf("instructions after `b` should be dropped:\n%s", got)
	}
	if !strings.Contains(got, ".Lend:") {
		t.Errorf("label must remain:\n%s", got)
	}
}

// `bx lr` is also an unconditional control transfer — same
// dead-code shape as `b LBL`. `ldr =.Lnext` references
// `.Lnext` so the dead-label sweep doesn't drop it.
func TestUnreachableAfterBxLrDropped(t *testing.T) {
	in := strings.Join([]string{
		"\tldr r2, =.Lnext",
		"\tbx lr",
		"\tldr r0, =42", // dead
		".Lnext:",
		"\tldr r1, =1",
	}, "\n")
	got := peephole(in)
	if strings.Contains(got, "ldr r0, =42") {
		t.Errorf("instruction after `bx lr` should be dropped:\n%s", got)
	}
	if !strings.Contains(got, "ldr r1, =1") {
		t.Errorf("instruction after the next label must remain:\n%s", got)
	}
}

// Branch inversion: `b<cc> THEN ; b ELSE ; THEN:` collapses
// to `b<!cc> ELSE ; THEN:` — the conditional jumps to the
// fallthrough alternative instead of forking around a
// trivial trampoline.
func TestBranchInversionCollapsesPair(t *testing.T) {
	in := strings.Join([]string{
		"\tcmp r1, #1",
		"\tbeq .LblkEnd_5",
		"\tb .LblkEnd_4",
		".LblkEnd_5:",
	}, "\n")
	got := peephole(in)
	if !strings.Contains(got, "bne .LblkEnd_4") {
		t.Errorf("expected `bne .LblkEnd_4`, got:\n%s", got)
	}
	if strings.Contains(got, "beq .LblkEnd_5") {
		t.Errorf("conditional branch over the next label should be folded away:\n%s", got)
	}
	if strings.Contains(got, "\tb .LblkEnd_4\n") {
		t.Errorf("unconditional branch should be elided:\n%s", got)
	}
}

// All the standard cc pairs invert correctly.
func TestBranchInversionEveryCondCode(t *testing.T) {
	pairs := [][2]string{
		{"eq", "ne"},
		{"ne", "eq"},
		{"cs", "cc"},
		{"cc", "cs"},
		{"mi", "pl"},
		{"pl", "mi"},
		{"vs", "vc"},
		{"vc", "vs"},
		{"hi", "ls"},
		{"ls", "hi"},
		{"ge", "lt"},
		{"lt", "ge"},
		{"gt", "le"},
		{"le", "gt"},
	}
	for _, p := range pairs {
		in := strings.Join([]string{
			"\tb" + p[0] + " .Lthen",
			"\tb .Lelse",
			".Lthen:",
		}, "\n")
		got := peephole(in)
		want := "b" + p[1] + " .Lelse"
		if !strings.Contains(got, want) {
			t.Errorf("%s should invert to %s; got:\n%s", p[0], p[1], got)
		}
	}
}

// When the conditional branch's target *isn't* the next
// label, the fold can't fire — taking either branch leads
// to a different place.
func TestBranchInversionSkipsWhenTargetDiffers(t *testing.T) {
	in := strings.Join([]string{
		"\tbeq .Lother",
		"\tb .Lelse",
		".Lthen:",
	}, "\n")
	got := peephole(in)
	if !strings.Contains(got, "beq .Lother") {
		t.Errorf("beq should remain (target isn't the next label):\n%s", got)
	}
}

// mov-chain elim: `mov r0, rB ; mov rA, r0` followed by some
// r0-not-using lines and then a pure r0-overwrite drops the
// first mov, since r0 was dead from the second mov onward.
func TestMovChainElimDropsRedundantR0Load(t *testing.T) {
	in := strings.Join([]string{
		"\tmov r0, r4",
		"\tmov r1, r0",
		"\tcmp r1, #0",
		"\tbne .LifElse",
		"\tldr r0, =1", // pure r0-overwrite — chain ends here
	}, "\n")
	got := peephole(in)
	if !strings.Contains(got, "mov r1, r4") {
		t.Errorf("expected fused `mov r1, r4`, got:\n%s", got)
	}
	if strings.Contains(got, "mov r0, r4") {
		t.Errorf("dead mov r0, r4 should be elided:\n%s", got)
	}
}

// When the next r0-write isn't pure (e.g. `add r0, r0, #N`
// reads r0), the chain isn't safe to fold — we'd be reading
// from a register we just dropped.
func TestMovChainElim_NotPureWriteSkipped(t *testing.T) {
	in := strings.Join([]string{
		"\tmov r0, r4",
		"\tmov r1, r0",
		"\tadd r0, r0, #1", // reads r0, writes r0 — chain must remain
	}, "\n")
	got := peephole(in)
	if !strings.Contains(got, "mov r0, r4") {
		t.Errorf("first mov must remain (next op reads r0):\n%s", got)
	}
}

// An intervening instruction that reads r0 invalidates the
// fold — the second mov can't be eliminated because r0's
// value was consumed.
func TestMovChainElim_InterveningR0ReadSkipped(t *testing.T) {
	in := strings.Join([]string{
		"\tmov r0, r4",
		"\tmov r1, r0",
		"\tstr r0, [r2]", // reads r0
		"\tldr r0, =5",
	}, "\n")
	got := peephole(in)
	if !strings.Contains(got, "mov r0, r4") {
		t.Errorf("first mov must remain (str reads r0):\n%s", got)
	}
}

// `ldr r0, =N ; pop {rN} ; cmp rN, r0` (small N) collapses to
// `pop {rN} ; cmp rN, #N`, dropping the literal load entirely.
func TestCmpAgainstSmallConstFolds(t *testing.T) {
	in := strings.Join([]string{
		"\tldr r0, =0",
		"\tpop {r1}",
		"\tcmp r1, r0",
	}, "\n")
	got := peephole(in)
	if !strings.Contains(got, "cmp r1, #0") {
		t.Errorf("expected `cmp r1, #0`, got:\n%s", got)
	}
	if strings.Contains(got, "ldr r0, =0") {
		t.Errorf("ldr should be elided:\n%s", got)
	}
}

func TestCmpAgainstSmallConstFolds_NonZero(t *testing.T) {
	in := strings.Join([]string{
		"\tldr r0, =42",
		"\tpop {r3}",
		"\tcmp r3, r0",
	}, "\n")
	got := peephole(in)
	if !strings.Contains(got, "cmp r3, #42") {
		t.Errorf("expected `cmp r3, #42`, got:\n%s", got)
	}
}

// Constants outside the simple 0..255 window stay as `ldr =N`
// so we don't have to reason about ARM's rotated-imm encoding.
func TestCmpAgainstLargeConstNotFolded(t *testing.T) {
	in := strings.Join([]string{
		"\tldr r0, =1000",
		"\tpop {r1}",
		"\tcmp r1, r0",
	}, "\n")
	got := peephole(in)
	if !strings.Contains(got, "ldr r0, =1000") {
		t.Errorf("ldr =1000 should not fold (out of imm window):\n%s", got)
	}
	if strings.Contains(got, "cmp r1, #1000") {
		t.Errorf("must not emit cmp r1, #1000 — encoding not validated:\n%s", got)
	}
}

// `pop {r0}` between the load and the cmp would overwrite the
// const we just loaded; the peephole leaves that case alone.
func TestCmpAgainstConstNotFoldedWhenPopClobbersR0(t *testing.T) {
	in := strings.Join([]string{
		"\tldr r0, =5",
		"\tpop {r0}",
		"\tcmp r0, r0",
	}, "\n")
	got := peephole(in)
	if !strings.Contains(got, "ldr r0, =5") {
		t.Errorf("ldr should stay (pop clobbers r0):\n%s", got)
	}
}

func TestFixedPointMultiplePasses(t *testing.T) {
	// First pass removes the self-mov; that puts str/ldr adjacent and
	// the second pass drops the ldr. Without the fixed-point loop only
	// the self-mov would be cleaned up.
	in := "\tstr r0, [fp, #-4]\n\tmov r0, r0\n\tldr r0, [fp, #-4]"
	got := peephole(in)
	if strings.Contains(got, "ldr") {
		t.Errorf("expected fixed-point str/ldr collapse, got:\n%s", got)
	}
	if strings.Contains(got, "mov r0, r0") {
		t.Errorf("self-mov should be gone:\n%s", got)
	}
}
