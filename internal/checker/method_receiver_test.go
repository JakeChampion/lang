package checker

import (
	"strings"
	"testing"
)

// A method call's receiver is type-checked once (#6998). Resolving dispatch
// needs the receiver's type before the callee is known, and the receiver then
// reaches the argument list (the dispatch rewrite prepends it) or stays the
// callee's target — either way a second check re-emits every diagnostic the
// receiver's subexpressions produced.
//
// Counts, not contents: a duplicate is byte-identical to the original, so an
// assertion on the message alone passes while the output carries it twice. The
// controls fail a fix that suppresses a second REAL error instead.
func TestMethodReceiverCheckedOnce(t *testing.T) {
	const decls = `struct P { n: i32 }
impl P {
  function get(self: Self): i32 { return self.n; }
  function to_string(self: Self): string { return "p"; }
}
function mk(a: i32): P { return P { n: a }; }
`
	cases := []struct {
		name string
		src  string
		want int
	}{
		{
			// The inner f-string desugars to a `.to_string()` chain and types
			// as `string` despite the error inside it, so dispatch resolves
			// and the receiver goes on to the argument list.
			name: "nested f-string",
			src:  `function main(): i32 { var s = f"x{f"y{qqq}"}"; return 0; }`,
			want: 1,
		},
		{
			// Control: one level deep the receiver is `qqq` itself, which
			// types as nil and bails before the second check.
			name: "single-level f-string",
			src:  `function main(): i32 { var s = f"x{qqq}"; return 0; }`,
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
			// A chain compounds: each link takes the one below it as its
			// receiver, so a per-link doubling is exponential in the depth.
			name: "method chain on an erroring receiver",
			src:  decls + `function main(): i32 { return mk(qqq).get().to_string().len(); }`,
			want: 1,
		},
		{
			// Control: two distinct undefined identifiers stay two errors.
			name: "two undefined identifiers in one receiver",
			src:  decls + `function main(): i32 { return mk(qqq + rrr).get(); }`,
			want: 2,
		},
		{
			// Control: a plain field access reaches the same field-typing
			// code without going through dispatch.
			name: "field access on an erroring target",
			src:  decls + `function main(): i32 { return mk(qqq).n; }`,
			want: 1,
		},
		{
			// The Display spine wraps a non-string `print` argument in
			// `.to_string()` after typing it, so the wrapper's receiver is
			// an expression the checker has already been through.
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
				if strings.Contains(e.Error(), "undefined identifier") {
					n++
				}
			}
			if n != tc.want {
				t.Errorf("got %d undefined-identifier errors, want %d:\n%v", n, tc.want, err)
			}
		})
	}
}
