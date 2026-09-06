package printer

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/parser"
)

// An arrow lambda's written return type reprints in the form the parser
// admits (#8706): a function type there keeps its grouping parens — printed
// bare, `(p: i32): (i32) => i32 => …` re-parses with `(i32)` as the return
// type and fails at the `i32` where the body would start — and a tuple
// prints as itself. Each output re-parses to the same lambda, still
// type-checks, and is a fixed point of Format.
func TestFormatArrowLambdaReturnTypeRoundTrip(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"tuple_braced", "function main(): i32 { var id = (p: (i32, i32)): (i32, i32) => { return p; }; var t = id((3, 4)); return t.0; }", "(p: (i32, i32)): (i32, i32) => p"},
		{"tuple_expression", "function main(): i32 { var id = (p: (i32, i32)): (i32, i32) => p; var t = id((3, 4)); return t.0; }", "(p: (i32, i32)): (i32, i32) => p"},
		{"grouped_scalar", "function main(): i32 { var g = (): (i32) => { return 7; }; return g(); }", "(): i32 => 7"},
		{"grouped_fn_expression", "function main(): i32 { var mk = (p: i32): ((i32) => i32) => (q: i32) => p + q; var f: (i32) => i32 = mk(1); return f(2); }", "(p: i32): ((i32) => i32) => (q: i32) => p + q"},
		{"grouped_fn_braced", "function main(): i32 { var mk = (p: i32): ((i32) => i32) => { var q = (r: i32): i32 => p + r; return q; }; var f: (i32) => i32 = mk(1); return f(2); }", "(p: i32): ((i32) => i32) => {"},
		{"tuple_with_fn_element", "function main(): i32 { var pair = (): ((i32) => i32, i32) => ((n: i32) => n + 1, 5); var pr = pair(); return pr.1; }", "(): ((i32) => i32, i32) => ((n: i32) => n + 1, 5)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := formatSrc(t, tc.src)
			if !strings.Contains(got, tc.want) {
				t.Errorf("got:\n%s\nwant it to contain %q", got, tc.want)
			}
			if _, err := parser.Parse(got); err != nil {
				t.Fatalf("formatted output no longer parses: %v\n%s", err, got)
			}
			if checkRejects(t, got) {
				t.Errorf("formatted output no longer type-checks:\n%s", got)
			}
			if again := formatSrc(t, got); again != got {
				t.Errorf("not idempotent:\nfirst:\n%s\nsecond:\n%s", got, again)
			}
		})
	}
}
