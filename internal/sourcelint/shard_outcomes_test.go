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
	// ciEnv, not os.Environ(): the ambient FERN_CI_TOLERATE_VANISHED_SHARDS
	// would otherwise decide what this test asserts. See its doc comment.
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
	if !strings.Contains(out, "tolerating 1 shard(s) that reported no result") {
		t.Errorf("missing tolerate note: %s", out)
	}
}

// `skipped` means the shard's TEST STEP never executed — a step before it
// failed, so the record step (which is `if: !cancelled()`) still wrote a
// marker. No tests ran, so it is not a verdict on the code and must not be
// reported as one. Fatal by default all the same: a shard that ran nothing
// must never read as green.
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
	// The regression this guards: a GitHub codeload outage reported as a real
	// test failure, sending the reader to a log holding only a download error.
	if strings.Contains(out, "FAILED: x86_64 shard2") {
		t.Errorf("a shard that ran no tests was called a test failure: %s", out)
	}
	if strings.Contains(out, "3 passed, 1 failed") {
		t.Errorf("never-ran shard counted in the failed tally: %s", out)
	}
}

// It shares the vanished dial, because it is the same fact: no signal from
// this shard, and either cause can be a stochastic infrastructure event.
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

// A real failure alongside a never-ran shard still fails, and the two are
// reported as the different things they are.
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

// DRIFT GUARD for OWN_JOB_TESTS (#6667). A name listed there is EXCLUDED from
// the shard partition, so it runs only if some job below names it — and a job
// that never runs it is invisible: `-test.list` still reports the test, the
// shards still skip it, and every lane stays green while the coverage is gone.
// The testname gate cannot see this; it only checks that the name resolves to a
// real test function, which a dropped-but-still-listed test does.
//
// Also fails on a weights entry for such a name: the partition never sees the
// name, so the weight steers nothing and reads as scheduling that is not there.
func TestSelfHostWorkflowOwnJobTestsEachHaveAJob(t *testing.T) {
	wf, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "test-e2e-selfhost.yml"))
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}
	src := string(wf)

	m := regexp.MustCompile(`(?m)^\s*OWN_JOB_TESTS:\s*"([^"]*)"`).FindStringSubmatch(src)
	if m == nil {
		t.Fatal("workflow does not define OWN_JOB_TESTS — did the env block change?")
	}
	weights, err := os.ReadFile(filepath.Join("..", "..", ".github", "selfhost-test-weights.txt"))
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

// DRIFT GUARD for the classifier's budget. ci-classify-vanished-shards decides
// "exceeded its budget" vs "runner reclaim" by comparing the job's wall-clock
// against a budget passed on the command line. If that number stops matching
// the shard job's own timeout-minutes, the verdict silently inverts: a shard
// that hit the wall gets reported as a reclaim (re-run it) or a reclaim gets
// reported as an over-budget shard (rebalance it) — and picking the wrong one
// is the whole confusion #6038 was filed about.
func TestSelfHostWorkflowVanishedShardBudgetMatchesTimeout(t *testing.T) {
	wf, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "test-e2e-selfhost.yml"))
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}
	src := string(wf)

	// The run-id argument is a `${{ … }}` expression with spaces inside it, so
	// match to the end of the line and take the trailing budget.
	classifyRe := regexp.MustCompile(`(?m)ci-classify-vanished-shards[^\n]*?(\d+)\s*$`)
	cm := classifyRe.FindStringSubmatch(src)
	if cm == nil {
		t.Fatal("workflow does not invoke ci-classify-vanished-shards with a budget")
	}
	budget := mustAtoi(t, cm[1])

	// The shard job is the one whose name is built from the matrix; take the
	// timeout-minutes that follows its `name:`/`runs-on:` pair.
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

// The classifier is advisory: it must never turn a diagnosis into a build
// failure of its own. An unreachable API (no GH_TOKEN, bogus run id) exits 0
// and says why.
func TestClassifyVanishedShardsIsAdvisory(t *testing.T) {
	p, err := filepath.Abs(filepath.Join("..", "..", "scripts", "ci-classify-vanished-shards"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("classifier script missing: %v", err)
	}
	for _, args := range [][]string{
		{"0", "20"},           // a run id that cannot resolve
		{"123", "notanumber"}, // a budget that is not minutes
	} {
		cmd := exec.Command("bash", append([]string{p}, args...)...)
		// No GH_TOKEN and no network expectation: gh either errors or is absent,
		// and either way the script must not fail the build.
		cmd.Env = append(os.Environ(), "GH_TOKEN=", "GITHUB_TOKEN=")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Errorf("args %v: classifier must exit 0 even when it cannot diagnose; got %v: %s", args, err, out)
		}
	}
}

// The three verdicts, against fixed durations. This is the whole value of the
// classifier — a wrong verdict sends you to rebalance a partition when the
// runner was reclaimed, or to re-run a shard that will hit the same wall — so
// it is tested against a saved jobs payload rather than live CI.
func TestClassifyVanishedShardsVerdicts(t *testing.T) {
	script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "ci-classify-vanished-shards"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	// 20m17s (over a 20m budget), 4m10s cancelled (reclaimed), 3m0s failure in
	// the test step, and 1m19s failure in a SETUP step (the 2026-08-17 codeload
	// outage shape: the action tarball never downloaded, so no test ran).
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
	cmd.Env = append(os.Environ(), "FERN_CI_JOBS_JSON="+path)
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
	// A shard that succeeded is not a suspect, and a non-shard job is out of
	// scope for the shard-name prefix — reporting either would send the reader
	// after the wrong job.
	for _, unwanted := range []string{"shard0", "cli-driver-tests"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("unexpectedly reported %q in:\n%s", unwanted, out)
		}
	}
}

// The classifier tells "a setup step failed" from "the tests failed" by
// comparing the failing step's name against a prefix it hard-codes. That
// prefix names a step in the workflow, so a rename there would silently
// return every infrastructure death to the "died on its own terms, READ ITS
// LOG" verdict this split exists to stop. Pin the two together.
func TestClassifierTestStepPrefixMatchesWorkflow(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("..", "..", "scripts", "ci-classify-vanished-shards"))
	if err != nil {
		t.Fatalf("read classifier: %v", err)
	}
	m := regexp.MustCompile(`(?m)^TEST_STEP="([^"]+)"`).FindSubmatch(script)
	if m == nil {
		t.Fatal("classifier no longer defines TEST_STEP; this guard needs updating with it")
	}
	prefix := string(m[1])

	wf, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "test-e2e-selfhost.yml"))
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}
	if !regexp.MustCompile(`(?m)^\s*-\s*name:\s*` + regexp.QuoteMeta(prefix)).Match(wf) {
		t.Errorf("no step in test-e2e-selfhost.yml starts with TEST_STEP %q — "+
			"the classifier would call every setup failure a test failure", prefix)
	}
}

// `ir.Op.Runtime` marks a callee as one the BACKEND provides. Marking a name
// the stdlib defines in Fern would invert the bug it exists to fix: the
// emitters would skip mangling and emit a bare reference to a symbol that only
// exists mangled, so the program would fail to link.
//
// The marking was derived from ir.providedSigs, which is not quite the same
// set — `internal/stdlib/core/map.fern` defines `__map_lookup_val`,
// `__map_drop_values` and friends as ordinary Fern functions. This pins the
// part that has to hold.
func TestRuntimeMarkedCalleesAreNotFernFunctions(t *testing.T) {
	root := filepath.Join("..", "..")
	irFiles, err := filepath.Glob(filepath.Join(root, "internal", "ir", "*.go"))
	if err != nil || len(irFiles) == 0 {
		t.Fatalf("glob internal/ir: %v", err)
	}
	marked := map[string]bool{}
	re := regexp.MustCompile(`Runtime: true, Str: "([^"]+)"`)
	for _, f := range irFiles {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for _, m := range re.FindAllSubmatch(b, -1) {
			marked[string(m[1])] = true
		}
	}
	if len(marked) == 0 {
		t.Fatal("no Runtime-marked callees found; this guard needs updating with the marking")
	}

	var fern []string
	err = filepath.Walk(filepath.Join(root, "internal", "stdlib"), func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(p, ".fern") {
			fern = append(fern, p)
		}
		return nil
	})
	if err != nil || len(fern) == 0 {
		t.Fatalf("walk internal/stdlib: %v (%d files)", err, len(fern))
	}
	decl := regexp.MustCompile(`(?m)^\s*(?:pub\s+)?function\s+([A-Za-z_][A-Za-z0-9_]*)`)
	for _, f := range fern {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for _, m := range decl.FindAllSubmatch(b, -1) {
			if name := string(m[1]); marked[name] {
				t.Errorf("%s defines %q in Fern, but the IR marks calls to it Runtime: "+
					"the emitters would emit it unmangled and the link would fail", f, name)
			}
		}
	}
}
