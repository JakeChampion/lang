package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const ratchetLimit = 10

const ratchetTolerance = 0.05

type ratchetTree struct {
	name    string
	dir     string
	ceiling int
	excess  int
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

func TestSelfHostRatchetIncludesLiterateSources(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the ratchet driver reads native paths")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/lint_ratchet_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/lint_ratchet_run.fern", "lint_ratchet_run")
	input := t.TempDir()
	document := "# Complexity\n\n```fern\n<<*>>=\nfunction complex(n: i32): i32 {\n" +
		strings.Repeat("  if (n > 0) { n = n - 1; }\n", 11) + "  return n;\n}\n```\n"
	if err := os.WriteFile(filepath.Join(input, "complex.fern.md"), []byte(document), 0o644); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(bin, input).CombinedOutput(); err != nil || !strings.Contains(string(output), " worst complex 12 excess 2\n") {
		t.Fatalf("literate complexity: %v\n%s", err, output)
	}
}
