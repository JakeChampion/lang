package lint_test

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/lint"
	"github.com/jakechampion/lang/internal/parser"
)

// The complexity limit this repository's own Fern sources are held to.
// It is the rule's default, not a second number invented for us: a limit
// the tool ships and the project does not keep would say the default is
// not meant seriously.
const repoLimit = lint.DefaultMaxComplexity

// tree is one body of first-party Fern source under the limit, with the
// two numbers that hold it to a RATCHET.
//
// Neither number is a permission slip. Both are the measured state of the
// tree, and TestRepoComplexityRatchet fails when either MOVES — up, because
// a change made the tree worse; down, because a change made it better and
// the recorded number now says less than it could. So the gate is real from
// the day it lands (nothing may regress) and cannot quietly go slack
// (an improvement is banked, in the same diff that earned it).
//
// To exempt a single function instead, annotate the function — a
// `// fern-lint: allow cyclomatic-complexity` comment above it, with a line
// saying why. That is reviewable where it applies; this table is not the
// place for a per-function exception.
type tree struct {
	name string
	dir  string
	// ceiling is the highest complexity any function in the tree reaches,
	// counting suppressed functions too. The asymmetry with excess is
	// deliberate: an `allow` says "this one may exceed the limit", never
	// "this one may be worse than anything here has ever been", so a new
	// function past the ceiling fails the gate however it is annotated.
	ceiling int
	// excess is the total distance over the limit: the sum of
	// (score - repoLimit) across every UNSUPPRESSED function above it.
	//
	// Deliberately not a COUNT of functions over the limit, which is the
	// obvious metric and the wrong one: splitting a 472-fork monster into
	// ten readable 40-fork helpers takes that count from 1 to 10, so the
	// gate would report the single most valuable refactor available as a
	// regression and block it. Summed distance calls the same split what
	// it is — 462 down to 300 — and keeps falling all the way to zero as
	// the pieces get simpler. It is monotone in the direction the campaign
	// actually moves.
	excess int
}

var trees = []tree{
	{name: "self-host compiler", dir: "../../examples/self_host", ceiling: 477, excess: 19869},
	{name: "stdlib", dir: "../stdlib/std", ceiling: 68, excess: 780},
}

func TestRepoComplexityRatchet(t *testing.T) {
	for _, tr := range trees {
		t.Run(tr.name, func(t *testing.T) {
			worst, worstName, excess := measureTree(t, tr.dir)

			if excess > tr.excess {
				t.Errorf("total complexity over the limit of %d is %d, up from the recorded %d.\n"+
					"A change added complexity above the limit. Split the new function up, or annotate it\n"+
					"with `// fern-lint: allow cyclomatic-complexity` and a line saying why.\n"+
					"Run: go run ./cmd/fern -lint %s", repoLimit, excess, tr.excess, tr.dir)
			} else if excess < tr.excess {
				t.Errorf("total complexity over the limit of %d is %d, down from the recorded %d.\n"+
					"Bank the improvement: set this tree's excess to %d in %s.",
					repoLimit, excess, tr.excess, excess, gateFile())
			}

			if worst > tr.ceiling {
				t.Errorf("`%s` reaches a complexity of %d, above the tree's recorded ceiling of %d.\n"+
					"Nothing here may become harder to read than the worst function already is.",
					worstName, worst, tr.ceiling)
			} else if worst < tr.ceiling {
				t.Errorf("the worst function in this tree is now `%s` at %d, below the recorded ceiling of %d.\n"+
					"Bank the improvement: set this tree's ceiling to %d in %s.",
					worstName, worst, tr.ceiling, worst, gateFile())
			}
		})
	}
}

func gateFile() string { return "internal/lint/repo_gate_test.go" }

// complexityRule names the one rule this gate counts, so adding a second
// rule to the registry cannot silently inflate the excess.
var complexityRule = (&lint.Complexity{}).Name()

// measureTree returns the tree's worst function, its name, and the total
// distance over repoLimit summed across the functions above it.
//
// The excess comes from the linter proper rather than from Score directly,
// so a `// fern-lint: allow` comment in a Fern source exempts that function
// here exactly as it does on the command line — one suppression mechanism,
// not a second one that only the gate honours.
func measureTree(t *testing.T, dir string) (worst int, worstName string, excess int) {
	t.Helper()
	cfg := lint.NewConfig()
	if err := cfg.SetOption("cyclomatic-complexity.max", fmt.Sprint(repoLimit)); err != nil {
		t.Fatal(err)
	}

	files := fernFiles(t, dir)
	if len(files) == 0 {
		t.Fatalf("no .fern sources under %s — the gate would pass vacuously", dir)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		prog, err := parser.Parse(src)
		if err != nil {
			t.Fatalf("%s: first-party source must parse: %v", f, err)
		}
		findings, err := lint.File(cfg, f, src, prog)
		if err != nil {
			t.Fatal(err)
		}
		for _, fd := range findings {
			switch fd.Rule {
			case lint.DirectiveRule:
				// A directive that silences nothing is a mistake in
				// first-party source, not a finding to count.
				t.Errorf("%s:%d: %s", fd.File, fd.Pos.Line, fd.Msg)
			case complexityRule:
				excess += fd.Value - repoLimit
			}
		}
		// The ceiling needs the score itself, which a finding reports as
		// prose; take it from the rule's own scorer instead of parsing
		// the message back out.
		for _, fn := range prog.Funcs {
			if fn.Body == nil {
				continue
			}
			if s := lint.Score(fn); s > worst {
				worst, worstName = s, lint.DisplayName(fn)
			}
		}
	}
	return worst, worstName, excess
}

func fernFiles(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".fern") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}
