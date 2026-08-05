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

// exactMidpoint renders the exact decimal value halfway between `v` and the
// next double above it. Both are dyadic rationals, so the midpoint terminates
// in decimal and can be written out in full. These are the inputs that force
// round-half-to-even: a parser that approximates rather than comparing the
// digits exactly picks the wrong neighbour. Returns false when `v` has no
// finite successor or Go's own parser doesn't land on one of the two.
func exactMidpoint(v float64) (string, bool) {
	w := math.Nextafter(v, math.Inf(1))
	if v <= 0 || math.IsInf(w, 0) {
		return "", false
	}
	rv, rw := new(big.Rat).SetFloat64(v), new(big.Rat).SetFloat64(w)
	if rv == nil || rw == nil {
		return "", false
	}
	mid := new(big.Rat).Quo(new(big.Rat).Add(rv, rw), big.NewRat(2, 1))
	// A midpoint of adjacent doubles needs at most ~1080 fractional digits
	// anywhere in the exponent range; trim the padding so the string is the
	// exact value rather than a rounded rendering of it.
	s := mid.FloatString(1100)
	if strings.Contains(s, ".") {
		s = strings.TrimSuffix(strings.TrimRight(s, "0"), ".")
	}
	got, err := strconv.ParseFloat(s, 64)
	if err != nil || (got != v && got != w) {
		return "", false
	}
	return s, true
}

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
	// The classic strtod near-halfway cases and the 2^53 integer seam, where
	// an approximate parser picks the wrong neighbour.
	edge = append(edge,
		"2.2250738585072011e-308", // Java/PHP strtod rounding bug
		"2.2250738585072012e-308",
		"2.2250738585072013e-308",
		"9007199254740993", "9007199254740992", "9007199254740991",
		"1.0000000000000000000000000000000000000001",
		"123456789012345678901234567890",
		"1e22", "1e-22", "2.4703282292062327e-324",
	)
	vals := append([]string{}, edge...)

	// Exact decimal midpoints between adjacent doubles: written out in full,
	// these sit precisely on the rounding boundary, so getting them right
	// requires comparing the digits exactly rather than approximating. They
	// are the strongest available evidence that the parser is correctly
	// rounded, and the corpus above had none.
	for e := -1060; e <= 1020; e += 29 {
		if s, ok := exactMidpoint(math.Ldexp(1, e)); ok {
			vals = append(vals, s)
		}
	}

	r := rand.New(rand.NewSource(20260724))
	for i := 0; i < 60; i++ {
		v := math.Float64frombits(r.Uint64())
		if v <= 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		if s, ok := exactMidpoint(v); ok {
			vals = append(vals, s)
		}
	}
	for len(vals) < 260 {
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

// TestFloatStringRoundTripIsExactIdentity pins the contract that falls out of
// combining the two halves: `to_string` emits the shortest decimal that
// round-trips, and `parse_float` is correctly rounded, so composing them is
// the EXACT identity on every finite value — not a tolerance. This is what
// user code relies on when it compares a parsed value with `==`, and nothing
// tested it while docs/FLOAT-SEMANTICS.md still claimed the round-trip held
// only "within a small relative tolerance".
func TestFloatStringRoundTripIsExactIdentity(t *testing.T) {
	vals := []float64{
		0.1, 1.0 / 3.0, 1.5, 0.1 + 0.2, 5e-324, 2.2250738585072014e-308,
		math.MaxFloat64, math.SmallestNonzeroFloat64, 1e21, 1e22, 1e23,
		-0.25, -123456.789, 42, 0.000125, 9.999999999999999e22,
	}
	// Powers of two exercise the formatter's shorter-interval branch.
	for e := -1074; e <= 1023; e += 7 {
		vals = append(vals, math.Ldexp(1, e))
	}
	r := rand.New(rand.NewSource(20260806))
	for len(vals) < 500 {
		v := math.Float64frombits(r.Uint64())
		if math.IsNaN(v) || math.IsInf(v, 0) || v == 0 {
			continue
		}
		vals = append(vals, v)
	}

	var b strings.Builder
	b.WriteString("import \"std/float\";\nimport \"std/string\";\nimport \"std/i64\";\n")
	b.WriteString("function main(): i32 {\n    var bad: i32 = 0;\n")
	for _, v := range vals {
		bits := int64(math.Float64bits(v))
		fmt.Fprintf(&b, "    match ((f64_from_bits(%d).to_string()).parse_float()) { Some(x) => { if (f64_bits(x) != %d) { bad = bad + 1; write(\"%d -> \"); write(f64_bits(x).to_string()); write(\"\\n\"); } }, None => { bad = bad + 1; write(\"%d -> None\\n\"); } }\n",
			bits, bits, bits, bits)
	}
	b.WriteString("    write(\"failures=\"); write(bad.to_string()); write(\"\\n\");\n    return 0;\n}\n")

	out, code := compileAndRunX86_64(t, b.String())
	if code != 0 {
		t.Fatalf("exit = %d\noutput:\n%s", code, out)
	}
	if !strings.Contains(out, "failures=0\n") {
		t.Errorf("parse_float ∘ to_string is not the identity over %d values:\n%s", len(vals), out)
	}
}
