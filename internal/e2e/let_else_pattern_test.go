// `let … else` consumes the shared pattern grammar (#5356), the sibling of
// the `if let` unification. The parser desugars
//
//	let PAT = E else { D };  <rest of block>
//
// into `match (E) { PAT => { <rest of block> }, _ => { D } }` tagged
// ast.Match.Origin "let_else". Putting the block's remainder in the success
// arm is what keeps the bindings live for the rest of the block without a
// bespoke node — they are arm bindings, and the arm spans everything that
// follows. These pin each pattern form across the backends against the
// interpreter oracle, plus the else-must-diverge rule the desugar has to
// keep carrying.
package e2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/diag"
	"github.com/jakechampion/lang/internal/modload"
)

var letElsePatternCases = []struct {
	name string
	src  string
	want int
}{
	{
		// The baseline: bindings live for the rest of the block, and the
		// else branch diverges on a miss.
		name: "variant_binding_outlives_stmt",
		src: `enum O { Has(i32), Nil }
function f(o: O): i32 {
  let Has(v) = o else { return 1; };
  var doubled: i32 = v * 2;
  return doubled;
}
function main(): i32 { return f(Has(5)) * 10 + f(Nil); }`,
		want: 101, // 10*10 + 1
	},
	{
		// `@` binding: `w` is the whole value, `v` the payload — both live
		// for the rest of the block.
		name: "at_binding",
		src: `enum Box { Full(i32), Empty }
function total(b: Box): i32 { match (b) { Full(v) => { return v; }, Empty => { return 0; } } return 0; }
function f(b: Box): i32 {
  let w @ Full(v) = b else { return 0; };
  return total(w) * 10 + v;
}
function main(): i32 { return f(Full(3)); }`,
		want: 33,
	},
	{
		// Or-pattern: each alternative binds its own name set, and the rest
		// of the block is cloned per alternative.
		name: "or_pattern",
		src: `enum E { A(i32), B(i32), C }
function pick(e: E): i32 {
  let A(x) | B(x) = e else { return 0; };
  return x + 1;
}
function main(): i32 { return pick(B(5)) * 10 + pick(A(3)) + pick(C); }`,
		want: 64, // 6*10 + 4 + 0
	},
	{
		// Nested pattern: the else block doubles as the inner fallthrough,
		// so a `Wrap(Err2)` runs it rather than falling off the merged
		// inner match.
		name: "nested_pattern",
		src: `enum Inner { Ok2(i32), Err2 }
enum Outer { Wrap(Inner), Bare }
function f(o: Outer): i32 {
  let Wrap(Ok2(n)) = o else { return 1; };
  return n;
}
function main(): i32 { return f(Wrap(Ok2(3))) * 10 + f(Wrap(Err2)) * 2 + f(Bare); }`,
		want: 33, // 3*10 + 1*2 + 1
	},
	{
		name: "qualified_variant",
		src: `enum Color { Red(i32), Blue }
function f(c: Color): i32 {
  let Color.Red(n) = c else { return 0; };
  return n;
}
function main(): i32 { return f(Red(6)); }`,
		want: 6,
	},
	{
		// A `break` also satisfies the divergence rule, and the success
		// arm is the rest of the loop body.
		name: "else_breaks",
		src: `enum O { Has(i32), Nil }
function main(): i32 {
  var xs: O[] = [Has(1), Has(2), Nil, Has(4)];
  var total: i32 = 0;
  var i: i32 = 0;
  while (i < xs.len()) {
    let Has(v) = xs[i] else { break; };
    total = total + v;
    i = i + 1;
  }
  return total;
}`,
		want: 3, // 1 + 2, then Nil breaks
	},
	{
		// Two let-elses in a row: the second is parsed inside the first's
		// success arm, and both sets of bindings stay live.
		name: "chained",
		src: `enum O { Has(i32), Nil }
function f(a: O, b: O): i32 {
  let Has(x) = a else { return 100; };
  let Has(y) = b else { return 200; };
  return x * 10 + y;
}
function main(): i32 { return f(Has(3), Has(4)); }`,
		want: 34,
	},
	{
		// A let-else nested inside a block binds for the rest of THAT
		// block only — the outer block's statements are untouched.
		name: "inner_block_scope",
		src: `enum O { Has(i32), Nil }
function f(o: O): i32 {
  var acc: i32 = 0;
  {
    let Has(v) = o else { return 9; };
    acc = v;
  }
  return acc + 1;
}
function main(): i32 { return f(Has(5)) * 10 + f(Nil); }`,
		want: 69, // 6*10 + 9
	},
	{
		// Tuple and struct destructuring keep their own irrefutable form
		// (no `else`) — the let-else branch must not steal them.
		name: "destructure_forms_untouched",
		src: `struct P { x: i32, y: i32 }
function main(): i32 {
  let (a, b) = (3, 4);
  let P { x, y } = P { x: 1, y: 2 };
  return a * 10 + b + x * 100 + y;
}`,
		want: 136, // 30 + 4 + 100 + 2
	},
}

func TestLetElsePatternInterp(t *testing.T) {
	for _, tc := range letElsePatternCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runInterpByte(t, tc.src); got != tc.want {
				t.Errorf("interp = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestLetElsePatternX86_64(t *testing.T) {
	for _, tc := range letElsePatternCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64(t, tc.src); code != tc.want {
				t.Errorf("native x86-64 = %d, want %d", code, tc.want)
			}
		})
	}
}

func TestLetElsePatternArm64(t *testing.T) {
	for _, tc := range letElsePatternCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, tc.src); code != tc.want {
				t.Errorf("native arm64 = %d, want %d", code, tc.want)
			}
		})
	}
}

func TestLetElsePatternWasm(t *testing.T) {
	for _, name := range []string{"variant_binding_outlives_stmt", "at_binding", "or_pattern", "nested_pattern", "chained"} {
		tc := letElsePatternCaseByName(t, name)
		t.Run(name, func(t *testing.T) {
			if got := runWasm(t, tc.src); got != tc.want {
				t.Errorf("wasm = %d, want %d", got, tc.want)
			}
		})
	}
}

// The pattern-binding diagnostics survive the desugar. The
// else-must-diverge rule is the one that only `let … else` carries: it is
// what guarantees the bindings are live for the rest of the block, so a
// non-diverging else is E022 — and only E022, not a second complaint from
// the missing-return analysis about the same mistake.
func TestLetElsePatternDiagnostics(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
		deny []string
	}{
		{
			name: "else_must_diverge",
			src: `enum O { Has(i32), Nil }
function main(): i32 { var o: O = Nil; let Has(v) = o else { var x: i32 = 1; }; return v; }`,
			want: []string{"E022"},
			deny: []string{"E052"},
		},
		{
			name: "else_loop_diverges_ok",
			src: `enum O { Has(i32), Nil }
function main(): i32 { var o: O = Nil; let Has(v) = o else { loop { } }; return v; }`,
		},
		{
			name: "source_not_enum",
			src:  `function main(): i32 { var n: i32 = 5; let Has(v) = n else { return 0; }; return 0; }`,
			want: []string{"E022"},
		},
		{
			name: "source_struct",
			src: `struct P { x: i32 }
function main(): i32 { var p: P = P { x: 1 }; let Has(v) = p else { return 0; }; return 0; }`,
			want: []string{"E022"},
		},
		{
			name: "unknown_variant",
			src: `enum O { Has(i32), Nil }
function main(): i32 { var o: O = Nil; let Bogus(v) = o else { return 0; }; return 0; }`,
			want: []string{"E014"},
		},
		{
			name: "payload_arity",
			src: `enum O { Has(i32), Nil }
function main(): i32 { var o: O = Nil; let Has(a, b) = o else { return 0; }; return 0; }`,
			want: []string{"E015"},
		},
	}
	dir := t.TempDir()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name+".fern")
			if err := os.WriteFile(path, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			prog, _, err := modload.Load(path)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			_, cerr := checker.Check(prog)
			if len(tc.want) == 0 {
				if cerr != nil {
					t.Fatalf("expected no error, got %v", diag.Format(path, tc.src, cerr))
				}
				return
			}
			if cerr == nil {
				t.Fatalf("expected %v, got no error", tc.want)
			}
			formatted := diag.Format(path, tc.src, cerr)
			got := ifLetCodeRE.FindAllString(formatted, -1)
			for _, w := range tc.want {
				if !containsStr(got, w) {
					t.Errorf("codes = %v, want %s\n%s", got, w, formatted)
				}
			}
			for _, d := range tc.deny {
				if containsStr(got, d) {
					t.Errorf("codes = %v, must not include %s\n%s", got, d, formatted)
				}
			}
		})
	}
}

func letElsePatternCaseByName(t *testing.T, name string) struct {
	name string
	src  string
	want int
} {
	t.Helper()
	for _, tc := range letElsePatternCases {
		if tc.name == name {
			return tc
		}
	}
	t.Fatalf("no let-else pattern case named %q", name)
	return letElsePatternCases[0]
}
