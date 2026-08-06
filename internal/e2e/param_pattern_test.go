// Destructuring parameters read the shared pattern grammar (#5356) — the
// last of the five binding sites. `parseParamPattern` reads the head with
// parseMatchPattern and desugars to a synthetic named param of the
// annotated type plus a leading *ast.Destructure, so struct patterns
// (with rename and `..`) join the tuple form parameters already had, and
// `w @ <pattern>` names the whole value alongside the destructure.
//
// A parameter binds unconditionally — there is no else branch — so the
// refutable patterns the grammar can express are rejected here, with a
// diagnostic that says why rather than the bare `expected ":"` the
// hand-rolled parse produced.
package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/modload"
)

var paramPatternCases = []struct {
	name string
	src  string
	want int
}{
	{
		name: "struct_shorthand",
		src: `struct Point { x: i32, y: i32 }
function f(Point { x, y }: Point): i32 { return x * 10 + y; }
function main(): i32 { return f(Point { x: 3, y: 4 }); }`,
		want: 34,
	},
	{
		// `field: local` renames, exactly as in a match arm.
		name: "struct_rename",
		src: `struct Point { x: i32, y: i32 }
function f(Point { x: a, y: b }: Point): i32 { return a * 10 + b; }
function main(): i32 { return f(Point { x: 3, y: 4 }); }`,
		want: 34,
	},
	{
		// `..` documents the fields left unbound.
		name: "struct_partial",
		src: `struct Point { x: i32, y: i32 }
function f(Point { x, .. }: Point): i32 { return x; }
function main(): i32 { return f(Point { x: 7, y: 4 }); }`,
		want: 7,
	},
	{
		// `@` names the whole value; the fields bind alongside it.
		name: "at_binding_struct",
		src: `struct Point { x: i32, y: i32 }
function f(w @ Point { x, y }: Point): i32 { return w.x * 100 + x * 10 + y; }
function main(): i32 { return f(Point { x: 1, y: 2 }) - 100; }`,
		want: 12, // 100 + 10 + 2 - 100
	},
	{
		name: "at_binding_tuple",
		src: `function f(w @ (a, b): (i32, i32)): i32 { return w.0 * 100 + a * 10 + b; }
function main(): i32 { return f((1, 2)) - 100; }`,
		want: 12,
	},
	{
		// The tuple form parameters already had, unchanged — including the
		// `_` discard element.
		name: "tuple_and_discard",
		src: `function f((a, b): (i32, i32)): i32 { return a * 10 + b; }
function g((a, _): (i32, i32)): i32 { return a; }
function main(): i32 { return f((3, 4)) + g((5, 9)); }`,
		want: 39,
	},
	{
		// Mixed param list: a destructured param in second position.
		name: "second_position",
		src: `struct Point { x: i32, y: i32 }
function f(k: i32, Point { x, y }: Point): i32 { return k + x * 10 + y; }
function main(): i32 { return f(1, Point { x: 3, y: 4 }); }`,
		want: 35,
	},
	{
		// Both lambda forms take the same parameter grammar as a named
		// function — the verbose one and the arrow one.
		name: "lambdas",
		src: `struct Point { x: i32, y: i32 }
function main(): i32 {
  var p: Point = Point { x: 3, y: 4 };
  var verbose = function(Point { x, y }: Point): i32 { return x * 10 + y; };
  var arrow = (Point { x, y }: Point) => x + y;
  return verbose(p) + arrow(p);
}`,
		want: 41, // 34 + 7
	},
	{
		// Two destructured params in one signature: the synthetic holders
		// are uniqued by source position, so they can't collide.
		name: "two_destructured_params",
		src: `struct Point { x: i32, y: i32 }
function f(Point { x, y }: Point, (a, b): (i32, i32)): i32 { return x * 1000 + y * 100 + a * 10 + b; }
function main(): i32 { return f(Point { x: 1, y: 2 }, (3, 4)) - 1000; }`,
		want: 234,
	},
}

func TestParamPatternInterp(t *testing.T) {
	for _, tc := range paramPatternCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runInterpByte(t, tc.src); got != tc.want {
				t.Errorf("interp = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestParamPatternX86_64(t *testing.T) {
	for _, tc := range paramPatternCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64(t, tc.src); code != tc.want {
				t.Errorf("native x86-64 = %d, want %d", code, tc.want)
			}
		})
	}
}

func TestParamPatternArm64(t *testing.T) {
	for _, tc := range paramPatternCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, tc.src); code != tc.want {
				t.Errorf("native arm64 = %d, want %d", code, tc.want)
			}
		})
	}
}

func TestParamPatternWasm(t *testing.T) {
	for _, name := range []string{"struct_shorthand", "struct_rename", "at_binding_struct", "tuple_and_discard", "lambdas"} {
		tc := paramPatternCaseByName(t, name)
		t.Run(name, func(t *testing.T) {
			if got := runWasm(t, tc.src); got != tc.want {
				t.Errorf("wasm = %d, want %d", got, tc.want)
			}
		})
	}
}

// A parameter has no else branch, so a refutable pattern is rejected —
// and the rejection names the reason instead of leaving the bare
// `expected ":"` the hand-rolled parameter parse produced.
func TestParamPatternRefutableRejected(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{
			name: "enum_variant",
			src: `enum O { A(i32), B }
function f(A(v): O): i32 { return v; }
function main(): i32 { return 0; }`,
			want: "an enum variant pattern can fail to match",
		},
		{
			name: "qualified_variant",
			src: `enum O { A(i32), B }
function f(O.A(v): O): i32 { return v; }
function main(): i32 { return 0; }`,
			want: "an enum variant pattern can fail to match",
		},
		{
			name: "literal_tuple_elem",
			src: `function f((1, b): (i32, i32)): i32 { return b; }
function main(): i32 { return 0; }`,
			want: "a literal tuple element can fail to match",
		},
	}
	dir := t.TempDir()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name+".fern")
			if err := os.WriteFile(path, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			_, _, err := modload.Load(path)
			if err == nil {
				t.Fatalf("expected a parse error mentioning %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err.Error(), tc.want)
			}
		})
	}
}

// A struct-pattern parameter whose annotation doesn't match still lands
// on the existing destructure diagnostics rather than crashing — the
// desugar routes through the same *ast.Destructure the `let S { … } = e;`
// form uses.
func TestParamPatternMismatchedAnnotation(t *testing.T) {
	src := `struct Point { x: i32, y: i32 }
function f(Point { x, y }: i32): i32 { return x + y; }
function main(): i32 { return f(1); }`
	prog, _, err := modload.LoadSource(src)
	if err != nil {
		return // a parse-level rejection is an acceptable outcome too
	}
	if _, cerr := checker.Check(prog); cerr == nil {
		t.Error("expected a type error for a struct pattern over an i32 parameter")
	}
}

func paramPatternCaseByName(t *testing.T, name string) struct {
	name string
	src  string
	want int
} {
	t.Helper()
	for _, tc := range paramPatternCases {
		if tc.name == name {
			return tc
		}
	}
	t.Fatalf("no param pattern case named %q", name)
	return paramPatternCases[0]
}

// The `let` / `var` destructuring statements read the same grammar, via
// the same irrefutableDestructure conversion — the last of #5356's five
// binding sites to stop hand-rolling its own parse. `_` is renamed per
// occurrence at every element position (#6346), which is what lets a
// pattern discard more than one slot without the elements colliding.
var destructureStmtCases = []struct {
	name string
	src  string
	want int
}{
	{
		name: "tuple_and_struct_forms",
		src: `struct P { x: i32, y: i32 }
function main(): i32 {
  var (a, b) = (1, 2);
  let (c, d) = (3, 4);
  var P { x, y } = P { x: 5, y: 6 };
  let P { x: nx, .. } = P { x: 7, y: 8 };
  return a + b + c + d + x + y + nx;
}`,
		want: 28, // 1+2+3+4+5+6+7
	},
	{
		// Repeated discards across and within patterns: each `_` gets its
		// own internal name, so none of these redeclare anything.
		name: "repeated_discards",
		src: `struct P { x: i32, y: i32 }
function main(): i32 {
  var (a, _) = (1, 2);
  var (_, b) = (3, 4);
  let (_, _) = (5, 6);
  let P { x: _, y } = P { x: 7, y: 8 };
  return a + b + y;
}`,
		want: 13, // 1 + 4 + 8
	},
	{
		// The same rule inside a destructured PARAMETER, which routes
		// through the identical conversion — this shape was E013
		// ("variable \"_\" already declared") before the two paths shared it.
		name: "repeated_discards_in_param",
		src: `function g((_, _): (i32, i32)): i32 { return 7; }
function h((a, _): (i32, i32), (_, b): (i32, i32)): i32 { return a * 10 + b; }
function main(): i32 { return g((1, 2)) + h((3, 4), (5, 6)); }`,
		want: 43, // 7 + 36
	},
}

func TestDestructureStmtInterp(t *testing.T) {
	for _, tc := range destructureStmtCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runInterpByte(t, tc.src); got != tc.want {
				t.Errorf("interp = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestDestructureStmtX86_64(t *testing.T) {
	for _, tc := range destructureStmtCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64(t, tc.src); code != tc.want {
				t.Errorf("native x86-64 = %d, want %d", code, tc.want)
			}
		})
	}
}

func TestDestructureStmtArm64(t *testing.T) {
	for _, tc := range destructureStmtCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, tc.src); code != tc.want {
				t.Errorf("native arm64 = %d, want %d", code, tc.want)
			}
		})
	}
}

func TestDestructureStmtWasm(t *testing.T) {
	for _, tc := range destructureStmtCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runWasm(t, tc.src); got != tc.want {
				t.Errorf("wasm = %d, want %d", got, tc.want)
			}
		})
	}
}

// A refutable pattern in a `let` / `var` destructure is rejected. The
// `let` spelling has a refutable form, so it asks for the `else` that
// makes it one; `var` has none, so the pattern simply isn't a
// destructure.
func TestDestructureStmtRefutableRejected(t *testing.T) {
	for _, src := range []string{
		"enum O { A(i32), B }\nfunction main(): i32 { let A(v) = A(1); return v; }",
		"enum O { A(i32), B }\nfunction main(): i32 { var A(v) = A(1); return v; }",
		"function main(): i32 { let (1, b) = (1, 2); return b; }",
		"struct P { x: i32 }\nfunction main(): i32 { var P {} = P { x: 1 }; return 0; }",
	} {
		if _, _, err := modload.LoadSource(src); err == nil {
			t.Errorf("expected a parse error for:\n%s", src)
		}
	}
}
