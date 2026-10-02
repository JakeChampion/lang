package sourcelint

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// tree seeds a source tree defining the named tests, for FERN_WEIGHT_TREE.
func tree(t *testing.T, names ...string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("package x\n\nimport \"testing\"\n\n")
	for _, n := range names {
		b.WriteString("func " + n + "(t *testing.T) {}\n")
	}
	return "FERN_WEIGHT_TREE=" + seed(t, map[string]string{"x_test.go": b.String()})
}

func TestCITestWeightsRefreshUsesIndependentRuns(t *testing.T) {
	env := []string{tree(t, "TestFaster", "TestSlower", "TestUnseen", "TestOnce", "TestTiny", "TestNew")}
	weights := weightsFile(t, "TestFaster 600\nTestSlower 3\nTestUnseen 17.25\nTestOnce 40\nTestTiny 8\n")
	first := seed(t, map[string]string{
		"shard0.timings": "TestFaster\t100.10\nTestSlower\t12.1\nTestOnce\t2\nTestTiny\t0.1\n",
		"retry.timings":  "TestFaster\t95\nTestOnce\t1\nTestNew\t9.5\n",
	})
	second := seed(t, map[string]string{
		"shard0.timings": "TestFaster\t120.9\nTestSlower\t8\nTestTiny\t0.2\n",
	})
	want := "TestFaster 121\nTestNew 10\nTestOnce 40\nTestSlower 13\nTestUnseen 17.25\n"
	for _, dirs := range [][]string{{first, second}, {second, first}} {
		code, out := runWeights(t, env, append([]string{"refresh", weights}, dirs...)...)
		if code != 0 || out != want {
			t.Fatalf("refresh = %d, %q; want %q", code, out, want)
		}
	}
}

// The runs predate the tree the table is for: a measured test deleted since,
// or a declared one, gets no row (the weights file's lookup is exact and the
// testname gate rejects a name nothing answers to), and the drop is reported
// for the rows the table would otherwise have held.
func TestCITestWeightsRefreshDropsTestsTheTreeNoLongerHas(t *testing.T) {
	env := []string{tree(t, "TestKept", "TestWeightOne")}
	weights := weightsFile(t, "TestKept 5\nTestRetired 40\n")
	first := seed(t, map[string]string{"shard.timings": "TestKept\t7\nTestDeleted\t90\nTestWeightOne\t0.5\nTestGoneFast\t0.5\n"})
	second := seed(t, map[string]string{"shard.timings": "TestKept\t6\nTestDeleted\t91\nTestGoneFast\t0.4\n"})
	code, out := runWeights(t, env, "refresh", weights, first, second)
	want := "ci-test-weights: dropped TestDeleted: not a test function in the tree\n" +
		"ci-test-weights: dropped TestRetired: not a test function in the tree\n" +
		"TestKept 7\n"
	if code != 0 || out != want {
		t.Fatalf("refresh = %d, %q; want %q", code, out, want)
	}
	if code, out := runWeights(t, []string{"FERN_WEIGHT_TREE=" + t.TempDir()}, "refresh", weights, first, second); code == 0 || !strings.Contains(out, "no test functions under") {
		t.Fatalf("a tree with no tests was accepted: %d, %s", code, out)
	}
}

func TestCITestWeightsRefreshRejectsBadEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, weights, timings string
	}{
		{"negative duration", "TestGood 2\n", "TestGood -1\n"},
		{"non-numeric duration", "TestGood 2\n", "TestGood NaN\n"},
		{"subtest duration", "TestGood 2\n", "TestGood/case 1\n"},
		{"missing duration", "TestGood 2\n", "TestGood\n"},
		{"empty run", "TestGood 2\n", ""},
		{"blank run", "TestGood 2\n", "\n\n"},
		{"negative weight", "TestGood -1\n", "TestGood 1\n"},
		{"duplicate weight", "TestGood 2\nTestGood 3\n", "TestGood 1\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			weights := weightsFile(t, tc.weights)
			first := seed(t, map[string]string{"shard.timings": "TestGood 1\n"})
			second := seed(t, map[string]string{"shard.timings": tc.timings})
			code, out := runWeights(t, []string{tree(t, "TestGood")}, "refresh", weights, first, second)
			if code == 0 || !strings.Contains(out, "ci-test-weights:") {
				t.Fatalf("bad evidence accepted: %d, %s", code, out)
			}
		})
	}
}

func TestCITestWeightsRefreshRequiresDistinctRunDirectories(t *testing.T) {
	weights := weightsFile(t, "TestGood 5\n")
	run := seed(t, map[string]string{"shard.timings": "TestGood 1\n"})
	for _, dirs := range [][]string{{run}, {run, filepath.Join(run, ".")}, {run, t.TempDir()}} {
		code, out := runWeights(t, []string{tree(t, "TestGood")}, append([]string{"refresh", weights}, dirs...)...)
		if code == 0 {
			t.Fatalf("incomplete evidence accepted: %s", out)
		}
	}
}

// merge is one run's refresh input printed from its shard artifacts: the
// slowest observation of each test, sorted, in the row format refresh reads.
func TestCITestWeightsMergeKeepsTheSlowestObservation(t *testing.T) {
	run := seed(t, map[string]string{
		"shard0.timings": "TestFaster\t100.10\nTestSlower\t12.1\nTestOnce\t2\n",
		"retry.timings":  "TestFaster\t95\nTestOnce\t3.5\nTestNew\t9.5\n",
	})
	want := "TestFaster\t100.10\nTestNew\t9.5\nTestOnce\t3.5\nTestSlower\t12.1\n"
	code, out := runWeights(t, nil, "merge", run)
	if code != 0 || out != want {
		t.Fatalf("merge = %d, %q; want %q", code, out, want)
	}

	// The printed rows stand in for the run's artifacts.
	weights := weightsFile(t, "TestFaster 600\nTestSlower 3\n")
	saved := seed(t, map[string]string{"run.timings": out})
	other := seed(t, map[string]string{"shard0.timings": "TestFaster\t120.9\nTestSlower\t8\n"})
	code, out = runWeights(t, []string{tree(t, "TestFaster", "TestSlower", "TestNew", "TestOnce")}, "refresh", weights, saved, other)
	if want := "TestFaster 121\nTestNew 10\nTestOnce 4\nTestSlower 13\n"; code != 0 || out != want {
		t.Fatalf("refresh over a merged run = %d, %q; want %q", code, out, want)
	}

	for name, timings := range map[string]string{
		"subtest row":    "TestGood/case 1\n",
		"missing number": "TestGood\n",
		"empty run":      "",
		"blank run":      "\n\n",
	} {
		bad := seed(t, map[string]string{"shard.timings": timings})
		if code, out := runWeights(t, nil, "merge", bad); code == 0 || !strings.Contains(out, "ci-test-weights:") {
			t.Errorf("%s: merge accepted it: %d, %s", name, code, out)
		}
	}
	if code, _ := runWeights(t, nil, "merge", t.TempDir()); code == 0 {
		t.Error("merge of a directory with no timing files succeeded")
	}
}

// The verify job prints the merged durations into its log, where a run's
// evidence outlives the artifacts' retention (docs/CI-WEIGHT-REFRESH.md). The
// step's conditions are the feature: it has to run on a red run, whose
// durations are the ones worth keeping, and a merge of no artifacts must not
// red the lane.
func TestSelfHostVerifyJobPrintsTheMergedDurations(t *testing.T) {
	wf, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "test-e2e-selfhost.yml"))
	if err != nil {
		t.Fatal(err)
	}
	job := sliceBlock(string(wf), regexp.MustCompile(`(?m)^  verify:$`), anyJobKey)
	if job == "" {
		t.Fatal("test-e2e-selfhost.yml has no `verify:` job")
	}
	if !strings.Contains(job, "needs.test.result != 'skipped'") {
		t.Error("the verify job no longer runs on a red test matrix, so a red run prints no durations")
	}
	step := sliceBlock(job, regexp.MustCompile(`(?m)^      - name: measured durations, this run's refresh input$`), anyStep)
	if step == "" {
		t.Fatal("the verify job has no `measured durations, this run's refresh input` step")
	}
	for _, want := range []struct{ needle, why string }{
		{"scripts/ci-test-weights merge shard-outcomes", "the step prints something other than the merged durations"},
		{"if: ${{ !cancelled() }}", "the step is skipped after a failed `verify every shard reported success`, the runs whose durations are worth keeping"},
		{"continue-on-error: true", "a merge of no artifacts reds the lane instead of printing nothing"},
		{"::group::measured durations", "docs/CI-WEIGHT-REFRESH.md tells the reader to save the `measured durations` group"},
	} {
		if !strings.Contains(step, want.needle) {
			t.Errorf("the measured-durations step lacks %q: %s", want.needle, want.why)
		}
	}
}
