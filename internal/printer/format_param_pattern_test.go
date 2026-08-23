package printer

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// A destructuring parameter is desugared at parse time into a holder param plus
// a leading `let` in the body. That lost the written form, and for an ARROW
// lambda it lost the program: the body was no longer the single `return expr`
// the arrow spelling needs, so the printer fell back to `function(…)`, which
// requires a return type. There is none to write — the printer runs before the
// checker, and the arrow never had one — so native invented `: void` and the
// self-host wrote nothing, which means the same thing. Either way `fern -fmt`
// turned a compiling program into one the compiler rejects (#7338).
//
// The property is not "the output looks right", it is "the output still
// compiles", so every case below is type-checked rather than string-matched
// alone.
func TestFormatDestructuringParamRoundTrips(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want string
	}{
		{
			// The issue's own repro.
			name: "arrow_tuple_param",
			src:  "function main(): i32 {\n    var b = ((p, q): (i32, i32)) => p - q;\n    if (b((5, 2)) != 3) { return 1; }\n    return 0;\n}",
			want: "((p, q): (i32, i32)) => p - q",
		},
		{
			name: "arrow_struct_param",
			src:  "struct Point { x: i32, y: i32 }\nfunction apply(f: (Point) => i32, p: Point): i32 { return f(p); }\nfunction main(): i32 {\n    var g: (Point) => i32 = (Point { x: a, y }: Point) => a + y;\n    return apply(g, Point { x: 3, y: 4 });\n}",
			want: "(Point { x: a, y }: Point) => a + y",
		},
		{
			// Field shorthand must not become `x: x`.
			name: "arrow_struct_param_shorthand",
			src:  "struct Point { x: i32, y: i32 }\nfunction apply(f: (Point) => i32, p: Point): i32 { return f(p); }\nfunction main(): i32 {\n    var g: (Point) => i32 = (Point { x, y }: Point) => x + y;\n    return apply(g, Point { x: 3, y: 4 });\n}",
			want: "(Point { x, y }: Point) => x + y",
		},
		{
			// A declaration has a written return type, so this was never a
			// compile failure — only the holder and the prelude leaking into
			// output someone has to read.
			name: "declaration_struct_param",
			src:  "struct Point { x: i32, y: i32 }\nfunction f(Point { x: a, y }: Point): i32 { return a + y; }\nfunction main(): i32 { return f(Point { x: 1, y: 2 }); }",
			want: "function f(Point { x: a, y }: Point): i32 {",
		},
		{
			// An `@` binding names the whole value beside the pattern, and the
			// parser uses THAT as the holder rather than minting one. Printing
			// the pattern alone drops the binding, and the body's `v.x` stops
			// resolving — which is how the fmt parity gate caught it, by
			// compiling the output rather than comparing text.
			name: "declaration_at_bound_param",
			src:  "struct P { x: i32, y: i32 }\nfunction whole(v @ P { x, y }: P): i32 { return v.x + x + y; }\nfunction main(): i32 { return whole(P { x: 1, y: 2 }); }",
			want: "function whole(v @ P { x, y }: P): i32 {",
		},
		{
			name: "declaration_tuple_param",
			src:  "function f((a, b): (i32, i32)): i32 { return a + b; }\nfunction main(): i32 { return f((1, 2)); }",
			want: "function f((a, b): (i32, i32)): i32 {",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The source has to compile, or the round-trip proves nothing.
			mustCheck(t, tc.src, "source")

			got := formatSrc(t, tc.src)
			if !strings.Contains(got, tc.want) {
				t.Errorf("got:\n%s\nwant it to contain %q", got, tc.want)
			}
			// The two spellings the desugar used to leak. `: void` is the one
			// that made the program stop compiling; `__ptuple_` is a name the
			// source never wrote.
			for _, bad := range []string{"__ptuple_", ": void"} {
				if strings.Contains(got, bad) {
					t.Errorf("got:\n%s\nwant it NOT to contain %q", got, bad)
				}
			}

			// The property that matters.
			mustCheck(t, got, "formatted output")

			if again := formatSrc(t, got); again != got {
				t.Errorf("not idempotent:\nfirst:\n%s\nsecond:\n%s", got, again)
			}
		})
	}
}

func mustCheck(t *testing.T, src, what string) {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("%s does not parse: %v\n%s", what, err, src)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("%s does not type-check: %v\n%s", what, err, src)
	}
}

// The prelude is matched by holder name, so a body whose leading statements are
// the USER's own destructuring keeps them. Without that, a `let` the source
// wrote would be swallowed into the parameter list and its bindings lost.
func TestFormatKeepsUserDestructuringInBody(t *testing.T) {
	src := "function f(t: (i32, i32), u: (i32, i32)): i32 {\n" +
		"    let (a, b) = t;\n" +
		"    let (c, d) = u;\n" +
		"    return a + b + c + d;\n" +
		"}\n" +
		"function main(): i32 { return f((1, 2), (3, 4)); }"
	mustCheck(t, src, "source")

	got := formatSrc(t, src)
	for _, want := range []string{"let (a, b) = t;", "let (c, d) = u;"} {
		if !strings.Contains(got, want) {
			t.Errorf("got:\n%s\nwant it to contain %q — a user's own destructuring "+
				"must not be mistaken for the parameter desugar's prelude", got, want)
		}
	}
	mustCheck(t, got, "formatted output")
}
