package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/modload"
)

var structMatchCases = []struct {
	name string
	src  string
	want int
}{
	{
		name: "guarded_fallthrough",
		src: `struct Point { x: i32, y: i32 }
function classify(p: Point): i32 {
  match (p) {
    Point { x, y } when x > y => { return 1; },
    Point { x, y } when x < y => { return 2; },
    Point { x, y } => { return 3; },
  }
  return 0;
}
function main(): i32 {
  return classify(Point { x: 5, y: 2 }) * 100
       + classify(Point { x: 1, y: 9 }) * 10
       + classify(Point { x: 4, y: 4 });
}`,
		want: 123,
	},
	{
		name: "single_arm",
		src: `struct P { x: i32, y: i32 }
function f(p: P): i32 { match (p) { P { x, y } => { return x + y; } } return 0; }
function main(): i32 { return f(P { x: 10, y: 20 }); }`,
		want: 30,
	},
	{
		name: "wildcard_default",
		src: `struct P { x: i32, y: i32 }
function f(p: P): i32 {
  match (p) {
    P { x, y } when x == 0 => { return 99; },
    _ => { return 7; },
  }
  return 0;
}
function main(): i32 { return f(P { x: 0, y: 1 }) + f(P { x: 5, y: 1 }); }`,
		want: 106,
	},
	{
		name: "string_field",
		src: `struct Named { id: i32, label: string }
function f(n: Named): i32 {
  match (n) {
    Named { id, label } when id > 5 => { return label.len() + 10; },
    Named { id, label } => { return label.len(); },
  }
  return 0;
}
function main(): i32 { return f(Named { id: 9, label: "abcd" }) * 10 + f(Named { id: 1, label: "ab" }); }`,
		want: 142,
	},
	{
		name: "partial_bind",
		src: `struct P { x: i32, y: i32, z: i32 }
function f(p: P): i32 { match (p) { P { y, .. } => { return y; } } return 0; }
function main(): i32 { return f(P { x: 1, y: 42, z: 3 }); }`,
		want: 42,
	},
	{
		name: "rest_marker",
		src: `struct P { x: i32, y: i32, z: i32 }
function f(p: P): i32 { match (p) { P { x, .. } => { return x; } } return 0; }
function main(): i32 { return f(P { x: 55, y: 6, z: 7 }); }`,
		want: 55,
	},
	{
		name: "expr_form",
		src: `struct Point { x: i32, y: i32 }
function area(p: Point): i32 {
  return match (p) {
    Point { x, y } when x == y => x * 10,
    Point { x, y } => x + y,
  };
}
function main(): i32 { return area(Point { x: 4, y: 4 }) + area(Point { x: 3, y: 5 }); }`,
		want: 48,
	},
	{
		name: "expr_string_result",
		src: `struct Named { id: i32, label: string }
function tag(n: Named): string {
  return match (n) {
    Named { id, label } when id > 0 => label,
    Named { id, label } => "none",
  };
}
function main(): i32 { return tag(Named { id: 1, label: "hello" }).len() + tag(Named { id: 0, label: "x" }).len(); }`,
		want: 9,
	},
	{
		name: "rename_stmt",
		src: `struct Point { x: i32, y: i32 }
function f(p: Point): i32 { match (p) { Point { x: a, y: b } => { return a * 10 + b; } } return 0; }
function main(): i32 { return f(Point { x: 3, y: 7 }); }`,
		want: 37,
	},
	{
		name: "rename_expr",
		src: `struct Point { x: i32, y: i32 }
function area(p: Point): i32 {
  return match (p) {
    Point { x: a, y: b } when a == b => a * 10,
    Point { x: a, y: b } => a + b,
  };
}
function main(): i32 { return area(Point { x: 4, y: 4 }) + area(Point { x: 3, y: 5 }); }`,
		want: 48,
	},
	{
		name: "rename_partial",
		src: `struct P { x: i32, y: i32, z: i32 }
function f(p: P): i32 { match (p) { P { y: m, .. } => { return m; } } return 0; }
function main(): i32 { return f(P { x: 1, y: 42, z: 3 }); }`,
		want: 42,
	},
	{
		name: "rename_mixed",
		src: `struct Named { id: i32, label: string }
function f(n: Named): i32 {
  match (n) {
    Named { id: k, label } when k > 5 => { return label.len() + 10; },
    Named { id, label: s } => { return s.len(); },
  }
  return 0;
}
function main(): i32 { return f(Named { id: 9, label: "abcd" }) * 10 + f(Named { id: 1, label: "ab" }); }`,
		want: 142,
	},
}

func TestStructMatchX86_64(t *testing.T) {
	for _, tc := range structMatchCases {
		t.Run(tc.name, func(t *testing.T) {
			oracle := runInterpByte(t, tc.src)
			if oracle != tc.want {
				t.Fatalf("interp oracle = %d, want %d", oracle, tc.want)
			}
			_, code := compileAndRunX86_64(t, tc.src)
			if code != tc.want {
				t.Errorf("native x86-64 = %d, want %d (interp oracle agrees at %d)", code, tc.want, oracle)
			}
		})
	}
}

func TestStructMatchArm64(t *testing.T) {
	for _, tc := range structMatchCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, tc.src); code != tc.want {
				t.Errorf("native arm64 = %d, want %d", code, tc.want)
			}
		})
	}
}

func TestStructMatchWasm(t *testing.T) {
	for _, name := range []string{"guarded_fallthrough", "string_field"} {
		tc := structMatchCaseByName(t, name)
		t.Run(name, func(t *testing.T) {
			if got := runWasm(t, tc.src); got != tc.want {
				t.Errorf("wasm = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestStructMatchRejected(t *testing.T) {
	cases := []string{
		`struct P { x: i32, y: i32 }
function f(p: P): i32 { match (p) { P { x, y } when x > 0 => { return 1; } } return 0; }
function main(): i32 { return f(P { x: 1, y: 2 }); }`,
		`struct P { x: i32 }
struct Q { x: i32 }
function f(p: P): i32 { match (p) { Q { x } => { return x; } } return 0; }
function main(): i32 { return f(P { x: 1 }); }`,
		`struct P { x: i32 }
function f(p: P): i32 { match (p) { P { z } => { return z; } } return 0; }
function main(): i32 { return f(P { x: 1 }); }`,
		`struct P { x: i32 }
function f(p: P): i32 { match (p) { 0 => { return 1; }, _ => { return 2; } } return 0; }
function main(): i32 { return f(P { x: 1 }); }`,
	}
	for _, src := range cases {
		prog, _, err := modload.LoadSource(src)
		if err != nil {
			continue
		}
		if _, cerr := checker.Check(prog); cerr == nil {
			t.Errorf("expected a check error for invalid struct match, got none:\n%s", src)
		}
	}
}

func structMatchCaseByName(t *testing.T, name string) struct {
	name string
	src  string
	want int
} {
	t.Helper()
	for _, tc := range structMatchCases {
		if tc.name == name {
			return tc
		}
	}
	t.Fatalf("no struct-match case named %q", name)
	return structMatchCases[0]
}
