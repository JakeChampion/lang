// Struct destructure coverage (#5354): `let Point { x, y } = p;` binds
// named struct fields into the enclosing scope. Shorthand `{ x }` binds
// field `x` to local `x`; `{ x: nx }` renames; a trailing `..` marks
// intentionally-omitted fields. Reuses the tuple-destructure AST node
// (ast.Destructure) with Fields set, so it lowers on every backend that
// already handles tuple destructure. These pin the native binaries
// against the interpreter oracle; a couple also run through wasm.
package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/modload"
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
  var p: Point = Point { x: 3, y: 4 };
  let Point { x, y } = p;
  return x * 10 + y;
}`,
		want: 34,
	},
	{
		name: "var_keyword",
		src: `struct Point { x: i32, y: i32 }
function main(): i32 {
  var p: Point = Point { x: 7, y: 2 };
  var Point { x, y } = p;
  return x - y;
}`,
		want: 5,
	},
	{
		name: "rename",
		src: `struct Point { x: i32, y: i32 }
function main(): i32 {
  var p: Point = Point { x: 8, y: 1 };
  let Point { x: a, y: b } = p;
  return a * 10 + b;
}`,
		want: 81,
	},
	{
		name: "rest_partial",
		src: `struct Point { x: i32, y: i32, z: i32 }
function main(): i32 {
  var p: Point = Point { x: 5, y: 6, z: 7 };
  let Point { x, z, .. } = p;
  return x * 10 + z;
}`,
		want: 57,
	},
	{
		// A string field is dup-on-projection'd, so the binding co-owns the
		// buffer and both source + binding survive to end of scope.
		name: "string_field",
		src: `struct Named { id: i32, label: string }
function main(): i32 {
  var n: Named = Named { id: 40, label: "abc" };
  let Named { id, label } = n;
  return id + label.len();
}`,
		want: 43,
	},
	{
		// Destructure from a call result (a fresh owned struct box).
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
		// A struct with a nested-struct field: the inner struct pointer is
		// extracted by reference (dup-on-projection), then read.
		name: "nested_struct_field",
		src: `struct Inner { a: i32, b: i32 }
struct Outer { tag: i32, inner: Inner }
function main(): i32 {
  var o: Outer = Outer { tag: 1, inner: Inner { a: 20, b: 3 } };
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

// A struct destructure that names the wrong struct type, an unknown field,
// or a non-struct expression must be a checker error, not silent
// miscompilation.
func TestStructDestructureRejected(t *testing.T) {
	cases := []string{
		// wrong struct name
		`struct P { x: i32 }
struct Q { x: i32 }
function main(): i32 { var p: P = P { x: 1 }; let Q { x } = p; return x; }`,
		// unknown field
		`struct P { x: i32 }
function main(): i32 { var p: P = P { x: 1 }; let P { z } = p; return z; }`,
		// non-struct scrutinee
		`function main(): i32 { var t = (1, 2); let Foo { a, b } = t; return a + b; }`,
	}
	for _, src := range cases {
		prog, _, err := modload.LoadSource(src)
		if err != nil {
			continue // a parse error is an acceptable rejection too
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
