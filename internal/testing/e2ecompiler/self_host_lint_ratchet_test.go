package e2ecompiler

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// ratchetLimit is the complexity limit this repository's own Fern sources are
// held to: the cyclomatic-complexity rule's default, not a second number.
const ratchetLimit = 10

// ratchetTolerance is how far a tree may drift from its recorded numbers
// before the gate calls it a regression. Main moves the numbers by a few
// tenths of a per cent inside the time one CI run takes, so an exact gate is
// stale before it lands; five per cent absorbs that churn and still fails a
// truly new hot spot. scripts/ci-check-perf and
// scripts/ci-check-driver-sizes take the same shape for the same reason.
const ratchetTolerance = 0.05

// ratchetTree is one body of first-party Fern source held to the limit. The
// numbers are the measured state, not an allowance: growth past the tolerance
// fails, and a shrink past it logs, asking for the improvement to be recorded.
// A per-function exception is a `// fern-lint: allow cyclomatic-complexity`
// comment on the function, never a row here.
type ratchetTree struct {
	name string
	dir  string
	// ceiling is the highest complexity any function reaches, suppressed
	// ones included: an `allow` lets a function exceed the limit, never be
	// worse than anything here has been.
	ceiling int
	// excess is the summed distance over the limit of every unsuppressed
	// function above it. Summed distance rather than a count, so splitting
	// one huge function into readable pieces reads as the improvement it is.
	excess int
}

var ratchetTrees = []ratchetTree{
	{name: "self-host compiler", dir: "compiler", ceiling: 221, excess: 15547},
	{name: "stdlib", dir: "internal/stdlib/std", ceiling: 68, excess: 780},
}

func ratchetOver(measured, want int) bool {
	return float64(measured) > float64(want)*(1+ratchetTolerance)
}

func ratchetUnder(measured, want int) bool {
	return float64(measured) < float64(want)*(1-ratchetTolerance)
}

var ratchetLineRe = regexp.MustCompile(`^tree (\S+) worst (\S*) (\d+) excess (\d+)$`)

// TestSelfHostRepoComplexityRatchet holds the compiler's and the stdlib's
// complexity to a ratchet, measured by compiler/lint.fern through
// drivers/lint_ratchet_run.fern; a malformed `fern-lint:` directive fails it.
func TestSelfHostRepoComplexityRatchet(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the ratchet driver reads the checkout's paths, which an emulated run does not see")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/lint_ratchet_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/lint_ratchet_run.fern", "lint_ratchet_run")
	var dirs []string
	for _, tr := range ratchetTrees {
		dirs = append(dirs, tr.dir)
	}
	cmd := exec.Command(bin, dirs...)
	cmd.Dir = e2eharness.RepoPath()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("lint_ratchet_run: %v\n%s", err, out)
	}
	measured := map[string][3]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.HasPrefix(line, "directive ") {
			t.Error(strings.TrimPrefix(line, "directive "))
			continue
		}
		if m := ratchetLineRe.FindStringSubmatch(line); m != nil {
			measured[m[1]] = [3]string{m[2], m[3], m[4]}
		}
	}
	for _, tr := range ratchetTrees {
		t.Run(tr.name, func(t *testing.T) {
			got, ok := measured[tr.dir]
			if !ok {
				t.Fatalf("no measurement for %s in:\n%s", tr.dir, out)
			}
			worstName := got[0]
			worst, _ := strconv.Atoi(got[1])
			excess, _ := strconv.Atoi(got[2])
			if worst == 0 {
				t.Fatalf("%s measured no functions — the gate would pass vacuously", tr.dir)
			}
			if ratchetOver(excess, tr.excess) {
				t.Errorf("total complexity over the limit of %d is %d, more than %.0f%% above the recorded %d.\n"+
					"A change added real complexity. Split the new function up, or annotate it with\n"+
					"`// fern-lint: allow cyclomatic-complexity` and a line saying why.\n"+
					"Run: fern -lint %s", ratchetLimit, excess, ratchetTolerance*100, tr.excess, tr.dir)
			} else if ratchetUnder(excess, tr.excess) {
				t.Logf("total complexity over the limit of %d is %d, well below the recorded %d.\n"+
					"Worth banking: set this tree's excess to %d in self_host_lint_ratchet_test.go.",
					ratchetLimit, excess, tr.excess, excess)
			}
			if ratchetOver(worst, tr.ceiling) {
				t.Errorf("`%s` reaches a complexity of %d, more than %.0f%% above the tree's recorded ceiling of %d.\n"+
					"Nothing here may become materially harder to read than the worst function already is.",
					worstName, worst, ratchetTolerance*100, tr.ceiling)
			} else if ratchetUnder(worst, tr.ceiling) {
				t.Logf("the worst function in this tree is now `%s` at %d, well below the recorded ceiling of %d.\n"+
					"Worth banking: set this tree's ceiling to %d in self_host_lint_ratchet_test.go.",
					worstName, worst, tr.ceiling, worst)
			}
		})
	}
}

// The tolerance's two edges, pinned with the main-branch churn that sized it:
// the self-host excess moved +22 and then +9 against ~19870 inside one
// two-hour window, and the ceiling 468 -> 477.
func TestSelfHostRatchetToleranceAbsorbsChurnButNotARegression(t *testing.T) {
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
		{"a 2000-fork regression", baseExcess + 2000, baseExcess, true},
		{"ceiling doubles", baseCeiling * 2, baseCeiling, true},
		{"exactly at the edge is not over", baseExcess + baseExcess/20, baseExcess, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ratchetOver(tc.measured, tc.want); got != tc.fails {
				t.Errorf("over(%d, %d) = %v, want %v", tc.measured, tc.want, got, tc.fails)
			}
		})
	}
	if ratchetOver(baseExcess/2, baseExcess) {
		t.Error("halving the excess must not be reported as growth")
	}
	if !ratchetUnder(baseExcess/2, baseExcess) {
		t.Error(fmt.Sprintf("halving the excess (%d) should be worth banking", baseExcess/2))
	}
}
