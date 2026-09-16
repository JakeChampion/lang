package x86_64ssa

import (
	"math"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// f64Call builds a call to an f64 helper; the result is the value's bits, so
// the call is 64 bits of register whatever the type says.
func f64Call(f *ssa.Func, b *ssa.Block, helper string, args ...ssa.Value) ssa.Value {
	v := callOp(f, b, helper, args...)
	b.Ops[len(b.Ops)-1].Width = 64
	return v
}

// withinOf builds main() = |helper(args) - want| < tol, through __abs_f64, so
// the exit status is 1 when the helper agrees with want.
func withinOf(helper string, args []float64, want, tol float64) *ssa.Func {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	var vals []ssa.Value
	for _, a := range args {
		vals = append(vals, constFloat(f, e, a))
	}
	got := f64Call(f, e, helper, vals...)
	diff := f.AddOp(e, ssa.OpFSub, got, constFloat(f, e, want))
	mag := f64Call(f, e, "__abs_f64", diff)
	f.SetRet(e, f.AddOp(e, ssa.OpFLt, mag, constFloat(f, e, tol)))
	return f
}

// The f64 helpers in the SSA GP convention, run natively: the rounding family
// and abs must land exactly (their answers are representable), and the
// transcendentals within a few ulp of Go's math, which is the same fdlibm
// lineage the kernels come from.
func TestF64HelpersRunNatively(t *testing.T) {
	cases := []struct {
		helper string
		args   []float64
		want   float64
		tol    float64
	}{
		{"__abs_f64", []float64{-3.25}, 3.25, 0},
		{"__abs_f64", []float64{2.5}, 2.5, 0},
		{"__sqrt_f64", []float64{2.25}, 1.5, 0},
		{"__floor_f64", []float64{-1.5}, -2, 0},
		{"__floor_f64", []float64{1.5}, 1, 0},
		{"__ceil_f64", []float64{-1.5}, -1, 0},
		{"__ceil_f64", []float64{1.25}, 2, 0},
		{"__trunc_f64", []float64{-1.75}, -1, 0},
		{"__trunc_f64", []float64{1.75}, 1, 0},
		{"__round_f64", []float64{2.5}, 3, 0},
		{"__round_f64", []float64{-2.5}, -3, 0},
		{"__round_f64", []float64{0.49}, 0, 0},
		{"__round_f64", []float64{-0.5}, -1, 0},
		{"__exp_f64", []float64{0}, 1, 0},
		{"__exp_f64", []float64{1}, math.E, 1e-14},
		{"__log_f64", []float64{1}, 0, 0},
		{"__log_f64", []float64{2}, math.Ln2, 1e-14},
		{"__sin_f64", []float64{0}, 0, 0},
		{"__sin_f64", []float64{1}, math.Sin(1), 1e-14},
		{"__cos_f64", []float64{0}, 1, 0},
		{"__cos_f64", []float64{1}, math.Cos(1), 1e-14},
		{"__pow_f64", []float64{1, 7.5}, 1, 0},
		{"__pow_f64", []float64{2, 0.5}, math.Sqrt2, 1e-14},
		{"__pow_f64", []float64{-2, 3}, -8, 1e-13},
	}
	for _, c := range cases {
		tol := c.tol
		if tol == 0 {
			tol = math.SmallestNonzeroFloat64 // |diff| < denorm-min holds only for an exact zero
		}
		if got := assembleRun(t, withinOf(c.helper, c.args, c.want, tol), 8); got != 1 {
			t.Errorf("%s(%v) is not within %g of %g (exit %d)", c.helper, c.args, c.tol, c.want, got)
		}
	}
}

// The kernel bundle is emitted once when a transcendental helper is
// referenced, and not at all when only the one-instruction helpers are.
func TestTranscendentalBundleFollowsItsHelpers(t *testing.T) {
	asm, err := EmitAsm(withinOf("__sin_f64", []float64{1}, math.Sin(1), 1e-14), 8)
	if err != nil {
		t.Fatalf("EmitAsm: %v", err)
	}
	for _, sym := range []string{"__fern_sin_f64:", "__fern_ksin:", ".Lfc_2opi_bits:"} {
		if n := strings.Count(asm, "\n"+sym); n != 1 {
			t.Errorf("%s appears %d times in a module calling __sin_f64, want once", sym, n)
		}
	}
	asm, err = EmitAsm(withinOf("__sqrt_f64", []float64{2.25}, 1.5, 1e-14), 8)
	if err != nil {
		t.Fatalf("EmitAsm: %v", err)
	}
	if strings.Contains(asm, "__fern_ksin:") || strings.Contains(asm, ".Lfc_2opi_bits:") {
		t.Errorf("a module calling only __sqrt_f64 carries the transcendental bundle")
	}
	if !strings.Contains(asm, "sqrtsd xmm0, xmm0") {
		t.Errorf("__sqrt_f64 is not the one sqrtsd:\n%s", asm)
	}
}

// __memcpy(dst, src, n) copies n bytes and hands dst back: the two words
// stored through src read back through dst, and the return value indexes the
// same bytes.
func TestMemcpyRunsNatively(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	src := f.AddOp(e, ssa.OpAlloc, constOp(f, e, 16))
	dst := f.AddOp(e, ssa.OpAlloc, constOp(f, e, 16))
	st := f.AddOpNoResult(e, ssa.OpStore, src, constOp(f, e, 0x1122334455667788))
	st.Imm = 0
	st = f.AddOpNoResult(e, ssa.OpStore, src, constOp(f, e, 77))
	st.Imm = 8
	ret := callOp(f, e, "__memcpy", dst, src, constOp(f, e, 16))
	e.Ops[len(e.Ops)-1].Width = 64
	lo := f.AddOp(e, ssa.OpLoad, dst)
	e.Ops[len(e.Ops)-1].Width = 64
	hi := f.AddOp(e, ssa.OpLoad, ret)
	e.Ops[len(e.Ops)-1].Imm = 8
	e.Ops[len(e.Ops)-1].Width = 64
	okLo := f.AddOp(e, ssa.OpEq, lo, constOp(f, e, 0x1122334455667788))
	okHi := f.AddOp(e, ssa.OpEq, hi, constOp(f, e, 77))
	same := f.AddOp(e, ssa.OpEq, ret, dst)
	f.SetRet(e, f.AddOp(e, ssa.OpAnd, f.AddOp(e, ssa.OpAnd, okLo, okHi), same))
	if got := assembleRun(t, f, 8); got != 1 {
		t.Errorf("memcpy round trip = %d, want 1", got)
	}
}
