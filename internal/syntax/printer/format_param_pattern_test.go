package printer

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestFormatDestructuringParamRoundTrips(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want string
	}{
		{
			name: "arrow_tuple_param",
			src:  "function main(): i32 {\n    let b = ((p, q): (i32, i32)) => p - q;\n    if (b((5, 2)) != 3) { return 1; }\n    return 0;\n}",
			want: "((p, q): (i32, i32)) => p - q",
		},
		{
			name: "arrow_struct_param",
			src:  "struct Point { x: i32, y: i32 }\nfunction apply(f: (Point) => i32, p: Point): i32 { return f(p); }\nfunction main(): i32 {\n    let g: (Point) => i32 = (Point { x: a, y }: Point) => a + y;\n    return apply(g, Point { x: 3, y: 4 });\n}",
			want: "(Point { x: a, y }: Point) => a + y",
		},
		{
			name: "arrow_struct_param_shorthand",
			src:  "struct Point { x: i32, y: i32 }\nfunction apply(f: (Point) => i32, p: Point): i32 { return f(p); }\nfunction main(): i32 {\n    let g: (Point) => i32 = (Point { x, y }: Point) => x + y;\n    return apply(g, Point { x: 3, y: 4 });\n}",
			want: "(Point { x, y }: Point) => x + y",
		},
		{
			name: "declaration_struct_param",
			src:  "struct Point { x: i32, y: i32 }\nfunction f(Point { x: a, y }: Point): i32 { return a + y; }\nfunction main(): i32 { return f(Point { x: 1, y: 2 }); }",
			want: "function f(Point { x: a, y }: Point): i32 {",
		},
		{
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
			mustCheck(t, tc.src, "source")

			got := formatSrc(t, tc.src)
			if !strings.Contains(got, tc.want) {
				t.Errorf("got:\n%s\nwant it to contain %q", got, tc.want)
			}
			for _, bad := range []string{"__ptuple_", ": void"} {
				if strings.Contains(got, bad) {
					t.Errorf("got:\n%s\nwant it NOT to contain %q", got, bad)
				}
			}

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
