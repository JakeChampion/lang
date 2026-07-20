// `@` bindings on variant and struct patterns (#5356): `match (b) { n @
// Full(v) => … }` / `match (p) { w @ Point { x, y } => … }` bind the whole
// matched value to the `@`-name (at the scrutinee's type) alongside the
// payload / field binds. The whole value is a borrowed reference to the
// scrutinee (box or struct pointer). Tuple / literal / wildcard sub-patterns
// stay rejected (v1). These pin the native binaries against the interp
// oracle; a couple also run through wasm.
package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/modload"
)

var atBindingCases = []struct {
	name string
	src  string
	want int
}{
	{
		// `n` is the whole value; `v` is the payload. total(n) re-matches n.
		name: "stmt_whole_and_payload",
		src: `enum Box { Full(i32), Empty }
function total(b: Box): i32 { match (b) { Full(v) => { return v; }, Empty => { return 0; } } return 0; }
function f(b: Box): i32 {
  match (b) {
    n @ Full(v) => { return total(n) * 10 + v; },
    Empty => { return 0; },
  }
  return 0;
}
function main(): i32 { return f(Full(3)); }`,
		want: 33, // total(Full(3))=3 → 30 + 3
	},
	{
		// Expression form + a guard that references the `@` binding.
		name: "expr_guard_uses_at",
		src: `enum Box { Full(i32), Empty }
function is_full(b: Box): boolean { match (b) { Full(v) => { return true; }, Empty => { return false; } } return false; }
function f(b: Box): i32 {
  return match (b) {
    n @ Full(v) when is_full(n) => v + 2,
    Full(v) => v,
    Empty => 0,
  };
}
function main(): i32 { return f(Full(3)); }`,
		want: 5,
	},
	{
		// The `@` binding flows through a nested match without disturbing
		// the payload binding.
		name: "at_reused",
		src: `enum Box { Full(i32), Empty }
function depth(b: Box): i32 {
  match (b) {
    a @ Full(v) => {
      match (a) {
        b2 @ Full(w) => { return w + 100; },
        Empty => { return 0; },
      }
      return v;
    },
    Empty => { return 0; },
  }
  return 0;
}
function main(): i32 { return depth(Full(7)); }`,
		want: 107,
	},
	{
		// `@` on a struct pattern: `w` is the whole struct, `x`/`y` the
		// fields. w.x + w.y + x + y.
		name: "struct_whole_and_fields",
		src: `struct Point { x: i32, y: i32 }
function f(p: Point): i32 {
  match (p) {
    w @ Point { x, y } => { return w.x + w.y + x + y; },
  }
  return 0;
}
function main(): i32 { return f(Point { x: 3, y: 4 }); }`,
		want: 14, // (3+4) + (3+4)
	},
	{
		// Expression form + a guard referencing the struct `@` binding.
		name: "struct_expr_guard_uses_at",
		src: `struct P { a: i32, b: i32 }
function f(p: P): i32 {
  return match (p) {
    w @ P { a, b } when w.a > 0 => a + b,
    P { a, b } => 0,
  };
}
function main(): i32 { return f(P { a: 2, b: 8 }); }`,
		want: 10,
	},
	{
		// `@` combined with field renaming (`x: nx`) in the same arm.
		name: "struct_at_with_rename",
		src: `struct Point { x: i32, y: i32 }
function f(p: Point): i32 { match (p) { w @ Point { x: nx, y: ny } => { return w.x * 100 + nx * 10 + ny; } } return 0; }
function main(): i32 { return f(Point { x: 1, y: 2 }); }`,
		want: 112, // 100 + 10 + 2
	},
}

func TestAtBindingX86_64(t *testing.T) {
	for _, tc := range atBindingCases {
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

func TestAtBindingArm64(t *testing.T) {
	for _, tc := range atBindingCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, tc.src); code != tc.want {
				t.Errorf("native arm64 = %d, want %d", code, tc.want)
			}
		})
	}
}

func TestAtBindingWasm(t *testing.T) {
	for _, name := range []string{"stmt_whole_and_payload", "expr_guard_uses_at", "struct_whole_and_fields", "struct_at_with_rename"} {
		tc := atBindingCaseByName(t, name)
		t.Run(name, func(t *testing.T) {
			if got := runWasm(t, tc.src); got != tc.want {
				t.Errorf("wasm = %d, want %d", got, tc.want)
			}
		})
	}
}

// `@` on a non-variant sub-pattern (tuple / literal / wildcard) is rejected
// (v1 restriction), and a nested `@` is rejected.
func TestAtBindingRejected(t *testing.T) {
	cases := []string{
		// `@` on a tuple pattern
		`function f(t: (i32, i32)): i32 { match (t) { n @ (a, b) => { return a; }, } return 0; }
function main(): i32 { return 0; }`,
		// `@` on a literal
		`function f(x: i32): i32 { match (x) { n @ 0 => { return 1; }, _ => { return 2; } } return 0; }
function main(): i32 { return 0; }`,
		// nested `@`
		`enum Box { Full(i32), Empty }
function f(b: Box): i32 { match (b) { a @ b2 @ Full(v) => { return v; }, Empty => { return 0; } } return 0; }
function main(): i32 { return 0; }`,
	}
	for _, src := range cases {
		prog, _, err := modload.LoadSource(src)
		if err != nil {
			continue // a parse error is the expected rejection
		}
		if _, cerr := checker.Check(prog); cerr == nil {
			t.Errorf("expected a parse/check error for invalid `@` binding, got none:\n%s", src)
		}
	}
}

func atBindingCaseByName(t *testing.T, name string) struct {
	name string
	src  string
	want int
} {
	t.Helper()
	for _, tc := range atBindingCases {
		if tc.name == name {
			return tc
		}
	}
	t.Fatalf("no at-binding case named %q", name)
	return atBindingCases[0]
}
