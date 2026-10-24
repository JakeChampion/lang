package parser

import (
	"errors"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/diag"
)

func TestFStringInterpolantPositions(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []ast.Position
	}{
		{
			name: "single interpolant",
			src:  `function main(): i32 { let s = f"{zzz}"; return 0; }`,
			want: []ast.Position{{Line: 1, Col: 35}},
		},
		{
			name: "two interpolants offset independently",
			src:  `function main(): i32 { let s = f"a{aaa} b{bbb}"; return 0; }`,
			want: []ast.Position{{Line: 1, Col: 36}, {Line: 1, Col: 43}},
		},
		{
			name: "f-string on a later line",
			src:  "function main(): i32 {\n    let s = f\"x{yy}\";\n    return 0;\n}",
			want: []ast.Position{{Line: 2, Col: 17}},
		},
		{
			name: "nested f-string rebases through both levels",
			src:  `function main(): i32 { let s = f"x{f"y{qqq}"}"; return 0; }`,
			want: []ast.Position{{Line: 1, Col: 36}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			fs := findFString(t, prog)
			var got []ast.Position
			for _, part := range fs.Parts {
				if part.Expr != nil {
					got = append(got, part.Expr.Pos())
				}
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d interpolants, want %d", len(got), len(tc.want))
			}
			for i, w := range tc.want {
				if got[i] != w {
					t.Errorf("interpolant %d at %v, want %v", i, got[i], w)
				}
			}
		})
	}
}

func TestFStringInterpolantInnerNodePositions(t *testing.T) {
	prog, err := Parse(`function main(): i32 { let s = f"{a + bb}"; return 0; }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	fs := findFString(t, prog)
	bin, ok := fs.Parts[0].Expr.(*ast.Binary)
	if !ok {
		t.Fatalf("interpolant is %T, want *ast.Binary", fs.Parts[0].Expr)
	}
	if got, want := bin.Left.Pos(), (ast.Position{Line: 1, Col: 35}); got != want {
		t.Errorf("left operand at %v, want %v", got, want)
	}
	if got, want := bin.Right.Pos(), (ast.Position{Line: 1, Col: 39}); got != want {
		t.Errorf("right operand at %v, want %v", got, want)
	}
}

func TestFStringTrailingTokensRejected(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want ast.Position
	}{
		{"two operands", `function main(): i32 { let x = 5; let y = 7; let s = f"[{x y}]"; return 0; }`, ast.Position{Line: 1, Col: 60}},
		{"comma list", `function main(): i32 { let x = 5; let y = 7; let s = f"[{x, y}]"; return 0; }`, ast.Position{Line: 1, Col: 59}},
		{"stray paren", `function main(): i32 { let x = 5; let s = f"[{x)}]"; return 0; }`, ast.Position{Line: 1, Col: 48}},
		{"format spec", `function main(): i32 { let x = 5; let s = f"{x:04}"; return 0; }`, ast.Position{Line: 1, Col: 47}},
		{"second placeholder", `function main(): i32 { let x = 5; let s = f"{x} {x x} {x x}"; return 0; }`, ast.Position{Line: 1, Col: 52}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.src)
			if !hasParseCode(err, "P001") {
				t.Fatalf("want P001, got %v", err)
			}
			var batch diag.Errors
			if !errors.As(err, &batch) || len(batch) != 1 {
				t.Fatalf("want exactly one error, got %v", err)
			}
			var pe *Error
			if !errors.As(batch[0], &pe) {
				t.Fatalf("want *Error, got %T", batch[0])
			}
			if pe.Msg != "f-string interpolation: unexpected trailing tokens after expression" {
				t.Errorf("message %q", pe.Msg)
			}
			if pe.Pos != tc.want {
				t.Errorf("at %v, want %v", pe.Pos, tc.want)
			}
		})
	}
}

func findFString(t *testing.T, prog *ast.Program) *ast.FString {
	t.Helper()
	var found *ast.FString
	ast.WalkProgram(prog, func(n ast.Node) bool {
		if fs, ok := n.(*ast.FString); ok && found == nil {
			found = fs
			return false
		}
		return true
	})
	if found == nil {
		t.Fatal("no *ast.FString in program")
	}
	return found
}
