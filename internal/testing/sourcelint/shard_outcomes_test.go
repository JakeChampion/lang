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

func scriptPath(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "..", "scripts", "ci-verify-shard-outcomes"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("verifier script missing: %v", err)
	}
	return p
}

func runVerify(t *testing.T, markers map[string]string, tolerate bool, args ...string) (int, string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range markers {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	cmd := exec.Command("bash", append([]string{scriptPath(t), dir}, args...)...)
	if tolerate {
		cmd.Env = ciEnv("FERN_CI_TOLERATE_VANISHED_SHARDS=1")
	} else {
		cmd.Env = ciEnv("FERN_CI_TOLERATE_VANISHED_SHARDS=0")
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

func allSuccess(host string, n int) map[string]string {
	m := map[string]string{}
	for i := 0; i < n; i++ {
		m[fmt.Sprintf("%s-%d", host, i)] = "success\n"
	}
	return m
}

func TestVerifyShardOutcomesAllSuccess(t *testing.T) {
	code, out := runVerify(t, allSuccess("x86_64", 4), false, "x86_64=4")
	if code != 0 {
		t.Fatalf("want exit 0, got %d: %s", code, out)
	}
	if !strings.Contains(out, "all expected shards reported success") {
		t.Errorf("missing success line: %s", out)
	}
}

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

func TestVerifyShardOutcomesFailureFatalEvenWhenTolerating(t *testing.T) {
	m := allSuccess("x86_64", 3)
	m["x86_64-1"] = "failure"
	code, out := runVerify(t, m, true, "x86_64=3")
	if code != 1 {
		t.Fatalf("tolerate must not absorb a real failure; got exit %d: %s", code, out)
	}
}

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

func TestVerifyShardOutcomesVanishedTolerated(t *testing.T) {
	m := allSuccess("x86_64", 4)
	delete(m, "x86_64-3")
	code, out := runVerify(t, m, true, "x86_64=4")
	if code != 0 {
		t.Fatalf("want exit 0 under tolerate, got %d: %s", code, out)
	}
	if !strings.Contains(out, "tolerating 1 shard(s) that reported no result") {
		t.Errorf("missing tolerate note: %s", out)
	}
}

func TestVerifyShardOutcomesSkippedIsNotATestFailure(t *testing.T) {
	m := allSuccess("x86_64", 4)
	m["x86_64-2"] = "skipped\n"
	code, out := runVerify(t, m, false, "x86_64=4")
	if code != 1 {
		t.Fatalf("want exit 1, got %d: %s", code, out)
	}
	if !strings.Contains(out, "NEVER RAN: x86_64 shard2") {
		t.Errorf("skipped shard not reported as never-ran: %s", out)
	}
	if strings.Contains(out, "FAILED: x86_64 shard2") {
		t.Errorf("a shard that ran no tests was called a test failure: %s", out)
	}
	if strings.Contains(out, "3 passed, 1 failed") {
		t.Errorf("never-ran shard counted in the failed tally: %s", out)
	}
}

func TestVerifyShardOutcomesSkippedTolerated(t *testing.T) {
	m := allSuccess("x86_64", 4)
	m["x86_64-1"] = "skipped"
	code, out := runVerify(t, m, true, "x86_64=4")
	if code != 0 {
		t.Fatalf("want exit 0 under tolerate, got %d: %s", code, out)
	}
	if !strings.Contains(out, "reported no result") {
		t.Errorf("missing tolerate note: %s", out)
	}
}

func TestVerifyShardOutcomesSkippedAndFailureAreDistinct(t *testing.T) {
	m := allSuccess("x86_64", 4)
	m["x86_64-1"] = "skipped"
	m["x86_64-2"] = "failure"
	code, out := runVerify(t, m, true, "x86_64=4")
	if code != 1 {
		t.Fatalf("tolerate must not absorb a real failure; got exit %d: %s", code, out)
	}
	if !strings.Contains(out, "NEVER RAN: x86_64 shard1") || !strings.Contains(out, "FAILED: x86_64 shard2") {
		t.Errorf("want both classes named separately: %s", out)
	}
}

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

func TestVerifyShardOutcomesTrimsWhitespace(t *testing.T) {
	code, out := runVerify(t, map[string]string{"x86_64-0": "  success \r\n"}, false, "x86_64=1")
	if code != 0 {
		t.Fatalf("want exit 0, got %d: %s", code, out)
	}
}

func TestVerifyShardOutcomesBadUsage(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"x86_64"},
		{"x86_64=notanum"},
	} {
		code, out := runVerify(t, nil, false, args...)
		if code != 2 {
			t.Errorf("args %v: want exit 2, got %d: %s", args, code, out)
		}
	}
}

func TestSelfHostWorkflowShardCountsMatchVerifyExpectations(t *testing.T) {
	wf, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", "test-e2e-selfhost.yml"))
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}
	src := string(wf)

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

func TestSelfHostWorkflowOwnJobTestsEachHaveAJob(t *testing.T) {
	wf, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", "test-e2e-selfhost.yml"))
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}
	src := string(wf)

	m := regexp.MustCompile(`(?m)^\s*OWN_JOB_TESTS:\s*"([^"]*)"`).FindStringSubmatch(src)
	if m == nil {
		t.Fatal("workflow does not define OWN_JOB_TESTS — did the env block change?")
	}
	weights, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "selfhost-test-weights.txt"))
	if err != nil {
		t.Fatalf("read weights: %v", err)
	}

	for _, name := range strings.Split(m[1], "|") {
		if name == "" {
			continue
		}
		if !strings.Contains(src, "-test.run '^"+name+"$'") {
			t.Errorf("OWN_JOB_TESTS names %s but no job runs it with -test.run '^%s$' — "+
				"it is excluded from the shards and run nowhere", name, name)
		}
		weightRe := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + `\s`)
		if weightRe.Match(weights) {
			t.Errorf("%s is in OWN_JOB_TESTS and also weighted in selfhost-test-weights.txt — "+
				"the partition never sees it, so the weight is dead", name)
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

func TestSelfHostWorkflowVanishedShardBudgetMatchesTimeout(t *testing.T) {
	wf, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", "test-e2e-selfhost.yml"))
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}
	src := string(wf)

	classifyRe := regexp.MustCompile(`(?m)ci-classify-vanished-shards[^\n]*?(\d+)\s*$`)
	cm := classifyRe.FindStringSubmatch(src)
	if cm == nil {
		t.Fatal("workflow does not invoke ci-classify-vanished-shards with a budget")
	}
	budget := mustAtoi(t, cm[1])

	shardJobRe := regexp.MustCompile(`name: test-e2e-selfhost-\$\{\{ matrix\.host \}\}-shard\$\{\{ matrix\.shard \}\}\s*\n\s*runs-on:[^\n]*\n\s*timeout-minutes:\s*(\d+)`)
	sm := shardJobRe.FindStringSubmatch(src)
	if sm == nil {
		t.Fatal("could not find the shard job's timeout-minutes — did the job header change?")
	}
	timeout := mustAtoi(t, sm[1])

	if budget != timeout {
		t.Errorf("ci-classify-vanished-shards is given a %dm budget but the shard job's timeout-minutes is %d — "+
			"the over-budget/reclaim verdict would be wrong", budget, timeout)
	}
}

func TestClassifyVanishedShardsIsAdvisory(t *testing.T) {
	p, err := filepath.Abs(filepath.Join("..", "..", "..", "scripts", "ci-classify-vanished-shards"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("classifier script missing: %v", err)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"0", "20"}, "skipping the diagnosis"},
		{[]string{"123", "notanumber"}, "is not a whole number of minutes"},
	} {
		cmd := exec.Command("bash", append([]string{p}, tc.args...)...)
		cmd.Env = ciEnv("GH_TOKEN=", "GITHUB_TOKEN=", "GH_CONFIG_DIR="+t.TempDir())
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Errorf("args %v: classifier must exit 0 even when it cannot diagnose; got %v: %s", tc.args, err, out)
		}
		if !strings.Contains(string(out), tc.want) {
			t.Errorf("args %v: want the advisory path (%q), got: %s", tc.args, tc.want, out)
		}
	}
}

func TestClassifyVanishedShardsVerdicts(t *testing.T) {
	script, err := filepath.Abs(filepath.Join("..", "..", "..", "scripts", "ci-classify-vanished-shards"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	const jobs = `{"jobs":[
	  {"name":"test-e2e-selfhost-x86_64-shard2","conclusion":"cancelled",
	   "started_at":"2026-08-07T15:53:53Z","completed_at":"2026-08-07T16:14:10Z"},
	  {"name":"test-e2e-selfhost-x86_64-shard7","conclusion":"cancelled",
	   "started_at":"2026-08-07T15:53:53Z","completed_at":"2026-08-07T15:58:03Z"},
	  {"name":"test-e2e-selfhost-aarch64-shard1","conclusion":"failure",
	   "started_at":"2026-08-07T15:53:53Z","completed_at":"2026-08-07T15:56:53Z",
	   "steps":[{"name":"Set up job","conclusion":"success"},
	            {"name":"run self-host shard 1/2 (prebuilt binary, streaming)","conclusion":"failure"}]},
	  {"name":"test-e2e-selfhost-x86_64-shard9","conclusion":"failure",
	   "started_at":"2026-08-07T15:53:53Z","completed_at":"2026-08-07T15:55:12Z",
	   "steps":[{"name":"Set up job","conclusion":"success"},
	            {"name":"Set up Go","conclusion":"failure"},
	            {"name":"run self-host shard 9/16 (prebuilt binary, streaming)","conclusion":"skipped"}]},
	  {"name":"test-e2e-selfhost-x86_64-shard0","conclusion":"success",
	   "started_at":"2026-08-07T15:53:53Z","completed_at":"2026-08-07T15:54:53Z"},
	  {"name":"cli-driver-tests-x86_64","conclusion":"failure",
	   "started_at":"2026-08-07T15:53:53Z","completed_at":"2026-08-07T16:14:10Z"}
	]}`
	path := filepath.Join(t.TempDir(), "jobs.json")
	if err := os.WriteFile(path, []byte(jobs), 0o644); err != nil {
		t.Fatalf("write jobs: %v", err)
	}
	cmd := exec.Command("bash", script, "12345", "20")
	cmd.Env = ciEnv("FERN_CI_JOBS_JSON=" + path)
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("classifier must exit 0; got %v: %s", err, raw)
	}
	out := string(raw)

	for _, want := range []string{
		"shard2: cancelled after 20m17s — EXCEEDED ITS 20m BUDGET",
		"shard7: cancelled after 4m10s, well inside the budget — RUNNER RECLAIM",
		"aarch64-shard1: failure after 3m0s — died on its own terms",
		`shard9: failure after 1m19s in step "Set up Go" — SETUP FAILED BEFORE THE TESTS RAN`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing verdict %q in:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"shard0", "cli-driver-tests"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("unexpectedly reported %q in:\n%s", unwanted, out)
		}
	}
}

func TestClassifyVanishedShardsNestedNames(t *testing.T) {
	script, err := filepath.Abs(filepath.Join("..", "..", "..", "scripts", "ci-classify-vanished-shards"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		job    string
		prefix string
	}{
		{"direct", "test-e2e-selfhost-x86_64-shard1", ""},
		{"called", "Test e2e self-host / test-e2e-selfhost-x86_64-shard1", ""},
		{"nested", "Full suite / Test e2e self-host / test-e2e-selfhost-x86_64-shard1", ""},
		{"custom", "Full suite / Special / special-shard-1", "special-shard-"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prefix := tc.prefix
			if prefix == "" {
				prefix = "test-e2e-selfhost-"
			}
			other := prefix + "caller / unrelated-job"
			jobs := fmt.Sprintf(`{"jobs":[
                {"name":%q,"conclusion":"failure","started_at":"2026-09-15T16:00:00Z","completed_at":"2026-09-15T16:03:00Z"},
                {"name":%q,"conclusion":"failure","started_at":"2026-09-15T16:00:00Z","completed_at":"2026-09-15T16:03:00Z"}
            ]}`, tc.job, other)
			path := filepath.Join(t.TempDir(), "jobs.json")
			if err := os.WriteFile(path, []byte(jobs), 0o644); err != nil {
				t.Fatal(err)
			}
			args := []string{script, "12345", "20"}
			if tc.prefix != "" {
				args = append(args, tc.prefix)
			}
			cmd := exec.Command("bash", args...)
			cmd.Env = ciEnv("FERN_CI_JOBS_JSON=" + path)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("classifier: %v: %s", err, out)
			}
			if !strings.Contains(string(out), tc.job+": failure after 3m0s") {
				t.Errorf("missing failure verdict for the full job name: %s", out)
			}
			if strings.Contains(string(out), other) {
				t.Errorf("classified an unrelated leaf based on its caller: %s", out)
			}
		})
	}
}

func TestClassifierTestStepPrefixMatchesWorkflow(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("..", "..", "..", "scripts", "ci-classify-vanished-shards"))
	if err != nil {
		t.Fatalf("read classifier: %v", err)
	}
	m := regexp.MustCompile(`(?m)^TEST_STEP="([^"]+)"`).FindSubmatch(script)
	if m == nil {
		t.Fatal("classifier no longer defines TEST_STEP; this guard needs updating with it")
	}
	prefix := string(m[1])

	wf, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", "test-e2e-selfhost.yml"))
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}
	if !regexp.MustCompile(`(?m)^\s*-\s*name:\s*` + regexp.QuoteMeta(prefix)).Match(wf) {
		t.Errorf("no step in test-e2e-selfhost.yml starts with TEST_STEP %q — "+
			"the classifier would call every setup failure a test failure", prefix)
	}
}
