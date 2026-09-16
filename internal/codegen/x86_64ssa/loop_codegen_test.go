package x86_64ssa

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// The four shapes a profile of coreutils/sort.fern under this backend named,
// each of which the hot loop paid per byte (#8822): a jump to the block laid
// out next, a constant moved into a register before every compare, the array
// length loaded into a register before every bounds check, and a sign
// extension after every zero-extending byte load. Each test pins the text and
// runs the function against ssa.Eval, since a branch inverted the wrong way
// or a constant folded into the wrong operand position is a wrong answer
// rather than an assembler error.

// jumpToNextLabel reports a `jmp .L_x` immediately followed by `.L_x:`.
func jumpToNextLabel(body string) bool {
	lines := strings.Split(body, "\n")
	for i := 0; i+1 < len(lines); i++ {
		l := strings.TrimSpace(lines[i])
		if strings.HasPrefix(l, "jmp ") && strings.TrimSpace(lines[i+1]) == strings.TrimPrefix(l, "jmp ")+":" {
			return true
		}
	}
	return false
}

// A diamond whose join follows the else arm: the then arm's jump to the join
// is real, the else arm's is a fallthrough, and the branch itself falls
// through to the then arm on the inverse condition.
func diamond(k ssa.OpKind) *ssa.Func {
	f := ssa.NewFunc("f")
	a, b := f.AddParam(), f.AddParam()
	e, then, els, join := f.NewBlock(), f.NewBlock(), f.NewBlock(), f.NewBlock()
	f.SetBrIf(e, f.AddOp(e, k, a, b), then, els)
	x := f.AddOp(then, ssa.OpAdd, a, constOp(f, then, 10))
	f.SetBr(then, join)
	y := f.AddOp(els, ssa.OpAdd, b, constOp(f, els, 20))
	f.SetBr(els, join)
	f.SetRet(join, f.AddPhi(join, x, y))
	return f
}

func TestBlockLaidOutNextIsFallenThroughTo(t *testing.T) {
	for _, p := range cmpPredicates {
		t.Run(p.name, func(t *testing.T) {
			body := emitOne(t, diamond(p.k))
			if jumpToNextLabel(body) {
				t.Errorf("a jump to the label that follows it survived:\n%s", body)
			}
			for _, a := range [][]int64{{3, 5}, {5, 3}, {4, 4}, {-1, 1}} {
				runMatchesEvalArgs(t, diamond(p.k), 8, a)
			}
		})
	}
}

// f(a) = a < 48 ? 10 : 20, the digit test the sort profile's inner loop is
// made of: the 48 is an immediate on the compare and no register ever holds
// it.
func cmpAgainstConst(k ssa.OpKind, c int64) *ssa.Func {
	f := ssa.NewFunc("f")
	a := f.AddParam()
	e, then, els := f.NewBlock(), f.NewBlock(), f.NewBlock()
	f.SetBrIf(e, f.AddOp(e, k, a, constOp(f, e, c)), then, els)
	f.SetRet(then, constOp(f, then, 10))
	f.SetRet(els, constOp(f, els, 20))
	return f
}

func TestCompareAgainstConstantTakesAnImmediate(t *testing.T) {
	for _, p := range cmpPredicates {
		t.Run(p.name, func(t *testing.T) {
			body := emitOne(t, cmpAgainstConst(p.k, 48))
			if !strings.Contains(body, "cmp rax, 48") {
				t.Errorf("the constant is not the compare's immediate:\n%s", body)
			}
			if strings.Contains(body, "mov rcx, 48") || strings.Contains(body, "mov rdx, 48") {
				t.Errorf("the constant was still materialised into a register:\n%s", body)
			}
			for _, a := range []int64{47, 48, 49, -1} {
				runMatchesEvalArgs(t, cmpAgainstConst(p.k, 48), 8, []int64{a})
			}
		})
	}
}

// A constant a store reads as well cannot be an immediate anywhere: the store
// needs it in a register, so the MovImm stays and the compare keeps reading
// the register.
func TestConstantWithARegisterReaderIsNotFolded(t *testing.T) {
	f := ssa.NewFunc("f")
	a := f.AddParam()
	e, then, els := f.NewBlock(), f.NewBlock(), f.NewBlock()
	c := constOp(f, e, 48)
	buf := addrCallOp(f, e, "__alloc_u8", constOp(f, e, 8))
	storeMem(f, e, buf, 0, c, ssa.OpStore)
	f.SetBrIf(e, f.AddOp(e, ssa.OpLt, a, c), then, els)
	f.SetRet(then, constOp(f, then, 10))
	f.SetRet(els, constOp(f, els, 20))
	asm, err := EmitAsmModule(map[string]*ssa.Func{"f": f}, "f", 8, nil)
	if err != nil {
		t.Fatalf("EmitAsmModule: %v", err)
	}
	body := funcText(t, asm, "fn_f")
	if strings.Contains(body, "cmp rax, 48") {
		t.Errorf("a constant with a store reading it was folded into the compare:\n%s", body)
	}
}

// f(a) = a + 5 - 3, then a & 7: the direct-form binary ops take the constant
// as an immediate too.
func TestBinaryOpAgainstConstantTakesAnImmediate(t *testing.T) {
	f := ssa.NewFunc("f")
	a := f.AddParam()
	e := f.NewBlock()
	s := f.AddOp(e, ssa.OpAdd, a, constOp(f, e, 5))
	d := f.AddOp(e, ssa.OpSub, s, constOp(f, e, 3))
	f.SetRet(e, f.AddOp(e, ssa.OpAnd, d, constOp(f, e, 7)))
	body := emitOne(t, f)
	for _, want := range []string{"add r", "sub r", "and r"} {
		// The destination is whatever register the allocator gave the result;
		// the immediate is the point.
		imm := map[string]string{"add r": ", 5", "sub r": ", 3", "and r": ", 7"}[want]
		found := false
		for _, line := range strings.Split(body, "\n") {
			l := strings.TrimSpace(line)
			if strings.HasPrefix(l, want) && strings.HasSuffix(l, imm) {
				found = true
			}
		}
		if !found {
			t.Errorf("no %q with immediate %q:\n%s", want, imm, body)
		}
	}
	for _, gone := range []string{"mov rcx, 5", "mov rcx, 3", "mov rcx, 7", "mov rdx, 5", "mov rdx, 3", "mov rdx, 7"} {
		if strings.Contains(body, gone) {
			t.Errorf("the constant was still materialised: %q:\n%s", gone, body)
		}
	}
	for _, a := range []int64{0, 1, 100, -9} {
		runMatchesEvalArgs(t, f, 8, []int64{a})
	}
}

// A zero-extending byte load leaves the register's high bits clear, which is
// the i32 sign extension of a value under 256, so no movsxd follows it.
func TestByteLoadNeedsNoSignFix(t *testing.T) {
	f := ssa.NewFunc("main")
	b := f.NewBlock()
	buf := byteBuf(f, b, 200, 4)
	f.SetRet(b, loadMem(f, b, buf, 0, ssa.OpLoad8U))
	asm := idxAsm(t, map[string]*ssa.Func{"main": f}, "main")
	body := idxFuncBody(t, asm, "main")
	lines := strings.Split(body, "\n")
	for i := 0; i+1 < len(lines); i++ {
		if strings.Contains(lines[i], "movzx") && strings.Contains(lines[i+1], "movsxd") {
			t.Errorf("a movsxd follows the zero-extending load:\n%s", body)
		}
	}
	if got := assembleRunModule(t, map[string]*ssa.Func{"main": f}, "main", 8, nil); got != 200 {
		t.Errorf("byte load = %d, want 200", got)
	}
}

// An array index compares the index against the length where it lies.
func TestArrayIndexComparesLengthInMemory(t *testing.T) {
	f := ssa.NewFunc("main")
	b := f.NewBlock()
	buf := byteBuf(f, b, 3, 4, 5)
	f.SetRet(b, loadMem(f, b, addrCallOp(f, b, "__arr_idx_1", buf, constOp(f, b, 2)), 0, ssa.OpLoad8U))
	asm := idxAsm(t, map[string]*ssa.Func{"main": f}, "main")
	body := idxFuncBody(t, asm, "main")
	if !strings.Contains(body, "dword ptr [") || strings.Contains(body, "mov r10d, [") {
		t.Errorf("the bounds check loads the length into a register first:\n%s", body)
	}
	if got := assembleRunModule(t, map[string]*ssa.Func{"main": f}, "main", 8, nil); got != 5 {
		t.Errorf("indexed byte = %d, want 5", got)
	}
}
