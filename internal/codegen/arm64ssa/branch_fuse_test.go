package arm64ssa_test

import (
	"regexp"
	"strings"
	"testing"

	arm64ssa "github.com/jakechampion/lang/internal/codegen/arm64ssa"
	"github.com/jakechampion/lang/internal/ssa"
)

// countingLoop builds `n = 0; while n < limit { n = n + 1 }; return n`, the
// shape whose comparison feeds a branch and whose back edge closes a loop.
func countingLoop(limit int64) *ssa.Func {
	f := ssa.NewFunc("main")
	entry, head, body, exit := f.NewBlock(), f.NewBlock(), f.NewBlock(), f.NewBlock()
	zero := constOp(f, entry, 0)
	f.SetBr(entry, head)

	n := f.AddOp(head, ssa.OpPhi, zero, ssa.Value{})
	cond := f.AddOp(head, ssa.OpLt, n, constOp(f, head, limit))
	f.SetBrIf(head, cond, body, exit)

	next := f.AddOp(body, ssa.OpAdd, n, constOp(f, body, 1))
	f.SetBr(body, head)
	head.Ops[0].Args[1] = next

	f.SetRet(exit, n)
	return f
}

// asmLines returns the emitted assembly of f as lines with surrounding space
// trimmed.
func asmLines(t *testing.T, f *ssa.Func, numAlloc int) []string {
	t.Helper()
	asm, err := arm64ssa.EmitAsm(f, numAlloc)
	if err != nil {
		t.Fatalf("EmitAsm: %v", err)
	}
	var out []string
	for _, l := range strings.Split(asm, "\n") {
		out = append(out, strings.TrimSpace(l))
	}
	return out
}

var branchToLabel = regexp.MustCompile(`^b (\.L\S+)$`)

// regCopy matches a register-to-register move, as opposed to an immediate one.
var regCopy = regexp.MustCompile(`^mov [wx]\d+, [wx]\d+$`)

// A jump to the label on the very next line does nothing. The abstract emitter
// numbers blocks in creation order and appends critical-edge splits at the end,
// so emitting in index order used to leave one of these in front of nearly
// every label; the layout walk plus fallthrough elision removes them.
func TestArmAsmHasNoBranchToTheFollowingLabel(t *testing.T) {
	for _, f := range []*ssa.Func{countingLoop(5), abs(-7)} {
		lines := asmLines(t, f, 8)
		for i := 0; i+1 < len(lines); i++ {
			m := branchToLabel.FindStringSubmatch(lines[i])
			if m == nil {
				continue
			}
			// Skip over the labels that follow, since several can stack up.
			if strings.TrimSuffix(lines[i+1], ":") == m[1] {
				t.Errorf("%s: branch to the immediately following label:\n\t%s\n\t%s",
					f.Name, lines[i], lines[i+1])
			}
		}
	}
}

// abs builds `if n < 0 { return -n }; return n` for a constant n.
func abs(n int64) *ssa.Func {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	v := f.AddOp(e, ssa.OpAdd, constOp(f, e, n), constOp(f, e, 0))
	neg := f.AddOp(e, ssa.OpLt, v, constOp(f, e, 0))
	then, els := f.NewBlock(), f.NewBlock()
	f.SetBrIf(e, neg, then, els)
	f.SetRet(then, f.AddOp(then, ssa.OpSub, constOp(f, then, 0), v))
	f.SetRet(els, v)
	return f
}

// A comparison whose 0/1 only the branch reads needs no 0/1: the flags the cmp
// already set decide the branch. Before this the renderer emitted the cset and
// then tested the register it wrote with cbnz.
func TestArmAsmBranchesOnFlagsInsteadOfMaterialisingTheComparison(t *testing.T) {
	for _, f := range []*ssa.Func{countingLoop(5), abs(-7)} {
		lines := asmLines(t, f, 8)
		joined := strings.Join(lines, "\n")
		for _, l := range lines {
			if strings.HasPrefix(l, "cset ") {
				t.Errorf("%s: comparison materialised into a register:\n%s", f.Name, joined)
				break
			}
		}
		found := false
		for _, l := range lines {
			if strings.HasPrefix(l, "b.") {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: no conditional branch on flags:\n%s", f.Name, joined)
		}
	}
}

// AArch64's cmp has no destination operand, so the copy the abstract
// two-address SetCmp needs in front of it is never read.
func TestArmAsmDropsTheCopyInFrontOfEveryComparison(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	a := f.AddOp(e, ssa.OpAdd, constOp(f, e, 3), constOp(f, e, 4))
	b := f.AddOp(e, ssa.OpAdd, constOp(f, e, 1), constOp(f, e, 2))
	// Two uses keep the comparison materialised, so the cmp is rendered as a
	// SetCmp rather than folded into a branch.
	lt := f.AddOp(e, ssa.OpLt, a, b)
	f.SetRet(e, f.AddOp(e, ssa.OpAdd, lt, lt))

	lines := asmLines(t, f, 8)
	for i, l := range lines {
		if !strings.HasPrefix(l, "cmp ") || i == 0 {
			continue
		}
		if strings.HasPrefix(lines[i-1], "mov x") {
			t.Errorf("dead copy in front of a comparison:\n\t%s\n\t%s", lines[i-1], l)
		}
	}
	if !strings.Contains(strings.Join(lines, "\n"), "cset ") {
		t.Fatalf("expected a materialised comparison:\n%s", strings.Join(lines, "\n"))
	}
}

// The behavioural gate: whatever the layout and fusion do, the program's value
// must still be the one ssa.Eval computes, at every register-file size.
func TestArmRunBranchFusionKeepsTheValue(t *testing.T) {
	for _, f := range []*ssa.Func{countingLoop(5), countingLoop(0), abs(-7), abs(7)} {
		for _, n := range []int{2, 4, 8} {
			runMatchesEval(t, f, n)
		}
	}
}

// `a + b` where `a` is read again afterwards is the case destination
// coalescing cannot help: the result must not land in a's register. AArch64's
// add is three-address, so it reads a where it lies and the copy goes anyway.
func TestArmAsmArithmeticReadsItsLeftOperandInPlace(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	a := f.AddOp(e, ssa.OpAdd, constOp(f, e, 3), constOp(f, e, 4))
	b := f.AddOp(e, ssa.OpAdd, constOp(f, e, 5), constOp(f, e, 6))
	sum := f.AddOp(e, ssa.OpAdd, a, b)
	f.SetRet(e, f.AddOp(e, ssa.OpAdd, sum, a)) // a is still live after the add
	lines := asmLines(t, f, 8)
	for i, l := range lines {
		if i == 0 || !strings.HasPrefix(l, "add x") {
			continue
		}
		if regCopy.MatchString(lines[i-1]) {
			t.Errorf("copy in front of a three-address add:\n\t%s\n\t%s", lines[i-1], l)
		}
	}
	for _, n := range []int{2, 4, 8} {
		runMatchesEval(t, f, n)
	}
}

// A load writes its destination and reads only its base, so it can land in the
// result's register home instead of being staged through a scratch and copied.
func TestArmAsmLoadsLandInTheResultHome(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	p := f.AddOp(e, ssa.OpAlloc, constOp(f, e, 32<<10))
	storeOp(f, e, p, constOp(f, e, 41), 0)
	lo := loadOp(f, e, p, 0)
	f.SetRet(e, f.AddOp(e, ssa.OpAdd, lo, constOp(f, e, 1)))
	lines := asmLines(t, f, 8)
	for i, l := range lines {
		if i+1 >= len(lines) || !strings.HasPrefix(l, "ldr x") {
			continue
		}
		if regCopy.MatchString(lines[i+1]) {
			t.Errorf("load staged through a scratch:\n\t%s\n\t%s", l, lines[i+1])
		}
	}
	for _, n := range []int{2, 4, 8} {
		runMatchesEval(t, f, n)
	}
}
