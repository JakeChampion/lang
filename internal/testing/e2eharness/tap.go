package e2eharness

import (
	"regexp"
	"strconv"
	"testing"
)

var (
	tapPlanRe  = regexp.MustCompile(`(?m)^\s*1\.\.(\d+)\s*$`)
	tapCountRe = regexp.MustCompile(`(?m)^\s*# (pass|fail|skip) (\d+)\s*$`)
)

// CheckTAPPlan holds a std/test suite that must pass without a pin of how
// many cases it has: the plan is not empty, each case of the plan passed or
// was skipped, and none failed. A new case in the suite then needs no edit in
// the Go test that runs it.
func CheckTAPPlan(t *testing.T, out string) {
	t.Helper()
	plan := tapPlanRe.FindStringSubmatch(out)
	if plan == nil {
		t.Fatalf("no TAP plan in:\n%s", out)
	}
	counts := map[string]int{}
	for _, m := range tapCountRe.FindAllStringSubmatch(out, -1) {
		counts[m[1]], _ = strconv.Atoi(m[2])
	}
	n, _ := strconv.Atoi(plan[1])
	if n == 0 || counts["fail"] != 0 || counts["pass"]+counts["skip"] != n {
		t.Errorf("plan 1..%d with %d passed, %d skipped, %d failed\nfull output:\n%s",
			n, counts["pass"], counts["skip"], counts["fail"], out)
	}
}
