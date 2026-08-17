package checker

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/diag"
)

// A diagnostic against an expression inside `f"{…}"` must point at that
// expression, not at 1:1 of the file (#6989). The interpolant is
// sub-parsed from raw text by a fresh lexer, and before the rebase every
// node it produced claimed 1:1 — so `f"{zzz}"` reported E001 with the
// caret on the first character of the source.
//
// Two interpolants pin the second one's offset independently: a rebase
// that used the f-string token's own position would place the first one
// plausibly and every later one short.
func TestFStringDiagnosticPositions(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []ast.Position
	}{
		{
			name: "single interpolant",
			src:  `function main(): i32 { var s = f"{zzz}"; return 0; }`,
			want: []ast.Position{{Line: 1, Col: 35}},
		},
		{
			name: "two interpolants",
			src:  `function main(): i32 { var s = f"a{aaa} b{bbb}"; return 0; }`,
			want: []ast.Position{{Line: 1, Col: 36}, {Line: 1, Col: 43}},
		},
		{
			name: "f-string on a later line",
			src:  "function main(): i32 {\n    var s = f\"x{yy}\";\n    return 0;\n}",
			want: []ast.Position{{Line: 2, Col: 17}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkSource(t, tc.src)
			if err == nil {
				t.Fatal("expected E001 for the undefined identifiers, got none")
			}
			var got []ast.Position
			for _, e := range flattenErrors(err) {
				pe, ok := e.(diag.Positioned)
				if !ok {
					t.Fatalf("error %v carries no position", e)
				}
				got = append(got, pe.Position())
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d errors at %v, want %d at %v", len(got), got, len(tc.want), tc.want)
			}
			for i, w := range tc.want {
				if got[i] != w {
					t.Errorf("error %d reported at %v, want %v (%q)", i, got[i], w, err.Error())
				}
			}
		})
	}
}

func flattenErrors(err error) []error {
	if es, ok := err.(diag.Errors); ok {
		var out []error
		for _, e := range es {
			out = append(out, flattenErrors(e)...)
		}
		return out
	}
	return []error{err}
}
