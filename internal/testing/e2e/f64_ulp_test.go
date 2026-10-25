package e2e

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

	"github.com/jakechampion/lang/internal/envflag"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const maxULP = 2

const interpULP = maxULP

const (
	logNear1Lo   = 0x3fee000000000000
	logNear1Hi   = 0x3ff1090000000000
	logOff       = 0x3fe6000000000000
	logTableBits = 7
)

var f64UlpInputs = []float64{
	0, 1e-8, 0.25, 0.5, 0.7853981633974483, 1, 1.5707963267948966,
	2, 3.141592653589793, 4, 6.283185307179586, 7, 10, 12.5, 25, 100,
	1000, 12345.678, 1e6,
	1e7, 1e8, 1e10, 1e15, 1e17, 1e18, 1e20, 1e30, 1e100, 1e300,
	5.3193726483265414e+255, math.MaxFloat64,
	-1e-8, -0.5, -1, -2, -3.141592653589793, -7, -10, -100, -1000,
	-1e7, -1e10, -1e18, -1e30, -1e300, -math.MaxFloat64,
	-8.167959597007928e+21, -9.85526768282372e+21, -2.217216507044164e+21,
	9.018962311905212e+21, -5.150187184512172e+21, -2.4335988196029356e+21,
	-4.0537208950325064e+21, -842990.3548382078, -58671.335186358076,
	727539.9738490027,
}

var f64UlpPosInputs = []float64{
	2.2250738585072014e-308, 1e-300, 1e-30, 1e-5, 0.1, 0.5, 0.9, 1, 1.0000001,
	1.5, 2, 2.718281828459045, 10, 1e5, 1e30, 1e300, math.MaxFloat64,
}

const sinCosULP = 1

const powULP = 1

const logULP = 1

func f64LogInputs() []float64 {
	var xs []float64
	for j := range 81 {
		xs = append(xs, 0.92+0.16*float64(j)/80)
	}
	for _, b := range []uint64{logNear1Lo, logNear1Hi, 0x3ff0000000000000} {
		x, y := math.Float64frombits(b), math.Float64frombits(b)
		for range 4 {
			x, y = math.Nextafter(x, 0), math.Nextafter(y, 2)
			xs = append(xs, x, y)
		}
	}
	for i := uint64(0); i <= 1<<logTableBits; i += 9 {
		edge := logOff + i<<(52-logTableBits)
		for _, k := range []int{-1000, -1, 1, 700} {
			xs = append(xs, math.Ldexp(math.Float64frombits(edge-1), k), math.Ldexp(math.Float64frombits(edge), k))
		}
	}
	return xs
}

func refLog(x float64) float64 {
	out, _ := bigLn(x).Float64()
	return out
}

const refPrecLnExp = 256

func newRef() *big.Float { return new(big.Float).SetPrec(refPrecLnExp) }

func bigLn(x float64) *big.Float {
	frac, e := math.Frexp(x)
	m := newRef().SetFloat64(2 * frac)
	t := newRef().Quo(newRef().Sub(m, newRef().SetInt64(1)), newRef().Add(m, newRef().SetInt64(1)))
	return newRef().Add(bigAtanh2(t), newRef().Mul(bigLn2(), newRef().SetInt64(int64(e-1))))
}

func bigAtanh2(t *big.Float) *big.Float {
	t2 := newRef().Mul(t, t)
	term, sum := newRef().Set(t), newRef().Set(t)
	for n := int64(3); t.Sign() != 0; n += 2 {
		term.Mul(term, t2)
		q := newRef().Quo(term, newRef().SetInt64(n))
		if q.MantExp(nil) < sum.MantExp(nil)-refPrecLnExp-8 {
			break
		}
		sum.Add(sum, q)
	}
	return sum.Mul(sum, newRef().SetInt64(2))
}

func bigLn2() *big.Float {
	return bigAtanh2(newRef().Quo(newRef().SetInt64(1), newRef().SetInt64(3)))
}

func bigExp(v *big.Float) *big.Float {
	ln2 := bigLn2()
	q, _ := newRef().Quo(v, ln2).Float64()
	k := math.Round(q)
	r := newRef().Sub(v, newRef().Mul(ln2, newRef().SetFloat64(k)))
	term, sum := newRef().SetInt64(1), newRef().SetInt64(1)
	for n := int64(1); term.Sign() != 0; n++ {
		term.Mul(term, r)
		term.Quo(term, newRef().SetInt64(n))
		if term.MantExp(nil) < sum.MantExp(nil)-refPrecLnExp-8 {
			break
		}
		sum.Add(sum, term)
	}
	return sum.SetMantExp(sum, int(k))
}

func refPow(x, y float64) float64 {
	sign := 1.0
	if x < 0 {
		x = -x
		if math.Mod(y, 2) != 0 {
			sign = -1
		}
	}
	out, _ := bigExp(newRef().Mul(bigLn(x), newRef().SetFloat64(y))).Float64()
	return sign * out
}

const piDigits = "3.1415926535897932384626433832795028841971693993751058209749445923078164062862089986280348253421170679821480865132823066470938446095505822317253594081284811174502841027019385211055596446229489549303819644288109756659334461284756482337867831652712019091456485669234603486104543266482133936072602491412737245870066063155881748815209209628292540917153643678925903600113305305488204665213841469519415116094330572703657595919"

const refPrec = 1400

func bigFrom(f float64) *big.Float { return new(big.Float).SetPrec(refPrec).SetFloat64(f) }

func refSinCos(x float64) (sin, cos float64) {
	pi, _, _ := big.ParseFloat(piDigits, 10, refPrec, big.ToNearestEven)
	half := new(big.Float).SetPrec(refPrec).Quo(pi, bigFrom(2))
	bx := bigFrom(x)

	q := new(big.Float).SetPrec(refPrec).Quo(bx, half)
	if q.Sign() >= 0 {
		q.Add(q, big.NewFloat(0.5))
	} else {
		q.Sub(q, big.NewFloat(0.5))
	}
	ki, _ := q.Int(nil)
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

func taylorSinCos(r *big.Float) (sin, cos *big.Float) {
	sin = new(big.Float).SetPrec(refPrec)
	cos = new(big.Float).SetPrec(refPrec)
	term := new(big.Float).SetPrec(refPrec).SetInt64(1)
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

var f64HypInputs = []float64{
	1e-300, 1e-20, 1e-10, 1e-5, 0.1, 0.25, 0.34657359027997264, 0.5, 0.75, 1,
	1.5, 2, 5, 10, 21.9, 22, 22.1, 50, 100, 500, 709, 709.78, 710, 710.4,
	710.4758600739439,
}

// refHyp returns sinh, cosh and tanh of x at refPrecLnExp bits. Below 2^-30 the
// exponentials cancel past the reference precision, so sinh is x + x^3/6 there.
func refHyp(x float64) (sinh, cosh, tanh float64) {
	bx := newRef().SetFloat64(x)
	half := newRef().SetFloat64(0.5)
	var bs, bc *big.Float
	if math.Abs(x) < 0x1p-30 {
		x3 := newRef().Mul(bx, newRef().Mul(bx, bx))
		bs = newRef().Add(bx, newRef().Quo(x3, newRef().SetInt64(6)))
		bc = newRef().Add(newRef().SetInt64(1), newRef().Mul(half, newRef().Mul(bx, bx)))
	} else {
		ex := bigExp(bx)
		emx := newRef().Quo(newRef().SetInt64(1), ex)
		bs = newRef().Mul(newRef().Sub(ex, emx), half)
		bc = newRef().Mul(newRef().Add(ex, emx), half)
	}
	sinh, _ = bs.Float64()
	cosh, _ = bc.Float64()
	tanh, _ = newRef().Quo(bs, bc).Float64()
	return sinh, cosh, tanh
}

type f64Case struct {
	call string
	want float64
}

func f64UlpCases() []f64Case {
	var cs []f64Case
	lit := func(v float64) string {
		s := strings.Replace(strconv.FormatFloat(v, 'g', 17, 64), "e+", "e", 1)
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
			f64Case{fmt.Sprintf("fsin(%s)", lit(x)), rs},
			f64Case{fmt.Sprintf("fcos(%s)", lit(x)), rc},
		)
		if math.Abs(x) <= 700 {
			cs = append(cs, f64Case{fmt.Sprintf("fexp(%s)", lit(x)), math.Exp(x)})
		}
	}
	for _, r := range []struct {
		x    float64
		bits uint64
	}{
		{-708.5, 0x000e6cf6d08897ac},
		{-709, 0x0008bfe55de02338},
		{-710, 0x00033802fd28b3c3},
		{-720, 0x0000000993b4dc95},
		{-730, 0x00000000001c7ea3},
		{-740, 0x0000000000000055},
		{-741, 0x000000000000001f},
		{-743, 0x0000000000000004},
		{-744, 0x0000000000000002},
		{-745, 0x0000000000000001},
		{709.5, 0x7fe81e9b4b52d0c9},
		{709.7, 0x7fed75ae7a50ee14},
		{709.78, 0x7fefe9ce5c4c52b4},
		{709.7827128933839, 0x7feffffffffffb2a},
	} {
		cs = append(cs, f64Case{fmt.Sprintf("fexp(%s)", lit(r.x)), math.Float64frombits(r.bits)})
	}
	for _, x := range f64HypInputs {
		for _, v := range []float64{x, -x} {
			rs, rc, rt := refHyp(v)
			cs = append(cs,
				f64Case{fmt.Sprintf("fsinh(%s)", lit(v)), rs},
				f64Case{fmt.Sprintf("fcosh(%s)", lit(v)), rc},
				f64Case{fmt.Sprintf("ftanh(%s)", lit(v)), rt},
			)
		}
	}
	for _, x := range append(f64UlpPosInputs, f64LogInputs()...) {
		cs = append(cs, f64Case{fmt.Sprintf("flog(%s)", lit(x)), refLog(x)})
	}
	for _, r := range []struct {
		x    float64
		bits uint64
	}{
		{1e-310, 0xc0864e69394d9508},
		{1e-320, 0xc087069e3078e52d},
		{5e-324, 0xc0874385446d71c3},
	} {
		cs = append(cs, f64Case{fmt.Sprintf("flog(%s)", lit(r.x)), math.Float64frombits(r.bits)})
	}
	for _, p := range []struct{ x, y float64 }{
		{3, 2}, {2, 10}, {10, 3}, {5, 4}, {2, -2}, {7, 0}, {2, 0.5}, {9, 0.5},
		{-2, 3}, {-2, 4}, {-3, 3}, {-2, -2}, {-2, -3}, {-0.5, 3}, {-1, 2},
		{2, 65}, {2, 280}, {2, 1023}, {10, 67}, {0.5, 1023}, {7, 300},
		{-3, 100}, {-2, 65}, {-0.5, 67}, {2, -100}, {0.5, -200},
		{2, -1074}, {2, -1030}, {10, -320}, {-2, -1073}, {-10, -320},
		{85.23944470508046, -29.321248316038552}, {2.5, 700.3}, {123.456, 147.3},
		{0.9999999, 7.1e9}, {1.0000001, 7.0e9}, {3.3e-5, 71.2}, {50.5, -189.5},
		{1e-310, -0.98}, {1e-310, 0.37}, {0.75, -2400.5}, {1.5, 1749.25},
	} {
		cs = append(cs, f64Case{
			fmt.Sprintf("fpow(%s, %s)", lit(p.x), lit(p.y)),
			refPow(p.x, p.y),
		})
	}
	return cs
}

func f64SpecialCases() []f64Case {
	inf := math.Inf(1)
	fromBits := func(v float64) string {
		return fmt.Sprintf("f64_from_bits(%d)", int64(math.Float64bits(v)))
	}
	return []f64Case{
		{fmt.Sprintf("fexp(%s)", fromBits(inf)), math.Exp(inf)},
		{fmt.Sprintf("fexp(%s)", fromBits(-inf)), math.Exp(-inf)},
		{fmt.Sprintf("fexp(%s)", fromBits(math.NaN())), math.NaN()},
		{"fexp(1000.0)", math.Exp(1000)},
		{"fexp((0.0 - 1000.0))", math.Exp(-1000)},
		{"fexp(709.0)", math.Exp(709)},
		{fmt.Sprintf("flog(%s)", fromBits(inf)), math.Log(inf)},
		{fmt.Sprintf("flog(%s)", fromBits(math.NaN())), math.NaN()},
		{"flog(0.0)", math.Log(0)},
		{"flog((0.0 - 1.0))", math.Log(-1)},
		{fmt.Sprintf("fsin(%s)", fromBits(inf)), math.Sin(inf)},
		{fmt.Sprintf("fsin(%s)", fromBits(-inf)), math.Sin(-inf)},
		{fmt.Sprintf("fsin(%s)", fromBits(math.NaN())), math.NaN()},
		{fmt.Sprintf("fcos(%s)", fromBits(inf)), math.Cos(inf)},
		{fmt.Sprintf("fcos(%s)", fromBits(math.NaN())), math.NaN()},
		{"fpow((0.0 - 1.0), 65.0)", math.Pow(-1, 65)},
		{"fpow((0.0 - 1.0), 100.0)", math.Pow(-1, 100)},
		{"fpow((0.0 - 1.0), 1023.0)", math.Pow(-1, 1023)},
		{"fpow((0.0 - 1.0), 1e10)", math.Pow(-1, 1e10)},
		{fmt.Sprintf("fpow((0.0 - 1.0), %s)", fromBits(inf)), math.Pow(-1, inf)},
		{fmt.Sprintf("fpow((0.0 - 1.0), %s)", fromBits(-inf)), math.Pow(-1, -inf)},
		{"fpow((0.0 - 2.0), 0.5)", math.Pow(-2, 0.5)},
		{"fpow((0.0 - 8.0), (1.0 / 3.0))", math.Pow(-8, 1.0/3.0)},
		{"fpow((0.0 - 2.0), 2.5)", math.Pow(-2, 2.5)},
		{"fpow(0.0, (0.0 - 1.0))", math.Pow(0, -1)},
		{fmt.Sprintf("fpow(1.0, %s)", fromBits(math.NaN())), math.Pow(1, math.NaN())},
		{fmt.Sprintf("fpow(%s, 0.0)", fromBits(math.NaN())), math.Pow(math.NaN(), 0)},
		{"fsinh(0.0)", 0},
		{"fsinh((-0.0))", math.Copysign(0, -1)},
		{"fsinh(710.5)", inf},
		{"fsinh((0.0 - 710.5))", -inf},
		{fmt.Sprintf("fsinh(%s)", fromBits(-inf)), -inf},
		{fmt.Sprintf("fsinh(%s)", fromBits(math.NaN())), math.NaN()},
		{"fcosh((-0.0))", 1},
		{"fcosh((0.0 - 710.5))", inf},
		{fmt.Sprintf("fcosh(%s)", fromBits(-inf)), inf},
		{fmt.Sprintf("fcosh(%s)", fromBits(math.NaN())), math.NaN()},
		{"ftanh(0.0)", 0},
		{"ftanh((-0.0))", math.Copysign(0, -1)},
		{fmt.Sprintf("ftanh(%s)", fromBits(inf)), 1},
		{fmt.Sprintf("ftanh(%s)", fromBits(-inf)), -1},
		{fmt.Sprintf("ftanh(%s)", fromBits(math.NaN())), math.NaN()},
	}
}

func TestRefSinCosLargeArguments(t *testing.T) {
	cases := []struct {
		x        float64
		sin, cos float64
	}{
		{1e30, 0.009331468931175825, -0.9999564608959665},
		{1e300, -0.8178819121159085, -0.5753861119575491},
		{math.MaxFloat64, 0.004961954789184062, -0.9999876894265599},
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

func f64UlpProg(cs []f64Case) string {
	var b strings.Builder
	b.WriteString("import \"std/i64\";\nimport \"std/f64\";\n")
	b.WriteString("function emit(v: f64): i32 { print(f64_bits(v).to_string()); return 0; }\n")
	for _, f := range []string{"sin", "cos", "exp", "log", "sinh", "cosh", "tanh"} {
		fmt.Fprintf(&b, "function f%s(x: f64): f64 { return x.%s(); }\n", f, f)
	}
	b.WriteString("function fpow(x: f64, y: f64): f64 { return x.pow(y); }\n")
	b.WriteString("function main(): i32 {\n")
	for _, c := range cs {
		fmt.Fprintf(&b, "    emit(%s);\n", c.call)
	}
	b.WriteString("    return 0;\n}\n")
	return b.String()
}

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
			if !math.IsNaN(got) {
				t.Errorf("%s: %s = %v, want NaN", backend, c.call, got)
				bad++
			}
		case math.IsInf(c.want, 0), c.want == 0:
			if got != c.want || math.Signbit(got) != math.Signbit(c.want) {
				t.Errorf("%s: %s = %v, want %v", backend, c.call, got, c.want)
				bad++
			}
		default:
			b := bound
			if strings.HasPrefix(c.call, "flog(") {
				b = min(b, logULP)
			}
			if strings.HasPrefix(c.call, "fsin(") || strings.HasPrefix(c.call, "fcos(") {
				b = min(b, sinCosULP)
			}
			if strings.HasPrefix(c.call, "fpow(") {
				b = min(b, powULP)
			}
			if d := ulpDist(got, c.want); d > b {
				t.Errorf("%s: %s = %v, want %v (%d ulp, bound %d)",
					backend, c.call, got, c.want, d, b)
				bad++
			}
			if (strings.HasPrefix(c.call, "fsin(") || strings.HasPrefix(c.call, "fcos(")) && math.Abs(got) > 1 {
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
	xs = append(xs, 5.3193726483265414e+255, 1e30, 1e300, math.MaxFloat64, 9.2e18, 2147483648.0)

	var cs []f64Case
	for _, x := range xs {
		rs, rc := refSinCos(x)
		lit := fmt.Sprintf("f64_from_bits(%d)", int64(math.Float64bits(x)))
		cs = append(cs,
			f64Case{fmt.Sprintf("fsin(%s)", lit), rs},
			f64Case{fmt.Sprintf("fcos(%s)", lit), rc})
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
	t.Run("wasm32-wasi", func(t *testing.T) {
		out := compileAndRunWasmCapture(t, prog)
		checkF64Output(t, "wasm32-wasi", out, cs, maxULP)
	})
	t.Logf("swept %d arguments (%d cases) over the Payne-Hanek path", len(xs), len(cs))
}

func TestF64PowExactIntegralPowers(t *testing.T) {
	var bases []float64
	for b := 2.0; b <= 24; b++ {
		bases = append(bases, b, -b)
	}
	bases = append(bases, 0.5, -0.5, 0.25, 1.5, -1.5, 0.75, 2.5, 1.0625, 1023, 1025, 9007199254740991)
	var cs []f64Case
	for _, b := range bases {
		rb := new(big.Rat).SetFloat64(b)
		for _, sign := range []int64{1, -1} {
			r := big.NewRat(1, 1)
			for n := int64(1); n <= 1074; n++ {
				r.Mul(r, rb)
				v := r
				if sign < 0 {
					v = new(big.Rat).Inv(r)
				}
				want, exact := v.Float64()
				if math.IsInf(want, 0) || want == 0 {
					break
				}
				if exact {
					cs = append(cs, f64Case{fmt.Sprintf("fpow(f64_from_bits(%d), %d.0)", int64(math.Float64bits(b)), sign*n), want})
				}
			}
		}
	}
	prog := f64UlpProg(cs)
	t.Run("interp", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "pow_exact.fern")
		if err := os.WriteFile(path, []byte(prog), 0o644); err != nil {
			t.Fatal(err)
		}
		code, out, errOut := runLangInterp(t, buildLangBinForInterp(t), path)
		if code != 0 {
			t.Fatalf("interp exited %d\nstdout: %s\nstderr: %s", code, out, errOut)
		}
		checkF64Output(t, "interp", out, cs, 0)
	})
	t.Run("x86-64", func(t *testing.T) {
		out, code := compileAndRunX86_64(t, prog)
		if code != 0 {
			t.Fatalf("x86-64 exited %d\n%s", code, out)
		}
		checkF64Output(t, "x86-64-linux", out, cs, 0)
	})
	t.Run("arm64", func(t *testing.T) {
		out, code := compileAndRunArm64(t, prog)
		if code != 0 {
			t.Fatalf("arm64 exited %d\n%s", code, out)
		}
		checkF64Output(t, "arm64-linux", out, cs, 0)
	})
	t.Run("wasm32-wasi", func(t *testing.T) {
		checkF64Output(t, "wasm32-wasi", compileAndRunWasmCapture(t, prog), cs, 0)
	})
	t.Logf("%d exactly representable integral powers", len(cs))
}

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

func TestF64TranscendentalUlpArm64(t *testing.T) {
	cs := append(f64UlpCases(), f64SpecialCases()...)
	out, code := compileAndRunArm64(t, f64UlpProg(cs))
	if code != 0 {
		t.Fatalf("arm64 exited %d\n%s", code, out)
	}
	checkF64Output(t, "arm64-linux", out, cs, maxULP)
}

func TestF64TranscendentalUlpWasm(t *testing.T) {
	cs := append(f64UlpCases(), f64SpecialCases()...)
	out := compileAndRunWasmCapture(t, f64UlpProg(cs))
	checkF64Output(t, "wasm32-wasi", out, cs, maxULP)
}

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
	if envflag.On("FERN_REQUIRE_CROSS_BACKENDS") {
		t.Fatalf("FERN_REQUIRE_CROSS_BACKENDS is set, so this lane is meant to have both register "+
			"toolchains, but these are missing: %s", strings.Join(missing, "; "))
	}
	t.Skipf("needs both register backends on one host; missing: %s "+
		"(the lane that has both is test-e2e-arm64.yml's cross-host job)", strings.Join(missing, "; "))
}

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
	if len(xl) != len(cs) {
		t.Fatalf("each backend printed %d values, want one per case (%d)", len(xl), len(cs))
	}
	compared := 0
	for i, c := range cs {
		compared++
		if xl[i] == al[i] {
			continue
		}
		xv, _ := strconv.ParseInt(xl[i], 10, 64)
		av, _ := strconv.ParseInt(al[i], 10, 64)
		if math.IsNaN(math.Float64frombits(uint64(xv))) && math.IsNaN(math.Float64frombits(uint64(av))) {
			continue
		}
		t.Errorf("%s: x86-64 %v, arm64 %v",
			c.call, math.Float64frombits(uint64(xv)), math.Float64frombits(uint64(av)))
	}
	if compared < 100 {
		t.Fatalf("compared %d cases, want the full corpus (measured 118)", compared)
	}
	t.Logf("compared %d transcendental results bit for bit across both register backends", compared)
}

func TestF64TranscendentalUlpSelfHostX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	driver := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "alr")
	root, err := filepath.Abs("../../stdlib")
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
	bin := buildBin(t, gcc, dir, "f64ulp_selfhost", asm)
	out, rc := runBin(runX86_64Bin(runner, bin), "")
	if rc != 0 {
		t.Fatalf("self-host x86-64 program exited %d\n%s", rc, out)
	}
	checkF64Output(t, "self-host x86-64", out, cs, maxULP)

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
		if math.IsNaN(math.Float64frombits(uint64(sv))) && math.IsNaN(math.Float64frombits(uint64(nv))) {
			continue
		}
		t.Errorf("%s: self-host %v, native %v",
			c.call, math.Float64frombits(uint64(sv)), math.Float64frombits(uint64(nv)))
	}
	t.Logf("compared %d transcendental results bit for bit, self-host x86-64 vs native x86-64", len(cs))
}

func TestF64TranscendentalLanesAgree(t *testing.T) {
	cs := append(f64UlpCases(), f64SpecialCases()...)
	prog := f64UlpProg(cs)
	dir := t.TempDir()
	path := filepath.Join(dir, "f64ulp.fern")
	if err := os.WriteFile(path, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	code, iOut, errOut := runLangInterp(t, buildLangBinForInterp(t), path)
	if code != 0 {
		t.Fatalf("interp exited %d\nstdout: %s\nstderr: %s", code, iOut, errOut)
	}
	wOut := compileAndRunWasmCapture(t, prog)
	xOut, xCode := compileAndRunX86_64(t, prog)
	if xCode != 0 {
		t.Fatalf("x86-64 exited %d\n%s", xCode, xOut)
	}
	lanes := map[string]string{"interp": iOut, "wasm32-wasi": wOut}
	want := strings.Fields(strings.TrimSpace(xOut))
	if len(want) != len(cs) {
		t.Fatalf("x86-64 printed %d values, want one per case (%d)", len(want), len(cs))
	}
	for lane, out := range lanes {
		got := strings.Fields(strings.TrimSpace(out))
		if len(got) != len(cs) {
			t.Fatalf("%s printed %d values, want one per case (%d)", lane, len(got), len(cs))
		}
		for i, c := range cs {
			if got[i] == want[i] {
				continue
			}
			gv, _ := strconv.ParseInt(got[i], 10, 64)
			wv, _ := strconv.ParseInt(want[i], 10, 64)
			if math.IsNaN(math.Float64frombits(uint64(gv))) && math.IsNaN(math.Float64frombits(uint64(wv))) {
				continue
			}
			t.Errorf("%s: %s %v, x86-64 %v", c.call, lane, math.Float64frombits(uint64(gv)), math.Float64frombits(uint64(wv)))
		}
	}
}
