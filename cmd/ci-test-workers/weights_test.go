package main

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestReadWeights(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		want        map[string]float64
	}{
		{"comments", " # measured seconds\n\nTestA 2.5\nTestα\t3\n", map[string]float64{"TestA": 2.5, "Testα": 3}},
		{"empty", "", map[string]float64{}},
		{"zero", "TestA 0", nil},
		{"negative", "TestA -2", nil},
		{"nan", "TestA NaN", nil},
		{"infinity", "TestA +Inf", nil},
		{"overflow", "TestA 1e999", nil},
		{"duplicate", "TestA 2\nTestA 3", nil},
		{"subtest", "TestA/child 2", nil},
		{"not test", "BenchmarkA 2", nil},
		{"missing", "TestA", nil},
		{"extra field", "TestA 2 3", nil},
		{"long line", "#" + strings.Repeat("x", 128*1024), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readWeights(strings.NewReader(tc.input))
			if (err != nil) != (tc.want == nil) || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("weights = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

func TestWeightedInventory(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		workers     int
		weights     map[string]float64
		want        [][]string
	}{
		{"default unchanged", "TestC TestB TestA", 2, nil, [][]string{{"TestC", "TestA"}, {"TestB"}}},
		{"balance heavy tests", "TestA TestB TestC TestD", 2, map[string]float64{"TestA": 10, "TestC": 8}, [][]string{{"TestA"}, {"TestB", "TestC", "TestD"}}},
		{"new tests default one", "TestA TestB TestC", 2, map[string]float64{"TestA": 3, "TestDeleted": 100}, [][]string{{"TestA"}, {"TestB", "TestC"}}},
		{"ties and registration order", "TestD TestC TestB TestA", 2, map[string]float64{}, [][]string{{"TestC", "TestA"}, {"TestD", "TestB"}}},
		{"every worker used", "TestA TestB TestC", 3, map[string]float64{"TestA": 100}, [][]string{{"TestA"}, {"TestB"}, {"TestC"}}},
		{"duplicate inventory", "TestA TestA", 2, map[string]float64{}, nil},
		{"too few", "TestA", 2, map[string]float64{}, nil},
		{"overflow sum", "TestA TestB", 1, map[string]float64{"TestA": math.MaxFloat64, "TestB": math.MaxFloat64}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := weightedInventory([]byte(tc.input), tc.workers, tc.weights)
			if (err != nil) != (tc.want == nil) || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("groups = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

func TestWeightedWorkerPipeline(t *testing.T) {
	if _, err := exec.LookPath("gotestsum"); err != nil {
		t.Skip("gotestsum is required for the subprocess integration test")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"pass", "fail", "crash"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("FERN_WORKER_FIXTURE", mode)
			weights := filepath.Join(t.TempDir(), "weights")
			if err := os.WriteFile(weights, []byte("TestWorkerFixtureB 10\nTestDeleted 100\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			c := config{binary: binary, output: filepath.Join(t.TempDir(), "results"), pattern: "^TestWorkerFixture[AB]$", weights: weights, workers: 2, cpus: 2, timeout: time.Minute}
			var console bytes.Buffer
			err := run(context.Background(), c, &console)
			if (err == nil) != (mode == "pass") {
				t.Fatalf("run error = %v\n%s", err, &console)
			}
			data, err := os.ReadFile(filepath.Join(c.output, "summary.json"))
			if err != nil {
				t.Fatal(err)
			}
			var results []result
			if err := json.Unmarshal(data, &results); err != nil {
				t.Fatal(err)
			}
			if len(results) != 2 || !reflect.DeepEqual(results[0].Tests, []string{"TestWorkerFixtureB"}) || !reflect.DeepEqual(results[1].Tests, []string{"TestWorkerFixtureA"}) {
				t.Fatalf("unexpected weighted assignments: %s", data)
			}
		})
	}
}
