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
