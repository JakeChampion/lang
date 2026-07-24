package e2e

import (
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

// TestParseFloatCorrectlyRounded is the correctness guard for the
// correctly-rounded decimal→f64 parser (#5566): `s.parse_float()` must
// return the f64 *nearest* the decimal value (round-half-to-even), bit-exact
// with Go's strconv.ParseFloat. The old parser applied the decimal exponent
// in the f64 domain (`× 10^exp`), drifting up to ~6 ULP at the exponent
// extremes; this pins the fix over edge cases + a seeded-random spread of
// shortest and high-precision decimal forms.
func TestParseFloatCorrectlyRounded(t *testing.T) {
	edge := []string{
		"0.30000000000000004", // 0.1 + 0.2, the headline #5536/#5566 case
		"1.5", "0.1", "0.2", "0.3", "3.141592653589793", "2.718281828459045",
		"2.2250738585072014e-308", // smallest normal
		"1.7976931348623157e308",  // max finite
		"5e-324", "4.9e-324",      // smallest subnormal / round-to-it
		"9.999999999999999e22", "1e23", // round-half-to-even boundary neighbours
		"123456789.123456789", "6.022e23", "1.602176634e-19",
		"1000000000000000000000", "0.0001", "42", "0", "0.5", "2.5", "0.025",
	}
	vals := append([]string{}, edge...)

	r := rand.New(rand.NewSource(20260724))
	for len(vals) < 120 {
		v := math.Float64frombits(r.Uint64())
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			continue
		}
		// Mix shortest 'g' forms with high-precision 'e' forms (up to 20
		// fraction digits) to stress the near-midpoint slow path.
		var s string
		if r.Intn(2) == 0 {
			s = strconv.FormatFloat(v, 'g', -1, 64)
		} else {
			s = strconv.FormatFloat(v, 'e', r.Intn(20), 64)
		}
		vals = append(vals, s)
	}

	var b strings.Builder
	b.WriteString("import \"std/string\";\nfunction main(): i32 {\n")
	for _, s := range vals {
		b.WriteString(fmt.Sprintf("    match (%q.parse_float()) {\n", s))
		b.WriteString("        Some(v) => { write(f64_bits(v).to_string()); write(\"\\n\"); },\n")
		b.WriteString("        None => { write(\"None\\n\"); },\n    }\n")
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
	for i, s := range vals {
		want, err := strconv.ParseFloat(s, 64)
		if err != nil {
			t.Fatalf("strconv could not parse test input %q: %v", s, err)
		}
		wantBits := int64(math.Float64bits(want))
		gotBits, err := strconv.ParseInt(strings.TrimSpace(lines[i]), 10, 64)
		if err != nil {
			t.Errorf("%q: parse_float printed %q, not an i64: %v", s, lines[i], err)
			continue
		}
		if gotBits != wantBits {
			t.Errorf("%q: parse_float bits=%d (%v), want %d (%v) — not correctly rounded",
				s, gotBits, math.Float64frombits(uint64(gotBits)), wantBits, want)
		}
	}
}
