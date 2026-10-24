package e2e

import (
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

func TestFloatShortestRoundTrip(t *testing.T) {
	edge := []float64{
		0.1 + 0.2, 1.0 / 3.0, 1.5, 0.1, 2.25, 100.0, 123456.789,
		1e20, 1.5e30, 1e-300, 1e300, 5e-324, 1.7976931348623157e308,
		2.2250738585072014e-308, 9.999999999999999e22, 42.0, 3.5, 0.000125,
	}
	vals := append([]float64{}, edge...)
	r := rand.New(rand.NewSource(20260724))
	for len(vals) < 80 {
		v := math.Float64frombits(r.Uint64())
		if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
			continue
		}
		vals = append(vals, v)
	}

	var b strings.Builder
	b.WriteString("import \"std/f64\";\nfunction main(): i32 {\n")
	for _, v := range vals {
		bi := int64(math.Float64bits(v))
		b.WriteString(fmt.Sprintf("    write(f64_from_bits(%d).to_string()); write(\"\\n\");\n", bi))
	}
	b.WriteString("    return 0;\n}\n")

	out, code := compileAndRunX86_64(t, b.String())
	if code != 0 {
		t.Fatalf("exit = %d\noutput:\n%s", code, out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != len(vals) {
		t.Fatalf("got %d lines, want %d\noutput:\n%s", len(lines), len(vals), out)
	}
	for i, v := range vals {
		got := lines[i]
		parsed, err := strconv.ParseFloat(got, 64)
		if err != nil {
			t.Errorf("v=%v: to_string=%q does not parse: %v", v, got, err)
			continue
		}
		if parsed != v {
			t.Errorf("v=%v (%#x): to_string=%q parses back to %v (%#x) — not round-trip",
				v, math.Float64bits(v), got, parsed, math.Float64bits(parsed))
		}
		wantSig := sigDigits(strconv.FormatFloat(v, 'g', -1, 64))
		if gotSig := sigDigits(got); gotSig > wantSig {
			t.Errorf("v=%v: to_string=%q has %d sig digits, Go shortest %q has %d",
				v, got, gotSig, strconv.FormatFloat(v, 'g', -1, 64), wantSig)
		}
	}
}

func sigDigits(s string) int {
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimPrefix(s, "-")
	s = strings.ReplaceAll(s, ".", "")
	s = strings.Trim(s, "0")
	return len(s)
}
