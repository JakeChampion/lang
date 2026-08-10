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
//
// Native wasm does not implement these builtins (there is no __sin_f64 in
// internal/codegen/wasmbin), so it is deliberately absent rather than skipped.

import (
	"fmt"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// maxULP is the bound each finite case must meet. The kernels are fdlibm's,
// which are ~1 ulp by construction; 2 leaves room for the last-place
// disagreement between two correctly-implemented roundings without leaving
// room for an algorithmic error.
const maxULP = 2

// interpULP is the interpreter's own bound, and it is looser for a reason
// worth recording: `fern -interp` evaluates these through Go's `math`, which
// is up to ~7 ulp near a zero of sine (see the oracle note below). So of the
// three implementations the INTERPRETER is now the least accurate — an
// inversion that matters because it is the oracle every differential suite
// compares against. Tightening this means giving the interpreter better
// kernels, not loosening the compiled bound.
const interpULP = 8

// f64UlpInputs spans the ranges where each function's argument reduction does
// different work: near zero, around the first few multiples of pi/2 (where
// sin/cos switch quadrant), and far enough out that a single-rounded pi/2
// reduction visibly loses digits — |x| ~ 10 was where the old kernels shed
// about seven of them.
var f64UlpInputs = []float64{
	0, 1e-8, 0.25, 0.5, 0.7853981633974483, 1, 1.5707963267948966,
	2, 3.141592653589793, 4, 6.283185307179586, 7, 10, 12.5, 25, 100,
	1000, 12345.678, 1e6,
	-1e-8, -0.5, -1, -2, -3.141592653589793, -7, -10, -100, -1000,
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
// So sin/cos are referenced against a 350-bit computation instead: reduce with
// a 100-digit π, then a Taylor series that converges long before the working
// precision runs out. exp/log/pow keep Go's math, which is accurate to within
// the bound over the inputs used here.

const piDigits = "3.14159265358979323846264338327950288419716939937510582097494459230781640628620899862803482534211706798"

const refPrec = 350

func bigFrom(f float64) *big.Float { return new(big.Float).SetPrec(refPrec).SetFloat64(f) }

// refSinCos computes sin(x) and cos(x) to refPrec bits, then rounds to double.
func refSinCos(x float64) (sin, cos float64) {
	pi, _, _ := big.ParseFloat(piDigits, 10, refPrec, big.ToNearestEven)
	half := new(big.Float).SetPrec(refPrec).Quo(pi, bigFrom(2))
	bx := bigFrom(x)

	// k = round(x / (π/2)); r = x − k·(π/2), all at refPrec so the
	// subtraction that matters is not the one that loses the answer.
	qf, _ := new(big.Float).SetPrec(refPrec).Quo(bx, half).Float64()
	k := int64(math.Round(qf))
	kb := new(big.Float).SetPrec(refPrec).SetInt64(k)
	r := new(big.Float).SetPrec(refPrec).Sub(bx, new(big.Float).SetPrec(refPrec).Mul(half, kb))

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

// taylorSinCos sums the two series for |r| <= π/4, where 60 terms is far past
// the point the tail drops below 2^-350.
func taylorSinCos(r *big.Float) (sin, cos *big.Float) {
	sin = new(big.Float).SetPrec(refPrec)
	cos = new(big.Float).SetPrec(refPrec)
	term := new(big.Float).SetPrec(refPrec).SetInt64(1) // r^n / n!
	for n := 0; n < 60; n++ {
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
	for _, x := range f64UlpPosInputs {
		cs = append(cs, f64Case{fmt.Sprintf("__log_f64(%s)", lit(x)), math.Log(x)})
	}
	// pow: the integer-exponent path must be EXACT, not merely close —
	// pow(3,2) truncating to 8 through `as i32` is what forced it to exist.
	for _, p := range []struct{ x, y float64 }{
		{3, 2}, {2, 10}, {10, 3}, {5, 4}, {2, -2}, {7, 0}, {2, 0.5}, {9, 0.5},
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

func TestF64TranscendentalUlpArm64(t *testing.T) {
	cs := append(f64UlpCases(), f64SpecialCases()...)
	out, code := compileAndRunArm64(t, f64UlpProg(cs))
	if code != 0 {
		t.Fatalf("arm64 exited %d\n%s", code, out)
	}
	checkF64Output(t, "arm64-linux", out, cs, maxULP)
}

// TestF64TranscendentalBackendsAgree pins the two register backends to each
// other bit for bit. They share an algorithm deliberately — down to arm64's
// `frintn` being chosen over the more familiar `frinta` so both round ties to
// even — and a divergence here means one of them has drifted.
func TestF64TranscendentalBackendsAgree(t *testing.T) {
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
	for i, c := range cs {
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
}
