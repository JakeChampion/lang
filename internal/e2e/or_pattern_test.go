// Literal or-pattern coverage (#5355): `match x { 1 | 2 | 3 => … }`.
//
// Literal or-patterns bind no names, so each `|` alternative is an
// independent literal pattern that the shared per-alternative clone-desugar
// (parseArmPatterns) expands into separate literal arms — the same
// mechanism variant or-patterns already used. `..`-ranges combine with `|`
// too. Tuple or-patterns stay restricted (they'd bind different names per
// alternative). These pin the native x86-64 binary against the interpreter
// oracle; a couple also run through wasm.
package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/modload"
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
		want: 27, // 9+9+9+0
	},
	{
		name: "literal_or_expr",
		src: `function f(x: i32): i32 {
  return match (x) { 1 | 2 => 7, 3 | 4 => 8, _ => 0 };
}
function main(): i32 { return f(2) * 10 + f(4) + f(9); }`,
		want: 78, // 7*10 + 8 + 0
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
		want: 50, // 5,0
	},
	{
		// The guard applies to every alternative: f(1) fails it (falls to _),
		// f(2) passes.
		name: "or_with_guard",
		src: `function f(x: i32): i32 {
  match (x) {
    1 | 2 | 3 when x > 1 => { return 9; },
    _ => { return 0; },
  }
  return 0 - 1;
}
function main(): i32 { return f(1) * 100 + f(2) * 10 + f(5); }`,
		want: 90, // 0,9,0
	},
	{
		// Ranges combine with `|`.
		name: "range_or",
		src: `function f(x: i32): i32 {
  match (x) {
    1..5 | 10..15 => { return 1; },
    _ => { return 0; },
  }
  return 0 - 1;
}
function main(): i32 { return f(3) * 100 + f(12) * 10 + f(7); }`,
		want: 110, // 1,1,0
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

func TestOrPatternWasm(t *testing.T) {
	for _, name := range []string{"literal_or", "range_or"} {
		tc := orPatternCaseByName(t, name)
		t.Run(name, func(t *testing.T) {
			if got := runWasm(t, tc.src); got != tc.want {
				t.Errorf("wasm = %d, want %d", got, tc.want)
			}
		})
	}
}

// Tuple or-patterns remain rejected (they'd bind different names per
// alternative) — the checker/parser must still emit P001.
func TestOrPatternTupleRejected(t *testing.T) {
	src := `function f(t: (i32, i32)): i32 {
  match (t) { (0, y) | (y, 0) => { return 1; }, _ => { return 0; } }
  return 0 - 1;
}
function main(): i32 { return 0; }`
	prog, _, err := modload.LoadSource(src)
	if err == nil {
		if _, cerr := checker.Check(prog); cerr == nil {
			t.Fatal("expected a parse/check error for a tuple or-pattern, got none")
		}
		return
	}
	// A parse error is the expected outcome too.
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
