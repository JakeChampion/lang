package e2e

import (
	"testing"
)

var orPatternCases = []struct {
	name string
	src  string
	want int
}{
	{
		name: "literal_or",
		src: `function f(x: i32): i32 {
  match (x) {
    1 | 2 | 3 => { return 9; },
    _ => { return 0; },
  }
  return 0 - 1;
}
function main(): i32 { return f(1) + f(2) + f(3) + f(5); }`,
		want: 27,
	},
	{
		name: "literal_or_expr",
		src: `function f(x: i32): i32 {
  return match (x) { 1 | 2 => 7, 3 | 4 => 8, _ => 0 };
}
function main(): i32 { return f(2) * 10 + f(4) + f(9); }`,
		want: 78,
	},
	{
		name: "string_or",
		src: `function f(s: string): i32 {
  match (s) {
    "a" | "b" | "c" => { return 5; },
    _ => { return 0; },
  }
  return 0 - 1;
}
function main(): i32 { return f("b") * 10 + f("z"); }`,
		want: 50,
	},
	{
		name: "or_with_guard",
		src: `function f(x: i32): i32 {
  match (x) {
    1 | 2 | 3 when x > 1 => { return 9; },
    _ => { return 0; },
  }
  return 0 - 1;
}
function main(): i32 { return f(1) * 100 + f(2) * 10 + f(5); }`,
		want: 90,
	},
	{
		name: "range_or",
		src: `function f(x: i32): i32 {
  match (x) {
    1..5 | 10..15 => { return 1; },
    _ => { return 0; },
  }
  return 0 - 1;
}
function main(): i32 { return f(3) * 100 + f(12) * 10 + f(7); }`,
		want: 110,
	},
	{
		name: "tuple_or_literal",
		src: `function f(t: (i32, i32)): i32 {
  match (t) {
    (1, 2) | (3, 4) => { return 20; },
    _ => { return 7; },
  }
  return 0 - 1;
}
function main(): i32 { return f((3, 4)) * 10 + f((5, 6)); }`,
		want: 207,
	},
	{
		name: "tuple_or_bind",
		src: `function f(t: (i32, i32)): i32 {
  match (t) {
    (1, x) | (x, 2) => { return x; },
    _ => { return 0; },
  }
  return 0 - 1;
}
function main(): i32 { return f((1, 42)) + f((99, 2)); }`,
		want: 141,
	},
	{
		name: "tuple_or_guard",
		src: `function f(t: (i32, i32)): i32 {
  match (t) {
    (1, x) | (x, 2) when x > 10 => { return x; },
    _ => { return 0; },
  }
  return 0 - 1;
}
function main(): i32 { return f((1, 5)) * 100 + f((1, 30)) + f((50, 2)); }`,
		want: 80,
	},
	{
		name: "tuple_or_expr",
		src: `function f(t: (i32, i32)): i32 {
  return match (t) { (1, 2) | (2, 1) => 5, _ => 0 };
}
function main(): i32 { return f((2, 1)) * 10 + f((9, 9)); }`,
		want: 50,
	},
	{
		name: "tuple_or_three",
		src: `function f(t: (i32, i32)): i32 {
  match (t) {
    (1, 1) | (2, 2) | (3, 3) => { return 88; },
    _ => { return 0; },
  }
  return 0 - 1;
}
function main(): i32 { return f((2, 2)) + f((1, 2)); }`,
		want: 88,
	},
}

func TestOrPatternX86_64(t *testing.T) {
	for _, tc := range orPatternCases {
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

func TestOrPatternArm64(t *testing.T) {
	for _, tc := range orPatternCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, tc.src); code != tc.want {
				t.Errorf("native arm64 = %d, want %d", code, tc.want)
			}
		})
	}
}

func TestOrPatternWasm(t *testing.T) {
	for _, name := range []string{"literal_or", "range_or", "tuple_or_literal", "tuple_or_bind"} {
		tc := orPatternCaseByName(t, name)
		t.Run(name, func(t *testing.T) {
			if got := runWasm(t, tc.src); got != tc.want {
				t.Errorf("wasm = %d, want %d", got, tc.want)
			}
		})
	}
}

func orPatternCaseByName(t *testing.T, name string) struct {
	name string
	src  string
	want int
} {
	t.Helper()
	for _, tc := range orPatternCases {
		if tc.name == name {
			return tc
		}
	}
	t.Fatalf("no or-pattern case named %q", name)
	return orPatternCases[0]
}
