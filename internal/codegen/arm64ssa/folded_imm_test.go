package arm64ssa_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/codegen/arm64ssa"
	"github.com/jakechampion/lang/internal/ssa"
)

// The shared emitter folds an i32 constant every reader takes as a right
// operand into the instruction (x86_64ssa's SrcImm), so the abstract program
// names no register for it. This renderer has to read the immediate: as the
// mnemonic's own immediate where cmp, add and sub encode one, and through the
// scratch register everywhere else. Each shape runs under qemu against
// ssa.Eval.
func TestFoldedImmediatesRender(t *testing.T) {
	// count(n) = the number of i in [0, n) with i*3+7 != 100, over a loop
	// whose bound compare, index add and body compare all fold.
	count := func() *ssa.Func {
		f := ssa.NewFunc("main")
		n := f.AddParam()
		entry, header, body, exit := f.NewBlock(), f.NewBlock(), f.NewBlock(), f.NewBlock()
		zero := constOp(f, entry, 0)
		f.SetBr(entry, header)
		iNext, accNext := f.NewValue(), f.NewValue()
		i := f.AddPhi(header, zero, iNext)
		acc := f.AddPhi(header, zero, accNext)
		f.SetBrIf(header, f.AddOp(header, ssa.OpLt, i, n), body, exit)
		scaled := f.AddOp(body, ssa.OpMul, i, constOp(f, body, 3))
		key := f.AddOp(body, ssa.OpAdd, scaled, constOp(f, body, 7))
		isHundred := f.AddOp(body, ssa.OpEq, key, constOp(f, body, 100))
		keep := f.AddOp(body, ssa.OpSub, constOp(f, body, 1), isHundred)
		add := f.AddOpNoResult(body, ssa.OpAdd, acc, keep)
		add.Result = accNext
		inc := f.AddOpNoResult(body, ssa.OpAdd, i, constOp(f, body, 1))
		inc.Result = iNext
		f.SetBr(body, header)
		f.SetRet(exit, acc)
		return f
	}
	// wide(x) exercises an immediate cmp and add cannot encode (100000 and
	// -5) and a mask and, which has no immediate form here.
	wide := func() *ssa.Func {
		f := ssa.NewFunc("main")
		x := f.AddParam()
		e := f.NewBlock()
		big := f.AddOp(e, ssa.OpGt, x, constOp(f, e, 100000))
		neg := f.AddOp(e, ssa.OpAdd, x, constOp(f, e, -5))
		low := f.AddOp(e, ssa.OpAnd, neg, constOp(f, e, 255))
		f.SetRet(e, f.AddOp(e, ssa.OpAdd, low, big))
		return f
	}
	for _, tc := range []struct {
		name  string
		build func() *ssa.Func
		args  []int64
	}{
		{"count", count, []int64{40}},
		{"wide", wide, []int64{100005}},
		{"wide-below", wide, []int64{300}},
	} {
		f := tc.build()
		funcs := map[string]*ssa.Func{"main": f}
		want, err := ssa.EvalIn(funcs, f, tc.args...)
		if err != nil {
			t.Fatalf("%s: Eval: %v", tc.name, err)
		}
		if got := assembleRunArmModule(t, funcs, "main", 12, tc.args...); got != int(uint8(want)) {
			t.Errorf("%s: qemu exit %d, want Eval&0xFF=%d (Eval=%d)", tc.name, got, int(uint8(want)), want)
		}
	}

	asm, err := arm64ssa.EmitAsmModule(map[string]*ssa.Func{"main": count()}, "main", 12, nil)
	if err != nil {
		t.Fatalf("EmitAsmModule: %v", err)
	}
	for _, want := range []string{", #100\n", ", #7\n", ", #1\n"} {
		if !strings.Contains(asm, want) {
			t.Errorf("a folded constant is not rendered as the immediate %q:\n%s", strings.TrimSpace(want), asm)
		}
	}
	asm, err = arm64ssa.EmitAsmModule(map[string]*ssa.Func{"main": wide()}, "main", 12, nil)
	if err != nil {
		t.Fatalf("EmitAsmModule: %v", err)
	}
	for _, line := range strings.Split(asm, "\n") {
		op := strings.TrimSpace(line)
		if !strings.HasPrefix(op, "cmp ") && !strings.HasPrefix(op, "add ") && !strings.HasPrefix(op, "and ") {
			continue
		}
		for _, imm := range []string{"#100000", "#-5", "#255"} {
			if strings.HasSuffix(op, imm) {
				t.Errorf("%q takes an immediate the mnemonic cannot encode; it must go through the scratch register:\n%s", op, asm)
			}
		}
	}
}
