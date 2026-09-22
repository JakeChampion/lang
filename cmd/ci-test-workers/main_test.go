package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestInventory(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		workers     int
		want        [][]string
	}{
		{"balanced", "TestA\nTestB\nTestC\n", 2, [][]string{{"TestA", "TestC"}, {"TestB"}}},
		{"unsorted listing", "TestC\nTestA\nTestB\n", 2, [][]string{{"TestA", "TestC"}, {"TestB"}}},
		{"unicode", "Testα\nTestβ\n", 1, [][]string{{"Testα", "Testβ"}}},
		{"empty", "", 1, nil},
		{"too few", "TestA", 2, nil},
		{"duplicate", "TestA\nTestA", 1, nil},
		{"noise", "warning: no tests", 1, nil},
		{"not a test", "BenchmarkA", 1, nil},
		{"invalid identifier", "TestA.*", 1, nil},
		{"zero workers", "TestA", 0, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := inventory([]byte(tc.input), tc.workers, nil)
			if (err != nil) != (tc.want == nil) || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("inventory = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
	pattern := regexp.MustCompile(exactPattern([]string{"TestA", "Testα"}))
	for _, name := range []string{"TestAB", "NotTestA", "TestA/subtest"} {
		if pattern.MatchString(name) {
			t.Errorf("pattern unexpectedly matches %q", name)
		}
	}
}

// Weighted assignment is longest-first to the least-loaded worker, so two
// heavy tests never share a worker while a light one runs alone.
func TestInventoryWeighted(t *testing.T) {
	weights := map[string]float64{"TestSlow": 100, "TestAlsoSlow": 90, "TestMid": 10}
	got, err := inventory([]byte("TestA\nTestSlow\nTestB\nTestAlsoSlow\nTestMid\n"), 2, weights)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"TestSlow", "TestA"}, {"TestAlsoSlow", "TestMid", "TestB"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("inventory = %v, want %v", got, want)
	}
}

func TestReadWeights(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weights.txt")
	if err := os.WriteFile(path, []byte("# comment\n\nTestA 12\n  TestB 0.5 \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readWeights(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]float64{"TestA": 12, "TestB": 0.5}; !reflect.DeepEqual(got, want) {
		t.Fatalf("readWeights = %v, want %v", got, want)
	}
	for _, bad := range []string{"TestA\n", "TestA x\n", "TestA -1\n", "TestA 1 2\n"} {
		if err := os.WriteFile(path, []byte(bad), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := readWeights(path); err == nil {
			t.Errorf("accepted malformed weights row %q", strings.TrimSpace(bad))
		}
	}
	if _, err := readWeights(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("accepted a missing weights file")
	}
	if got, err := readWeights(""); err != nil || len(got) != 0 {
		t.Errorf("no path: got %v, %v", got, err)
	}
}

func TestVerifyOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name, events, wantError string
	}{
		{"pass and skip", "run:A run:A/child skip:A/child pass:A run:B pass:B pass:", ""},
		{"test failed", "run:A fail:A", "failed test"},
		{"package failed", "fail:", "package reported fail"},
		{"package skipped", "skip:", "package reported skip"},
		{"missing test", "run:A pass:A pass:", "missing test outcome B"},
		{"unfinished subtest", "run:A run:A/child pass:A run:B pass:B pass:", "unfinished test"},
		{"unexpected test", "run:C", "unexpected test"},
		{"duplicate start", "run:A run:A", "duplicate test start"},
		{"duplicate terminal", "run:A pass:A pass:A", "duplicate outcome"},
		{"missing start", "pass:A", "missing start"},
		{"missing package", "run:A pass:A run:B pass:B", "missing successful package"},
		{"duplicate package", "run:A pass:A run:B pass:B pass: pass:", "duplicate package"},
		{"empty", "", "missing test outcome"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var data bytes.Buffer
			for _, item := range strings.Fields(tc.events) {
				action, name, _ := strings.Cut(item, ":")
				if name != "" {
					name = "Test" + name
				}
				if err := json.NewEncoder(&data).Encode(map[string]string{"Action": action, "Test": name}); err != nil {
					t.Fatal(err)
				}
			}
			_, err := verify(&data, []string{"TestA", "TestB"})
			if tc.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(strings.ReplaceAll(err.Error(), "Test", ""), tc.wantError) {
				t.Fatalf("error = %v, want %q", err, tc.wantError)
			}
		})
	}
	if _, err := verify(strings.NewReader(`{"Action":`), []string{"TestA"}); err == nil {
		t.Fatal("accepted truncated JSON")
	}
}

func TestWorkerPipeline(t *testing.T) {
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
			c := config{binary: binary, output: filepath.Join(t.TempDir(), "results"), pattern: "^TestWorkerFixture[AB]$", workers: 2, cpus: 2, timeout: time.Minute}
			var console bytes.Buffer
			err := run(context.Background(), c, &console)
			if (err == nil) != (mode == "pass") {
				t.Fatalf("run error = %v\n%s", err, &console)
			}
			if _, err := os.Stat(filepath.Join(c.output, "summary.json")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWorkerFixtureA(t *testing.T) {
	mode := os.Getenv("FERN_WORKER_FIXTURE")
	if mode == "" {
		t.Skip("subprocess fixture")
	}
	if runtime.GOMAXPROCS(0) != 1 {
		t.Fatalf("worker CPU budget = %d, want 1", runtime.GOMAXPROCS(0))
	}
	switch mode {
	case "fail":
		t.Fatal("deliberate worker failure")
	case "crash":
		os.Exit(2)
	}
	t.Run("child", func(t *testing.T) {})
}

func TestWorkerFixtureB(t *testing.T) {
	if os.Getenv("FERN_WORKER_FIXTURE") == "" {
		t.Skip("subprocess fixture")
	}
	t.Run("skipped", func(t *testing.T) { t.Skip("deliberate skip") })
}
