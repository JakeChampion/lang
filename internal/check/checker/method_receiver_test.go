package checker

import (
	"strings"
	"testing"
)

func TestMethodReceiverCheckedOnce(t *testing.T) {
	const decls = `struct P { n: i32 }
function (self: P) get(): i32 { return self.n; }
function (self: P) to_string(): string { return "p"; }
function mk(a: i32): P { return P { n: a }; }
`
	cases := []struct {
		name string
		src  string
		want int
	}{
		{
			name: "nested f-string",
			src:  `function main(): i32 { let s = f"x{f"y{qqq}"}"; return 0; }`,
			want: 1,
		},
		{
			name: "single-level f-string",
			src:  `function main(): i32 { let s = f"x{qqq}"; return 0; }`,
			want: 1,
		},
		{
			name: "resolved method on an erroring receiver",
			src:  decls + `function main(): i32 { return mk(qqq).get(); }`,
			want: 1,
		},
		{
			name: "unresolved method on an erroring receiver",
			src:  decls + `function main(): i32 { return mk(qqq).nosuch(); }`,
			want: 1,
		},
		{
			name: "method chain on an erroring receiver",
			src:  decls + `function main(): i32 { return mk(qqq).get().to_string().len(); }`,
			want: 1,
		},
		{
			name: "two undefined identifiers in one receiver",
			src:  decls + `function main(): i32 { return mk(qqq + rrr).get(); }`,
			want: 2,
		},
		{
			name: "field access on an erroring target",
			src:  decls + `function main(): i32 { return mk(qqq).n; }`,
			want: 1,
		},
		{
			name: "display spine wraps a print argument",
			src:  decls + `function main(): i32 { print(mk(qqq)); return 0; }`,
			want: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkSource(t, tc.src)
			if err == nil {
				t.Fatal("expected an undefined-identifier error, got none")
			}
			n := 0
			for _, e := range flattenErrors(err) {
				if strings.Contains(e.Error(), "cannot find") {
					n++
				}
			}
			if n != tc.want {
				t.Errorf("got %d undefined-identifier errors, want %d:\n%v", n, tc.want, err)
			}
		})
	}
}
