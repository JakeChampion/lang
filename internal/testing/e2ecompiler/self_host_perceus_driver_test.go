package e2ecompiler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func perceusDriverFile(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "fern"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{name + ".fern", "fern/scale.fern", "fern/run_" + name + ".fern"} {
		data, err := os.ReadFile(filepath.Join("../../../bench/perceus", rel))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, rel), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "fern", "run_"+name+".fern")
}

func perceusDriverAnswer(name string, n int) string {
	switch name {
	case "nqueens":
		return fmt.Sprintf("%d\n", len(perceusQueensOracle(n)))
	case "rbtree":
		count := 0
		for k := 0; k < n; k++ {
			if k%10 == 0 {
				count++
			}
		}
		return fmt.Sprintf("%d\n", count)
	case "rbtree_ck":
		count := 0
		for k := 1; k <= n; k++ {
			if k%10 == 0 {
				count++
			}
		}
		return fmt.Sprintf("%d\n", count)
	case "cfold":
		constant, _ := perceusFoldOracle(n, 1)
		return fmt.Sprintf("%d\n%d\n", constant, constant)
	case "deriv":
		// Independently pinned by the arbitrary-precision expression audit.
		counts := []int{6, 22, 90, 420, 2202, 12886}
		var out strings.Builder
		for i := 0; i < n; i++ {
			fmt.Fprintf(&out, "%d count: %d\n", i+1, counts[i])
		}
		return out.String() + "done\n"
	default:
		panic("unknown Perceus workload")
	}
}

func TestSelfHostPerceusDrivers(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			for _, tc := range []struct {
				name    string
				maximum int
				sizes   []int
			}{
				{"nqueens", 13, []int{0, 1, 4, 8}},
				{"rbtree", 4200000, []int{0, 1, 10, 31, 257}},
				{"rbtree_ck", 4200000, []int{0, 1, 5, 10, 31, 257}},
				{"deriv", 10, []int{0, 1, 4, 6}},
				{"cfold", 20, []int{0, 1, 5, 8}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					command := kvDriverCommand(t, cli, perceusDriverFile(t, tc.name), target)
					check := func(t *testing.T, args []string, wantCode int, want string) {
						t.Helper()
						cmd := command(args...)
						var stderr bytes.Buffer
						cmd.Stderr = &stderr
						out, err := cmd.Output()
						code := 0
						if err != nil {
							if exit, ok := err.(*exec.ExitError); ok {
								code = exit.ExitCode()
							} else {
								t.Fatal(err)
							}
						}
						if code != wantCode || string(out) != want {
							t.Fatalf("args %q: exit %d, stdout %q; want %d, %q; stderr %s", args, code, out, wantCode, want, stderr.String())
						}
						allocs, frees, live := parseLeakcheck(t, tc.name, stderr.String())
						if allocs != frees || live != 0 {
							t.Fatalf("unbalanced census: %s", stderr.String())
						}
					}
					for _, n := range tc.sizes {
						t.Run(strconv.Itoa(n), func(t *testing.T) {
							check(t, []string{strconv.Itoa(n)}, 0, perceusDriverAnswer(tc.name, n))
						})
					}
					for i, args := range [][]string{nil, {""}, {"-1"}, {"bad"}, {"1x"}, {"1.5"}, {"4294967296"}, {strconv.Itoa(tc.maximum + 1)}, {"1", "extra"}} {
						t.Run(fmt.Sprintf("invalid-%d", i), func(t *testing.T) { check(t, args, 2, "") })
					}
				})
			}
		})
	}
}

// perf-bench records instructions even for nonzero checksum exits. Pin the
// actual answers here so the error sentinel cannot pass as a fast benchmark.
func TestSelfHostPerceusPerformanceChecksums(t *testing.T) {
	cli := buildSelfHostCLI(t)
	constant, _ := perceusFoldOracle(8, 1)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			for _, tc := range []struct {
				name string
				want int
			}{
				{"rbtree", (26 * 160) % 97},
				{"rbtree_ck", (25 * 160) % 97},
				{"nqueens", (len(perceusQueensOracle(6)) * 1400) % 97},
				{"deriv", ((6 + 22 + 90) * 2400) % 97},
				{"cfold", int((constant * 2 * 400) % 97)},
			} {
				t.Run(tc.name, func(t *testing.T) {
					dir := t.TempDir()
					if err := os.Mkdir(filepath.Join(dir, "gates"), 0700); err != nil {
						t.Fatal(err)
					}
					for _, rel := range []string{"gates/perceus_" + tc.name + ".fern", tc.name + ".fern"} {
						data, err := os.ReadFile(filepath.Join("../../../bench/perceus", rel))
						if err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(filepath.Join(dir, rel), data, 0600); err != nil {
							t.Fatal(err)
						}
					}
					path := filepath.Join(dir, "gates", "perceus_"+tc.name+".fern")
					command := kvDriverCommand(t, cli, path, target)
					cmd := command()
					var stderr bytes.Buffer
					cmd.Stderr = &stderr
					out, err := cmd.Output()
					code := 0
					if err != nil {
						if exit, ok := err.(*exec.ExitError); ok {
							code = exit.ExitCode()
						} else {
							t.Fatal(err)
						}
					}
					if code != tc.want || len(out) != 0 {
						t.Fatalf("exit %d, output %q; want %d and no output; stderr %s", code, out, tc.want, stderr.String())
					}
					allocs, frees, live := parseLeakcheck(t, tc.name, stderr.String())
					if allocs != frees || live != 0 {
						t.Fatalf("unbalanced census: %s", stderr.String())
					}
				})
			}
		})
	}
}
