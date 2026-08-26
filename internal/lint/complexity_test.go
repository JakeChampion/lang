package lint_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/lint"
	"github.com/jakechampion/lang/internal/parser"
)

// scoreOf parses a whole program and returns the named function's score.
func scoreOf(t *testing.T, name, src string) int {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, src)
	}
	for _, fn := range prog.Funcs {
		if fn.Body != nil && lint.DisplayName(fn) == name {
			return lint.Score(fn)
		}
	}
	t.Fatalf("no function %q in:\n%s", name, src)
	return 0
}

// The scoring model is the whole content of the metric, so every construct
// that does or does not fork is pinned individually. A traversal change
// that quietly starts or stops counting one of these moves every number in
// the repo gate; this table is what makes that a test failure and not a
// mystery.
func TestScoreModel(t *testing.T) {
	cases := []struct {
		name string
		want int
		body string
	}{
		{"straight-line", 1, `var a: i32 = 1; return a;`},
		{"if", 2, `if (n > 0) { return 1; } return 0;`},
		{"if-else", 2, `if (n > 0) { return 1; } else { return 2; }`},
		// `else if` nests a second If, so a three-way chain costs two
		// conditions — not one per arm, and not one for the whole chain.
		{"else-if chain", 3, `if (n > 0) { return 1; } else if (n < 0) { return 2; } else { return 3; }`},
		{"if-expression", 2, `return if (n > 0) { 1 } else { 2 };`},
		{"while", 2, `while (n > 0) { n = n - 1; } return n;`},
		{"c-for", 2, `for (var i: i32 = 0; i < n; i = i + 1) { n = n; } return n;`},
		{"loop", 2, `loop { break; } return n;`},
		{"and", 3, `if (n > 0 && n < 9) { return 1; } return 0;`},
		{"or", 3, `if (n > 0 || n < 9) { return 1; } return 0;`},
		{"and-or chain", 4, `if (n > 0 && n < 9 || n == 42) { return 1; } return 0;`},
		// Comparison and arithmetic are not forks; only the two
		// short-circuiting operators are.
		{"non-logical binary", 1, `return n + 1 * 2 - 3;`},
		{"break and continue", 2, `while (n > 0) { n = n - 1; continue; } return n;`},
		// An assert desugars to an If but states a precondition rather
		// than steering the reader down a second path, and -O deletes it.
		{"assert", 1, `assert(n > 0); return n;`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "function f(n: i32): i32 {\n" + tc.body + "\n}\n"
			if got := scoreOf(t, "f", src); got != tc.want {
				t.Errorf("score = %d, want %d for:\n%s", got, tc.want, src)
			}
		})
	}
}

// `todo;` desugars to a `loop`, which would otherwise make every unwritten
// stub score as though it branched.
func TestScoreTodoIsNotALoop(t *testing.T) {
	if got := scoreOf(t, "f", "function f(n: i32): i32 {\n todo;\n}\n"); got != 1 {
		t.Errorf("score = %d, want 1 — a `todo` stub has no control flow", got)
	}
}

// Each arm that can fail to match is a test; the `_` fall-through is not,
// and a guard is a second condition on top of its pattern.
func TestScoreMatchArms(t *testing.T) {
	cases := []struct {
		name string
		want int
		src  string
	}{
		{"two variants", 3, `
enum E { A(i32), B(i32) }
function f(e: E): i32 {
  match (e) {
    A(x) => { return x; },
    B(x) => { return x + 1; }
  }
}
`},
		{"wildcard is the fall-through", 2, `
enum E { A(i32), B(i32) }
function f(e: E): i32 {
  match (e) {
    A(x) => { return x; },
    _ => { return 0; }
  }
}
`},
		{"guard adds a condition", 3, `
enum E { A(i32), B(i32) }
function f(e: E): i32 {
  match (e) {
    A(x) when x > 0 => { return x; },
    _ => { return 0; }
  }
}
`},
		// `if let` is a match with a synthesised wildcard else-arm, so it
		// costs exactly what the equivalent `if` costs.
		{"if let", 2, `
enum E { A(i32), B(i32) }
function f(e: E): i32 {
  if let A(x) = e { return x; }
  return 0;
}
`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := scoreOf(t, "f", tc.src); got != tc.want {
				t.Errorf("score = %d, want %d for:\n%s", got, tc.want, tc.src)
			}
		})
	}
}

func TestScoreForEachAndTry(t *testing.T) {
	src := `
function f(xs: i32[]): i32 {
  var t: i32 = 0;
  for x in xs { t = t + x; }
  return t;
}
`
	if got := scoreOf(t, "f", src); got != 2 {
		t.Errorf("foreach score = %d, want 2", got)
	}

	// `?` returns early on the error path — a fork the reader must
	// account for even though nothing is spelled `if`.
	try := `
function g(o: Option[i32]): Option[i32] {
  var v: i32 = o?;
  return Some(v + 1);
}
`
	if got := scoreOf(t, "g", try); got != 2 {
		t.Errorf("try score = %d, want 2", got)
	}
}

// A lambda's branches count into the function that spells it: an inline
// closure is code the reader walks past, so hiding an `if` in one must not
// make the enclosing function read as simpler than it is.
func TestScoreLambdaFoldsIntoEnclosingFunction(t *testing.T) {
	src := `
function f(xs: i32[]): i32[] {
  return map(xs, (x: i32) => { if (x > 0) { return x; } return 0 - x; });
}
pub function map[T, U](xs: T[], fn: (T) => U): U[] {
  var out: U[] = [];
  return out;
}
`
	if got := scoreOf(t, "f", src); got != 2 {
		t.Errorf("score = %d, want 2 — the lambda's `if` counts into f", got)
	}
}

// Methods are reported under `Type.method`; the checker's mangled
// `__method_T_m` spelling is an implementation detail, and lints run
// before it exists anyway.
func TestDisplayNameUsesReceiver(t *testing.T) {
	src := `
struct Counter { n: i32 }
function (c: Counter) bump(): i32 {
  if (c.n > 0) { return c.n + 1; }
  return 1;
}
`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var names []string
	for _, fn := range prog.Funcs {
		names = append(names, lint.DisplayName(fn))
	}
	if !contains(names, "Counter.bump") {
		t.Errorf("names = %v, want one to be Counter.bump", names)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// The rule reports strictly above its limit: a function scoring exactly
// Max is within it.
func TestComplexityRuleBoundary(t *testing.T) {
	src := "function f(n: i32): i32 {\nif (n > 0) { return 1; }\nif (n < 0) { return 2; }\nreturn 0;\n}\n"
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		max   int
		fires bool
	}{{2, true}, {3, false}, {4, false}} {
		cfg := lint.NewConfig()
		if err := cfg.SetOption("cyclomatic-complexity.max", strconv.Itoa(tc.max)); err != nil {
			t.Fatal(err)
		}
		fs, err := lint.File(cfg, "t.fern", src, prog)
		if err != nil {
			t.Fatal(err)
		}
		if got := len(fs) > 0; got != tc.fires {
			t.Errorf("max=%d: fired=%v, want %v (score is 3)", tc.max, got, tc.fires)
		}
		if tc.fires && !strings.Contains(fs[0].Msg, "complexity of 3") {
			t.Errorf("max=%d: message %q should carry the score", tc.max, fs[0].Msg)
		}
	}
}
