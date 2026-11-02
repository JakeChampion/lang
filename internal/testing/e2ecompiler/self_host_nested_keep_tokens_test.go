package e2ecompiler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func nestedKeepSource(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("testdata", "nested_keep_tokens.fern"))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSelfHostNestedKeepPlan(t *testing.T) {
	h := selfHostCLIForHost(t)
	source, err := os.ReadFile(nestedKeepSource(t))
	if err != nil {
		t.Fatal(err)
	}
	prefix, _, ok := strings.Cut(string(source), "@noinline\nfunction step_c")
	if !ok {
		t.Fatal("nested keep fixture has no step_c boundary")
	}
	for _, imp := range []string{"std/i32", "std/result"} {
		prefix = strings.ReplaceAll(prefix, "import \""+imp+"\";\n", "")
	}
	input := filepath.Join(t.TempDir(), "plan.fern")
	if err := os.WriteFile(input, []byte(prefix), 0o644); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "plan")
	driver := e2eharness.RepoPath("compiler", "drivers", "ssa_keep_region_plans_run.fern")
	build := exec.Command(h.cli, "-target", e2eharness.HostSelfHostTarget(), "-sanitize", "-o", binary, driver, h.stdlib)
	build.Env = childEnv("FERN_IR_VERIFY=1", "FERN_STRICT_IR=1")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("compile nested keep plan: %v\n%s", err, out)
	}
	run := exec.Command(binary, input)
	run.Env = childEnv("FERN_LEAKCHECK=1")
	var stderr bytes.Buffer
	run.Stderr = &stderr
	out, err := run.Output()
	if err != nil || string(out) != "functions=2 keeps=2 peak=2\n" {
		t.Fatalf("nested keep plan: %v\nstdout: %s\nstderr: %s", err, out, stderr.String())
	}
	assertBalancedCensus(t, stderr.String())
}

func TestSelfHostNestedKeepRegions(t *testing.T) {
	h := selfHostCLIForHost(t)
	for _, driver := range []string{"ssa_tokens_run.fern", "ssa_keep_region_run.fern"} {
		for _, target := range h.targets {
			t.Run(driver+"/"+target.target, func(t *testing.T) {
				binary := filepath.Join(t.TempDir(), "regions")
				entry := e2eharness.RepoPath("compiler", "drivers", driver)
				build := exec.Command(h.cli, "-target", target.target, "-sanitize", "-o", binary, entry, h.stdlib)
				build.Env = childEnv("FERN_IR_VERIFY=1", "FERN_STRICT_IR=1")
				if out, err := build.CombinedOutput(); err != nil {
					t.Fatalf("compile token region checks: %v\n%s", err, out)
				}
				command := append(append([]string{}, target.runner...), binary)
				run := exec.Command(command[0], command[1:]...)
				run.Env = childEnv("FERN_LEAKCHECK=1")
				var stderr bytes.Buffer
				run.Stderr = &stderr
				out, err := run.Output()
				if err != nil || len(out) != 0 {
					t.Fatalf("token region checks: %v\nstdout: %s\nstderr: %s", err, out, stderr.String())
				}
				assertBalancedCensus(t, stderr.String())
			})
		}
	}
}

func TestSelfHostNestedKeepTokens(t *testing.T) {
	h := selfHostCLIForHost(t)
	targets := append([]ssaBackendTarget{}, h.targets...)
	if wasmtime, err := exec.LookPath("wasmtime"); err == nil {
		targets = append(targets, ssaBackendTarget{target: "wasm32-wasi", runner: []string{wasmtime, "run", "--env", "FERN_LEAKCHECK=1"}})
	} else {
		t.Run("wasm32-wasi", func(t *testing.T) { t.Skip("requires wasmtime") })
	}
	for _, target := range targets {
		t.Run(target.target, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "nested")
			args := []string{"-target", target.target, "-sanitize"}
			if target.target == "wasm32-wasi" {
				args = append(args, "-emit", "core-module")
			}
			args = append(args, "-o", binary, nestedKeepSource(t), h.stdlib)
			build := exec.Command(h.cli, args...)
			build.Env = childEnv("FERN_IR_VERIFY=1", "FERN_STRICT_IR=1")
			if out, err := build.CombinedOutput(); err != nil {
				t.Fatalf("compile nested keep controls: %v\n%s", err, out)
			}
			for _, mode := range []string{"a", "b", "c", "h", "p", "d", "s"} {
				for _, sharing := range []string{"unique", "shared"} {
					var pilot int64
					for _, steps := range []int{8, 1000} {
						t.Run(fmt.Sprintf("%s/%s/%d", mode, sharing, steps), func(t *testing.T) {
							command := append(append([]string{}, target.runner...), binary, mode, fmt.Sprint(steps), sharing)
							run := exec.Command(command[0], command[1:]...)
							run.Env = childEnv("FERN_LEAKCHECK=1")
							var stderr bytes.Buffer
							run.Stderr = &stderr
							out, err := run.Output()
							if err != nil {
								t.Fatalf("nested keep control: %v\nstdout: %s\nstderr: %s", err, out, stderr.String())
							}
							var gotMode string
							var gotSteps int
							var allocations int64
							n, scanErr := fmt.Sscanf(string(out), "mode=%s steps=%d allocations=%d", &gotMode, &gotSteps, &allocations)
							if scanErr != nil || n != 3 || gotMode != mode || gotSteps != steps || allocations < 0 {
								t.Fatalf("invalid nested keep measurement: %q (%v)", out, scanErr)
							}
							if steps == 8 {
								pilot = allocations
							} else if sharing == "unique" && allocations != pilot {
								t.Errorf("unique updates allocate per step: %d at 8 steps, %d at %d steps", pilot, allocations, steps)
							}
							assertBalancedCensus(t, stderr.String())
						})
					}
				}
			}
		})
	}
}
