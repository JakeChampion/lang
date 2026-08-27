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

// tolerance is how far a tree may drift from its recorded numbers before
// the gate calls it a regression.
//
// It is not slack for the sake of it — it is sized from what this tree
// actually does. An earlier draft of this gate was exact in both directions,
// and measuring it against real main traffic showed why that cannot work
// here: in one two-hour window main landed rc commits that moved the
// ceiling 468 -> 477 (+1.9%) and the excess 19847 -> 19869 -> 19878
// (+0.11%, then +0.05%). A full CI run on a PR takes about three and a half
// hours. So an exact gate is stale before it can land, and once landed would
// red-light main for whoever pushed the next rc commit — a gate nobody can
// keep, which is the same failure as a limit nobody can meet.
//
// Five per cent absorbs that churn and still bites: one new 400-fork
// function is +2% of the excess on its own, and a ceiling past 500 fails.
// The shape — a checked-in baseline with a tolerance, growth fatal, both
// directions reported — is the one `scripts/ci-check-perf` and
// `scripts/ci-check-driver-sizes` already use for the same reason.
const tolerance = 0.05

// tree is one body of first-party Fern source held to the limit.
//
// The numbers are the measured state, not a permission slip. Growth past
// `tolerance` FAILS. A shrink past it does not fail — it logs, asking for the
// improvement to be banked. That asymmetry is deliberate: making an unrelated
// PR red because it happened to simplify something is how a gate gets
// disabled. A stale-low baseline only makes the gate stricter, so it is safe
// in the direction it rots.
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
	{name: "self-host compiler", dir: "../../examples/self_host", ceiling: 411, excess: 18615},
	{name: "stdlib", dir: "../stdlib/std", ceiling: 68, excess: 780},
}

// over reports whether measured has grown past want by more than tolerance.
func over(measured, want int) bool {
	return float64(measured) > float64(want)*(1+tolerance)
}

// under reports whether measured has fallen below want by more than
// tolerance — worth banking, never worth failing.
func under(measured, want int) bool {
	return float64(measured) < float64(want)*(1-tolerance)
}

func TestRepoComplexityRatchet(t *testing.T) {
	for _, tr := range trees {
		t.Run(tr.name, func(t *testing.T) {
			worst, worstName, excess := measureTree(t, tr.dir)

			if over(excess, tr.excess) {
				t.Errorf("total complexity over the limit of %d is %d, more than %.0f%% above the recorded %d.\n"+
					"A change added real complexity. Split the new function up, or annotate it with\n"+
					"`// fern-lint: allow cyclomatic-complexity` and a line saying why.\n"+
					"Run: go run ./cmd/fern -lint %s", repoLimit, excess, tolerance*100, tr.excess, tr.dir)
			} else if under(excess, tr.excess) {
				t.Logf("total complexity over the limit of %d is %d, well below the recorded %d.\n"+
					"Worth banking: set this tree's excess to %d in %s.",
					repoLimit, excess, tr.excess, excess, gateFile())
			}

			if over(worst, tr.ceiling) {
				t.Errorf("`%s` reaches a complexity of %d, more than %.0f%% above the tree's recorded ceiling of %d.\n"+
					"Nothing here may become materially harder to read than the worst function already is.",
					worstName, worst, tolerance*100, tr.ceiling)
			} else if under(worst, tr.ceiling) {
				t.Logf("the worst function in this tree is now `%s` at %d, well below the recorded ceiling of %d.\n"+
					"Worth banking: set this tree's ceiling to %d in %s.",
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

// The tolerance is the whole reason this gate can live on a branch that takes
// hours to land. These pin its two edges with the numbers that motivated it:
// main moved the self-host excess by +22 and then +9 against ~19870 inside one
// two-hour window, and moved the ceiling 468 -> 477.
func TestToleranceAbsorbsMainsChurnButNotARegression(t *testing.T) {
	const (
		baseExcess  = 19869
		baseCeiling = 468
	)
	cases := []struct {
		name     string
		measured int
		want     int
		fails    bool
	}{
		{"real main churn: +22 excess", baseExcess + 22, baseExcess, false},
		{"real main churn: +9 excess", baseExcess + 9, baseExcess, false},
		{"real main churn: ceiling 468 -> 477", 477, baseCeiling, false},
		// One new 400-fork function is +390 excess, about 2% — under the
		// band on its own. Enough of them, or one truly enormous function,
		// is what this catches.
		{"a 2000-fork regression", baseExcess + 2000, baseExcess, true},
		{"ceiling doubles", baseCeiling * 2, baseCeiling, true},
		{"exactly at the edge is not over", baseExcess + baseExcess/20, baseExcess, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := over(tc.measured, tc.want); got != tc.fails {
				t.Errorf("over(%d, %d) = %v, want %v", tc.measured, tc.want, got, tc.fails)
			}
		})
	}

	// A shrink is never a failure — only ever a note. An unrelated PR that
	// happens to simplify something must not go red for it.
	if over(baseExcess/2, baseExcess) {
		t.Error("halving the excess must not be reported as growth")
	}
	if !under(baseExcess/2, baseExcess) {
		t.Error("halving the excess should be worth banking")
	}
}
