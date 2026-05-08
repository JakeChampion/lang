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
	in := "\tb .Lend\n.Lend:\n\tmov sp, fp"
	got := peephole(in)
	if strings.Contains(got, "\tb .Lend") {
		t.Errorf("branch should be removed:\n%s", got)
	}
	if !strings.Contains(got, ".Lend:") {
		t.Errorf("label should remain:\n%s", got)
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
	// code may branch to the label and skip the str.
	in := "\tstr r0, [fp, #-4]\n.Lother:\n\tldr r0, [fp, #-4]"
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
	in := "\tbne .Lnext\n.Lnext:\n\tmov sp, fp"
	got := peephole(in)
	if strings.Contains(got, "bne .Lnext") {
		t.Errorf("conditional branch to next line should be removed:\n%s", got)
	}
	if !strings.Contains(got, ".Lnext:") {
		t.Errorf("label should remain:\n%s", got)
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
