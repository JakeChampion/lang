package checker

import (
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/diag"
)

func TestFStringDiagnosticPositions(t *testing.T) {
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
			name: "two interpolants",
			src:  `function main(): i32 { let s = f"a{aaa} b{bbb}"; return 0; }`,
			want: []ast.Position{{Line: 1, Col: 36}, {Line: 1, Col: 43}},
		},
		{
			name: "f-string on a later line",
			src:  "function main(): i32 {\n    let s = f\"x{yy}\";\n    return 0;\n}",
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
