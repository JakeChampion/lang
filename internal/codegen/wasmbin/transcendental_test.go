package wasmbin

import (
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// TestEmitTranscendentalHelpers — exp / log / sin / cos / pow had no lowering
// in this backend at all, so thirteen std/float methods were unbuildable for
// wasm and failed with `unknown callee "__exp_f64"` (#6404). These five are the
// only primitives; exp2, exp10, log2, log10, tan, sinh, cosh, tanh and cbrt
// compose from them in float.fern.
//
// The bar is deliberately NOT "matches Go's math package". These are fdlibm
// kernels, shared with the x86-64 / arm64 emitters and with the self-host WAT
// path, so they are expected to sit within a ulp or so of a correctly-rounded
// libm and to AGREE WITH EACH OTHER. What this pins is that the port did not
// mistranslate a constant or a stack order — an error of that kind is orders
// out, not ulps.
//
// The domain guards are checked EXACTLY, because they are the part with no
// tolerance to spend: without them exp(1000) returns -6.1e-183 rather than
// +Inf (2^k is built as (k+1023)<<52 and k=1443 overflows the exponent field
// into the sign bit) and log(0) returns -709.09 rather than -Inf.
func TestEmitTranscendentalHelpers(t *testing.T) {
	// ulpDiff counts representable doubles between a and b. 0 == bit-identical.
	ulpDiff := func(a, b float64) uint64 {
		if a == b {
			return 0
		}
		ai, bi := int64(math.Float64bits(a)), int64(math.Float64bits(b))
		if (ai < 0) != (bi < 0) {
			// Straddles zero; only meaningful when both are tiny.
			return math.MaxUint64
		}
		if ai > bi {
			ai, bi = bi, ai
		}
		return uint64(bi - ai)
	}

	run1 := func(t *testing.T, callee string, arg float64) float64 {
		t.Helper()
		prog := &ir.Program{Funcs: []*ir.Func{{
			Name:       "main",
			ReturnType: f64(),
			Ops: []ir.Op{
				{Kind: ir.OpConstF64, F64: arg},
				{Kind: ir.OpCallDirect, Str: callee},
			},
		}}}
		bin, err := Emit(prog)
		if err != nil {
			t.Fatalf("Emit(%s): %v", callee, err)
		}
		return parseWasmFloat(t, runUnderWasmtime(t, bin, "main"))
	}

	run2 := func(t *testing.T, callee string, x, y float64) float64 {
		t.Helper()
		prog := &ir.Program{Funcs: []*ir.Func{{
			Name:       "main",
			ReturnType: f64(),
			Ops: []ir.Op{
				{Kind: ir.OpConstF64, F64: x},
				{Kind: ir.OpConstF64, F64: y},
				{Kind: ir.OpCallDirect, Str: callee},
			},
		}}}
		bin, err := Emit(prog)
		if err != nil {
			t.Fatalf("Emit(%s): %v", callee, err)
		}
		return parseWasmFloat(t, runUnderWasmtime(t, bin, "main"))
	}

	// Ordinary values: within a few ulp of the reference.
	const tol = 4
	for _, c := range []struct {
		name   string
		callee string
		arg    float64
		want   float64
	}{
		{"exp_1.5", "__exp_f64", 1.5, math.Exp(1.5)},
		{"exp_0", "__exp_f64", 0, 1},
		{"exp_neg1", "__exp_f64", -1, math.Exp(-1)},
		{"exp_20", "__exp_f64", 20, math.Exp(20)},
		{"log_e", "__log_f64", math.E, 1},
		{"log_1", "__log_f64", 1, 0},
		{"log_100", "__log_f64", 100, math.Log(100)},
		{"log_half", "__log_f64", 0.5, math.Log(0.5)},
		{"sin_0", "__sin_f64", 0, 0},
		{"sin_1", "__sin_f64", 1, math.Sin(1)},
		{"sin_100", "__sin_f64", 100, math.Sin(100)},
		{"cos_0", "__cos_f64", 0, 1},
		{"cos_1", "__cos_f64", 1, math.Cos(1)},
		{"cos_100", "__cos_f64", 100, math.Cos(100)},
	} {
		c := c
		t.Run(c.name, func(t *testing.T) {
			got := run1(t, c.callee, c.arg)
			if d := ulpDiff(got, c.want); d > tol {
				t.Errorf("%s(%v) = %v, want %v (%d ulp apart, tolerance %d)",
					c.callee, c.arg, got, c.want, d, tol)
			}
		})
	}

	// pow: the integer-exponent path must be EXACT, which is the whole reason
	// it exists — exp(y*ln x) cannot return exactly 9 for pow(3,2).
	for _, c := range []struct {
		name string
		x, y float64
		want float64
	}{
		{"pow_3_2_exact", 3, 2, 9},
		{"pow_2_10_exact", 2, 10, 1024},
		{"pow_5_3_exact", 5, 3, 125},
		{"pow_2_neg3_exact", 2, -3, 0.125},
		{"pow_7_0_exact", 7, 0, 1},
	} {
		c := c
		t.Run(c.name, func(t *testing.T) {
			got := run2(t, "__pow_f64", c.x, c.y)
			if got != c.want {
				t.Errorf("pow(%v, %v) = %v, want exactly %v — the integral-y path "+
					"is repeated squaring precisely so this is exact", c.x, c.y, got, c.want)
			}
		})
	}
	t.Run("pow_fractional", func(t *testing.T) {
		got := run2(t, "__pow_f64", 2, 0.5)
		if d := ulpDiff(got, math.Sqrt2); d > tol {
			t.Errorf("pow(2, 0.5) = %v, want ~%v (%d ulp)", got, math.Sqrt2, d)
		}
	})

	// Domain guards — exact, no tolerance.
	for _, c := range []struct {
		name   string
		callee string
		arg    float64
		check  func(float64) bool
		desc   string
	}{
		{"exp_overflow", "__exp_f64", 1000, func(v float64) bool { return math.IsInf(v, 1) }, "+Inf"},
		{"exp_underflow", "__exp_f64", -1000, func(v float64) bool { return v == 0 }, "0"},
		{"exp_nan", "__exp_f64", math.NaN(), math.IsNaN, "NaN"},
		{"log_zero", "__log_f64", 0, func(v float64) bool { return math.IsInf(v, -1) }, "-Inf"},
		{"log_negative", "__log_f64", -1, math.IsNaN, "NaN"},
		{"log_inf", "__log_f64", math.Inf(1), func(v float64) bool { return math.IsInf(v, 1) }, "+Inf"},
		{"log_nan", "__log_f64", math.NaN(), math.IsNaN, "NaN"},
		{"sin_inf", "__sin_f64", math.Inf(1), math.IsNaN, "NaN"},
		{"sin_nan", "__sin_f64", math.NaN(), math.IsNaN, "NaN"},
		{"cos_inf", "__cos_f64", math.Inf(-1), math.IsNaN, "NaN"},
	} {
		c := c
		t.Run(c.name, func(t *testing.T) {
			got := run1(t, c.callee, c.arg)
			if !c.check(got) {
				t.Errorf("%s(%v) = %v, want %s — the domain guard is what keeps "+
					"2^k from overflowing the exponent field into the sign bit",
					c.callee, c.arg, got, c.desc)
			}
		})
	}
}

// parseWasmFloat reads the f64 runUnderWasmtime printed, accepting the
// Inf/NaN spellings the runtime's formatter uses.
func parseWasmFloat(t *testing.T, s string) float64 {
	t.Helper()
	s = strings.TrimSpace(s)
	switch s {
	case "Inf", "inf", "+Inf":
		return math.Inf(1)
	case "-Inf", "-inf":
		return math.Inf(-1)
	case "NaN", "nan", "-NaN", "-nan":
		return math.NaN()
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return v
}
