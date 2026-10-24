package printer

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestFormatPatternBindingSites(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{
			name: "for_struct_shorthand",
			src: `struct Point { x: i32, y: i32 }
function f(ps: Point[]): i32 { let acc = 0; for Point { x, y } in ps { acc = acc + x + y; } return acc; }`,
			want: "for Point { x, y } in ps {",
		},
		{
			name: "for_struct_rename",
			src: `struct Point { x: i32, y: i32 }
function f(ps: Point[]): i32 { let acc = 0; for Point { x: a, y: b } in ps { acc = acc + a + b; } return acc; }`,
			want: "for Point { x: a, y: b } in ps {",
		},
		{
			name: "for_at_struct",
			src: `struct Point { x: i32, y: i32 }
function f(ps: Point[]): i32 { let acc = 0; for w @ Point { x, y } in ps { acc = acc + w.x + x + y; } return acc; }`,
			want: "for w @ Point { x, y } in ps {",
		},
		{
			name: "for_at_tuple",
			src:  `function f(ts: (i32, i32)[]): i32 { let acc = 0; for w @ (a, b) in ts { acc = acc + w.0 + a + b; } return acc; }`,
			want: "for w @ (a, b) in ts {",
		},
		{
			name: "for_tuple_stays_tuple",
			src:  `function f(ts: (i32, i32)[]): i32 { let acc = 0; for (a, b) in ts { acc = acc + a + b; } return acc; }`,
			want: "for (a, b) in ts {",
		},
		{
			name: "destructure_at_struct",
			src: `struct Point { x: i32, y: i32 }
function f(p: Point): i32 { let w @ Point { x, y } = p; return w.x + x + y; }`,
			want: "let w @ Point { x, y } = p;",
		},
		{
			name: "for_struct_rest",
			src: `struct Point { x: i32, y: i32, z: i32 }
function f(ps: Point[]): i32 { let acc = 0; for Point { x, .. } in ps { acc = acc + x; } return acc; }`,
			want: "for Point { x, .. } in ps {",
		},
		{
			name: "destructure_rest",
			src: `struct Point { x: i32, y: i32, z: i32 }
function f(p: Point): i32 { let Point { x, .. } = p; return x; }`,
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
function f(p: Point): i32 { let w @ Point { x, .. } = p; return w.y + x; }`,
			want: "let w @ Point { x, .. } = p;",
		},
		{
			name: "no_rest_stays_absent",
			src: `struct Point { x: i32, y: i32 }
function f(p: Point): i32 { let Point { x, y } = p; return x + y; }`,
			want: "let Point { x, y } = p;",
		},
		{
			name: "destructure_at_tuple",
			src:  `function f(t: (i32, i32)): i32 { let w @ (a, b) = t; return w.0 + a + b; }`,
			want: "let w @ (a, b) = t;",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := formatSrc(t, tc.src)
			if !strings.Contains(got, tc.want) {
				t.Errorf("formatted output missing %q:\n%s", tc.want, got)
			}
			if again := formatSrc(t, got); again != got {
				t.Errorf("not idempotent:\nfirst:\n%s\nsecond:\n%s", got, again)
			}
			if _, err := parser.Parse(got); err != nil {
				t.Errorf("formatted output does not parse: %v\n%s", err, got)
			}
		})
	}
}
