package e2ecompiler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostInterpreterFunctionBindings(t *testing.T) {
	native := e2eharness.HostSelfHostTarget()
	if native == "" {
		t.Skip("requires a native self-host target")
	}
	cli := e2eharness.SelfHostCLI(t)
	project := t.TempDir()
	copySelfHostFiles(t, project, "interp.fern")
	source, err := os.ReadFile("testdata/interp_function_bindings/main.fern")
	if err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(project, "main.fern")
	if err := os.WriteFile(entry, source, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{native, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "bindings")
			args := []string{"-target", target, "-sanitize"}
			var wasmtime string
			if target == e2eharness.TargetWasm32Wasi {
				wasmtime = e2eharness.Wasmtime(t)
				args = append(args, "-emit", "core-module")
			}
			args = append(args, "-o", binary, entry, e2eharness.SelfHostStdlibRoot(t))
			compile := exec.Command(cli, args...)
			compile.Env = childEnv("FERN_IR_VERIFY=1", "FERN_STRICT_IR=1")
			if out, err := compile.CombinedOutput(); err != nil {
				t.Fatalf("compile function bindings: %v\n%s", err, out)
			}
			for _, rounds := range []int{8, 1024} {
				t.Run(fmt.Sprint(rounds), func(t *testing.T) {
					run := exec.Command(binary)
					if wasmtime != "" {
						run = exec.Command(wasmtime, "run", "--env", "FERN_LEAKCHECK=1", "--env", fmt.Sprintf("FERN_HEAP_PILOT_ROUNDS=%d", rounds), binary)
					}
					run.Env = childEnv("FERN_LEAKCHECK=1", fmt.Sprintf("FERN_HEAP_PILOT_ROUNDS=%d", rounds))
					var stderr bytes.Buffer
					run.Stderr = &stderr
					out, err := run.Output()
					if err != nil || string(out) != "function bindings and cycles pass\n" {
						t.Fatalf("function bindings: %v\nstdout: %s\nstderr: %s", err, out, stderr.String())
					}
					assertBalancedCensus(t, stderr.String())
				})
			}
		})
	}
}
