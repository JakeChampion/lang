package checker

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestCastSettlesPolymorphicOperandAtSignedI32(t *testing.T) {
	cases := []struct {
		name      string
		decl      string
		wantWidth int
	}{
		{"subtract to f64", "let v: f64 = (3 - 4) as f64;", 32},
		{"subtract to f32", "let v: f32 = (3 - 4) as f32;", 32},
		{"negative divide narrowed to u8", "let v: u8 = ((0 - 7) / 2) as u8;", 32},
		{"arithmetic to char", "let v: char = (0 - 7 + 72) as char;", 32},
		{"i64 operand stays i64", "let v: f64 = (x - 4611686018427387904) as f64;", 64},
		{"wide literal widens the operand", "let v: f64 = (3 - 4611686018427387904) as f64;", 64},
		{"wide literal widens a narrowing operand", "let v: u8 = (4611686018427387904 % 256) as u8;", 64},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prog, err := parser.Parse("function main(): i32 {\n    let x: i64 = 3;\n    " + c.decl + "\n    return 0;\n}\n")
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

func TestCastOperandLiteralRangeIsJudgedAtI32(t *testing.T) {
	err := checkSource(t, "function main(): i32 {\n    let a: i32 = 3;\n    let v: f64 = (a - 4611686018427387904) as f64;\n    return 0;\n}\n")
	if err == nil {
		t.Fatal("accepted, want E047")
	}
	if msg := err.Error(); !strings.Contains(msg, "does not fit in i32") {
		t.Errorf("want the literal judged against i32, got: %s", msg)
	}
}

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
				t.Fatalf("let %s: init is %T, want *ast.CastExpr", name, v.Init)
			}
			bin, ok := cast.Inner.(*ast.Binary)
			if !ok {
				t.Fatalf("let %s: cast operand is %T, want *ast.Binary", name, cast.Inner)
			}
			return bin
		}
	}
	t.Fatalf("no `let %s` in main", name)
	return nil
}

func TestCastOperandTakesNoTypeFromTheDestination(t *testing.T) {
	const decls = "struct P[S, M] { view: (S) => M }\n" +
		"function run[S, M](p: P[S, M], s: S): M { return p.view(s); }\n" +
		"function double(n: i32): i64 { return (n * 2) as i64; }\n" +
		"function first[T](xs: T[]): T { return xs[0]; }\n"
	cases := []struct {
		name string
		body string
		code string
	}{
		{"the literal binds the result type", "return run(P { view: double }, 1) as i32 - 2;", ""},
		{"the binding type does not reach the call", "let k: i32 = first([]) as i32; return k;", "E040"},
		{"the return type does not reach the call", "return first([]) as i32;", "E040"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prog, err := parser.Parse(decls + "function main(): i32 { " + c.body + " }\n")
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			_, err = Check(prog)
			if got := errCodes(err); got != c.code {
				t.Errorf("codes %q, want %q: %v", got, c.code, err)
			}
		})
	}
}
