package sourcelint

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func weightScript(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "..", "scripts", "ci-test-weights"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("weight script missing: %v", err)
	}
	return p
}

func ciEnv(extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		switch {
		case strings.HasPrefix(kv, "GITHUB_"),
			strings.HasPrefix(kv, "RUNNER_"),
			strings.HasPrefix(kv, "FERN_"):
			continue
		}
		env = append(env, kv)
	}
	return append(env, extra...)
}

func runWeights(t *testing.T, env []string, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command("bash", append([]string{weightScript(t)}, args...)...)
	cmd.Env = ciEnv(env...)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if ok := asExitError(err, &ee); !ok {
			t.Fatalf("run weight script: %v (output: %s)", err, out)
		}
		code = ee.ExitCode()
	}
	return code, string(out)
}

func seed(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	return dir
}

func weightsFile(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "weights.txt")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write weights: %v", err)
	}
	return p
}

func TestCITestWeightsExtractPicksTopLevelDurations(t *testing.T) {
	dir := seed(t, map[string]string{"run.json": strings.Join([]string{
		`{"Time":"2026-08-14T10:00:00Z","Action":"run","Package":"e2ecompiler","Test":"TestSelfHostHeavy"}`,
		`{"Time":"2026-08-14T10:01:00Z","Action":"pass","Package":"e2ecompiler","Test":"TestSelfHostHeavy/sub","Elapsed":60.01}`,
		`{"Time":"2026-08-14T10:08:03Z","Action":"pass","Package":"e2ecompiler","Test":"TestSelfHostHeavy","Elapsed":482.11}`,
		`{"Time":"2026-08-14T10:08:04.6Z","Action":"run","Package":"e2ecompiler","Test":"TestSelfHostNoisy"}`,
		`{"Time":"2026-08-14T10:08:04Z","Action":"output","Package":"e2ecompiler","Test":"TestSelfHostNoisy",` +
			`"Output":"printed {\"Action\":\"pass\",\"Test\":\"TestSelfHostFake\",\"Elapsed\":9999}\n"}`,
		`{"Time":"2026-08-14T10:08:05Z","Action":"fail","Package":"e2ecompiler","Test":"TestSelfHostNoisy","Elapsed":0.4}`,
		`{"Time":"2026-08-14T10:08:06Z","Action":"fail","Package":"e2ecompiler","Elapsed":482.9}`,
		"",
	}, "\n")})

	code, out := runWeights(t, nil, "extract", filepath.Join(dir, "run.json"))
	if code != 0 {
		t.Fatalf("want exit 0, got %d: %s", code, out)
	}
	want := "TestSelfHostHeavy\t483.00\nTestSelfHostNoisy\t0.40\n"
	if out != want {
		t.Errorf("extract output:\n%q\nwant:\n%q", out, want)
	}
}

func TestCITestWeightsQuietOnAccurateWeights(t *testing.T) {
	timings := seed(t, map[string]string{
		"x86_64-11.timings": "TestSelfHostHeavy\t482.00\nTestSelfHostTiny\t0.40\n",
		"x86_64-3.timings":  "TestSelfHostMid\t120.00\n",
	})
	w := weightsFile(t, "# comment\nTestSelfHostHeavy 738\nTestSelfHostMid 125\n")

	for _, env := range [][]string{{"GITHUB_ACTIONS=true"}, nil} {
		code, out := runWeights(t, env, "check", timings, w)
		if code != 0 {
			t.Fatalf("env %v: want exit 0, got %d: %s", env, code, out)
		}
		if !strings.Contains(out, "No weight discrepancies") {
			t.Errorf("env %v: want the all-clear line, got:\n%s", env, out)
		}
		for _, bad := range []string{"UNWEIGHTED", "UNDER-WEIGHTED", "WARNING", "::warning", "Corrected weights"} {
			if strings.Contains(out, bad) {
				t.Errorf("env %v: accurate weights produced %q:\n%s", env, bad, out)
			}
		}
	}
}

func TestCITestWeightsFlagsUnweightedHeavyTest(t *testing.T) {
	timings := seed(t, map[string]string{
		"x86_64-11.timings": "TestSelfHostHeavy\t482.00\n",
	})
	w := weightsFile(t, "TestSelfHostSomethingElse 125\n")

	code, out := runWeights(t, []string{"GITHUB_ACTIONS=1"}, "check", timings, w)
	if code != 0 {
		t.Fatalf("advisory check must exit 0, got %d: %s", code, out)
	}
	if !strings.Contains(out, "UNWEIGHTED: TestSelfHostHeavy measured 482s") {
		t.Errorf("missing the unweighted finding:\n%s", out)
	}
	if !strings.Contains(out, "::warning title=unweighted heavy test: TestSelfHostHeavy::") {
		t.Errorf("missing the GitHub annotation:\n%s", out)
	}
	if !strings.Contains(out, "::TestSelfHostHeavy measured 482s but has no entry") {
		t.Errorf("the annotation message must name the test, not only the title:\n%s", out)
	}
	if !strings.Contains(out, "TestSelfHostHeavy 723") {
		t.Errorf("missing the copy-pasteable corrected weight:\n%s", out)
	}
}

func TestCITestWeightsFlagsUnderWeightedTest(t *testing.T) {
	timings := seed(t, map[string]string{
		"x86_64-11.timings": "TestSelfHostInterp\t170.50\n",
	})
	w := weightsFile(t, "TestSelfHostInterp 7\n")

	code, out := runWeights(t, nil, "check", timings, w)
	if code != 0 {
		t.Fatalf("want exit 0, got %d: %s", code, out)
	}
	if !strings.Contains(out, "UNDER-WEIGHTED: TestSelfHostInterp measured 170s against entry 7 (24.4x)") {
		t.Errorf("missing the under-weighted finding:\n%s", out)
	}
	if !strings.Contains(out, "TestSelfHostInterp 256") {
		t.Errorf("missing the corrected weight:\n%s", out)
	}
}

func TestCITestWeightsIgnoresCheapTests(t *testing.T) {
	timings := seed(t, map[string]string{
		"x86_64-1.timings": "TestSelfHostCheap\t12.00\n",
	})
	w := weightsFile(t, "TestSelfHostCheap 1\n")

	code, out := runWeights(t, nil, "check", timings, w)
	if code != 0 {
		t.Fatalf("want exit 0, got %d: %s", code, out)
	}
	if strings.Contains(out, "TestSelfHostCheap") {
		t.Errorf("a 12s test must not be flagged:\n%s", out)
	}
}

func TestCITestWeightsSuggestionsOnlyRaise(t *testing.T) {
	timings := seed(t, map[string]string{
		"x86_64-11.timings": "TestSelfHostHeavy\t100.00\n",
	})
	w := weightsFile(t, "TestSelfHostHeavy 738\n")

	code, out := runWeights(t, nil, "check", timings, w)
	if code != 0 {
		t.Fatalf("want exit 0, got %d: %s", code, out)
	}
	if strings.Contains(out, "Corrected weights") {
		t.Errorf("an over-weighted test must not be suggested down:\n%s", out)
	}
}

func TestCITestWeightsFlagsShardNearBudget(t *testing.T) {
	timings := seed(t, map[string]string{
		"x86_64-11.timings": "TestSelfHostHeavy\t738.00\nTestSelfHostMid\t190.00\nTestSelfHostAlso\t54.00\n",
	})
	w := weightsFile(t, "TestSelfHostHeavy 738\nTestSelfHostMid 130\nTestSelfHostAlso 54\n")

	code, out := runWeights(t, []string{"GITHUB_ACTIONS=1", "FERN_WEIGHT_SHARD_BUDGET_SECONDS=1080"}, "check", timings, w)
	if code != 0 {
		t.Fatalf("want exit 0, got %d: %s", code, out)
	}
	if !strings.Contains(out, "SHARD NEAR BUDGET: x86_64-11 measured 982s = 90% of 1080s") {
		t.Errorf("missing the shard-load finding:\n%s", out)
	}
	if !strings.Contains(out, "::warning title=shard x86_64-11 is at 90% of its budget::") {
		t.Errorf("missing the shard-load annotation:\n%s", out)
	}
}

func TestCITestWeightsReportsWhenItRanBlind(t *testing.T) {
	weights := weightsFile(t, "TestSelfHostHeavy 738\n")

	for _, tc := range []struct {
		name string
		env  []string
		want string
	}{
		{"on a runner", []string{"GITHUB_ACTIONS=true"}, "::warning title=shard weight audit ran blind::"},
		{"off a runner", nil, "WARNING: shard weight audit ran blind"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, out := runWeights(t, tc.env, "check", t.TempDir(), weights)
			if code != 0 {
				t.Fatalf("want exit 0, got %d: %s", code, out)
			}
			if !strings.Contains(out, "NO TIMING FILES") {
				t.Errorf("a missing-artifact run must say so:\n%s", out)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("want %q in:\n%s", tc.want, out)
			}
		})
	}
}

func TestCITestWeightsReportsEmptyTimingFiles(t *testing.T) {
	timings := seed(t, map[string]string{
		"x86_64-1.timings": "",
		"x86_64-2.timings": "\n",
	})
	w := weightsFile(t, "TestSelfHostHeavy 738\n")

	code, out := runWeights(t, nil, "check", timings, w)
	if code != 0 {
		t.Fatalf("want exit 0, got %d: %s", code, out)
	}
	if !strings.Contains(out, "NOT ONE DURATION") {
		t.Errorf("empty timing files must be reported as blind:\n%s", out)
	}
	if !strings.Contains(out, "WARNING: shard weight audit ran blind") {
		t.Errorf("empty timing files must annotate:\n%s", out)
	}
	if strings.Contains(out, "No weight discrepancies") {
		t.Errorf("having compared nothing must not read as an all-clear:\n%s", out)
	}
}

func TestCITestWeightsStrictModeIsFatal(t *testing.T) {
	timings := seed(t, map[string]string{
		"x86_64-11.timings": "TestSelfHostHeavy\t482.00\n",
	})
	w := weightsFile(t, "TestSelfHostSomethingElse 125\n")

	code, out := runWeights(t, []string{"FERN_CI_WEIGHT_GATE_STRICT=1"}, "check", timings, w)
	if code != 1 {
		t.Fatalf("want exit 1 under strict mode, got %d: %s", code, out)
	}
	if !strings.Contains(out, "1 finding(s) and FERN_CI_WEIGHT_GATE_STRICT=1") {
		t.Errorf("missing the strict-mode reason:\n%s", out)
	}
}

func TestCITestWeightsWritesJobSummary(t *testing.T) {
	timings := seed(t, map[string]string{
		"x86_64-11.timings": "TestSelfHostHeavy\t482.00\n",
	})
	w := weightsFile(t, "TestSelfHostSomethingElse 125\n")

	t.Run("summary file set", func(t *testing.T) {
		summary := filepath.Join(t.TempDir(), "summary.md")
		code, out := runWeights(t, []string{"GITHUB_STEP_SUMMARY=" + summary}, "check", timings, w)
		if code != 0 {
			t.Fatalf("want exit 0, got %d: %s", code, out)
		}
		if !strings.Contains(out, "UNWEIGHTED: TestSelfHostHeavy") {
			t.Errorf("stdout must carry the report too:\n%s", out)
		}
		body, err := os.ReadFile(summary)
		if err != nil {
			t.Fatalf("read summary: %v", err)
		}
		if !strings.Contains(string(body), "UNWEIGHTED: TestSelfHostHeavy") {
			t.Errorf("summary missing the finding:\n%s", body)
		}
		if strings.Contains(string(body), "::warning") || strings.Contains(string(body), "WARNING:") {
			t.Errorf("annotations must not be pasted into the summary:\n%s", body)
		}
	})

	t.Run("summary file unset", func(t *testing.T) {
		dir := t.TempDir()
		code, out := runWeights(t, nil, "check", timings, w)
		if code != 0 {
			t.Fatalf("want exit 0, got %d: %s", code, out)
		}
		if !strings.Contains(out, "UNWEIGHTED: TestSelfHostHeavy") {
			t.Errorf("with no summary file the report must still reach stdout:\n%s", out)
		}
		ents, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read dir: %v", err)
		}
		if len(ents) != 0 {
			t.Errorf("no summary file was configured, yet %d file(s) were written", len(ents))
		}
	})
}

func TestCITestWeightsBadUsage(t *testing.T) {
	for _, args := range [][]string{{}, {"nonsense"}, {"check"}, {"check", "/nonexistent-dir", "/nonexistent-weights"}, {"merge"}, {"merge", "a", "b"}, {"merge", "/nonexistent-dir"}} {
		code, out := runWeights(t, nil, args...)
		if code == 0 {
			t.Errorf("args %q exited 0: %s", args, out)
		}
	}
}

func TestSelfHostWeightGateBudgetMatchesShardTimeout(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("..", "..", "..", "scripts", "ci-test-weights"))
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	budgetRe := regexp.MustCompile(`FERN_WEIGHT_SHARD_BUDGET_SECONDS:-(\d+)`)
	bm := budgetRe.FindStringSubmatch(string(script))
	if bm == nil {
		t.Fatal("ci-test-weights has no FERN_WEIGHT_SHARD_BUDGET_SECONDS default")
	}
	budget := mustAtoi(t, bm[1])

	wf, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", "test-e2e-selfhost.yml"))
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}
	timeoutRe := regexp.MustCompile(`-timeout (\d+)m -run "\$PAT"`)
	var mins []int
	for _, line := range strings.Split(string(wf), "\n") {
		if !strings.Contains(line, `-run "$PAT"`) {
			continue
		}
		m := timeoutRe.FindStringSubmatch(line)
		if m == nil {
			t.Fatalf("shard run line has no -timeout: %q", line)
		}
		mins = append(mins, mustAtoi(t, m[1]))
	}
	if len(mins) == 0 {
		t.Fatal("found no shard `-run \"$PAT\"` invocations — did the run step change?")
	}
	for _, m := range mins {
		if m*60 != budget {
			t.Errorf("shard -test.timeout is %dm (%ds) but the weight audit assumes %ds", m, m*60, budget)
		}
	}
}
