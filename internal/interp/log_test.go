package interp

import (
	"math"
	"math/big"
	"testing"
)

// ---- the reference ----
//
// Go's math.Log is NOT a usable oracle over the subnormal band: it recovers
// the reduction's k from the stored exponent field, which a subnormal leaves
// at 0, so every argument below 2^-1022 answers ln(2^-1022) ~ -709.09 however
// small it is. Measuring against it would have passed the defect this file
// gates. So log is referenced against a 1400-bit computation, the way exp and
// sin/cos already are.
//
// ln m = 2*atanh((m-1)/(m+1)) with m in [1, 2) converges by a factor of at
// least 9 per term, and ln2 comes from bigLn2 (exp_test.go), which derives it
// the same way rather than transcribing a long literal.

const logRefPrec = expRefPrec

// refLog computes ln x to logRefPrec bits, then rounds to double. The
// exponent split is exact — frexp only moves the binary point — so the only
// approximation is the series, and the only rounding is the final Float64.
func refLog(x float64) float64 {
	frac, e := math.Frexp(x) // frac in [0.5, 1), exactly x / 2^e
	m := new(big.Float).SetPrec(logRefPrec).SetFloat64(frac)
	one := new(big.Float).SetPrec(logRefPrec).SetInt64(1)
	t := new(big.Float).SetPrec(logRefPrec).Quo(
		new(big.Float).SetPrec(logRefPrec).Sub(m, one),
		new(big.Float).SetPrec(logRefPrec).Add(m, one))
	t2 := new(big.Float).SetPrec(logRefPrec).Mul(t, t)
	term := new(big.Float).SetPrec(logRefPrec).Set(t)
	sum := new(big.Float).SetPrec(logRefPrec).Set(t)
	for n := int64(3); n < 3000; n += 2 {
		term.Mul(term, t2)
		q := new(big.Float).SetPrec(logRefPrec).Quo(term, new(big.Float).SetPrec(logRefPrec).SetInt64(n))
		if q.Sign() == 0 {
			break
		}
		sum.Add(sum, q)
	}
	sum.Mul(sum, new(big.Float).SetPrec(logRefPrec).SetInt64(2))
	sum.Add(sum, new(big.Float).SetPrec(logRefPrec).Mul(
		bigLn2(), new(big.Float).SetPrec(logRefPrec).SetInt64(int64(e))))
	out, _ := sum.Float64()
	return out
}

// logULP is the bound the compiled backends are held to in
// internal/e2e/f64_ulp_test.go.
const logULP = 2

func TestFernLogMatchesHighPrecisionReference(t *testing.T) {
	xs := []float64{
		5e-324, 1e-320, 1e-310, 2.2250738585072014e-308, 1e-300, 1e-30, 1e-5,
		0.1, 0.5, 0.9, 1, 1.0000001, 1.5, 2, 2.718281828459045, 10, 1e5, 1e30,
		1e300, math.MaxFloat64,
	}
	// The whole subnormal range, four mantissas per exponent — the band where
	// every argument answered ln(2^-1022) before the prescale (#8497).
	for e := -1074; e < -1022; e++ {
		for _, mul := range []float64{1, 1.3, 1.7, 1.9} {
			if x := math.Ldexp(mul, e); x > 0 {
				xs = append(xs, x)
			}
		}
	}
	worst, worstX := 0.0, 0.0
	for _, x := range xs {
		if u := ulpApart(fernLog(x), refLog(x)); u > worst {
			worst, worstX = u, x
		}
	}
	if worst > logULP {
		t.Errorf("worst error %v ulp at x = %v (fernLog = %v, reference = %v); bound is %d",
			worst, worstX, fernLog(worstX), refLog(worstX), logULP)
	}
}

// The specific regression: Go's math.Log answers ~-709.09 for every one of
// these, and the true values run to -744.44.
func TestFernLogSubnormalsAreNotSaturated(t *testing.T) {
	for _, c := range []struct {
		x    float64
		bits uint64
	}{
		{1e-310, 0xc0864e69394d9508},
		{1e-320, 0xc087069e3078e52d},
		{5e-324, 0xc0874385446d71c3},
	} {
		want := math.Float64frombits(c.bits)
		if got := fernLog(c.x); ulpApart(got, want) > logULP {
			t.Errorf("fernLog(%v) = %v, want %v", c.x, got, want)
		}
	}
}

func TestFernLogSpecials(t *testing.T) {
	if got := fernLog(math.NaN()); !math.IsNaN(got) {
		t.Errorf("log(NaN) = %v, want NaN", got)
	}
	if got := fernLog(math.Inf(1)); !math.IsInf(got, 1) {
		t.Errorf("log(+Inf) = %v, want +Inf", got)
	}
	if got := fernLog(0); !math.IsInf(got, -1) {
		t.Errorf("log(0) = %v, want -Inf", got)
	}
	if got := fernLog(math.Copysign(0, -1)); !math.IsInf(got, -1) {
		t.Errorf("log(-0) = %v, want -Inf", got)
	}
	if got := fernLog(-1); !math.IsNaN(got) {
		t.Errorf("log(-1) = %v, want NaN", got)
	}
	if got := fernLog(1); got != 0 {
		t.Errorf("log(1) = %v, want 0", got)
	}
}
