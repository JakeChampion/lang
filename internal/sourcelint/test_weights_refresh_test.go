package sourcelint

import (
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
