package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/parser"
)

// nestedParens is a program whose `return` nests n levels of parentheses.
// Parens are the cheapest nesting per byte, so this reaches a given depth
// with the smallest source both front ends have to lex.
func nestedParens(n int) string {
	return "function main(): i32 { return " + strings.Repeat("(", n) + "0" + strings.Repeat(")", n) + "; }\n"
}

// nativeAcceptsNesting reports whether the native parser takes n levels.
func nativeAcceptsNesting(n int) bool {
	_, err := parser.Parse(nestedParens(n))
	return err == nil
}

// nativeNestingLimit finds the deepest nesting the native parser accepts, by
// bisection rather than a hard-coded number: the budget is a count of parse
// recursion units, not of `(`, so the level it works out to is an emergent
// property that a parser refactor can shift. The test that matters is that
// both front ends shift together.
func nativeNestingLimit(t *testing.T) int {
	t.Helper()
	lo, hi := 1, 1<<14 // hi must be past the bound
	if nativeAcceptsNesting(hi) {
		t.Fatalf("native still accepts %d levels — raise the bisection ceiling", hi)
	}
	if !nativeAcceptsNesting(lo) {
		t.Fatalf("native rejects %d level(s) — the bound is not where this test assumes", lo)
	}
	for hi-lo > 1 {
		mid := (lo + hi) / 2
		if nativeAcceptsNesting(mid) {
			lo = mid
		} else {
			hi = mid
		}
	}
	return lo
}

// The self-host parser recursed without a bound, so deeply nested input took
// the compiled binary down with SIGSEGV — the same defect #7941 fixed in the
// native parser, and worse, because a Fern binary has no fatal-error
// diagnostic to print on the way out. Measured before the fix: 5000 levels
// compiled fine, 20000 and 60000 both segfaulted.
//
// Two things have to hold, and the second is the one that needs a test.
// Refusing to recurse is easy; refusing at the SAME DEPTH native refuses is
// what keeps a program from compiling under one front end and failing under
// the other. docs/NATIVE-CONVERGENCE.md makes the self-host the definition
// after the freeze, so a split here would be a split in the language.
func TestSelfHostDeepNestingMatchesNativeAndDoesNotCrash(t *testing.T) {
	requireSelfHostDiffLeg(t)
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the self-host CLI driver runs only natively (argv paths)")
	}
	limit := nativeNestingLimit(t)
	t.Logf("native accepts %d levels of nesting and refuses %d", limit, limit+1)

	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	// compile runs the self-host CLI over a program nested n deep and
	// returns its combined output. It compiles rather than `-check`ing:
	// the parse-unknown scan that turns the sentinel into a diagnostic runs
	// on the codegen path, so `-check` reports the generic checker message
	// for an unknown node instead.
	compile := func(t *testing.T, n int) (string, bool) {
		t.Helper()
		work := t.TempDir()
		src := filepath.Join(work, "main.fern")
		if err := os.WriteFile(src, []byte(nestedParens(n)), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		cmd := exec.Command(fernBin, "-target", "x86-64-linux", "-o", filepath.Join(work, "out.s"), src)
		cmd.Env = append(os.Environ(), "FERN_STDLIB_ROOT="+stdlibRoot)
		r := runSelfHostBin(cmd, "")
		if !r.exited {
			t.Fatalf("%d levels: self-host died on a signal (%s) instead of reporting — "+
				"the parser is recursing past its bound again", n, r.state)
		}
		if r.timedOut {
			t.Fatalf("%d levels: self-host hung", n)
		}
		return r.stdout + r.stderr, r.exit == 0
	}

	t.Run("at the limit it still compiles", func(t *testing.T) {
		if out, ok := compile(t, limit); !ok {
			t.Errorf("native accepts %d levels but the self-host refused them:\n%s", limit, out)
		}
	})

	t.Run("one past the limit it reports P005", func(t *testing.T) {
		out, ok := compile(t, limit+1)
		if ok {
			t.Fatalf("native refuses %d levels but the self-host accepted them", limit+1)
		}
		if !strings.Contains(out, "P005") {
			t.Errorf("want the P005 nesting diagnostic native reports, got:\n%s", out)
		}
	})

	// Far past the bound is the case that used to segfault. compile() fails
	// the test on a signal, so reaching the assertion is most of the point.
	for _, n := range []int{20000, 60000} {
		t.Run("no crash at "+strconv.Itoa(n), func(t *testing.T) {
			out, ok := compile(t, n)
			if ok {
				t.Fatalf("%d levels compiled clean; expected a refusal", n)
			}
			if !strings.Contains(out, "P005") {
				t.Errorf("want P005 at %d levels, got:\n%s", n, out)
			}
		})
	}
}
