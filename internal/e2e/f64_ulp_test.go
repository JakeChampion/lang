package e2e

// The accuracy gate for the f64 transcendentals — sin / cos / exp / log —
// on every backend that implements them.
//
// This exists because its absence was the bug. The kernels shipped for months
// described in their own comments as accurate to "a few ulp" while actually
// measuring 3.2e10 ulp (sin), 4.5e7 (exp) and 9844 (log), and the only test
// covering them asserted `assert_eq_f64_near(..., 0.000001)` — a tolerance
// loose enough to pass anyway. A relative-epsilon assertion cannot express
// "correct to the last bit"; ulp distance can, so that is what this measures.
//
// The comparison is on RAW BIT PATTERNS via `f64_bits`, not on printed
// decimals, so a rounding difference in float formatting cannot mask or
// manufacture a discrepancy.
//
// Special values are checked first and separately. They are where the failure
// mode is qualitative rather than quantitative: before the domain guards,
// exp(1000) returned -6.1e-183 instead of +Inf (the 2^k reconstruction builds
// the exponent field as (k+1023)<<52 and silently overflowed into the SIGN
// bit), log(0) returned -709.09 instead of -Inf, and log(-1) returned 0
// instead of NaN. None of those are "inaccurate" — they are wrong answers on
// ordinary inputs, and no ulp bound would have caught them because the
// sweep that found the accuracy problem never probed there.

import (
	"fmt"
	"math"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// maxULP is the bound each finite case must meet. The kernels are fdlibm's,
// which are ~1 ulp by construction; 2 leaves room for the last-place
// disagreement between two correctly-implemented roundings without leaving
// room for an algorithmic error.
const maxULP = 2

// interpULP matches the compiled bound: the interpreter carries its own
// fdlibm sin/cos (internal/interp/trig.go, the same algorithm the backends
// emit) rather than delegating to Go's `math`, whose reduction error is
// unbounded in ulp terms near a zero of sine. Loosening this again would be
// re-recording Go's inaccuracy as Fern's contract — the fix is always better
// interpreter kernels, never a bigger bound.
const interpULP = maxULP

// f64UlpInputs spans the ranges where each function's argument reduction does
// different work: near zero, around the first few multiples of pi/2 (where
// sin/cos switch quadrant), far enough out that a single-rounded pi/2
// reduction visibly loses digits (|x| ~ 10 was where the old kernels shed
// about seven of them), and past 2^20, where Cody-Waite stops being exact and
// the Payne-Hanek path takes over (#7878) — up to the top of the exponent
// range, including 6381956970095103*2^797, the double with the smallest
// reduced remainder of all. Before those large entries existed, sin(1e18)
// returned -3.47e18 on every compiled backend and nothing failed.
var f64UlpInputs = []float64{
	0, 1e-8, 0.25, 0.5, 0.7853981633974483, 1, 1.5707963267948966,
	2, 3.141592653589793, 4, 6.283185307179586, 7, 10, 12.5, 25, 100,
	1000, 12345.678, 1e6,
	1e7, 1e8, 1e10, 1e15, 1e17, 1e18, 1e20, 1e30, 1e100, 1e300,
	5.3193726483265414e+255, math.MaxFloat64,
	-1e-8, -0.5, -1, -2, -3.141592653589793, -7, -10, -100, -1000,
	-1e7, -1e10, -1e18, -1e30, -1e300, -math.MaxFloat64,
}

// f64UlpPosInputs are the log-only inputs: strictly positive, spanning
// subnormal-adjacent through the top of the exponent range.
var f64UlpPosInputs = []float64{
	1e-300, 1e-30, 1e-5, 0.1, 0.5, 0.9, 1, 1.0000001, 1.5, 2, 2.718281828459045,
	10, 1e5, 1e30, 1e300,
}

// ---- the reference ----
//
// Go's own math.Sin is NOT a usable oracle near a zero of sine. For
// x = π (as a double), the true sin(x) is π − x = 1.2246467991473531772e-16,
// so the correctly-rounded double ends …532; Go returns …515, which is ~7 ulp
// and 1.4e-15 relative. Measuring against it would have failed a CORRECT
// implementation and, worse, would have passed a subtly wrong one that
// happened to reproduce Go's error.
//
// So sin/cos are referenced against a 1400-bit computation instead: reduce
// with a 420-digit π, then a Taylor series that converges long before the
// working precision runs out. exp/log/pow keep Go's math, which is accurate
// to within the bound over the inputs used here.

// piDigits is 420 significant digits (~1395 bits) and refPrec is sized to
// match. Both are set by the LARGEST argument the sweep reduces, not by the
// precision of the answer: r = x - k*(pi/2) cancels away everything but the
// last few bits, so reducing a value near the top of the exponent range needs
// pi to ~1024 + 53 bits before the remainder has any correct digits at all.
// At the previous 350 bits / 101 digits, every input past ~1e19 was reduced
// against noise, which is why none were in the corpus.
const piDigits = "3.1415926535897932384626433832795028841971693993751058209749445923078164062862089986280348253421170679821480865132823066470938446095505822317253594081284811174502841027019385211055596446229489549303819644288109756659334461284756482337867831652712019091456485669234603486104543266482133936072602491412737245870066063155881748815209209628292540917153643678925903600113305305488204665213841469519415116094330572703657595919"

const refPrec = 1400

func bigFrom(f float64) *big.Float { return new(big.Float).SetPrec(refPrec).SetFloat64(f) }

// refSinCos computes sin(x) and cos(x) to refPrec bits, then rounds to double.
func refSinCos(x float64) (sin, cos float64) {
	pi, _, _ := big.ParseFloat(piDigits, 10, refPrec, big.ToNearestEven)
	half := new(big.Float).SetPrec(refPrec).Quo(pi, bigFrom(2))
	bx := bigFrom(x)

	// k = round(x / (π/2)); r = x − k·(π/2), all at refPrec so the
	// subtraction that matters is not the one that loses the answer. k is a
	// big.Int because it reaches ~2^1024/(π/2) at the top of the range, where
	// an int64 silently saturates.
	q := new(big.Float).SetPrec(refPrec).Quo(bx, half)
	if q.Sign() >= 0 {
		q.Add(q, big.NewFloat(0.5))
	} else {
		q.Sub(q, big.NewFloat(0.5))
	}
	ki, _ := q.Int(nil) // truncation of q±½ is round-half-away
	kb := new(big.Float).SetPrec(refPrec).SetInt(ki)
	r := new(big.Float).SetPrec(refPrec).Sub(bx, new(big.Float).SetPrec(refPrec).Mul(half, kb))
	k := new(big.Int).Mod(ki, big.NewInt(4)).Int64()

	s, c := taylorSinCos(r)
	neg := func(v *big.Float) *big.Float { return new(big.Float).SetPrec(refPrec).Neg(v) }
	switch ((k % 4) + 4) % 4 {
	case 1:
		s, c = c, neg(s)
	case 2:
		s, c = neg(s), neg(c)
	case 3:
		s, c = neg(c), s
	}
	sin, _ = s.Float64()
	cos, _ = c.Float64()
	return sin, cos
}

// taylorSinCos sums the two series for |r| <= π/4. The term count follows
// refPrec: at 1400 bits the tail has to fall below 2^-1400, which 0.785^n/n!
// reaches around n = 250.
func taylorSinCos(r *big.Float) (sin, cos *big.Float) {
	sin = new(big.Float).SetPrec(refPrec)
	cos = new(big.Float).SetPrec(refPrec)
	term := new(big.Float).SetPrec(refPrec).SetInt64(1) // r^n / n!
	for n := 0; n < 300; n++ {
		if n > 0 {
			term.Mul(term, r)
			term.Quo(term, new(big.Float).SetPrec(refPrec).SetInt64(int64(n)))
		}
		add := func(dst *big.Float) {
			if (n/2)%2 == 0 {
				dst.Add(dst, term)
			} else {
				dst.Sub(dst, term)
			}
		}
		if n%2 == 0 {
			add(cos)
		} else {
			add(sin)
		}
	}
	return sin, cos
}

type f64Case struct {
	call string  // Fern expression producing the value
	want float64 // the correctly-rounded reference
}

func f64UlpCases() []f64Case {
	var cs []f64Case
	lit := func(v float64) string {
		s := strings.Replace(strconv.FormatFloat(v, 'g', 17, 64), "e+", "e", 1)
		// Fern has no unary minus on a literal in every position, and no
		// bare "1e+06" integer-looking form; normalise both.
		if !strings.ContainsAny(s, ".e") {
			s += ".0"
		}
		if strings.HasPrefix(s, "-") {
			return "(0.0 - " + s[1:] + ")"
		}
		return s
	}
	for _, x := range f64UlpInputs {
		rs, rc := refSinCos(x)
		cs = append(cs,
			f64Case{fmt.Sprintf("__sin_f64(%s)", lit(x)), rs},
			f64Case{fmt.Sprintf("__cos_f64(%s)", lit(x)), rc},
		)
		// exp overflows past ~709; keep the argument in range so this
		// measures the polynomial, not the overflow guard (covered below).
		if math.Abs(x) <= 700 {
			cs = append(cs, f64Case{fmt.Sprintf("__exp_f64(%s)", lit(x)), math.Exp(x)})
		}
	}
	// The band whose true result is SUBNORMAL, between the smallest normal
	// and expunf. Nothing above reaches it — the loop stops at |x| <= 700 and
	// exp(-1000) is past expunf, so it tests the guard rather than the gap.
	// A single (k+1023)<<52 field cannot hold k < -1022: exp(-720) returned
	// -6.6e+303, finite, enormous and the WRONG SIGN, which no caller can
	// detect (#8237). ulpDist orders across zero, so the sign flip shows up
	// as a vast distance rather than being lost in a denormal ulp count.
	for _, x := range []float64{-708.5, -709, -710, -720, -730, -740, -745, -745.13} {
		cs = append(cs, f64Case{fmt.Sprintf("__exp_f64(%s)", lit(x)), math.Exp(x)})
	}
	for _, x := range f64UlpPosInputs {
		cs = append(cs, f64Case{fmt.Sprintf("__log_f64(%s)", lit(x)), math.Log(x)})
	}
	// pow: the integer-exponent path must be EXACT, not merely close —
	// pow(3,2) truncating to 8 through `as i32` is what forced it to exist.
	// The negative bases stay on that same path: off it, ln|x| is only
	// approximate, so those go in f64SpecialCases where the answer is
	// exactly representable whatever the kernel does.
	//
	// The large-|y| rows are the ones that gate the fast path's BOUND. Every
	// row above answers correctly whatever the bound is — a small exponent is
	// on the exact path either way, and the |x| == 1 and special rows are
	// exactly representable however they are computed. So for as long as the
	// corpus stopped at |y| <= 10, a bound sending representable results down
	// exp(y*log|x|) was invisible here: #6405 measured 162 ulp on pow(2,280)
	// while this suite was green at 2. These rows are inexact AND large, so
	// they can only pass on the squaring path.
	for _, p := range []struct{ x, y float64 }{
		{3, 2}, {2, 10}, {10, 3}, {5, 4}, {2, -2}, {7, 0}, {2, 0.5}, {9, 0.5},
		{-2, 3}, {-2, 4}, {-3, 3}, {-2, -2}, {-2, -3}, {-0.5, 3}, {-1, 2},
		{2, 65}, {2, 280}, {2, 1023}, {10, 67}, {0.5, 1023}, {7, 300},
		{-3, 100}, {-2, 65}, {-0.5, 67}, {2, -100}, {0.5, -200},
	} {
		cs = append(cs, f64Case{
			fmt.Sprintf("__pow_f64(%s, %s)", lit(p.x), lit(p.y)),
			math.Pow(p.x, p.y),
		})
	}
	return cs
}

// f64SpecialCases pin the domain edges against the same reference. `bits`
// rather than a literal, because Fern source has no spelling for Inf or NaN.
func f64SpecialCases() []f64Case {
	inf := math.Inf(1)
	fromBits := func(v float64) string {
		return fmt.Sprintf("f64_from_bits(%d)", int64(math.Float64bits(v)))
	}
	return []f64Case{
		{fmt.Sprintf("__exp_f64(%s)", fromBits(inf)), math.Exp(inf)},
		{fmt.Sprintf("__exp_f64(%s)", fromBits(-inf)), math.Exp(-inf)},
		{fmt.Sprintf("__exp_f64(%s)", fromBits(math.NaN())), math.NaN()},
		{"__exp_f64(1000.0)", math.Exp(1000)},
		{"__exp_f64((0.0 - 1000.0))", math.Exp(-1000)},
		{"__exp_f64(709.0)", math.Exp(709)},
		{fmt.Sprintf("__log_f64(%s)", fromBits(inf)), math.Log(inf)},
		{fmt.Sprintf("__log_f64(%s)", fromBits(math.NaN())), math.NaN()},
		{"__log_f64(0.0)", math.Log(0)},
		{"__log_f64((0.0 - 1.0))", math.Log(-1)},
		{fmt.Sprintf("__sin_f64(%s)", fromBits(inf)), math.Sin(inf)},
		{fmt.Sprintf("__sin_f64(%s)", fromBits(-inf)), math.Sin(-inf)},
		{fmt.Sprintf("__sin_f64(%s)", fromBits(math.NaN())), math.NaN()},
		{fmt.Sprintf("__cos_f64(%s)", fromBits(inf)), math.Cos(inf)},
		{fmt.Sprintf("__cos_f64(%s)", fromBits(math.NaN())), math.NaN()},
		// pow over a negative base. Off the repeated-squaring path the
		// general case is exp(y*ln|x|), and ln of a negative base is NaN —
		// so before the sign/magnitude split these returned NaN for an
		// ordinary finite answer (#7879). |x| == 1 gives an exactly
		// representable result at every exponent, which is what lets these
		// sit here rather than under a ulp bound.
		{"__pow_f64((0.0 - 1.0), 65.0)", math.Pow(-1, 65)},
		{"__pow_f64((0.0 - 1.0), 100.0)", math.Pow(-1, 100)},
		{"__pow_f64((0.0 - 1.0), 1023.0)", math.Pow(-1, 1023)},
		{"__pow_f64((0.0 - 1.0), 1e10)", math.Pow(-1, 1e10)},
		{fmt.Sprintf("__pow_f64((0.0 - 1.0), %s)", fromBits(inf)), math.Pow(-1, inf)},
		{fmt.Sprintf("__pow_f64((0.0 - 1.0), %s)", fromBits(-inf)), math.Pow(-1, -inf)},
		// A non-integral exponent over a negative base is the one case that
		// really is NaN, so the split must not over-correct into a value.
		{"__pow_f64((0.0 - 2.0), 0.5)", math.Pow(-2, 0.5)},
		{"__pow_f64((0.0 - 8.0), (1.0 / 3.0))", math.Pow(-8, 1.0/3.0)},
		{"__pow_f64((0.0 - 2.0), 2.5)", math.Pow(-2, 2.5)},
		// The remaining IEEE-754 pow edges, none of which reach ln at all.
		{"__pow_f64(0.0, (0.0 - 1.0))", math.Pow(0, -1)},
		{fmt.Sprintf("__pow_f64(1.0, %s)", fromBits(math.NaN())), math.Pow(1, math.NaN())},
		{fmt.Sprintf("__pow_f64(%s, 0.0)", fromBits(math.NaN())), math.Pow(math.NaN(), 0)},
	}
}

// TestRefSinCosLargeArguments gates the ORACLE, which nothing else does.
//
// refSinCos is the only trustworthy sin/cos reference in the tree, and every
// compiled bound below is measured against it — so a silent regression in its
// precision would relax every one of them at once rather than failing. The
// expected values here were computed three independent ways that agree to the
// bit: this reference, a 700-digit Python `decimal` reduction, and glibc's
// sin/cos via C.
//
// They deliberately include the values where Go's math is NOT usable as a
// check, because that is the point. Go reduces large arguments less
// accurately than glibc: it is 40 ulp out at 1e30, and at the classic
// worst-case argument below it returns -4.8435e-19 for a true -4.6872e-19 —
// a 3% error. That inaccuracy is why the interpreter carries its own fdlibm
// reduction (internal/interp/trig.go) instead of delegating to Go's math,
// and why this reference, never the interpreter, is the oracle sin/cos are
// measured against (#7878).
func TestRefSinCosLargeArguments(t *testing.T) {
	cases := []struct {
		x        float64
		sin, cos float64
	}{
		{1e30, 0.009331468931175825, -0.9999564608959665},
		{1e300, -0.8178819121159085, -0.5753861119575491},
		{math.MaxFloat64, 0.004961954789184062, -0.9999876894265599},
		// 6381956970095103 * 2^797 — the standard worst case for argument
		// reduction, whose reduced argument is ~4.6872e-19.
		{5.3193726483265414e+255, 1.0, -4.687165924254628e-19},
	}
	for _, c := range cases {
		gotSin, gotCos := refSinCos(c.x)
		if d := ulpDist(gotSin, c.sin); d != 0 {
			t.Errorf("refSinCos(%g) sin = %v, want %v (%d ulp)", c.x, gotSin, c.sin, d)
		}
		if d := ulpDist(gotCos, c.cos); d != 0 {
			t.Errorf("refSinCos(%g) cos = %v, want %v (%d ulp)", c.x, gotCos, c.cos, d)
		}
		if math.Abs(gotSin) > 1 || math.Abs(gotCos) > 1 {
			t.Errorf("refSinCos(%g) left [-1,1]: sin=%v cos=%v", c.x, gotSin, gotCos)
		}
	}
}

// f64UlpProg renders the cases into a program that prints one raw bit pattern
// per line, in order.
func f64UlpProg(cs []f64Case) string {
	var b strings.Builder
	b.WriteString("import \"std/i64\";\n")
	b.WriteString("function emit(v: f64): i32 { print(f64_bits(v).to_string()); return 0; }\n")
	b.WriteString("function main(): i32 {\n")
	for _, c := range cs {
		fmt.Fprintf(&b, "    emit(%s);\n", c.call)
	}
	b.WriteString("    return 0;\n}\n")
	return b.String()
}

// ulpDist is the number of representable doubles between a and b. Mapping the
// sign-magnitude bit pattern onto a monotonic integer makes the subtraction
// meaningful across zero.
func ulpDist(a, b float64) int64 {
	if a == b {
		return 0
	}
	ord := func(f float64) int64 {
		u := int64(math.Float64bits(f))
		if u < 0 {
			return math.MinInt64 - u
		}
		return u
	}
	d := ord(a) - ord(b)
	if d < 0 {
		return -d
	}
	return d
}

// checkF64Output compares one backend's printed bit patterns against the
// reference, reporting every violation rather than stopping at the first —
// a systematic error (a wrong coefficient, a botched reduction) shows up as a
// pattern across cases, and that pattern is the diagnosis.
func checkF64Output(t *testing.T, backend, out string, cs []f64Case, bound int64) {
	t.Helper()
	lines := strings.Fields(strings.TrimSpace(out))
	if len(lines) != len(cs) {
		t.Fatalf("%s: got %d result lines, want %d\n%s", backend, len(lines), len(cs), out)
	}
	bad := 0
	for i, c := range cs {
		raw, err := strconv.ParseInt(lines[i], 10, 64)
		if err != nil {
			t.Errorf("%s: %s: unparseable output %q", backend, c.call, lines[i])
			continue
		}
		got := math.Float64frombits(uint64(raw))
		switch {
		case math.IsNaN(c.want):
			// Any NaN is acceptable; the sign and payload of a NaN are not
			// specified, and the backends legitimately differ there.
			if !math.IsNaN(got) {
				t.Errorf("%s: %s = %v, want NaN", backend, c.call, got)
				bad++
			}
		case math.IsInf(c.want, 0), c.want == 0:
			// Exact by nature: an infinity or a zero is right or it is not.
			if got != c.want || math.Signbit(got) != math.Signbit(c.want) {
				t.Errorf("%s: %s = %v, want %v", backend, c.call, got, c.want)
				bad++
			}
		default:
			if d := ulpDist(got, c.want); d > bound {
				t.Errorf("%s: %s = %v, want %v (%d ulp, bound %d)",
					backend, c.call, got, c.want, d, bound)
				bad++
			}
			// sin/cos are total on the finite doubles with range [-1, 1], so
			// a result outside it is a hard range violation whatever the ulp
			// distance says — this is the oracle-free invariant that would
			// have caught #7878's 1.78e158 on its own.
			if (strings.HasPrefix(c.call, "__sin_f64") || strings.HasPrefix(c.call, "__cos_f64")) && math.Abs(got) > 1 {
				t.Errorf("%s: %s = %v, outside [-1, 1]", backend, c.call, got)
				bad++
			}
		}
	}
	if bad > 0 {
		t.Logf("%s: %d/%d cases outside the bound", backend, bad, len(cs))
	}
}

func TestF64TranscendentalUlpInterp(t *testing.T) {
	cs := append(f64UlpCases(), f64SpecialCases()...)
	// runLangInterp takes a source PATH, not source text.
	dir := t.TempDir()
	path := filepath.Join(dir, "f64ulp.fern")
	if err := os.WriteFile(path, []byte(f64UlpProg(cs)), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runLangInterp(t, buildLangBinForInterp(t), path)
	if code != 0 {
		t.Fatalf("interp exited %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	checkF64Output(t, "interp", out, cs, interpULP)
}

func TestF64TranscendentalUlpX86_64(t *testing.T) {
	cs := append(f64UlpCases(), f64SpecialCases()...)
	out, code := compileAndRunX86_64(t, f64UlpProg(cs))
	if code != 0 {
		t.Fatalf("x86-64 exited %d\n%s", code, out)
	}
	checkF64Output(t, "x86-64-linux", out, cs, maxULP)
}

// TestF64SinCosLargeArgument sweeps the Payne-Hanek path — every argument at
// or above 2^20, which is where Cody-Waite stops being exact and the old code
// silently left [-1, 1] entirely (#7878).
//
// It is a sweep rather than a handful of literals because the failure it
// guards is magnitude-dependent: the reduction degrades continuously with the
// exponent, so a fixed shortlist proves only the exponents in it. 1158
// arguments at four mantissas across every seventh exponent, both signs.
//
// Every lane that implements sin/cos runs it: both register backends, the
// SSA arm64 backend, wasm, and the interpreter — the last is a valid lane
// only because it carries its own fdlibm reduction (internal/interp/trig.go)
// rather than Go's math, whose error near a zero of sin/cos is unbounded in
// ulp terms (617 ulp at 2^728, 3% at the worst-case argument below). The
// reference, not the interpreter, remains the oracle everything is measured
// against.
func TestF64SinCosLargeArgument(t *testing.T) {
	var xs []float64
	for e := 20; e <= 1023; e += 7 {
		for _, m := range []float64{1.0, 1.3, 1.7, 1.9} {
			x := math.Ldexp(m, e)
			if math.IsInf(x, 0) {
				continue
			}
			xs = append(xs, x, -x)
		}
	}
	// 6381956970095103 * 2^797, the argument with the smallest reduced
	// remainder of any double, plus the values named in #7878.
	xs = append(xs, 5.3193726483265414e+255, 1e30, 1e300, math.MaxFloat64, 9.2e18, 2147483648.0)

	var cs []f64Case
	for _, x := range xs {
		rs, rc := refSinCos(x)
		lit := fmt.Sprintf("f64_from_bits(%d)", int64(math.Float64bits(x)))
		cs = append(cs,
			f64Case{fmt.Sprintf("__sin_f64(%s)", lit), rs},
			f64Case{fmt.Sprintf("__cos_f64(%s)", lit), rc})
	}
	prog := f64UlpProg(cs)
	t.Run("interp", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "sincos_large.fern")
		if err := os.WriteFile(path, []byte(prog), 0o644); err != nil {
			t.Fatal(err)
		}
		code, out, errOut := runLangInterp(t, buildLangBinForInterp(t), path)
		if code != 0 {
			t.Fatalf("interp exited %d\nstdout: %s\nstderr: %s", code, out, errOut)
		}
		checkF64Output(t, "interp", out, cs, interpULP)
	})
	t.Run("x86-64", func(t *testing.T) {
		out, code := compileAndRunX86_64(t, prog)
		if code != 0 {
			t.Fatalf("x86-64 exited %d\n%s", code, out)
		}
		checkF64Output(t, "x86-64-linux", out, cs, maxULP)
	})
	t.Run("arm64", func(t *testing.T) {
		out, code := compileAndRunArm64(t, prog)
		if code != 0 {
			t.Fatalf("arm64 exited %d\n%s", code, out)
		}
		checkF64Output(t, "arm64-linux", out, cs, maxULP)
	})
	t.Run("arm64-ssa", func(t *testing.T) {
		out := compileAndRunArm64SSACapture(t, prog)
		checkF64Output(t, "arm64-ssa", out, cs, maxULP)
	})
	// Until #7878 the wasm backend did not merely lose accuracy here: its
	// reduction ran every argument through i64.trunc_f64_s, a TRAPPING
	// instruction, so |x| >= 2^63*pi/2 aborted the module. This lane holds
	// both properties — no trap, and the same bound as the register backends.
	t.Run("wasm32-wasi", func(t *testing.T) {
		out := compileAndRunWasmCapture(t, prog)
		checkF64Output(t, "wasm32-wasi", out, cs, maxULP)
	})
	t.Logf("swept %d arguments (%d cases) over the Payne-Hanek path", len(xs), len(cs))
}

// compileAndRunWasmCapture compiles src to a raw wasm32-wasi module via the
// CLI and returns its stdout from `wasmtime run`. Not runWasmCapturingStdout:
// that helper strips trailing integer-parseable lines to drop the `--invoke`
// result line, and this file's programs print nothing BUT integers, so it
// would strip the entire output.
func compileAndRunWasmCapture(t *testing.T, src string) string {
	t.Helper()
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	mod := filepath.Join(dir, "prog.wasm")
	cli := buildLangBinForInterp(t)
	if out, err := exec.Command(cli, "-target", "wasm32-wasi", "-o", mod, srcPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm compile: %v\n%s", err, out)
	}
	out, err := exec.Command(wasmtime, "run", mod).Output()
	if err != nil {
		t.Fatalf("wasmtime run: %v\n%s", err, out)
	}
	return string(out)
}

// compileAndRunArm64SSACapture compiles src with the SSA arm64 backend
// (`-target arm64-linux -backend ssa`) and returns its stdout, running the
// binary natively on arm64 or under qemu elsewhere.
func compileAndRunArm64SSACapture(t *testing.T, src string) string {
	t.Helper()
	qemu := arm64QemuOrEmpty(t)
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "prog")
	cli := buildLangBinForInterp(t)
	if out, err := exec.Command(cli, "-target", "arm64-linux", "-backend", "ssa", "-o", bin, srcPath).CombinedOutput(); err != nil {
		t.Fatalf("arm64-ssa compile: %v\n%s", err, out)
	}
	var cmd *exec.Cmd
	if qemu == "" {
		cmd = exec.Command(bin)
	} else {
		cmd = exec.Command(qemu, bin)
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("arm64-ssa run: %v\n%s", err, out)
	}
	return string(out)
}

func TestF64TranscendentalUlpArm64(t *testing.T) {
	cs := append(f64UlpCases(), f64SpecialCases()...)
	out, code := compileAndRunArm64(t, f64UlpProg(cs))
	if code != 0 {
		t.Fatalf("arm64 exited %d\n%s", code, out)
	}
	checkF64Output(t, "arm64-linux", out, cs, maxULP)
}

// TestF64TranscendentalUlpWasm holds the wasm backend to the same bound as
// the register backends over the full corpus. It had no lane here at all —
// this file's header used to claim wasm does not implement these builtins,
// which had been stale since wasmbin gained them — and that absence is why
// its trig traps and range violations (#7878) went unnoticed.
func TestF64TranscendentalUlpWasm(t *testing.T) {
	cs := append(f64UlpCases(), f64SpecialCases()...)
	out := compileAndRunWasmCapture(t, f64UlpProg(cs))
	checkF64Output(t, "wasm32-wasi", out, cs, maxULP)
}

// requireBothRegisterBackends resolves the tooling for BOTH register backends
// on this one host.
//
// That combination exists on exactly one lane — test-e2e-arm64.yml's cross-host
// job (x86 host + aarch64 cross toolchain + qemu-user) — and until #6849 no lane
// ran this file at all: the catch-all `test-e2e-other` lane owns the name, its
// x86 leg has no aarch64 cross toolchain and its aarch64 leg has no qemu-x86_64,
// so whichever runner it landed on skipped before the first assertion. A test
// that compares two backends cannot report a one-backend host as coverage.
//
// FERN_REQUIRE_CROSS_BACKENDS is set by that job, and turns a missing toolchain
// from a skip into a failure — otherwise the lane losing a toolchain would put
// the comparison straight back where it was, silently.
func requireBothRegisterBackends(t *testing.T) {
	t.Helper()
	var missing []string
	if _, _, ok := e2eharness.LookupX86_64Tooling(); !ok {
		missing = append(missing, "x86-64 (gcc, plus qemu-x86_64 off an amd64 Linux host)")
	}
	if _, _, ok := e2eharness.LookupArm64Tooling(); !ok {
		missing = append(missing, "aarch64 (aarch64-linux-gnu-gcc + qemu-aarch64)")
	}
	if len(missing) == 0 {
		return
	}
	if os.Getenv("FERN_REQUIRE_CROSS_BACKENDS") != "" {
		t.Fatalf("FERN_REQUIRE_CROSS_BACKENDS is set, so this lane is meant to have both register "+
			"toolchains, but these are missing: %s", strings.Join(missing, "; "))
	}
	t.Skipf("needs both register backends on one host; missing: %s "+
		"(the lane that has both is test-e2e-arm64.yml's cross-host job)", strings.Join(missing, "; "))
}

// TestF64TranscendentalBackendsAgree pins the two register backends to each
// other bit for bit. They share an algorithm deliberately — down to arm64's
// `frintn` being chosen over the more familiar `frinta` so both round ties to
// even — and a divergence here means one of them has drifted.
func TestF64TranscendentalBackendsAgree(t *testing.T) {
	requireBothRegisterBackends(t)
	cs := append(f64UlpCases(), f64SpecialCases()...)
	prog := f64UlpProg(cs)
	xOut, xCode := compileAndRunX86_64(t, prog)
	aOut, aCode := compileAndRunArm64(t, prog)
	if xCode != 0 || aCode != 0 {
		t.Fatalf("exit codes x86-64=%d arm64=%d", xCode, aCode)
	}
	xl, al := strings.Fields(strings.TrimSpace(xOut)), strings.Fields(strings.TrimSpace(aOut))
	if len(xl) != len(al) {
		t.Fatalf("line count differs: x86-64 %d, arm64 %d", len(xl), len(al))
	}
	// Against len(cs), not just against each other: two EMPTY outputs have equal
	// length, so the check above passes and the loop below iterates nothing —
	// the test would report a clean comparison having compared nothing.
	if len(xl) != len(cs) {
		t.Fatalf("each backend printed %d values, want one per case (%d)", len(xl), len(cs))
	}
	compared := 0
	for i, c := range cs {
		compared++
		if xl[i] == al[i] {
			continue
		}
		// A NaN payload is unspecified, so only disagreement on a non-NaN
		// value is a fault.
		xv, _ := strconv.ParseInt(xl[i], 10, 64)
		av, _ := strconv.ParseInt(al[i], 10, 64)
		if math.IsNaN(math.Float64frombits(uint64(xv))) && math.IsNaN(math.Float64frombits(uint64(av))) {
			continue
		}
		t.Errorf("%s: x86-64 %v, arm64 %v",
			c.call, math.Float64frombits(uint64(xv)), math.Float64frombits(uint64(av)))
	}
	// The corpus is built from package-level input tables, so a shrunken one
	// means the case builders stopped producing rather than that the backends
	// agree. Measured at 118; the floor leaves room to add inputs, not to lose
	// most of them.
	if compared < 100 {
		t.Fatalf("compared %d cases, want the full corpus (measured 118)", compared)
	}
	t.Logf("compared %d transcendental results bit for bit across both register backends", compared)
}

// TestF64TranscendentalUlpSelfHostX86_64 holds the SELF-HOSTED x86-64 backend
// (examples/self_host/asm_ir.fern) to the same bound as the native one, then
// pins the two to each other bit for bit.
//
// It is the gate the self-host half was missing, and its absence is why that
// half was the last copy of the old math in the tree: asm_ir.fern lowered
// sin / cos / exp / log / pow straight onto the x87 FPU — `fsin`, `fyl2x`,
// `f2xm1` + `fscale` — for three PRs after every other backend had moved to
// the fdlibm kernels, because nothing compared a self-host transcendental
// RESULT against native's. Measured on this corpus that path was 1.6e11 ulp at
// sin(pi) (x87 reduces against a 66-bit pi, which is not enough near a zero of
// sine, where the reduced argument IS the answer), 21 ulp at sin(1e6), and it
// returned a finite number for exp(+Inf).
func TestF64TranscendentalUlpSelfHostX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	// asm_load_run, not asm_ir_run: the program imports std/i64 for the i64
	// printing, and only the loading driver resolves imports.
	copySelfHostDriver(t, dir, "asm_load_run.fern")
	driver := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "alr")
	root, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	cs := append(f64UlpCases(), f64SpecialCases()...)
	prog := f64UlpProg(cs)
	entry := filepath.Join(dir, "f64ulp.fern")
	if err := os.WriteFile(entry, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	asm, code := runBin(runX86_64Bin(runner, driver, entry, root), "")
	if code != 0 || len(asm) == 0 {
		t.Fatalf("self-host driver exited %d, emitted %d bytes", code, len(asm))
	}
	// The lowering, not just the numbers: a `call` into the shared runtime
	// bundle, and no x87 left behind it.
	if !strings.Contains(asm, "call __fern_sin_f64") {
		t.Error("self-host emit has no call into __fern_sin_f64 — the transcendental runtime was not reached")
	}
	for _, x87 := range []string{"fsin", "fcos", "fyl2x", "f2xm1"} {
		// Matched as a whole emitted line: `.Lfsin_ret` and friends are
		// label names, not instructions.
		if strings.Contains(asm, "\n    "+x87+"\n") {
			t.Errorf("self-host emit still contains the x87 instruction %q", x87)
		}
	}

	bin := buildBin(t, gcc, dir, "f64ulp_selfhost", asm)
	out, rc := runBin(runX86_64Bin(runner, bin), "")
	if rc != 0 {
		t.Fatalf("self-host x86-64 program exited %d\n%s", rc, out)
	}
	checkF64Output(t, "self-host x86-64", out, cs, maxULP)

	// Bit-for-bit against the native backend it is a transliteration of. The
	// ulp bound above says each is close enough to the reference; this says
	// they have not drifted from each other, which is the property the two
	// hand-written copies exist to break.
	nOut, nCode := compileAndRunX86_64(t, prog)
	if nCode != 0 {
		t.Fatalf("native x86-64 exited %d\n%s", nCode, nOut)
	}
	sl, nl := strings.Fields(strings.TrimSpace(out)), strings.Fields(strings.TrimSpace(nOut))
	if len(sl) != len(cs) || len(nl) != len(cs) {
		t.Fatalf("printed %d (self-host) / %d (native) values, want one per case (%d)", len(sl), len(nl), len(cs))
	}
	for i, c := range cs {
		if sl[i] == nl[i] {
			continue
		}
		sv, _ := strconv.ParseInt(sl[i], 10, 64)
		nv, _ := strconv.ParseInt(nl[i], 10, 64)
		// A NaN payload is unspecified, so only a non-NaN disagreement is a fault.
		if math.IsNaN(math.Float64frombits(uint64(sv))) && math.IsNaN(math.Float64frombits(uint64(nv))) {
			continue
		}
		t.Errorf("%s: self-host %v, native %v",
			c.call, math.Float64frombits(uint64(sv)), math.Float64frombits(uint64(nv)))
	}
	t.Logf("compared %d transcendental results bit for bit, self-host x86-64 vs native x86-64", len(cs))
}
