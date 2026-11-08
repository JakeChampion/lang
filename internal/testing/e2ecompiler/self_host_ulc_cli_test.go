package e2ecompiler

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostULCCLIWASI(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime is unavailable")
	}
	bin := filepath.Join(t.TempDir(), "ulc.wasm")
	cmd := e2eharness.SelfHostCompileCmd(t, e2eharness.TargetWasm32Wasi, e2eharness.RepoPath("examples", "ulc", "ulc.fern"), bin)
	cmd.Env = e2eharness.SelfHostChildEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile portable CLI: %v\n%s", err, out)
	}
	out, err := e2eharness.RunWasmCore(t, bin).CombinedOutput()
	status, ok := err.(*exec.ExitError)
	if !ok || status.ExitCode() != 1 || !strings.Contains(string(out), "ULC2 codec in Fern") {
		t.Fatalf("usage: %v\n%s", err, out)
	}
}

func TestSelfHostULCNativeCLI(t *testing.T) {
	target := e2eharness.HostSelfHostTarget()
	if target == "" {
		t.Skip("no native self-host target")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is unavailable")
	}
	bin := filepath.Join(t.TempDir(), "ulc")
	cmd := e2eharness.SelfHostCompileCmd(t, target, e2eharness.RepoPath("examples", "ulc", "ulc.fern"), bin)
	cmd.Env = e2eharness.SelfHostChildEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile native CLI: %v\n%s", err, out)
	}
	run := exec.Command(python, e2eharness.RepoPath("examples", "ulc", "tests", "integration.py"))
	for _, value := range e2eharness.SelfHostChildEnv() {
		if !strings.HasPrefix(value, "ULC=") && !strings.HasPrefix(value, "ULC_REFERENCE_DIR=") {
			run.Env = append(run.Env, value)
		}
	}
	run.Env = append(run.Env, "ULC="+bin)
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("native CLI integration: %v\n%s", err, out)
	}
}
