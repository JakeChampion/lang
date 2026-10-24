package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/modload"
)

var structDestructureCases = []struct {
	name string
	src  string
	want int
}{
	{
		name: "shorthand",
		src: `struct Point { x: i32, y: i32 }
function main(): i32 {
  let p: Point = Point { x: 3, y: 4 };
  let Point { x, y } = p;
  return x * 10 + y;
}`,
		want: 34,
	},
	{
		name: "var_keyword",
		src: `struct Point { x: i32, y: i32 }
function main(): i32 {
  let p: Point = Point { x: 7, y: 2 };
  let Point { x, y } = p;
  return x - y;
}`,
		want: 5,
	},
	{
		name: "rename",
		src: `struct Point { x: i32, y: i32 }
function main(): i32 {
  let p: Point = Point { x: 8, y: 1 };
  let Point { x: a, y: b } = p;
  return a * 10 + b;
}`,
		want: 81,
	},
	{
		name: "rest_partial",
		src: `struct Point { x: i32, y: i32, z: i32 }
function main(): i32 {
  let p: Point = Point { x: 5, y: 6, z: 7 };
  let Point { x, z, .. } = p;
  return x * 10 + z;
}`,
		want: 57,
	},
	{
		name: "string_field",
		src: `struct Named { id: i32, label: string }
function main(): i32 {
  let n: Named = Named { id: 40, label: "abc" };
  let Named { id, label } = n;
  return id + label.len();
}`,
		want: 43,
	},
	{
		name: "from_call",
		src: `struct Point { x: i32, y: i32 }
function mk(a: i32, b: i32): Point { return Point { x: a, y: b }; }
function main(): i32 {
  let Point { x, y } = mk(9, 6);
  return x * 10 + y;
}`,
		want: 96,
	},
	{
		name: "nested_struct_field",
		src: `struct Inner { a: i32, b: i32 }
struct Outer { tag: i32, inner: Inner }
function main(): i32 {
  let o: Outer = Outer { tag: 1, inner: Inner { a: 20, b: 3 } };
  let Outer { tag, inner } = o;
  return tag + inner.a + inner.b;
}`,
		want: 24,
	},
}

func TestStructDestructureX86_64(t *testing.T) {
	for _, tc := range structDestructureCases {
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

func TestStructDestructureArm64(t *testing.T) {
	for _, tc := range structDestructureCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, tc.src); code != tc.want {
				t.Errorf("native arm64 = %d, want %d", code, tc.want)
			}
		})
	}
}

func TestStructDestructureWasm(t *testing.T) {
	for _, name := range []string{"shorthand", "rename", "string_field"} {
		tc := structDestructureCaseByName(t, name)
		t.Run(name, func(t *testing.T) {
			if got := runWasm(t, tc.src); got != tc.want {
				t.Errorf("wasm = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestStructDestructureRejected(t *testing.T) {
	cases := []string{
		`struct P { x: i32 }
struct Q { x: i32 }
function main(): i32 { let p: P = P { x: 1 }; let Q { x } = p; return x; }`,
		`struct P { x: i32 }
function main(): i32 { let p: P = P { x: 1 }; let P { z } = p; return z; }`,
		`function main(): i32 { let t = (1, 2); let Foo { a, b } = t; return a + b; }`,
	}
	for _, src := range cases {
		prog, _, err := modload.LoadSource(src)
		if err != nil {
			continue
		}
		if _, cerr := checker.Check(prog); cerr == nil {
			t.Errorf("expected a check error for invalid struct destructure, got none:\n%s", src)
		}
	}
}

func structDestructureCaseByName(t *testing.T, name string) struct {
	name string
	src  string
	want int
} {
	t.Helper()
	for _, tc := range structDestructureCases {
		if tc.name == name {
			return tc
		}
	}
	t.Fatalf("no struct-destructure case named %q", name)
	return structDestructureCases[0]
}
