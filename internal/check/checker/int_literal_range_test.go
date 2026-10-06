package checker

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/constfold"
	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/diag"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

// The most negative number of a width had no literal spelling. A NumberLit
// carries only the magnitude — the sign is a separate unary node — so the
// range check judged `-2147483648` as the out-of-range POSITIVE 2147483648
// and refused it. `std/math`'s own `i32_min` is written `0 - 2147483647 - 1`
// for exactly this reason, and `i64` never showed the fault because its
// range check returns early.
func TestIntLiteralRangeCountsTheSign(t *testing.T) {
	cases := []struct {
		src      string
		accepted bool
		// When rejected, the message must quote the value as WRITTEN. It used
		// to print the magnitude, so `-2147483649` was reported as a problem
		// with `2147483649` — a number not in the source.
		wantInMsg string
	}{
		{"let x: i32 = -2147483648;", true, ""},
		{"let x: i32 = 2147483647;", true, ""},
		{"let x: i32 = -1;", true, ""},
		{"let x: i32 = 0;", true, ""},
		// Double negation is a positive value again.
		{"let x: i32 = --5;", true, ""},
		{"let x: i32 = -2147483649;", false, "-2147483649"},
		{"let x: i32 = 2147483648;", false, "2147483648"},
		{"let x: i32 = -4000000000;", false, "-4000000000"},
		// Negating the smallest value overflows in the other direction, and
		// the two minuses cancel back to a positive out-of-range magnitude.
		{"let x: i32 = -(-2147483648);", false, "2147483648"},
	}
	for _, c := range cases {
		err := checkSource(t, "function main(): i32 { "+c.src+" return 0; }")
		if c.accepted {
			if err != nil {
				t.Errorf("%s: rejected, want accepted: %v", c.src, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: accepted, want rejected", c.src)
			continue
		}
		if !strings.Contains(err.Error(), "does not fit") {
			t.Errorf("%s: wrong error: %v", c.src, err)
			continue
		}
		if !strings.Contains(err.Error(), c.wantInMsg) {
			t.Errorf("%s: message does not quote %q as written: %v", c.src, c.wantInMsg, err)
		}
	}
}

// The sign is tracked through every position a literal can settle in, not
// only a var initialiser — an argument, a return, an array element and a
// struct field all reach the same check by different routes.
func TestIntLiteralRangeInEveryPosition(t *testing.T) {
	sources := []string{
		`struct S { v: i32 }
function take(v: i32): i32 { return v; }
function main(): i32 { return take(-2147483648); }`,
		`function smallest(): i32 { return -2147483648; }
function main(): i32 { return smallest() + 1; }`,
		`function main(): i32 { let a: i32[] = [-2147483648, 0]; return a[1]; }`,
		`struct S { v: i32 }
function main(): i32 { let s: S = S { v: -2147483648 }; return s.v + 1; }`,
	}
	for i, src := range sources {
		if err := checkSource(t, src); err != nil {
			t.Errorf("position %d rejected the smallest i32: %v", i, err)
		}
	}
}

// The unsigned side was left open when the signed range check landed ("a
// negative literal there wraps today, which is a separate question"). It did
// not wrap consistently: the sign lives on the enclosing unary and the check
// tested the magnitude alone, so `let a: u8 = -1` was accepted and the
// natives stored 0xFFFFFFFF into a u8 slot while the interpreter read 255
// (#8448). A negative literal has no unsigned reading, so it is rejected.
func TestIntLiteralRangeRejectsNegativeUnsigned(t *testing.T) {
	rejected := []string{
		`let x: u8 = -1;`,
		`let x: u32 = -5;`,
		`let x: u64 = -1;`,
		`let x: usize = -1;`,
		`let x: u32 = -2147483649;`,
	}
	for _, src := range rejected {
		err := checkSource(t, "function main(): i32 { "+src+" return 0; }")
		if err == nil {
			t.Errorf("%s: accepted; a negative literal has no unsigned reading", src)
			continue
		}
		if !strings.Contains(err.Error(), "does not fit") {
			t.Errorf("%s: wrong error: %v", src, err)
		}
	}
	accepted := []string{
		`let x: u8 = 0;`,
		`let x: u8 = 255;`,
		`let x: u32 = 4294967295;`,
		`let x: u64 = 18446744073709551615;`,
		// Zero is spelled with a sign in generated code often enough to
		// matter, and negating it is still zero.
		`let x: u32 = -0;`,
	}
	for _, src := range accepted {
		if err := checkSource(t, "function main(): i32 { "+src+" return 0; }"); err != nil {
			t.Errorf("%s: rejected, want accepted: %v", src, err)
		}
	}
}

// The 64-bit widths returned early from the range check, so a literal at
// 2^63 wrapped to i64 MIN with no diagnostic — the parser deferred to the
// checker and the checker deferred to nobody (#8449). Past i64 max the
// literal's Value holds a wrapped bit pattern, so the message has to quote
// the magnitude the source actually wrote.
func TestIntLiteral64BitRange(t *testing.T) {
	cases := []struct {
		src       string
		accepted  bool
		wantInMsg string
	}{
		{`let x: i64 = 9223372036854775807;`, true, ""},
		{`let x: i64 = -9223372036854775808;`, true, ""},
		{`let x: u64 = 18446744073709551615;`, true, ""},
		{`let x: i64 = 9223372036854775808;`, false, "9223372036854775808"},
		{`let x: i64 = 18446744073709551615;`, false, "18446744073709551615"},
		{`let x: i32 = 9223372036854775808;`, false, "9223372036854775808"},
	}
	for _, c := range cases {
		err := checkSource(t, "function main(): i32 { "+c.src+" return 0; }")
		if c.accepted {
			if err != nil {
				t.Errorf("%s: rejected, want accepted: %v", c.src, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: accepted, want rejected", c.src)
			continue
		}
		if !strings.Contains(err.Error(), c.wantInMsg) {
			t.Errorf("%s: message does not quote %q as written: %v", c.src, c.wantInMsg, err)
		}
	}
}

// A hex literal with the top bit set was rejected outright while the same
// value in decimal parsed (#8417): the hex path had no unsigned retry. Both
// spellings reach the same range rules now.
func TestHexLiteralTopBitSet(t *testing.T) {
	accepted := []string{
		`let x: u64 = 0xFFFFFFFFFFFFFFFF;`,
		`let x: u64 = 0x8000000000000000;`,
		`let x: i64 = 0x7FFFFFFFFFFFFFFF;`,
		`let x: u32 = 0xFFFFFFFF;`,
	}
	for _, src := range accepted {
		if err := checkSource(t, "function main(): i32 { "+src+" return 0; }"); err != nil {
			t.Errorf("%s: rejected, want accepted: %v", src, err)
		}
	}
	// The same value that overflows i64 is still refused for a signed slot.
	if err := checkSource(t, `function main(): i32 { let x: i64 = 0xFFFFFFFFFFFFFFFF; return 0; }`); err == nil {
		t.Error("0xFFFFFFFFFFFFFFFF accepted for i64; it exceeds the signed range")
	}
}

// checkErrors runs the checker and returns every diagnostic it recorded, so a
// test can assert the code and the position and not only the words.
func checkErrors(t *testing.T, src string) []*Error {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, err = Check(prog)
	if err == nil {
		return nil
	}
	es, ok := err.(diag.Errors)
	if !ok {
		t.Fatalf("checker returned %T, want diag.Errors: %v", err, err)
	}
	var out []*Error
	for _, e := range es {
		ce, ok := e.(*Error)
		if !ok {
			t.Fatalf("diagnostic %T is not a checker Error: %v", e, e)
		}
		out = append(out, ce)
	}
	return out
}

// A literal no 64-bit type can hold used to die in the parser as P002, quoting
// strconv's own words. The parser keeps it now (ExceedsU64) and the checker
// refuses it as E047 against the type the context asked for, at the literal's
// own column — the report every other over-range literal gets (#8563). With no
// context to name, the refusal names the type the literal would otherwise
// default to: i64 for an unannotated binding (the #3676 widening, which reads
// through the binding's arithmetic and its tuple / array elements too), i32
// anywhere else.
func TestIntLiteralPastU64IsE047(t *testing.T) {
	const lit = "18446744073709551616"
	cases := []struct {
		src     string
		wantLit string
		wantIn  string
		wantCol int
	}{
		{`let a: u64 = ` + lit + `;`, lit, "u64", 37},
		{`let a: i64 = ` + lit + `;`, lit, "i64", 37},
		{`let a: i32 = ` + lit + `;`, lit, "i32", 37},
		{`let a: u8 = ` + lit + `;`, lit, "u8", 36},
		{`let a = ` + lit + `;`, lit, "i64", 32},
		{`let a = -` + lit + `;`, "-" + lit, "i64", 33},
		{`let b = ` + lit + ` + 1;`, lit, "i64", 32},
		{`let a = ` + lit + ` as u64;`, lit, "u64", 32},
		// Typed by its suffix, so it never settles: judged against that type.
		{`let a = ` + lit + `u64;`, lit, "u64", 32},
		// A comparison of two literals settles them itself, at the i64 the
		// wide one selects (#8668).
		{`let b: boolean = ` + lit + ` > 1;`, lit, "i64", 41},
		// A composite init settles its elements at the same i64 the wide
		// literal selects for a scalar one (#8722).
		{`let t = (` + lit + `, 1);`, lit, "i64", 33},
		{`let xs = [` + lit + `];`, lit, "i64", 34},
		// Hex is quoted as written.
		{`let a: u64 = 0xFFFFFFFFFFFFFFFFFFFF;`, "0xFFFFFFFFFFFFFFFFFFFF", "u64", 37},
		// Float context has no integer to promote.
		{`let f: f64 = ` + lit + `;`, lit, "any integer type", 37},
	}
	for _, c := range cases {
		errs := checkErrors(t, "function main(): i32 { "+c.src+" return 0; }")
		if len(errs) != 1 {
			t.Errorf("%s: %d diagnostics, want exactly one: %v", c.src, len(errs), errs)
			continue
		}
		e := errs[0]
		if e.ErrCode != "E047" {
			t.Errorf("%s: code %q, want E047", c.src, e.ErrCode)
		}
		if e.Pos.Line != 1 || e.Pos.Col != c.wantCol {
			t.Errorf("%s: reported at %d:%d, want the literal at 1:%d", c.src, e.Pos.Line, e.Pos.Col, c.wantCol)
		}
		want := "literal " + c.wantLit + " does not fit in " + c.wantIn
		if !strings.Contains(e.Msg, want) {
			t.Errorf("%s: message %q does not contain %q", c.src, e.Msg, want)
		}
		if strings.Contains(e.Msg, "strconv") {
			t.Errorf("%s: message leaks a Go library name: %q", c.src, e.Msg)
		}
	}
}

// A literal past i64 max fits only u64 (or i64 as its minimum), so it is valid
// only where a context settles it. One left unsettled — a tuple or array
// element — lowered as the i32 default and wrapped silently (the #8449
// family). An unannotated binding's arithmetic, a comparison of two literals
// (#8668) and a tuple / array element (#8722) now settle themselves at the i64
// default a wide literal selects, so the literal is judged there; one nothing
// settles at all is refused against the i32 default, and a settled one is
// untouched.
func TestWideIntLiteralLeftUnsettledIsE047(t *testing.T) {
	rejected := []struct {
		src     string
		wantCol int
		wantIn  string
	}{
		{`let b = 9223372036854775808 + 1;`, 32, "i64"},
		{`let t = (9223372036854775808, 1);`, 33, "i64"},
		{`let xs = [18446744073709551615];`, 34, "i64"},
		{`let b: boolean = 9223372036854775808 > 1;`, 41, "i64"},
	}
	for _, c := range rejected {
		errs := checkErrors(t, "function main(): i32 { "+c.src+" return 0; }")
		if len(errs) != 1 {
			t.Errorf("%s: %d diagnostics, want exactly one: %v", c.src, len(errs), errs)
			continue
		}
		e := errs[0]
		if e.ErrCode != "E047" || e.Pos.Col != c.wantCol || !strings.Contains(e.Msg, "does not fit in "+c.wantIn) {
			t.Errorf("%s: got %s at %d:%d %q, want E047 at 1:%d naming %s", c.src, e.ErrCode, e.Pos.Line, e.Pos.Col, e.Msg, c.wantCol, c.wantIn)
		}
	}
	accepted := []string{
		`let q: i64 = -9223372036854775808; let t = (q, 1);`,
		`let u: u64 = 18446744073709551615; let t = (u, 1);`,
		`let u: u64 = 18446744073709551615 - 1;`,
		`let f: f64 = 9223372036854775808;`,
		`let big = 9223372036854775807;`,
	}
	for _, src := range accepted {
		if errs := checkErrors(t, "function main(): i32 { "+src+" return 0; }"); len(errs) != 0 {
			t.Errorf("%s: rejected, want accepted: %v", src, errs)
		}
	}
}

// constfold substitutes an untyped negative const as a literal whose Value
// carries the sign (`const NEG = -5` arrives as a NumberLit holding -5, with
// no width). Read as a magnitude that is 2^64-5: `let x = NEG` widened to i64
// and `let y: i32 = NEG` drew E047. The folded sign is judged as a sign.
func TestFoldedNegativeConstIsJudgedBySign(t *testing.T) {
	src := `const NEG = -5;
const MIN = 0 - 2147483647 - 1;
function main(): i32 {
	let x = NEG;
	let y: i32 = NEG;
	let m: i32 = MIN;
	let q: i64 = NEG;
	if (x == y && y == 0 - 5 && m < 0 && q < 0) { return 0; }
	return 1;
}`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("fold: %v", err)
	}
	info, err := Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	for v, ty := range info.VarTypes {
		if v.Name == "x" {
			if n, ok := ty.(ast.NumberType); !ok || n.NormalWidth() != 32 {
				t.Errorf("let x = NEG has type %v, want the i32 default: a folded -5 is in i32 range", ty)
			}
		}
	}
	// The unsigned refusal still applies to the folded value.
	prog, err = parser.Parse("const NEG = -5;\nfunction main(): i32 { let z: u8 = NEG; return 0; }")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("fold: %v", err)
	}
	_, err = Check(prog)
	if err == nil || !strings.Contains(err.Error(), "literal -5 does not fit in u8") {
		t.Errorf("let z: u8 = NEG: got %v, want E047 naming -5", err)
	}
}

// A typed suffix pins the literal's width in the parser, so no settling hint
// ever reaches it — and the range rule, which lived on the settle path, judged
// nothing. `300u8` type-checked and reached codegen wrapped, where the same
// literal without its suffix is E047 (#8639). The sign still comes from the
// enclosing unary, so the most negative value of each signed width keeps its
// only spelling.
func TestSuffixedIntLiteralRange(t *testing.T) {
	cases := []struct {
		src       string
		accepted  bool
		wantInMsg string
	}{
		// In range at the boundary, in every suffix the language has.
		{`let x: u8 = 255u8;`, true, ""},
		{`let x: u8 = 0u8;`, true, ""},
		{`let x: u32 = 4294967295u32;`, true, ""},
		{`let x: u64 = 18446744073709551615u64;`, true, ""},
		{`let x: i32 = 2147483647i32;`, true, ""},
		{`let x: i32 = -2147483648i32;`, true, ""},
		{`let x: i64 = 9223372036854775807i64;`, true, ""},
		{`let x: i64 = -9223372036854775808i64;`, true, ""},
		// Two minuses cancel, so the magnitude is judged as positive again.
		{`let x: i64 = - -9223372036854775807i64;`, true, ""},
		// Zero has no sign, so a written one is not an unsigned negative.
		{`let x: u32 = -0u32;`, true, ""},
		// Hex spellings reach the same bounds.
		{`let x: u32 = 0xFFFFFFFFu32;`, true, ""},
		{`let x: i64 = -0x8000000000000000i64;`, true, ""},
		// One past each bound.
		{`let x: u8 = 300u8;`, false, "literal 300 does not fit in u8"},
		{`let x: u8 = 256u8;`, false, "literal 256 does not fit in u8"},
		{`let x: u32 = 4294967296u32;`, false, "literal 4294967296 does not fit in u32"},
		{`let x: i32 = 2147483648i32;`, false, "literal 2147483648 does not fit in i32"},
		{`let x: i64 = 9223372036854775808i64;`, false, "literal 9223372036854775808 does not fit in i64"},
		{`let x: u32 = 0x100000000u32;`, false, "literal 0x100000000 does not fit in u32"},
		// The negated side of each signed width is one further down, and one
		// past THAT is still refused, quoted as written.
		{`let x: i32 = -2147483649i32;`, false, "literal -2147483649 does not fit in i32"},
		{`let x: i64 = -9223372036854775809i64;`, false, "literal -9223372036854775809 does not fit in i64"},
		// A negative literal has no unsigned reading, suffix or not.
		{`let x: u8 = -1u8;`, false, "unsigned types have no negative values"},
		{`let x: u64 = -1u64;`, false, "unsigned types have no negative values"},
		// Double negation is positive, so this one is out of range as written.
		{`let x: i64 = - -9223372036854775808i64;`, false, "literal 9223372036854775808 does not fit in i64"},
	}
	for _, c := range cases {
		err := checkSource(t, "function main(): i32 { "+c.src+" return 0; }")
		if c.accepted {
			if err != nil {
				t.Errorf("%s: rejected, want accepted: %v", c.src, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: accepted, want rejected", c.src)
			continue
		}
		if !strings.Contains(err.Error(), c.wantInMsg) {
			t.Errorf("%s: message %v does not contain %q", c.src, err, c.wantInMsg)
		}
	}
}

// The suffix rule has to reach every position a literal can be written in, not
// only a let initialiser: nothing settles a suffixed literal, so there is no
// settle path to inherit the coverage from.
func TestSuffixedIntLiteralRangeInEveryPosition(t *testing.T) {
	rejected := []string{
		`function take(v: u8): i32 { return 0; }
function main(): i32 { return take(300u8); }`,
		`function big(): u8 { return 300u8; }
function main(): i32 { return 0; }`,
		`function main(): i32 { let xs = [300u8, 1u8]; return 0; }`,
		`struct S { v: u8 }
function main(): i32 { let s = S { v: 300u8 }; return 0; }`,
		`function main(): i32 { let t = (300u8, 1); return 0; }`,
		`function main(): i32 { let x = 300u8 + 1u8; return 0; }`,
		// No annotation to settle against at all.
		`function main(): i32 { let x = 300u8; return 0; }`,
	}
	for _, src := range rejected {
		errs := checkErrors(t, src)
		found := false
		for _, e := range errs {
			if e.ErrCode == "E047" && strings.Contains(e.Msg, "does not fit in u8") {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: no E047 for the out-of-range u8 literal, got %v", src, errs)
		}
	}
}
