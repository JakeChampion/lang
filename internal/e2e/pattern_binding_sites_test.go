// One pattern grammar at every irrefutable binding site (#5356). The five
// sites the issue named all read `parseMatchPattern`, but each still kept its
// own lookahead deciding whether a pattern was there at all, and the three
// gates admitted different subsets: a `for` header took only a tuple head, and
// neither `for` nor the `let` / `var` destructure took an `@` binding, which a
// destructured parameter had.
//
// `atPatternHead` is now the one lookahead all of them ask, so a head admitted
// at one site is admitted at all. What that opens up is exercised here: struct
// patterns (with rename and `..`) and nested patterns in a `for` header, and
// `@` bindings naming the whole value at the `for` and destructure sites.
package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/modload"
)

var patternBindingSiteCases = []struct {
	name string
	src  string
	want int
}{
	{
		name: "for_struct_shorthand",
		src: `struct Point { x: i32, y: i32 }
function main(): i32 {
  var ps: Point[] = [Point { x: 3, y: 4 }, Point { x: 1, y: 2 }];
  var acc = 0;
  for Point { x, y } in ps { acc = acc + x * 10 + y; }
  return acc;
}`,
		want: 46,
	},
	{
		// `field: local` renames, exactly as in a match arm.
		name: "for_struct_rename",
		src: `struct Point { x: i32, y: i32 }
function main(): i32 {
  var ps: Point[] = [Point { x: 3, y: 4 }];
  var acc = 0;
  for Point { x: a, y: b } in ps { acc = acc + a * 10 + b; }
  return acc;
}`,
		want: 34,
	},
	{
		// `..` documents the fields left unbound.
		name: "for_struct_rest",
		src: `struct Point { x: i32, y: i32, z: i32 }
function main(): i32 {
  var ps: Point[] = [Point { x: 5, y: 6, z: 7 }];
  var acc = 0;
  for Point { x, z, .. } in ps { acc = acc + x * 10 + z; }
  return acc;
}`,
		want: 57,
	},
	{
		// The element variable IS the whole value, so that is what the `@`
		// binding names — the same placement a parameter's holder gets.
		name: "for_at_struct",
		src: `struct Point { x: i32, y: i32 }
function main(): i32 {
  var ps: Point[] = [Point { x: 2, y: 3 }];
  var acc = 0;
  for w @ Point { x, y } in ps { acc = acc + w.x + w.y + x + y; }
  return acc;
}`,
		want: 10,
	},
	{
		name: "for_at_tuple",
		src: `function main(): i32 {
  var ts: (i32, i32)[] = [(3, 4), (5, 6)];
  var acc = 0;
  for w @ (a, b) in ts { acc = acc + w.0 + b; }
  return acc;
}`,
		want: 18,
	},
	{
		name: "for_nested_tuple",
		src: `function main(): i32 {
  var ts: ((i32, i32), i32)[] = [((1, 2), 3)];
  var acc = 0;
  for ((a, b), c) in ts { acc = acc + a + b + c; }
  return acc;
}`,
		want: 6,
	},
	{
		// `_` is a discard at every binding site, so two of them in one
		// pattern do not collide (#6346).
		name: "for_struct_discard",
		src: `struct Point { x: i32, y: i32 }
function main(): i32 {
  var ps: Point[] = [Point { x: 4, y: 9 }];
  var acc = 0;
  for Point { x: _, y: _ } in ps { acc = acc + 7; }
  return acc;
}`,
		want: 7,
	},
	{
		// The destructure's holding local already holds the whole value
		// between the init's evaluation and the per-name loads, so an `@`
		// binding names that rather than getting a slot of its own.
		name: "var_at_struct",
		src: `struct Point { x: i32, y: i32 }
function main(): i32 {
  var p = Point { x: 6, y: 1 };
  var w @ Point { x, y } = p;
  return w.x * 10 + w.y + x + y;
}`,
		want: 68,
	},
	{
		name: "let_at_struct_rename",
		src: `struct Point { x: i32, y: i32 }
function main(): i32 {
  var p = Point { x: 8, y: 3 };
  let w @ Point { x: a, y: b } = p;
  return w.x * 10 + a + b;
}`,
		want: 91,
	},
	{
		name: "var_at_tuple",
		src: `function main(): i32 {
  var w @ (a, b) = (9, 2);
  return w.0 * 10 + w.1 + a + b;
}`,
		want: 103,
	},
	{
		name: "at_struct_from_call",
		src: `struct Point { x: i32, y: i32 }
function mk(a: i32, b: i32): Point { return Point { x: a, y: b }; }
function main(): i32 {
  let w @ Point { x, y } = mk(9, 6);
  return w.x * 10 + y;
}`,
		want: 96,
	},
	{
		// A string field is refcounted: the holder and the binding co-own it,
		// and both are released on scope exit.
		name: "at_struct_string_field",
		src: `struct Named { id: i32, label: string }
function main(): i32 {
  var n = Named { id: 30, label: "abcd" };
  let w @ Named { id, label } = n;
  return id + label.len() + w.label.len();
}`,
		want: 38,
	},
}

func patternBindingSiteCaseByName(t *testing.T, name string) struct {
	name string
	src  string
	want int
} {
	t.Helper()
	for _, tc := range patternBindingSiteCases {
		if tc.name == name {
			return tc
		}
	}
	t.Fatalf("no pattern-binding-site case named %q", name)
	return patternBindingSiteCases[0]
}

func TestPatternBindingSitesInterp(t *testing.T) {
	for _, tc := range patternBindingSiteCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runInterpByte(t, tc.src); got != tc.want {
				t.Errorf("interp = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestPatternBindingSitesX86_64(t *testing.T) {
	for _, tc := range patternBindingSiteCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64(t, tc.src); code != tc.want {
				t.Errorf("native x86-64 = %d, want %d", code, tc.want)
			}
		})
	}
}

func TestPatternBindingSitesArm64(t *testing.T) {
	for _, tc := range patternBindingSiteCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, tc.src); code != tc.want {
				t.Errorf("native arm64 = %d, want %d", code, tc.want)
			}
		})
	}
}

func TestPatternBindingSitesWasm(t *testing.T) {
	for _, name := range []string{"for_struct_shorthand", "for_at_struct", "for_at_tuple", "var_at_struct", "var_at_tuple", "at_struct_string_field"} {
		tc := patternBindingSiteCaseByName(t, name)
		t.Run(name, func(t *testing.T) {
			if got := runWasm(t, tc.src); got != tc.want {
				t.Errorf("wasm = %d, want %d", got, tc.want)
			}
		})
	}
}

// A Map's key and value come off the entry cursor in separate columns, so no
// whole value exists for an `@` binding to name. Admitting the head without
// this check would bind nothing and leave the name undefined at its first use.
func TestPatternBindingSitesMapAtRejected(t *testing.T) {
	const src = `import "core/map";
function main(): i32 {
  var m: Map[string, i32] = map_new(8);
  m = m.insert("a", 1);
  var acc = 0;
  for w @ (k, v) in m { acc = acc + v; }
  return acc;
}`
	dir := t.TempDir()
	path := filepath.Join(dir, "map_at.fern")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	prog, _, err := modload.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	_, cerr := checker.Check(prog)
	if cerr == nil {
		t.Fatal("expected the Map `@` rejection, got no diagnostics")
	}
	if !strings.Contains(cerr.Error(), `no whole value for "w" to name`) {
		t.Errorf("diagnostics = %q, want the Map `@` rejection", cerr.Error())
	}
}
