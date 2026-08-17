package parser

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// An f-string interpolant is sub-parsed from raw text by a fresh lexer
// that numbers the fragment from 1:1, so every node it produces has to
// be rebased onto where the interpolant sits in the enclosing file
// (#6989). Unrebased, every node inside `f"{…}"` claimed 1:1 and every
// diagnostic against it pointed at the first character of the file.
//
// Multiple interpolants are the discriminating case: rebasing onto the
// f-string token's own position gets the first one roughly right and
// every later one wrong.
func TestFStringInterpolantPositions(t *testing.T) {
	cases := []struct {
		name string
		src  string
		// want is the position of each interpolant's root Expr, in
		// source order.
		want []ast.Position
	}{
		{
			name: "single interpolant",
			src:  `function main(): i32 { var s = f"{zzz}"; return 0; }`,
			want: []ast.Position{{Line: 1, Col: 35}},
		},
		{
			name: "two interpolants offset independently",
			src:  `function main(): i32 { var s = f"a{aaa} b{bbb}"; return 0; }`,
			want: []ast.Position{{Line: 1, Col: 36}, {Line: 1, Col: 43}},
		},
		{
			name: "f-string on a later line",
			src:  "function main(): i32 {\n    var s = f\"x{yy}\";\n    return 0;\n}",
			want: []ast.Position{{Line: 2, Col: 17}},
		},
		{
			name: "nested f-string rebases through both levels",
			src:  `function main(): i32 { var s = f"x{f"y{qqq}"}"; return 0; }`,
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

// The rebase has to reach every node in the sub-parsed expression, not
// only its root — a binary operand or a call argument is what a
// diagnostic usually points at.
func TestFStringInterpolantInnerNodePositions(t *testing.T) {
	prog, err := Parse(`function main(): i32 { var s = f"{a + bb}"; return 0; }`)
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
