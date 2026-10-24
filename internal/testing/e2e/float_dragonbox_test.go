package e2e

import (
	"fmt"
	"math"
	"math/big"
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

func decimalNorm(s string) string {
	s = strings.TrimSpace(s)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	exp := 0
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		e, err := strconv.Atoi(s[i+1:])
		if err != nil {
			return "?" + s
		}
		exp, s = e, s[:i]
	}
	if i := strings.Index(s, "."); i >= 0 {
		exp -= len(s) - i - 1
		s = s[:i] + s[i+1:]
	}
	s = strings.TrimLeft(s, "0")
	for len(s) > 1 && strings.HasSuffix(s, "0") {
		s = s[:len(s)-1]
		exp++
	}
	if s == "" {
		return "0e0"
	}
	sign := ""
	if neg {
		sign = "-"
	}
	return fmt.Sprintf("%s%se%d", sign, s, exp)
}

func shortestTie(got, want string, v float64, bits int) bool {
	if !roundTripsTo(got, v, bits) {
		return false
	}
	gd, ge, ok := splitDecimalNorm(got)
	if !ok {
		return false
	}
	wd, we, ok := splitDecimalNorm(want)
	if !ok || len(gd) != len(wd) {
		return false
	}
	rv := new(big.Rat).SetFloat64(v)
	dg := decimalDistance(gd, ge, rv)
	dw := decimalDistance(wd, we, rv)
	return dg != nil && dw != nil && dg.Cmp(dw) == 0
}

func roundTripsTo(s string, v float64, bits int) bool {
	p, err := strconv.ParseFloat(s, bits)
	if err != nil {
		return false
	}
	if bits == 32 {
		return float64(float32(p)) == v
	}
	return p == v
}

func splitDecimalNorm(s string) (digits string, exp int, ok bool) {
	i := strings.IndexByte(s, 'e')
	if i < 0 {
		return "", 0, false
	}
	e, err := strconv.Atoi(s[i+1:])
	if err != nil {
		return "", 0, false
	}
	return s[:i], e, true
}

func decimalDistance(digits string, exp int, rv *big.Rat) *big.Rat {
	n, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return nil
	}
	r := new(big.Rat).SetInt(n)
	p := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(abs(exp))), nil)
	if exp >= 0 {
		r.Mul(r, new(big.Rat).SetInt(p))
	} else {
		r.Quo(r, new(big.Rat).SetInt(p))
	}
	return r.Sub(r, rv).Abs(r)
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func TestShortestTieAdmitsOnlyExactTies(t *testing.T) {
	pow12 := math.Ldexp(1, -12)
	pow24 := math.Ldexp(1, -24)
	tiny := math.SmallestNonzeroFloat64

	for _, tc := range []struct {
		name      string
		got, want string
		v         float64
		bits      int
		accept    bool
	}{
		{"tie, round-half-to-even's side", "24414062e-11", "24414063e-11", pow12, 32, true},
		{"tie, strconv's side", "24414063e-11", "24414062e-11", pow12, 32, true},
		{"equidistant but one ULP short", "5960464477539062e-23", "5960464477539063e-23", pow24, 64, false},
		{"the side of that pair that parses back", "5960464477539063e-23", "5960464477539062e-23", pow24, 64, true},
		{"exact, but a digit longer", "244140625e-12", "24414063e-11", pow12, 32, false},
		{"parses back, but is not equidistant", "4e-324", "5e-324", tiny, 64, false},
		{"not a decimal at all", "?0.0001", "24414063e-11", pow12, 32, false},
	} {
		if got := shortestTie(tc.got, tc.want, tc.v, tc.bits); got != tc.accept {
			t.Errorf("%s: shortestTie(%q, %q, %v, %d) = %v, want %v",
				tc.name, tc.got, tc.want, tc.v, tc.bits, got, tc.accept)
		}
	}
}

func runFloatStrings(t *testing.T, body string, n int) []string {
	t.Helper()
	src := "import \"std/f64\";\nfunction main(): i32 {\n" + body + "    return 0;\n}\n"
	out, code := compileAndRunX86_64(t, src)
	if code != 0 {
		t.Fatalf("exit = %d\noutput:\n%s", code, out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != n {
		t.Fatalf("got %d lines, want %d\noutput:\n%s", len(lines), n, out)
	}
	return lines
}

func TestFloatShortestPowersOfTwoF64(t *testing.T) {
	var vals []float64
	for e := -1074; e <= 1023; e++ {
		v := math.Ldexp(1, e)
		if v != 0 && !math.IsInf(v, 0) {
			vals = append(vals, v)
		}
	}

	var b strings.Builder
	for _, v := range vals {
		fmt.Fprintf(&b, "    write(f64_from_bits(%d).to_string()); write(\"\\n\");\n",
			int64(math.Float64bits(v)))
	}
	lines := runFloatStrings(t, b.String(), len(vals))

	for i, v := range vals {
		got := lines[i]
		parsed, err := strconv.ParseFloat(got, 64)
		if err != nil {
			t.Errorf("2^%d: to_string=%q does not parse: %v", i-1074, got, err)
			continue
		}
		if parsed != v {
			t.Errorf("2^%d: to_string=%q parses back to %#x, want %#x — not round-trip",
				i-1074, got, math.Float64bits(parsed), math.Float64bits(v))
			continue
		}
		if want := decimalNorm(strconv.FormatFloat(v, 'e', -1, 64)); decimalNorm(got) != want &&
			!shortestTie(decimalNorm(got), want, v, 64) {
			t.Errorf("2^%d: to_string=%q normalises to %q, want %q",
				i-1074, got, decimalNorm(got), want)
		}
	}
}

func TestFloatShortestPowersOfTwoF32(t *testing.T) {
	var vals []float32
	for e := -149; e <= 127; e++ {
		v := float32(math.Ldexp(1, e))
		if v != 0 && !math.IsInf(float64(v), 0) {
			vals = append(vals, v)
		}
	}

	var b strings.Builder
	for _, v := range vals {
		fmt.Fprintf(&b, "    write(f32_from_bits(%d).to_string()); write(\"\\n\");\n",
			int32(math.Float32bits(v)))
	}
	lines := runFloatStrings(t, b.String(), len(vals))

	for i, v := range vals {
		got := lines[i]
		parsed, err := strconv.ParseFloat(got, 32)
		if err != nil {
			t.Errorf("2^%d (f32): to_string=%q does not parse: %v", i-149, got, err)
			continue
		}
		if float32(parsed) != v {
			t.Errorf("2^%d (f32): to_string=%q parses back to %#x, want %#x — not round-trip",
				i-149, got, math.Float32bits(float32(parsed)), math.Float32bits(v))
			continue
		}
		if want := decimalNorm(strconv.FormatFloat(float64(v), 'e', -1, 32)); decimalNorm(got) != want &&
			!shortestTie(decimalNorm(got), want, float64(v), 32) {
			t.Errorf("2^%d (f32): to_string=%q normalises to %q, want %q",
				i-149, got, decimalNorm(got), want)
		}
	}
}

func TestFloatShortestMatchesStrconvExactly(t *testing.T) {
	vals := []float64{
		0.1, 0.2, 0.3, 0.1 + 0.2, 1.0 / 3.0, 1.5, 2.25, 100, 123456.789,
		1e20, 1e21, 1e22, 1e23, 9.999999999999999e22,
		5e-324, 1e-323, 2.2250738585072014e-308,
		4.450147717014403e-308, 8.98846567431158e307,
		math.MaxFloat64, math.SmallestNonzeroFloat64,
		0.000125, 3.5, 42, 1, 10,
	}
	for e := -323; e <= 308; e++ {
		if v, err := strconv.ParseFloat(fmt.Sprintf("1e%d", e), 64); err == nil {
			vals = append(vals, v)
		}
	}
	for _, v := range append([]float64{}, vals...) {
		for _, w := range []float64{
			math.Nextafter(v, math.Inf(1)), math.Nextafter(v, math.Inf(-1)),
		} {
			if w > 0 && !math.IsInf(w, 0) {
				vals = append(vals, w)
			}
		}
	}
	r := rand.New(rand.NewSource(20260805))
	for len(vals) < 3000 {
		v := math.Float64frombits(r.Uint64())
		if v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0) {
			vals = append(vals, v)
		}
	}

	var b strings.Builder
	for _, v := range vals {
		fmt.Fprintf(&b, "    write(f64_from_bits(%d).to_string()); write(\"\\n\");\n",
			int64(math.Float64bits(v)))
	}
	lines := runFloatStrings(t, b.String(), len(vals))

	bad := 0
	for i, v := range vals {
		want := decimalNorm(strconv.FormatFloat(v, 'e', -1, 64))
		if got := decimalNorm(lines[i]); got != want && !shortestTie(got, want, v, 64) {
			bad++
			if bad <= 10 {
				t.Errorf("v=%v (%#x): to_string=%q normalises to %q, want %q",
					v, math.Float64bits(v), lines[i], got, want)
			}
		}
	}
	if bad > 10 {
		t.Errorf("... and %d more mismatches (%d total of %d)", bad-10, bad, len(vals))
	}
}
