package x86_64ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// constOp adds an integer constant op and sets its immediate.
func constOp(f *ssa.Func, b *ssa.Block, imm int64) ssa.Value {
	v := f.AddOp(b, ssa.OpConstInt)
	b.Ops[len(b.Ops)-1].Imm = imm
	return v
}

// differential asserts Run(Emit(f, n)) == ssa.Eval(f, args) for several
// register-file sizes (small ones force spills), over every argument tuple.
func differential(t *testing.T, f func() *ssa.Func, argSets [][]int64) {
	t.Helper()
	for _, nAlloc := range []int{1, 2, 3, 8} {
		prog, err := Emit(f(), nAlloc)
		if err != nil {
			t.Fatalf("nAlloc=%d: Emit: %v", nAlloc, err)
		}
		for _, args := range argSets {
			want, err := ssa.Eval(f(), args...)
			if err != nil {
				t.Fatalf("Eval(%v): %v", args, err)
			}
			got, err := Run(prog, args)
			if err != nil {
				t.Fatalf("nAlloc=%d Run(%v): %v", nAlloc, args, err)
			}
			if got != want {
				t.Errorf("nAlloc=%d args=%v: Run=%d, Eval=%d", nAlloc, args, got, want)
			}
		}
	}
}

// f(a, b) = (a + b) * 3 - b
func TestEmitArithChain(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("f")
		a := f.AddParam()
		b := f.AddParam()
		e := f.NewBlock()
		sum := f.AddOp(e, ssa.OpAdd, a, b)
		three := constOp(f, e, 3)
		prod := f.AddOp(e, ssa.OpMul, sum, three)
		res := f.AddOp(e, ssa.OpSub, prod, b)
		f.SetRet(e, res)
		return f
	}
	differential(t, build, [][]int64{{4, 5}, {0, 0}, {-3, 7}, {100, -50}})
}

// f(a) = a  — a param returned directly (exercises a degenerate interval and
// the place/materialize identity path).
func TestEmitIdentity(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("id")
		a := f.AddParam()
		e := f.NewBlock()
		f.SetRet(e, a)
		return f
	}
	differential(t, build, [][]int64{{0}, {42}, {-1}})
}

// f(a, b) = a < b  (returns 0/1).
func TestEmitComparison(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("lt")
		a := f.AddParam()
		b := f.AddParam()
		e := f.NewBlock()
		c := f.AddOp(e, ssa.OpLt, a, b)
		f.SetRet(e, c)
		return f
	}
	differential(t, build, [][]int64{{1, 2}, {2, 1}, {5, 5}, {-3, 0}})
}

// i32 overflow must wrap identically in Run and Eval.
func TestEmitWidthMasking(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("ovf")
		e := f.NewBlock()
		max := constOp(f, e, 0x7fffffff)
		one := constOp(f, e, 1)
		sum := f.AddOp(e, ssa.OpAdd, max, one)
		f.SetRet(e, sum)
		return f
	}
	differential(t, build, [][]int64{{}})
}

// Many simultaneously-live temporaries: at nAlloc=1/2 this forces spills, so
// the spill load/store path is exercised and must still match Eval.
func TestEmitForcesSpills(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("manytemps")
		a := f.AddParam()
		b := f.AddParam()
		e := f.NewBlock()
		t1 := f.AddOp(e, ssa.OpAdd, a, b)
		t2 := f.AddOp(e, ssa.OpMul, a, b)
		t3 := f.AddOp(e, ssa.OpSub, a, b)
		t4 := f.AddOp(e, ssa.OpAnd, a, b)
		t5 := f.AddOp(e, ssa.OpXor, a, b)
		// Sum them all — every t_i stays live to here.
		s1 := f.AddOp(e, ssa.OpAdd, t1, t2)
		s2 := f.AddOp(e, ssa.OpAdd, s1, t3)
		s3 := f.AddOp(e, ssa.OpAdd, s2, t4)
		s4 := f.AddOp(e, ssa.OpAdd, s3, t5)
		f.SetRet(e, s4)
		return f
	}
	differential(t, build, [][]int64{{3, 4}, {-7, 11}, {0, 9}, {123, 456}})

	// Confirm the test actually exercises spilling at a tight register file.
	prog, err := Emit(build(), 2)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if prog.NumSlots == 0 {
		t.Error("expected spills at nAlloc=2 for the many-temps function, got none")
	}
}

// Unsupported ops return a clear error, not a silent wrong answer.
func TestEmitRejectsUnsupported(t *testing.T) {
	// Unsupported op (division — deferred to the real-asm slice for idiv).
	g := ssa.NewFunc("dv")
	x := g.AddParam()
	y := g.AddParam()
	ge := g.NewBlock()
	q := g.AddOp(ge, ssa.OpDiv, x, y)
	g.SetRet(ge, q)
	if _, err := Emit(g, 4); err == nil {
		t.Error("expected an error for an unsupported op (OpDiv)")
	}
}
