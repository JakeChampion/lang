package e2e

import (
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

// decimalNorm reduces a formatted float to `sign + significand digits + "e" +
// decimal exponent`, so a comparison ignores notation entirely — Fern's
// fixed/scientific thresholds differ from Go's 'g'. "1.25e+02", "125e0" and
// "125" all normalise to "125e0". What survives is exactly what has to match:
// the digits and the value.
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

// runFloatStrings compiles a program that prints one `to_string()` per line for
// `n` values and returns the lines.
func runFloatStrings(t *testing.T, body string, n int) []string {
	t.Helper()
	src := "import \"std/float\";\nfunction main(): i32 {\n" + body + "    return 0;\n}\n"
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

// TestFloatShortestPowersOfTwoF64 pins the shorter-interval branch of the
// Dragonbox formatter. At a power of two the significand field is zero, so the
// gap below the value is half the gap above it and the rounding interval is
// ASYMMETRIC. The exact-bignum formatter this replaced tested every candidate
// against the symmetric interval (2m±1)*2^(e-1) regardless, so at a power of
// two it accepted a significand one digit too short: 2^-1019 formatted as
// "1.780059086805761e-307", which parses back to 18014398509481983 — one ULP
// BELOW the value being formatted. Every power of two must round-trip exactly
// and agree with Go's shortest digit for digit.
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
		if want := decimalNorm(strconv.FormatFloat(v, 'e', -1, 64)); decimalNorm(got) != want {
			t.Errorf("2^%d: to_string=%q normalises to %q, want %q",
				i-1074, got, decimalNorm(got), want)
		}
	}
}

// TestFloatShortestPowersOfTwoF32 is the binary32 sibling of
// TestFloatShortestPowersOfTwoF64, covering the same shorter-interval branch
// over the f32 exponent range.
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
		if want := decimalNorm(strconv.FormatFloat(float64(v), 'e', -1, 32)); decimalNorm(got) != want {
			t.Errorf("2^%d (f32): to_string=%q normalises to %q, want %q",
				i-149, got, decimalNorm(got), want)
		}
	}
}

// TestFloatShortestMatchesStrconvExactly is the stronger form of
// TestFloatShortestRoundTrip. Dragonbox is correctly rounded and produces the
// UNIQUE shortest representation, so the digits must equal Go's shortest
// exactly — not merely be no longer than them, which is all the round-trip
// tests could assert while the formatter was approximate. Covers the boundary
// classes each Dragonbox branch handles: subnormals, the normal/subnormal
// seam, powers of ten, the extremes, and a random spread.
func TestFloatShortestMatchesStrconvExactly(t *testing.T) {
	vals := []float64{
		0.1, 0.2, 0.3, 0.1 + 0.2, 1.0 / 3.0, 1.5, 2.25, 100, 123456.789,
		1e20, 1e21, 1e22, 1e23, 9.999999999999999e22,
		5e-324, 1e-323, 2.2250738585072014e-308, // subnormal / normal seam
		4.450147717014403e-308, 8.98846567431158e307,
		math.MaxFloat64, math.SmallestNonzeroFloat64,
		0.000125, 3.5, 42, 1, 10,
	}
	for e := -323; e <= 308; e++ {
		if v, err := strconv.ParseFloat(fmt.Sprintf("1e%d", e), 64); err == nil {
			vals = append(vals, v)
		}
	}
	// Neighbours of every value above exercise the ties and the
	// even/odd-significand interval switch.
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
		if got := decimalNorm(lines[i]); got != want {
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
