package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// checker.fern's bind_stmt is check_stmt's SCOPE half without the inference
// (#8181). Twelve diagnostic walkers thread a Scope through a body and throw
// the inferred type away, so running the whole type checker to obtain that
// Scope had the program type-checked thirteen times over — 14.41% of a
// `checker.fern` compile went into scope-only re-walks.
//
// What that trades away is a single implementation. If the two steppers ever
// disagree about what a statement binds, the type-check pass resolves a name
// the twelve diagnostic passes cannot see (or the reverse), and the symptom is
// a spurious or missing diagnostic somewhere else entirely — nowhere near the
// statement that caused it.
//
// So this runs both over the same statements and compares the bindings they
// leave, on a corpus that covers every ast.Stmt form and every shape of `var`
// the checker binds differently. bind_stmt_parity_run.fern does the walk: for
// each statement of each function body (recursing into nested blocks as the
// walkers do) and of the top level, in the scope check_module would walk it
// in, it reports every name or type the two disagree on.
//
// Its companion is TestCheckStmtAndBindStmtBindTheSameStatementForms
// (internal/sourcelint), which reads the two functions' arms out of the source.
// The division is deliberate and neither half covers the other: this gate
// catches a wrong binding RULE (an annotation bound to the inferred type
// rather than the declared one, say), but only for forms the corpus contains;
// the lint catches a whole binding FORM appearing in one stepper and not the
// other, which by definition no fixed corpus can contain in advance.
//
// It drives the driver under the native interpreter, so it needs no cross
// toolchain and runs on every host.
type bindStmtParityCase struct {
	name string
	src  string
	// minCompared is a floor on the statements the driver reports comparing.
	// Without it a row that stopped parsing — or a walk that stopped
	// descending — would agree vacuously and pass.
	minCompared int
}

var bindStmtParityCases = []bindStmtParityCase{
	{
		// Every shape of `var` the annotated path can take, including the two
		// check_stmt reports a type error for and still binds the ANNOTATION
		// under (unknown annotation, init/annotation mismatch): the error goes
		// in the `ty` all twelve callers discard, so bind_stmt must not be
		// tempted to disagree about the scope there.
		name: "var forms",
		src: `struct P { x: i32, y: string }
struct G { v: i32 }
type Shape = P | G;
function mk(): P { return P { x: 1, y: "a" }; }
function main(): i32 {
  var inferred = 41;
  var annotated: i32 = 1;
  var s: string = "hi";
  var sv: str = "view";
  var f: f64 = 1.5;
  var b: boolean = true;
  var arr: i32[] = [1, 2, 3];
  var arr2: string[][] = [];
  var p: P = mk();
  var u: Shape = mk();
  var m: Map[string, i32] = {};
  var tup: (i32, string) = (1, "a");
  var fnv: fn = (z: i32): i32 => { return z; };
  var unknownAnn: NoSuchType = 1;
  var mismatch: i32 = "not an i32";
  var slice: [i32] = arr;
  return annotated + inferred + p.x + arr[0];
}`,
		minCompared: 18,
	},
	{
		// The destructuring path: bind_destructure_names is the whole binding,
		// and it takes the statement and the scope with no inferred type at
		// all. Tuple, struct, single-field struct (no comma, so the marker is
		// what says it is a destructure) and the `@` whole-value binding.
		name: "destructuring",
		src: `struct P { x: i32, y: i32 }
struct One { only: i32 }
function main(): i32 {
  var p: P = P { x: 1, y: 2 };
  let (a, b) = (1, "two");
  let P { x, y } = p;
  let One { only } = One { only: 3 };
  let whole @ P { x: rx, y: ry } = p;
  var t3 = (1, 2, 3);
  let (c, d, e) = t3;
  return a + x + y + only + rx + ry + c + d + e + whole.x;
}`,
		minCompared: 8,
	},
	{
		// The nested bodies the walkers recurse into, plus the forms that bind
		// nothing and must still hand the scope back untouched.
		name: "nested bodies",
		src: `function side(n: i32): i32 { return n; }
function main(): i32 {
  var n: i32 = 0;
  side(n);
  if (n > 0) { var t: i32 = 1; n = n + t; } else { var e: string = "x"; n = n + e.len(); }
  while (n < 5) { var w = n; if (w > 3) { break; } n = n + 1; continue; }
  var arr: i32[] = [1, 2];
  for x in arr { var q: i32 = x; n = n + q; }
  defer side(n);
  return n;
}`,
		minCompared: 21,
	},
	{
		// The recursive-local self-binding: `var f = function(…) { … }` must
		// have `f` in scope while its own initialiser is inferred, or the
		// self-call inside it resolves to nothing.
		name: "lambdas and recursive locals",
		src: `function main(): i32 {
  function rec(n: i32): i32 { if (n <= 0) { return 0; } return rec(n - 1); }
  var lam = (z: i32): i32 => { return z + 1; };
  var lam2: fn = (z: f64): f64 => { return z; };
  var arrfn: fn[] = [];
  var used = lam(1) + rec(2);
  return used + arrfn.len();
}`,
		minCompared: 6,
	},
	{
		name: "top-level statements",
		src: `struct P { x: i32 }
var top: i32 = 7;
var topInferred = top + 1;
let (ta, tb) = (1, 2);
function main(): i32 { return top + topInferred + ta + tb; }`,
		minCompared: 4,
	},
	{
		// A statement the parser could not read becomes StmtUnknown, which
		// binds nothing — and the statements after it still have to be walked.
		name: "unparseable statement",
		src: `function main(): i32 {
  var arr: i32[] = [1];
  for in arr { var q = 1; }
  var after: i32 = 2;
  return after;
}`,
		minCompared: 7,
	},
	{
		name: "match arms",
		src: `struct P { x: i32 }
struct Q { r: string }
type Shape = P | Q;
function main(): i32 {
  var sh: Shape = P { x: 2 };
  var n: i32 = 0;
  match (sh) {
    P(pp) => { var mv: i32 = pp.x; n = n + mv; },
    Q(qq) => { var mq = qq.r; n = n + mq.len(); }
  }
  match (n) { 0 => { var z = 1; n = n + z; }, _ => { var o: i32 = 2; n = n + o; } }
  return n;
}`,
		minCompared: 13,
	},
}

// stmtUnionRE reads the `pub type Stmt = A | B | …;` declaration out of
// ast.fern. The corpus is held to covering every member: a new statement form
// that no row exercises would be compared by neither stepper, which is exactly
// the blind spot this gate exists to close.
var stmtUnionRE = regexp.MustCompile(`(?s)pub type Stmt =(.*?);`)

func astStmtForms(t *testing.T) []string {
	t.Helper()
	p, err := filepath.Abs("../../examples/self_host/ast.fern")
	if err != nil {
		t.Fatalf("abs ast.fern: %v", err)
	}
	src, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read ast.fern: %v", err)
	}
	m := stmtUnionRE.FindStringSubmatch(string(src))
	if m == nil {
		t.Fatalf("ast.fern no longer declares `pub type Stmt = …;`, so this gate cannot tell which statement forms the corpus has to cover")
	}
	var out []string
	for _, part := range strings.Split(m[1], "|") {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		if !strings.HasPrefix(name, "Stmt") {
			t.Fatalf("read %q out of ast.fern's Stmt union, which is not a statement form — the declaration's shape changed and this gate is reading it wrong", name)
		}
		out = append(out, name)
	}
	// A mis-read that yielded one or two names would let the coverage check
	// below pass on a corpus covering almost nothing.
	if len(out) < 8 {
		t.Fatalf("ast.fern's Stmt union read as only %d forms (%s); the coverage check below would be vacuous", len(out), strings.Join(out, ", "))
	}
	sort.Strings(out)
	return out
}

func TestSelfHostBindStmtScopeParity(t *testing.T) {
	interpBin := buildLangBinForInterp(t)
	driver, err := filepath.Abs("../../examples/self_host/bind_stmt_parity_run.fern")
	if err != nil {
		t.Fatalf("abs driver path: %v", err)
	}

	covered := map[string]bool{}
	for _, tc := range bindStmtParityCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(interpBin, "-interp", driver)
			cmd.Stdin = strings.NewReader(tc.src)
			var stderr strings.Builder
			cmd.Stderr = &stderr
			out, runErr := cmd.Output()

			var diffs []string
			compared := -1
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				switch {
				case line == "":
				case strings.HasPrefix(line, "# compared "):
					n, err := strconv.Atoi(strings.TrimPrefix(line, "# compared "))
					if err != nil {
						t.Fatalf("unreadable count line %q", line)
					}
					compared = n
				case strings.HasPrefix(line, "# forms "):
					for _, f := range strings.Split(strings.TrimPrefix(line, "# forms "), ",") {
						covered[f] = true
					}
				default:
					diffs = append(diffs, line)
				}
			}

			if compared < 0 {
				t.Fatalf("the driver reported no statement count — it did not run to the end.\nstdout:\n%s\nstderr:\n%s\nerr: %v", out, stderr.String(), runErr)
			}
			if compared < tc.minCompared {
				t.Errorf("the driver compared %d statements, fewer than the %d this row is written to reach — the program stopped parsing, or the walk stopped descending, so an agreement here proves nothing.\nstderr:\n%s",
					compared, tc.minCompared, stderr.String())
			}
			if len(diffs) > 0 {
				t.Errorf("bind_stmt and check_stmt left different bindings on %d statement(s):\n    %s\n\n"+
					"    bind_stmt is check_stmt's scope half, and twelve diagnostic walkers take their scope from it. A name that resolves\n"+
					"    in one and not the other shows up as a spurious or missing diagnostic far from the statement that caused it.\n"+
					"    Move the two into step in examples/self_host/checker.fern; do not adjust the corpus to avoid the shape.",
					len(diffs), strings.Join(diffs, "\n    "))
			}
			if runErr != nil && len(diffs) == 0 && compared >= tc.minCompared {
				t.Errorf("driver exited non-zero with nothing to report: %v\nstderr:\n%s", runErr, stderr.String())
			}
		})
	}

	var missing []string
	for _, form := range astStmtForms(t) {
		if !covered[form] {
			missing = append(missing, form)
		}
	}
	if len(missing) > 0 {
		t.Errorf("no corpus row reaches %s, so bind_stmt and check_stmt are never compared on %s.\n"+
			"    Add a row that contains one (the driver reports the forms it walked on its `# forms` line).",
			strings.Join(missing, ", "), pluralForms(len(missing)))
	}
}

func pluralForms(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}
