package printer

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/parser"
)

// A destructuring pattern renders through one function at every binding site
// (#5356). Rendering it site-locally is how a struct pattern came to reprint
// as the positional tuple form, silently rebinding by field position (#6374) —
// so the `for` header and the `let` / `var` destructure, which only reached
// the shared grammar once the pattern-head lookahead was unified, are pinned
// here in the same shape a parameter already was.
func TestFormatPatternBindingSites(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{
			// The struct name and the fields must survive: as a tuple this
			// reads as bind-by-position over a different set of names.
			name: "for_struct_shorthand",
			src: `struct Point { x: i32, y: i32 }
function f(ps: Point[]): i32 { var acc = 0; for Point { x, y } in ps { acc = acc + x + y; } return acc; }`,
			want: "for Point { x, y } in ps {",
		},
		{
			name: "for_struct_rename",
			src: `struct Point { x: i32, y: i32 }
function f(ps: Point[]): i32 { var acc = 0; for Point { x: a, y: b } in ps { acc = acc + a + b; } return acc; }`,
			want: "for Point { x: a, y: b } in ps {",
		},
		{
			// The `@` binding is a real local the body reads, so dropping it
			// makes the formatted program stop compiling.
			name: "for_at_struct",
			src: `struct Point { x: i32, y: i32 }
function f(ps: Point[]): i32 { var acc = 0; for w @ Point { x, y } in ps { acc = acc + w.x + x + y; } return acc; }`,
			want: "for w @ Point { x, y } in ps {",
		},
		{
			name: "for_at_tuple",
			src:  `function f(ts: (i32, i32)[]): i32 { var acc = 0; for w @ (a, b) in ts { acc = acc + w.0 + a + b; } return acc; }`,
			want: "for w @ (a, b) in ts {",
		},
		{
			name: "for_tuple_stays_tuple",
			src:  `function f(ts: (i32, i32)[]): i32 { var acc = 0; for (a, b) in ts { acc = acc + a + b; } return acc; }`,
			want: "for (a, b) in ts {",
		},
		{
			name: "destructure_at_struct",
			src: `struct Point { x: i32, y: i32 }
function f(p: Point): i32 { var w @ Point { x, y } = p; return w.x + x + y; }`,
			want: "let w @ Point { x, y } = p;",
		},
		{
			// The trailing `..` binds nothing — a named-field pattern binds
			// only the fields it lists — so it survives only by being carried
			// to the printer. Dropping it made `-fmt -w` delete what the
			// author wrote, at every site that takes a struct pattern.
			name: "for_struct_rest",
			src: `struct Point { x: i32, y: i32, z: i32 }
function f(ps: Point[]): i32 { var acc = 0; for Point { x, .. } in ps { acc = acc + x; } return acc; }`,
			want: "for Point { x, .. } in ps {",
		},
		{
			name: "destructure_rest",
			src: `struct Point { x: i32, y: i32, z: i32 }
function f(p: Point): i32 { var Point { x, .. } = p; return x; }`,
			want: "let Point { x, .. } = p;",
		},
		{
			name: "param_rest",
			src: `struct Point { x: i32, y: i32, z: i32 }
function f(Point { x, .. }: Point): i32 { return x; }`,
			want: "function f(Point { x, .. }: Point): i32 {",
		},
		{
			name: "match_arm_rest",
			src: `struct Point { x: i32, y: i32, z: i32 }
function f(p: Point): i32 { match (p) { Point { x, .. } => { return x; } } }`,
			want: "Point { x, .. } => {",
		},
		{
			name: "match_expr_arm_rest",
			src: `struct Point { x: i32, y: i32, z: i32 }
function f(p: Point): i32 { return match (p) { Point { x, .. } => x }; }`,
			want: "Point { x, .. } => x",
		},
		{
			name: "at_with_rest",
			src: `struct Point { x: i32, y: i32, z: i32 }
function f(p: Point): i32 { var w @ Point { x, .. } = p; return w.y + x; }`,
			want: "let w @ Point { x, .. } = p;",
		},
		{
			// A pattern that lists every field wrote no `..`, and must not
			// grow one.
			name: "no_rest_stays_absent",
			src: `struct Point { x: i32, y: i32 }
function f(p: Point): i32 { var Point { x, y } = p; return x + y; }`,
			want: "let Point { x, y } = p;",
		},
		{
			name: "destructure_at_tuple",
			src:  `function f(t: (i32, i32)): i32 { var w @ (a, b) = t; return w.0 + a + b; }`,
			want: "let w @ (a, b) = t;",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := formatSrc(t, tc.src)
			if !strings.Contains(got, tc.want) {
				t.Errorf("formatted output missing %q:\n%s", tc.want, got)
			}
			// Formatting is a fixed point, and the formatted text parses —
			// the two properties a silently-rewritten pattern breaks. (The
			// AST-equality roundTrip helper is not usable here: a foreach
			// carries a synthetic loop ID that re-parsing renumbers, so it
			// fails for a plain `for a in xs` too.)
			if again := formatSrc(t, got); again != got {
				t.Errorf("not idempotent:\nfirst:\n%s\nsecond:\n%s", got, again)
			}
			if _, err := parser.Parse(got); err != nil {
				t.Errorf("formatted output does not parse: %v\n%s", err, got)
			}
		})
	}
}
