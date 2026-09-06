package checker

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/parser"
)

// A cast that keeps its target out of a compound operand settles that
// operand at its own integer type, standing in i32 while it is still
// polymorphic. That i32 was spelled `NumberType{Width: 32}`, which IsSigned()
// reads as u32 — only a zero Width defaults to signed — so `(3 - 4) as f64`
// wrapped to 4294967295 on every engine while `(p - q) as f64` gave -1, and
// `((0 - 7) / 2) as u8` was 252 from literals and 253 from a variable. Every
// branch that settles a cast operand this way has to agree on the sign.
func TestCastSettlesPolymorphicOperandAtSignedI32(t *testing.T) {
	cases := []struct {
		name      string
		decl      string
		wantWidth int
	}{
		{"subtract to f64", "var v: f64 = (3 - 4) as f64;", 32},
		{"subtract to f32", "var v: f32 = (3 - 4) as f32;", 32},
		{"negative divide narrowed to u8", "var v: u8 = ((0 - 7) / 2) as u8;", 32},
		{"arithmetic to char", "var v: char = (0 - 7 + 72) as char;", 32},
		// A committed operand keeps its own type; the i32 stand-in is only
		// for one that nothing has settled yet.
		{"i64 operand stays i64", "var v: f64 = (x - 4611686018427387904) as f64;", 64},
		// An unsettled operand holding a literal past i32 range takes the
		// i64 default instead — the reading `var t = 3 - 4611686018427387904`
		// gets (#8668) — rather than an E047 the bare `4611686018427387904 as
		// f64` never got.
		{"wide literal widens the operand", "var v: f64 = (3 - 4611686018427387904) as f64;", 64},
		{"wide literal widens a narrowing operand", "var v: u8 = (4611686018427387904 % 256) as u8;", 64},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prog, err := parser.Parse("function main(): i32 {\n    var x: i64 = 3;\n    " + c.decl + "\n    return 0;\n}\n")
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if _, err := Check(prog); err != nil {
				t.Fatalf("check: %v", err)
			}
			bin := castOperandBinary(t, prog, "v")
			if bin.IntWidth != c.wantWidth {
				t.Errorf("operand settled at width %d, want %d", bin.IntWidth, c.wantWidth)
			}
			if bin.IsUnsigned {
				t.Errorf("operand settled UNSIGNED — `%s` computes at i%d, not u%d", c.decl, c.wantWidth, c.wantWidth)
			}
		})
	}
}

// The range check on a compound operand's literal judges it against the type
// the operand computes in. Beside an operand already committed to i32, a
// literal only i64 can hold is an E047 naming i32 — never u32, and never a
// widening of the variable behind its back.
func TestCastOperandLiteralRangeIsJudgedAtI32(t *testing.T) {
	err := checkSource(t, "function main(): i32 {\n    var a: i32 = 3;\n    var v: f64 = (a - 4611686018427387904) as f64;\n    return 0;\n}\n")
	if err == nil {
		t.Fatal("accepted, want E047")
	}
	if msg := err.Error(); !strings.Contains(msg, "does not fit in i32") {
		t.Errorf("want the literal judged against i32, got: %s", msg)
	}
}

// castOperandBinary finds `var <name> = (<binary>) as T;` in main and returns
// the binary the cast wraps.
func castOperandBinary(t *testing.T, prog *ast.Program, name string) *ast.Binary {
	t.Helper()
	for _, fn := range prog.Funcs {
		if fn.Name != "main" {
			continue
		}
		for _, st := range fn.Body.Stmts {
			v, ok := st.(*ast.Var)
			if !ok || v.Name != name {
				continue
			}
			cast, ok := v.Init.(*ast.CastExpr)
			if !ok {
				t.Fatalf("var %s: init is %T, want *ast.CastExpr", name, v.Init)
			}
			bin, ok := cast.Inner.(*ast.Binary)
			if !ok {
				t.Fatalf("var %s: cast operand is %T, want *ast.Binary", name, cast.Inner)
			}
			return bin
		}
	}
	t.Fatalf("no `var %s` in main", name)
	return nil
}
