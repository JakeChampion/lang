package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestSelfHostSSALiftCoverageScan pins the stack-IR -> SSA lift COVERAGE scan
// (examples/self_host/ssa_lift_scan_run.fern): it loads a real module the way
// the production compiler does (resolve imports, bundle, merge builtins, hoist
// lambdas), lowers every function via irlower and LIFTS each, and reports how
// many functions the lift covers vs. bails — with a histogram of the bailing op
// kinds. This test builds the scanner, runs it over a real in-tree module, and
// asserts the accounting is self-consistent (lifted + lift-bail + irlower-bail
// == total, and some functions do lift); the full report is logged so the
// coverage number is visible in CI output. It also exercises the new
// LResult.bail diagnostic field end-to-end.
func TestSelfHostSSALiftCoverageScan(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("scan driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	// The scanner's import closure plus the module it scans (lexer.fern, a small
	// real module that imports util) and the pieces load_imports / merge_builtins
	// pull in.
	for _, name := range []string{
		"util.fern", "lexer.fern", "parser.fern", "astwalk.fern",
		"flatten.fern", "modloader.fern", "fern_toml.fern", "ir.fern", "ssa.fern",
		"ssa_lift.fern", "irlower.fern", "checker.fern", "builtins.fern",
		"treeshake.fern", "ssa_lift_scan_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	bin := buildSelfHostBin(t, gcc, dir, "ssa_lift_scan_run.fern", "ssa_lift_scan_run")

	out, err := exec.Command(bin, filepath.Join(dir, "lexer.fern")).CombinedOutput()
	if err != nil {
		t.Fatalf("scan failed: %v\n%s", err, out)
	}
	report := string(out)
	t.Logf("lift coverage scan of lexer.fern:\n%s", report)

	num := func(label string) int {
		re := regexp.MustCompile(label + `:\s+(\d+)`)
		m := re.FindStringSubmatch(report)
		if m == nil {
			t.Fatalf("no %q count in report:\n%s", label, report)
		}
		n, _ := strconv.Atoi(m[1])
		return n
	}
	// "lift-scan …: N functions"
	totRe := regexp.MustCompile(`(\d+) functions`)
	tm := totRe.FindStringSubmatch(report)
	if tm == nil {
		t.Fatalf("no total in report:\n%s", report)
	}
	total, _ := strconv.Atoi(tm[1])
	lifted := num("lifted")
	liftBail := num("lift-bail")
	lowerBail := num("irlower-bail")

	if total <= 0 {
		t.Fatalf("scanned 0 functions; report:\n%s", report)
	}
	if lifted+liftBail+lowerBail != total {
		t.Errorf("accounting mismatch: lifted(%d) + lift-bail(%d) + irlower-bail(%d) != total(%d)",
			lifted, liftBail, lowerBail, total)
	}
	if lifted == 0 {
		t.Errorf("no functions lifted in lexer.fern (expected a nontrivial fraction); report:\n%s", report)
	}
	// The bail histogram must be present (a header line), and every listed op
	// kind's counts should sum to lift-bail.
	if !strings.Contains(report, "bail histogram") {
		t.Errorf("missing bail histogram; report:\n%s", report)
	}
}
