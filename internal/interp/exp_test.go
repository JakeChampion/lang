package interp

import (
	"math"
	"math/big"
	"testing"
)

// ---- the reference ----
//
// Go's math.Exp is NOT a usable oracle for this function, at either end.
// Above 709.436 — where k = round(x/ln2) reaches 1024 — it returns +Inf for
// arguments whose true value is a perfectly ordinary finite double, and in
// the deepest subnormal band its result is host-architecture dependent (amd64
// and arm64 disagree at exp(-745)). So exp is referenced against a 1400-bit
// computation, the way sin/cos already are.
//
// ln2 is derived rather than transcribed: 2*atanh(1/3) converges by a factor
// of 9 per term, so ~450 terms clear 1400 bits, and a typo in a 420-digit
// literal cannot hide in it.

const expRefPrec = 1400

func bigLn2() *big.Float {
	third := new(big.Float).SetPrec(expRefPrec).Quo(
		new(big.Float).SetPrec(expRefPrec).SetInt64(1),
		new(big.Float).SetPrec(expRefPrec).SetInt64(3))
	ninth := new(big.Float).SetPrec(expRefPrec).Mul(third, third)
	term := new(big.Float).SetPrec(expRefPrec).Set(third)
	sum := new(big.Float).SetPrec(expRefPrec).Set(third)
	for n := int64(3); n < 1000; n += 2 {
		term.Mul(term, ninth)
		q := new(big.Float).SetPrec(expRefPrec).Quo(term, new(big.Float).SetPrec(expRefPrec).SetInt64(n))
		sum.Add(sum, q)
	}
	return sum.Mul(sum, new(big.Float).SetPrec(expRefPrec).SetInt64(2))
}

// refExp computes e^x to expRefPrec bits, then rounds to double: reduce
// x = k*ln2 + r with |r| <= ln2/2, sum the Taylor series for e^r (which
// converges in well under 100 terms at that magnitude), and scale by 2^k.
// The single rounding happens in Float64, so a subnormal result rounds once.
func refExp(x float64) float64 {
	ln2 := bigLn2()
	bx := new(big.Float).SetPrec(expRefPrec).SetFloat64(x)
	q, _ := new(big.Float).SetPrec(expRefPrec).Quo(bx, ln2).Float64()
	k := int64(math.RoundToEven(q))
	r := new(big.Float).SetPrec(expRefPrec).Sub(bx,
		new(big.Float).SetPrec(expRefPrec).Mul(new(big.Float).SetPrec(expRefPrec).SetInt64(k), ln2))
	sum := new(big.Float).SetPrec(expRefPrec).SetInt64(1)
	term := new(big.Float).SetPrec(expRefPrec).SetInt64(1)
	for n := int64(1); n < 220; n++ {
		term.Mul(term, r)
		term.Quo(term, new(big.Float).SetPrec(expRefPrec).SetInt64(n))
		sum.Add(sum, term)
	}
	out, _ := sum.SetMantExp(sum, int(k)).Float64()
	return out
}

// expULP is the same bound the compiled backends are held to in
// internal/e2e/f64_ulp_test.go: the kernel is ~1 ulp by construction, and 2
// leaves room for a last-place disagreement without leaving room for an
// algorithmic error.
const expULP = 2

func ulpApart(got, want float64) float64 {
	if got == want {
		return 0
	}
	if math.IsNaN(got) != math.IsNaN(want) || math.IsInf(want, 0) || math.IsInf(got, 0) {
		return math.Inf(1)
	}
	g, w := int64(math.Float64bits(got)), int64(math.Float64bits(want))
	d := g - w
	if d < 0 {
		d = -d
	}
	return float64(d)
}

func TestFernExpMatchesHighPrecisionReference(t *testing.T) {
	var xs []float64
	xs = append(xs,
		0, 1e-300, 1e-16, 0.25, 0.5, 0.6931471805599453, 1, 1.5, 2, 5, 10, 50, 100, 500, 700, 709,
		-1e-16, -0.5, -1, -2, -10, -100, -500, -700, -745,
	)
	// The band Go's math.Exp gets wrong: k reaches 1024 at ln2*1023.5 =
	// 709.436, and everything below expovf = 709.7827 has a finite answer.
	for x := 709.4; x < 709.7827; x += 0.007 {
		xs = append(xs, x)
	}
	// The deep subnormal band, all the way to expunf. #8237's two half-scales
	// are what make these representable at all; the previous single exponent
	// field put them in the sign bit.
	for x := -708.0; x > -745.13; x -= 0.37 {
		xs = append(xs, x)
	}
	worst, worstX := 0.0, 0.0
	for _, x := range xs {
		got, want := fernExp(x), refExp(x)
		if u := ulpApart(got, want); u > worst {
			worst, worstX = u, x
		}
	}
	if worst > expULP {
		t.Errorf("worst error %v ulp at x = %v (fernExp = %v, reference = %v); bound is %d",
			worst, worstX, fernExp(worstX), refExp(worstX), expULP)
	}
}

// The specific regression: every one of these has an ordinary finite double
// answer, and Go's math.Exp returns +Inf for all of them.
func TestFernExpIsFiniteWhereGoOverflows(t *testing.T) {
	for _, x := range []float64{709.5, 709.7, 709.78, 709.7827128933839} {
		got := fernExp(x)
		if math.IsInf(got, 0) {
			t.Errorf("fernExp(%v) = +Inf, want the finite %v", x, refExp(x))
			continue
		}
		if u := ulpApart(got, refExp(x)); u > expULP {
			t.Errorf("fernExp(%v) = %v, want %v (%v ulp)", x, got, refExp(x), u)
		}
	}
}

func TestFernExpSpecials(t *testing.T) {
	if got := fernExp(math.NaN()); !math.IsNaN(got) {
		t.Errorf("exp(NaN) = %v, want NaN", got)
	}
	if got := fernExp(math.Inf(1)); !math.IsInf(got, 1) {
		t.Errorf("exp(+Inf) = %v, want +Inf", got)
	}
	if got := fernExp(math.Inf(-1)); got != 0 {
		t.Errorf("exp(-Inf) = %v, want 0", got)
	}
	if got := fernExp(0); got != 1 {
		t.Errorf("exp(0) = %v, want 1", got)
	}
	if got := fernExp(1000); !math.IsInf(got, 1) {
		t.Errorf("exp(1000) = %v, want +Inf", got)
	}
	if got := fernExp(-1000); got != 0 {
		t.Errorf("exp(-1000) = %v, want 0", got)
	}
	// One ulp past expunf still rounds to a subnormal rather than to zero,
	// which is the boundary the underflow guard has to leave alone.
	if got := fernExp(-745.0); got == 0 {
		t.Errorf("exp(-745.0) = 0, want the subnormal %v", refExp(-745.0))
	}
}
