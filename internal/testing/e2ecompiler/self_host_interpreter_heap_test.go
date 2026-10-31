package e2ecompiler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostInterpreterHeap(t *testing.T) {
	native := e2eharness.HostSelfHostTarget()
	if native == "" {
		t.Skip("requires a native self-host target")
	}
	driver := e2eharness.SelfHostCLI(t)
	for _, tc := range []struct {
		name   string
		scales []int
	}{
		{"module", []int{8, 4096, 100000}},
		{"growth", []int{8, 16384}},
		{"churn", []int{8, 16384}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proj := t.TempDir()
			copySelfHostFiles(t, proj, "interpheap.fern")
			source, err := os.ReadFile(filepath.Join("testdata", "interpheap", tc.name+".fern"))
			if err != nil {
				t.Fatal(err)
			}
			entry := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(entry, source, 0o644); err != nil {
				t.Fatal(err)
			}
			for _, target := range []string{native, e2eharness.TargetWasm32Wasi} {
				t.Run(target, func(t *testing.T) {
					var wasmtime string
					if target == e2eharness.TargetWasm32Wasi {
						wasmtime = e2eharness.Wasmtime(t)
					}
					binary := filepath.Join(t.TempDir(), "probe")
					args := []string{"-target", target, "-sanitize"}
					if wasmtime != "" {
						args = append(args, "-emit", "core-module")
					}
					args = append(args, "-o", binary, entry, e2eharness.SelfHostStdlibRoot(t))
					compile := exec.Command(driver, args...)
					compile.Env = childEnv("FERN_IR_VERIFY=1", "FERN_STRICT_IR=1")
					if out, err := compile.CombinedOutput(); err != nil {
						t.Fatalf("compile heap probe: %v\n%s", err, out)
					}
					for _, count := range tc.scales {
						t.Run(fmt.Sprint(count), func(t *testing.T) {
							run := exec.Command(binary)
							if wasmtime != "" {
								run = exec.Command(wasmtime, "run", "--env", "FERN_LEAKCHECK=1", "--env", fmt.Sprintf("FERN_HEAP_PILOT_ROUNDS=%d", count), binary)
							}
							run.Env = childEnv("FERN_LEAKCHECK=1", fmt.Sprintf("FERN_HEAP_PILOT_ROUNDS=%d", count))
							var stderr bytes.Buffer
							run.Stderr = &stderr
							out, err := run.Output()
							if err != nil {
								t.Fatalf("run heap probe: %v\nstdout: %s\nstderr: %s", err, out, stderr.String())
							}
							want := fmt.Sprintf("heap module rounds=%d slots=4 pass\n", count)
							if tc.name != "module" {
								slots := count
								if tc.name == "churn" {
									slots *= 2
								}
								pattern := fmt.Sprintf(`\Aheap %s nodes=%d slots=%d collections=[0-9]+ scanned=[0-9]+ pass\n\z`, tc.name, count, slots)
								if !regexp.MustCompile(pattern).Match(out) {
									t.Fatalf("heap probe output %q does not match %s", out, pattern)
								}
							} else if string(out) != want {
								t.Fatalf("heap probe output %q, want %q", out, want)
							}
							assertBalancedCensus(t, stderr.String())
						})
					}
				})
			}
		})
	}
}

func TestSelfHostInterpManagedArrayIdentity(t *testing.T) {
	testSelfHostInterpManagedIdentity(t, "length", "managed interpreter array identity pass\n", true)
}

func TestSelfHostInterpManagedSocketIdentity(t *testing.T) {
	testSelfHostInterpManagedIdentity(t, "tcp", "managed interpreter socket identity pass\n", false)
}

func TestSelfHostInterpManagedReactorIdentity(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires the Linux reactor")
	}
	testSelfHostInterpManagedIdentity(t, "reactor", "managed interpreter reactor identity pass\n", false)
}

func testSelfHostInterpManagedIdentity(t *testing.T, fixture, expected string, portable bool) {
	t.Helper()
	native := e2eharness.HostSelfHostTarget()
	if native == "" {
		t.Skip("requires a native self-host target")
	}
	driver := e2eharness.SelfHostCLI(t)
	oracle := buildLangBinForInterp(t)
	entry, err := filepath.Abs(filepath.Join("testdata", "interpheap_identity", fixture+".fern"))
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"interp", "oracle", "native", "wasi", "native-interp", "wasi-interp"} {
		if !portable && strings.HasPrefix(mode, "wasi") {
			continue
		}
		t.Run(mode, func(t *testing.T) {
			var run *exec.Cmd
			census := false
			switch mode {
			case "interp":
				run = exec.Command(driver, "-interp", entry, e2eharness.SelfHostStdlibRoot(t))
			case "oracle":
				run = exec.Command(oracle, "-interp", entry)
			default:
				target := native
				var wasmtime string
				if strings.HasPrefix(mode, "wasi") {
					target = e2eharness.TargetWasm32Wasi
					wasmtime = e2eharness.Wasmtime(t)
				}
				input := entry
				if strings.HasSuffix(mode, "-interp") {
					input = e2eharness.RepoPath("compiler", "drivers", "interp_run.fern")
				}
				binary := filepath.Join(t.TempDir(), "identity")
				args := []string{"-target", target, "-sanitize"}
				if wasmtime != "" {
					args = append(args, "-emit", "core-module")
				}
				args = append(args, "-o", binary, input, e2eharness.SelfHostStdlibRoot(t))
				compile := exec.Command(driver, args...)
				compile.Env = childEnv("FERN_IR_VERIFY=1", "FERN_STRICT_IR=1")
				if out, err := compile.CombinedOutput(); err != nil {
					t.Fatalf("compile identity probe: %v\n%s", err, out)
				}
				run = exec.Command(binary)
				if wasmtime != "" {
					run = exec.Command(wasmtime, "run", "--env", "FERN_LEAKCHECK=1", binary)
				}
				if strings.HasSuffix(mode, "-interp") {
					run.Stdin = bytes.NewReader(source)
				}
				census = true
			}
			run.Env = childEnv("FERN_LEAKCHECK=1")
			var stderr bytes.Buffer
			run.Stderr = &stderr
			out, err := run.Output()
			if err != nil || string(out) != expected {
				t.Fatalf("array identity: %v\nstdout: %s\nstderr: %s", err, out, stderr.String())
			}
			if census {
				assertBalancedCensus(t, stderr.String())
			}
		})
	}
}
