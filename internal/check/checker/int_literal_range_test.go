package checker

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/constfold"
	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/diag"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestIntLiteralRangeCountsTheSign(t *testing.T) {
	cases := []struct {
		src       string
		accepted  bool
		wantInMsg string
	}{
		{"let x: i32 = -2147483648;", true, ""},
		{"let x: i32 = 2147483647;", true, ""},
		{"let x: i32 = -1;", true, ""},
		{"let x: i32 = 0;", true, ""},
		{"let x: i32 = --5;", true, ""},
		{"let x: i32 = -2147483649;", false, "-2147483649"},
		{"let x: i32 = 2147483648;", false, "2147483648"},
		{"let x: i32 = -4000000000;", false, "-4000000000"},
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
		`let x: u32 = -0;`,
	}
	for _, src := range accepted {
		if err := checkSource(t, "function main(): i32 { "+src+" return 0; }"); err != nil {
			t.Errorf("%s: rejected, want accepted: %v", src, err)
		}
	}
}

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
	if err := checkSource(t, `function main(): i32 { let x: i64 = 0xFFFFFFFFFFFFFFFF; return 0; }`); err == nil {
		t.Error("0xFFFFFFFFFFFFFFFF accepted for i64; it exceeds the signed range")
	}
}

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
		{`let a = ` + lit + `u64;`, lit, "u64", 32},
		{`let b: boolean = ` + lit + ` > 1;`, lit, "i64", 41},
		{`let t = (` + lit + `, 1);`, lit, "i64", 33},
		{`let xs = [` + lit + `];`, lit, "i64", 34},
		{`let a: u64 = 0xFFFFFFFFFFFFFFFFFFFF;`, "0xFFFFFFFFFFFFFFFFFFFF", "u64", 37},
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

func TestSuffixedIntLiteralRange(t *testing.T) {
	cases := []struct {
		src       string
		accepted  bool
		wantInMsg string
	}{
		{`let x: u8 = 255u8;`, true, ""},
		{`let x: u8 = 0u8;`, true, ""},
		{`let x: u32 = 4294967295u32;`, true, ""},
		{`let x: u64 = 18446744073709551615u64;`, true, ""},
		{`let x: i32 = 2147483647i32;`, true, ""},
		{`let x: i32 = -2147483648i32;`, true, ""},
		{`let x: i64 = 9223372036854775807i64;`, true, ""},
		{`let x: i64 = -9223372036854775808i64;`, true, ""},
		{`let x: i64 = - -9223372036854775807i64;`, true, ""},
		{`let x: u32 = -0u32;`, true, ""},
		{`let x: u32 = 0xFFFFFFFFu32;`, true, ""},
		{`let x: i64 = -0x8000000000000000i64;`, true, ""},
		{`let x: u8 = 300u8;`, false, "literal 300 does not fit in u8"},
		{`let x: u8 = 256u8;`, false, "literal 256 does not fit in u8"},
		{`let x: u32 = 4294967296u32;`, false, "literal 4294967296 does not fit in u32"},
		{`let x: i32 = 2147483648i32;`, false, "literal 2147483648 does not fit in i32"},
		{`let x: i64 = 9223372036854775808i64;`, false, "literal 9223372036854775808 does not fit in i64"},
		{`let x: u32 = 0x100000000u32;`, false, "literal 0x100000000 does not fit in u32"},
		{`let x: i32 = -2147483649i32;`, false, "literal -2147483649 does not fit in i32"},
		{`let x: i64 = -9223372036854775809i64;`, false, "literal -9223372036854775809 does not fit in i64"},
		{`let x: u8 = -1u8;`, false, "unsigned types have no negative values"},
		{`let x: u64 = -1u64;`, false, "unsigned types have no negative values"},
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
