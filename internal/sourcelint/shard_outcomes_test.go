// Package sourcelint holds fast, dependency-free repo-hygiene checks that run
// in the ordinary `go test ./...` lane (no build tools, no fixtures).
package sourcelint

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// scriptPath locates scripts/ci-verify-shard-outcomes from this package dir.
func scriptPath(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "scripts", "ci-verify-shard-outcomes"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("verifier script missing: %v", err)
	}
	return p
}

// runVerify invokes the verifier over a temp dir seeded with `markers`
// (name → contents) and returns its exit code plus combined output.
func runVerify(t *testing.T, markers map[string]string, tolerate bool, args ...string) (int, string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range markers {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	cmd := exec.Command("bash", append([]string{scriptPath(t), dir}, args...)...)
	cmd.Env = append(os.Environ(), "FERN_CI_TOLERATE_VANISHED_SHARDS=0")
	if tolerate {
		cmd.Env = append(os.Environ(), "FERN_CI_TOLERATE_VANISHED_SHARDS=1")
	}
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if ok := asExitError(err, &ee); !ok {
			t.Fatalf("run verifier: %v (output: %s)", err, out)
		}
		code = ee.ExitCode()
	}
	return code, string(out)
}

func asExitError(err error, dst **exec.ExitError) bool {
	if ee, ok := err.(*exec.ExitError); ok {
		*dst = ee
		return true
	}
	return false
}

// allSuccess builds markers for shards 0..n-1 of one host, all passing.
func allSuccess(host string, n int) map[string]string {
	m := map[string]string{}
	for i := 0; i < n; i++ {
		m[fmt.Sprintf("%s-%d", host, i)] = "success\n"
	}
	return m
}

// A complete set of success markers is the only green state.
func TestVerifyShardOutcomesAllSuccess(t *testing.T) {
	code, out := runVerify(t, allSuccess("x86_64", 4), false, "x86_64=4")
	if code != 0 {
		t.Fatalf("want exit 0, got %d: %s", code, out)
	}
	if !strings.Contains(out, "all expected shards reported success") {
		t.Errorf("missing success line: %s", out)
	}
}

// A shard that reports `failure` is a real test failure: always fatal, and
// named in the output so the log is findable.
func TestVerifyShardOutcomesFailureIsFatal(t *testing.T) {
	m := allSuccess("x86_64", 4)
	m["x86_64-2"] = "failure\n"
	code, out := runVerify(t, m, false, "x86_64=4")
	if code != 1 {
		t.Fatalf("want exit 1, got %d: %s", code, out)
	}
	if !strings.Contains(out, "FAILED: x86_64 shard2") {
		t.Errorf("failure not named: %s", out)
	}
}

// The tolerate dial covers VANISHED shards only — a reported failure stays
// fatal regardless, which is the enforcement gap #5912 item 1 was filed for.
func TestVerifyShardOutcomesFailureFatalEvenWhenTolerating(t *testing.T) {
	m := allSuccess("x86_64", 3)
	m["x86_64-1"] = "failure"
	code, out := runVerify(t, m, true, "x86_64=3")
	if code != 1 {
		t.Fatalf("tolerate must not absorb a real failure; got exit %d: %s", code, out)
	}
}

// A missing marker means the shard never reached its record step (runner
// reclaim / OOM / timeout). Fatal by default: a silently absent shard ran no
// tests, which is how the #5901 regression stayed invisible.
func TestVerifyShardOutcomesVanishedIsFatalByDefault(t *testing.T) {
	m := allSuccess("x86_64", 4)
	delete(m, "x86_64-3")
	code, out := runVerify(t, m, false, "x86_64=4")
	if code != 1 {
		t.Fatalf("want exit 1, got %d: %s", code, out)
	}
	if !strings.Contains(out, "VANISHED: x86_64 shard3") {
		t.Errorf("vanished shard not named: %s", out)
	}
}

// ...but it is the half with a policy dial, since a stochastic reclaim would
// otherwise be a red build someone has to re-run.
func TestVerifyShardOutcomesVanishedTolerated(t *testing.T) {
	m := allSuccess("x86_64", 4)
	delete(m, "x86_64-3")
	code, out := runVerify(t, m, true, "x86_64=4")
	if code != 0 {
		t.Fatalf("want exit 0 under tolerate, got %d: %s", code, out)
	}
	if !strings.Contains(out, "tolerating 1 vanished shard") {
		t.Errorf("missing tolerate note: %s", out)
	}
}

// Multiple hosts are checked independently in one invocation.
func TestVerifyShardOutcomesMultiHost(t *testing.T) {
	m := allSuccess("x86_64", 3)
	for k, v := range allSuccess("aarch64", 2) {
		m[k] = v
	}
	code, out := runVerify(t, m, false, "x86_64=3", "aarch64=2")
	if code != 0 {
		t.Fatalf("want exit 0, got %d: %s", code, out)
	}
	if !strings.Contains(out, "5 passed") {
		t.Errorf("want 5 passed: %s", out)
	}
}

// An empty marker is not a pass. A recording step that ran but wrote nothing
// must not read as success.
func TestVerifyShardOutcomesEmptyMarkerIsFailure(t *testing.T) {
	m := allSuccess("x86_64", 2)
	m["x86_64-0"] = ""
	code, out := runVerify(t, m, false, "x86_64=2")
	if code != 1 {
		t.Fatalf("want exit 1, got %d: %s", code, out)
	}
	if !strings.Contains(out, "(empty)") {
		t.Errorf("empty marker not reported as such: %s", out)
	}
}

// Markers arrive via artifact download; tolerate incidental whitespace.
func TestVerifyShardOutcomesTrimsWhitespace(t *testing.T) {
	code, out := runVerify(t, map[string]string{"x86_64-0": "  success \r\n"}, false, "x86_64=1")
	if code != 0 {
		t.Fatalf("want exit 0, got %d: %s", code, out)
	}
}

// Malformed usage is a distinct exit code (2), never a silent pass.
func TestVerifyShardOutcomesBadUsage(t *testing.T) {
	for _, args := range [][]string{
		{},                 // no expectations
		{"x86_64"},         // missing =NSHARD
		{"x86_64=notanum"}, // non-numeric
	} {
		code, out := runVerify(t, nil, false, args...)
		if code != 2 {
			t.Errorf("args %v: want exit 2, got %d: %s", args, code, out)
		}
	}
}

// DRIFT GUARD. The verifier only enforces the shards it is TOLD to expect, so
// a shard added to the matrix but not to the verify job's expectation list
// would silently stop being covered — reintroducing the exact invisibility
// this whole mechanism exists to remove. Tie the two together.
func TestSelfHostWorkflowShardCountsMatchVerifyExpectations(t *testing.T) {
	wf, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "test-e2e-selfhost.yml"))
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}
	src := string(wf)

	// Count matrix entries per host, and collect each entry's declared nshard.
	matrixRe := regexp.MustCompile(`host:\s*(\w+),\s*nshard:\s*(\d+),\s*shard:\s*(\d+)`)
	seen := map[string]map[int]bool{}
	declared := map[string]int{}
	for _, m := range matrixRe.FindAllStringSubmatch(src, -1) {
		host, nshard, shard := m[1], mustAtoi(t, m[2]), mustAtoi(t, m[3])
		if _, ok := seen[host]; !ok {
			seen[host] = map[int]bool{}
		}
		seen[host][shard] = true
		if prev, ok := declared[host]; ok && prev != nshard {
			t.Fatalf("host %s declares conflicting nshard %d vs %d", host, prev, nshard)
		}
		declared[host] = nshard
	}
	if len(seen) == 0 {
		t.Fatal("no shard matrix entries found — did the matrix format change?")
	}

	// Every host's matrix must be a complete 0..nshard-1 cover: a gap means
	// the partition assigns tests to a bucket no job ever runs.
	for host, shards := range seen {
		n := declared[host]
		if len(shards) != n {
			t.Errorf("host %s: %d matrix entries but nshard=%d", host, len(shards), n)
		}
		for i := 0; i < n; i++ {
			if !shards[i] {
				t.Errorf("host %s: matrix has no shard %d (nshard=%d)", host, i, n)
			}
		}
	}

	// The verify job's expectations must name every host at its real count.
	expectRe := regexp.MustCompile(`ci-verify-shard-outcomes\s+\S+\s+([^\n]*)`)
	em := expectRe.FindStringSubmatch(src)
	if em == nil {
		t.Fatal("workflow does not invoke ci-verify-shard-outcomes")
	}
	got := map[string]int{}
	for _, tok := range strings.Fields(em[1]) {
		if k, v, ok := strings.Cut(tok, "="); ok {
			got[k] = mustAtoi(t, v)
		}
	}
	for host, n := range declared {
		if got[host] != n {
			t.Errorf("verify expects %s=%d, matrix has %d shards — enforcement would miss the difference",
				host, got[host], n)
		}
	}
	for host := range got {
		if _, ok := declared[host]; !ok {
			t.Errorf("verify expects host %q with no matrix entries", host)
		}
	}
}

func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("atoi %q: %v", s, err)
	}
	return n
}
