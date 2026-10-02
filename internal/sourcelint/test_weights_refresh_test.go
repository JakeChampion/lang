package sourcelint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCITestWeightsRefreshUsesIndependentRuns(t *testing.T) {
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
		code, out := runWeights(t, nil, append([]string{"refresh", weights}, dirs...)...)
		if code != 0 || out != want {
			t.Fatalf("refresh = %d, %q; want %q", code, out, want)
		}
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
			code, out := runWeights(t, nil, "refresh", weights, first, second)
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
		code, out := runWeights(t, nil, append([]string{"refresh", weights}, dirs...)...)
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
	code, out = runWeights(t, nil, "refresh", weights, saved, other)
	if want := "TestFaster 121\nTestNew 10\nTestOnce 4\nTestSlower 13\n"; code != 0 || out != want {
		t.Fatalf("refresh over a merged run = %d, %q; want %q", code, out, want)
	}

	for name, timings := range map[string]string{
		"subtest row":    "TestGood/case 1\n",
		"missing number": "TestGood\n",
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
// evidence outlives the artifacts' retention (docs/CI-WEIGHT-REFRESH.md).
func TestSelfHostVerifyJobPrintsTheMergedDurations(t *testing.T) {
	wf, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "test-e2e-selfhost.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wf), "\n          scripts/ci-test-weights merge shard-outcomes\n") {
		t.Fatal("test-e2e-selfhost.yml's verify job does not print `ci-test-weights merge shard-outcomes`")
	}
}
