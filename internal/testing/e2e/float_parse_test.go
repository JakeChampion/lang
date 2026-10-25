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

func TestParseFloatCorrectlyRounded(t *testing.T) {
	edge := []string{
		"0.30000000000000004",
		"1.5", "0.1", "0.2", "0.3", "3.141592653589793", "2.718281828459045",
		"2.2250738585072014e-308",
		"1.7976931348623157e308",
		"5e-324", "4.9e-324",
		"9.999999999999999e22", "1e23",
		"123456789.123456789", "6.022e23", "1.602176634e-19",
		"1000000000000000000000", "0.0001", "42", "0", "0.5", "2.5", "0.025",
	}
	edge = append(edge,
		"2.2250738585072011e-308",
		"2.2250738585072012e-308",
		"2.2250738585072013e-308",
		"9007199254740993", "9007199254740992", "9007199254740991",
		"1.0000000000000000000000000000000000000001",
		"123456789012345678901234567890",
		"1e22", "1e-22", "2.4703282292062327e-324",
	)
	edge = append(edge,
		"1234567890123456789", "12345678901234567890", "12345678901234567891",
		"1.2345678901234567890000000000", "100000000000000000000000000000",
		"0.000000000000000000001234567890123456789", "1234567890123456789e-30",
		"9999999999999999999", "18446744073709551615", "18446744073709551616",
		"1.", ".5", "-0.0001",
	)
	vals := append([]string{}, edge...)

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
		var s string
		if r.Intn(2) == 0 {
			s = strconv.FormatFloat(v, 'g', -1, 64)
		} else {
			s = strconv.FormatFloat(v, 'e', r.Intn(20), 64)
		}
		vals = append(vals, s)
	}

	var b strings.Builder
	b.WriteString("import \"std/f64\";\nimport \"std/string\";\nfunction main(): i32 {\n")
	for _, s := range vals {
		b.WriteString(fmt.Sprintf("    match (f64.parse_f64(%q)) {\n", s))
		b.WriteString("        Ok(v) => { write(f64_bits(v).to_string()); write(\"\\n\"); },\n")
		b.WriteString("        Err(_) => { write(\"None\\n\"); },\n    }\n")
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

func TestFloatStringRoundTripIsExactIdentity(t *testing.T) {
	vals := []float64{
		0.1, 1.0 / 3.0, 1.5, 0.1 + 0.2, 5e-324, 2.2250738585072014e-308,
		math.MaxFloat64, math.SmallestNonzeroFloat64, 1e21, 1e22, 1e23,
		-0.25, -123456.789, 42, 0.000125, 9.999999999999999e22,
	}
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
	b.WriteString("import \"std/f64\";\nimport \"std/string\";\nimport \"std/i64\";\n")
	b.WriteString("function main(): i32 {\n    let bad: i32 = 0;\n")
	for _, v := range vals {
		bits := int64(math.Float64bits(v))
		fmt.Fprintf(&b, "    match (f64.parse_f64((f64_from_bits(%d).to_string()))) { Ok(x) => { if (f64_bits(x) != %d) { bad = bad + 1; write(\"%d -> \"); write(f64_bits(x).to_string()); write(\"\\n\"); } }, Err(_) => { bad = bad + 1; write(\"%d -> None\\n\"); } }\n",
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

var eiselLemireFallbackInputs = []string{
	"4.4503711836601074411763531681463e-296",
	"3.36302169273979401078e-166",
	"1.47285417837035957993593e-286",
	"9.565069466618366712741110144771814372e221",
	"1.141268837741204946519519578962597028451340e64",
	"3.874187492813244237443348798714779e244",
	"5.10138240580919706169586572034706818814e205",
	"4.8869072634591218364450729e-12",
	"4.36774107033378682824525868889962571015865e-170",
	"1.3521474617058375324272254e50",
	"8.3262956204380971506040e-201",
	"1.2953469714866854880915224074720591739e76",

	"1.00000000000000011102230246251565404236316680908203125",
	"7.4109846876186981626485318930233205854758970392148714663837852375101326090531312779794975454245398856969484704316857659638998506553390969459816219401617281718945106978546710679176872575177347315553308e-324",
}

func TestParseFloatEiselLemireFallback(t *testing.T) {
	var b strings.Builder
	b.WriteString("import \"std/f64\";\nimport \"std/string\";\nimport \"std/i64\";\nfunction main(): i32 {\n")
	for _, s := range eiselLemireFallbackInputs {
		fmt.Fprintf(&b, "    match (f64.parse_f64((%q))) { Ok(x) => { write(f64_bits(x).to_string()); }, Err(_) => { write(\"NONE\"); } } write(\"\\n\");\n", s)
	}
	b.WriteString("    return 0;\n}\n")

	out, code := compileAndRunX86_64(t, b.String())
	if code != 0 {
		t.Fatalf("exit = %d\noutput:\n%s", code, out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != len(eiselLemireFallbackInputs) {
		t.Fatalf("got %d lines, want %d\n%s", len(lines), len(eiselLemireFallbackInputs), out)
	}
	for i, s := range eiselLemireFallbackInputs {
		want, err := strconv.ParseFloat(s, 64)
		if err != nil {
			t.Fatalf("test corpus bug: strconv rejects %q: %v", s, err)
		}
		gotBits, err := strconv.ParseInt(lines[i], 10, 64)
		if err != nil {
			t.Errorf("%q: parse_float printed %q", s, lines[i])
			continue
		}
		if uint64(gotBits) != math.Float64bits(want) {
			t.Errorf("%q: parse_float = %#016x (%v), strconv = %#016x (%v)",
				s, uint64(gotBits), math.Float64frombits(uint64(gotBits)), math.Float64bits(want), want)
		}
	}
}
