package e2e

import (
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

// TestFloatShortestF32RoundTrip is the f32 sibling of
// TestFloatShortestRoundTrip: whatever `(v: f32).to_string()` produces must
// parse back (as f32) to the exact same f32, and use no more significant
// digits than Go's shortest f32 form. f32 `.to_string()` shares the shortest-
// digit search with f64 (only the IEEE field widths differ), capped at 9
// digits (the max shortest length of an f32). Notation-agnostic — the
// invariant is round-trip + minimality, not a byte-identical string.
func TestFloatShortestF32RoundTrip(t *testing.T) {
	edge := []float32{
		0.1, 1.0 / 3.0, 3.14, 42.0, 1.5, 0.5, 2.5, 123456.79,
		1e20, 1e-20, 1e-45, 1.4e-45, // smallest subnormal region
		3.4028235e38,  // max finite f32
		1.1754944e-38, // smallest normal f32
		0.000125, 9999999.0,
	}
	vals := append([]float32{}, edge...)
	r := rand.New(rand.NewSource(20260724))
	for len(vals) < 90 {
		v := math.Float32frombits(r.Uint32())
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) || v <= 0 {
			continue
		}
		vals = append(vals, v)
	}

	var b strings.Builder
	b.WriteString("import \"std/float\";\nfunction main(): i32 {\n")
	for _, v := range vals {
		bi := int32(math.Float32bits(v))
		b.WriteString(fmt.Sprintf("    write(f32_from_bits(%d).to_string()); write(\"\\n\");\n", bi))
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
		parsed, err := strconv.ParseFloat(got, 32)
		if err != nil {
			t.Errorf("v=%v: to_string=%q does not parse: %v", v, got, err)
			continue
		}
		if float32(parsed) != v {
			t.Errorf("v=%v (%#x): to_string=%q parses back to %v (%#x) — not round-trip",
				v, math.Float32bits(v), got, float32(parsed), math.Float32bits(float32(parsed)))
		}
		wantSig := sigDigits(strconv.FormatFloat(float64(v), 'g', -1, 32))
		if gotSig := sigDigits(got); gotSig > wantSig {
			t.Errorf("v=%v: to_string=%q has %d sig digits, Go shortest %q has %d",
				v, got, gotSig, strconv.FormatFloat(float64(v), 'g', -1, 32), wantSig)
		}
	}
}
