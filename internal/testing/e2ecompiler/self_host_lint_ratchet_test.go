package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

var ratchetTrees = []string{"compiler", "internal/stdlib/std"}

var ratchetLineRe = regexp.MustCompile(`^tree (\S+) worst (\S*) (\d+) excess (\d+)$`)

type treeComplexity struct {
	worstName string
	worst     int
	excess    int
}

// measureComplexity runs the ratchet driver over ratchetTrees below root. It
// reports each suppression comment that the linter rejects as a test error.
func measureComplexity(t *testing.T, runner []string, bin, root string) map[string]treeComplexity {
	t.Helper()
	cmd := runX86_64Bin(runner, bin, ratchetTrees...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("lint_ratchet_run in %s: %v\n%s", root, err, out)
	}
	measured := map[string]treeComplexity{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.HasPrefix(line, "directive ") {
			t.Error(strings.TrimPrefix(line, "directive "))
			continue
		}
		if m := ratchetLineRe.FindStringSubmatch(line); m != nil {
			worst, _ := strconv.Atoi(m[3])
			excess, _ := strconv.Atoi(m[4])
			measured[m[1]] = treeComplexity{m[2], worst, excess}
		}
	}
	for _, tree := range ratchetTrees {
		if measured[tree].worst == 0 {
			t.Fatalf("%s measured no functions in %s:\n%s", tree, root, out)
		}
	}
	return measured
}

// excessBudget is the growth of the total excess that one change can add to a
// tree: 1% of the excess of its base, and not less than 10.
func excessBudget(baseExcess int) int {
	return max(baseExcess/100, 10)
}

// worstLimit lets the worst function of a tree grow 5% in one change.
func worstLimit(baseWorst int) int {
	return baseWorst + baseWorst/20
}

// This test holds what a comparison of two trees cannot see: a suppression
// comment that the linter rejects, and a tree that the linter does not measure.
func TestSelfHostRepoLintDirectives(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/lint_ratchet_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/lint_ratchet_run.fern", "lint_ratchet_run")
	measureComplexity(t, runner, bin, e2eharness.RepoPath())
}

// The complexity of a tree is compared with the tree of the base commit, not
// with a pinned number, so each change answers only for what it adds. Two
// pull requests that each pin the same new number pass alone and fail
// together. scripts/check-ratchets gives the tree.
func TestSelfHostRepoComplexityDelta(t *testing.T) {
	baseTree := os.Getenv("FERN_RATCHET_BASE_TREE")
	if baseTree == "" {
		t.Skip("FERN_RATCHET_BASE_TREE is not set: scripts/check-ratchets runs this test with the tree of the base commit")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/lint_ratchet_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/lint_ratchet_run.fern", "lint_ratchet_run")
	head := measureComplexity(t, runner, bin, e2eharness.RepoPath())
	base := measureComplexity(t, runner, bin, baseTree)
	for _, tree := range ratchetTrees {
		h, b := head[tree], base[tree]
		if grown := h.excess - b.excess; grown > excessBudget(b.excess) {
			t.Errorf("%s: this change raises the total complexity over the limit from %d to %d. One change can add %d.\n"+
				"Split the new function, or put `// fern-lint: allow cyclomatic-complexity` and the reason on it.\n"+
				"Run: fern -lint %s", tree, b.excess, h.excess, excessBudget(b.excess), tree)
		}
		if h.worst > worstLimit(b.worst) {
			t.Errorf("%s: `%s` reaches a complexity of %d. The worst function of the base is `%s` at %d.\n"+
				"No function can become materially harder to read than the worst function already is.",
				tree, h.worstName, h.worst, b.worstName, b.worst)
		}
	}
}

func TestComplexityDeltaLimits(t *testing.T) {
	for _, tc := range []struct{ base, want int }{{17447, 174}, {803, 10}, {0, 10}} {
		if got := excessBudget(tc.base); got != tc.want {
			t.Errorf("excessBudget(%d) = %d, want %d", tc.base, got, tc.want)
		}
	}
	if got := worstLimit(221); got != 232 {
		t.Errorf("worstLimit(221) = %d, want 232", got)
	}
}

func TestSelfHostRatchetIncludesLiterateSources(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/lint_ratchet_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/lint_ratchet_run.fern", "lint_ratchet_run")
	input := t.TempDir()
	document := "# Complexity\n\n```fern\n<<*>>=\nfunction complex(n: i32): i32 {\n" +
		strings.Repeat("  if (n > 0) { n = n - 1; }\n", 11) + "  return n;\n}\n```\n"
	if err := os.WriteFile(filepath.Join(input, "complex.fern.md"), []byte(document), 0o644); err != nil {
		t.Fatal(err)
	}
	if output, err := runX86_64Bin(runner, bin, input).CombinedOutput(); err != nil || !strings.Contains(string(output), " worst complex 12 excess 2\n") {
		t.Fatalf("literate complexity: %v\n%s", err, output)
	}
}
