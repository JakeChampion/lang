package x86_64ssa

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// cmpPredicates is every comparison setccMnemonic knows, signed and unsigned,
// paired with a name for subtest output.
var cmpPredicates = []struct {
	name string
	k    ssa.OpKind
}{
	{"eq", ssa.OpEq}, {"ne", ssa.OpNe},
	{"lt", ssa.OpLt}, {"le", ssa.OpLe}, {"gt", ssa.OpGt}, {"ge", ssa.OpGe},
	{"ltu", ssa.OpLtU}, {"leu", ssa.OpLeU}, {"gtu", ssa.OpGtU}, {"geu", ssa.OpGeU},
}

// cmpBranchOnly builds f(a, b) = (a K b) ? 10 : 20. Nothing but the branch
// reads the comparison, so CondFuse holds and the 0/1 is never materialised.
func cmpBranchOnly(k ssa.OpKind) *ssa.Func {
	f := ssa.NewFunc("f")
	a, b := f.AddParam(), f.AddParam()
	e, then, els := f.NewBlock(), f.NewBlock(), f.NewBlock()
	f.SetBrIf(e, f.AddOp(e, k, a, b), then, els)
	f.SetRet(then, constOp(f, then, 10))
	f.SetRet(els, constOp(f, els, 20))
	return f
}

// cmpBranchTwice is the same comparison with a second reader — the taken arm
// returns the 0/1 itself — so the setcc/movzx must survive and only the
// redundant test/jnz goes.
func cmpBranchTwice(k ssa.OpKind) *ssa.Func {
	f := ssa.NewFunc("f")
	a, b := f.AddParam(), f.AddParam()
	e, then, els := f.NewBlock(), f.NewBlock(), f.NewBlock()
	c := f.AddOp(e, k, a, b)
	f.SetBrIf(e, c, then, els)
	f.SetRet(then, f.AddOp(then, ssa.OpAdd, c, constOp(f, then, 10)))
	f.SetRet(els, constOp(f, els, 20))
	return f
}

// cmpBranchSelf compares one value with itself, which emits `cmp d, d` — the
// shape where the comparison reads its own destination as the right operand,
// and so the one the dead-copy drop must not rewrite.
func cmpBranchSelf(k ssa.OpKind) *ssa.Func {
	f := ssa.NewFunc("f")
	a, b := f.AddParam(), f.AddParam()
	e, then, els := f.NewBlock(), f.NewBlock(), f.NewBlock()
	sum := f.AddOp(e, ssa.OpAdd, a, b)
	f.SetBrIf(e, f.AddOp(e, k, sum, sum), then, els)
	f.SetRet(then, constOp(f, then, 10))
	f.SetRet(els, constOp(f, els, 20))
	return f
}

// Branching on the flags directly has to pick the jcc that matches the setcc it
// replaced, for all ten predicates and in both directions: a suffix that is one
// row off in the table, or a signed/unsigned mix-up, takes the wrong arm. So
// every predicate is assembled, run for real, and diffed against ssa.Eval —
// with operand pairs that separate < from <= (the equal pair) and signed from
// unsigned (the negative pairs, which are the large values unsigned).
func TestAsmRunConditionalBranchEveryPredicate(t *testing.T) {
	args := [][]int64{{3, 5}, {5, 3}, {4, 4}, {-1, 1}, {1, -1}, {-2, -2}}
	for _, p := range cmpPredicates {
		t.Run(p.name, func(t *testing.T) {
			for _, a := range args {
				for _, n := range []int{2, 8} {
					runMatchesEvalArgs(t, cmpBranchOnly(p.k), n, a)
					runMatchesEvalArgs(t, cmpBranchTwice(p.k), n, a)
					runMatchesEvalArgs(t, cmpBranchSelf(p.k), n, a)
				}
			}
		})
	}
}

// funcText is the emitted text of one function, from its label to the next
// top-level one, so an assertion about a function's body cannot be satisfied
// (or broken) by _start or a runtime helper.
func funcText(t *testing.T, asm, label string) string {
	t.Helper()
	body, ok := strings.CutPrefix(asm[strings.Index(asm, "\n"+label+":\n")+1:], label+":\n")
	if !ok {
		t.Fatalf("no %s: in the emitted module", label)
	}
	for i, line := range strings.Split(body, "\n") {
		if line != "" && !strings.HasPrefix(line, "\t") && !strings.HasPrefix(line, ".L_") {
			return strings.Join(strings.Split(body, "\n")[:i], "\n")
		}
	}
	return body
}

func emitOne(t *testing.T, f *ssa.Func) string {
	t.Helper()
	asm, err := EmitAsmModule(map[string]*ssa.Func{"f": f}, "f", 8, nil)
	if err != nil {
		t.Fatalf("EmitAsmModule: %v", err)
	}
	return funcText(t, asm, "fn_f")
}

// The shape #6979 item 3 asks for: cmp and jcc, nothing between them. The 0/1
// the branch used to test, the widening that followed it, and the copy that
// only existed to give the comparison a left operand are all gone.
func TestFusedBranchIsCmpAndJccAlone(t *testing.T) {
	body := emitOne(t, cmpBranchOnly(ssa.OpLt))
	for _, gone := range []string{"setl", "movzx", "test "} {
		if strings.Contains(body, gone) {
			t.Errorf("fused branch still emits %q:\n%s", gone, body)
		}
	}
	if !strings.Contains(body, "\tcmp rax, rcx\n\tjl .L_fn_f_b") {
		t.Errorf("fused branch is not `cmp` immediately followed by `jl`:\n%s", body)
	}
}

// A second reader keeps the 0/1, but the branch still reads the flags the cmp
// set: setcc and movzx write none, so the test is redundant either way.
func TestUnfusedBranchKeepsTheBoolButStillBranchesOnFlags(t *testing.T) {
	body := emitOne(t, cmpBranchTwice(ssa.OpLt))
	if !strings.Contains(body, "setl") || !strings.Contains(body, "movzx") {
		t.Errorf("the 0/1 a second site reads was dropped:\n%s", body)
	}
	if strings.Contains(body, "test ") {
		t.Errorf("branch still tests the materialised 0/1:\n%s", body)
	}
	if !strings.Contains(body, "\tjl .L_fn_f_b") {
		t.Errorf("branch does not use a direct jl:\n%s", body)
	}
}

// A float comparison is not fusable at all: ucomisd's flags do not answer the
// predicate the way an integer cmp's do (fCmpSeq reverses operands for the
// `<` family and consults PF for equality), and the FEq/FNe sequences end in a
// flag-writing and/or on the byte. So the setcc, the widening, and the test all
// have to stay.
func TestFloatComparisonDoesNotFuse(t *testing.T) {
	f := ssa.NewFunc("f")
	a, b := f.AddParam(), f.AddParam()
	e, then, els := f.NewBlock(), f.NewBlock(), f.NewBlock()
	f.SetBrIf(e, f.AddOp(e, ssa.OpFLt, a, b), then, els)
	f.SetRet(then, constOp(f, then, 10))
	f.SetRet(els, constOp(f, els, 20))
	body := emitOne(t, f)
	if !strings.Contains(body, "ucomisd") || !strings.Contains(body, "test ") {
		t.Errorf("float branch lost its 0/1 test:\n%s", body)
	}
}

// The comparison is only the last instruction of its block when nothing follows
// it there, and only then do the flags still describe it at the branch. A
// comparison a later instruction separates from the terminator keeps the test.
func TestBranchOnAnEarlierComparisonKeepsTheTest(t *testing.T) {
	f := ssa.NewFunc("f")
	a, b := f.AddParam(), f.AddParam()
	e, then, els := f.NewBlock(), f.NewBlock(), f.NewBlock()
	c := f.AddOp(e, ssa.OpLt, a, b)
	f.AddOp(e, ssa.OpAdd, a, b) // writes flags between the cmp and the branch
	f.SetBrIf(e, c, then, els)
	f.SetRet(then, constOp(f, then, 10))
	f.SetRet(els, constOp(f, els, 20))
	if body := emitOne(t, f); !strings.Contains(body, "test ") {
		t.Errorf("branch fused past an intervening flag write:\n%s", body)
	}
}
