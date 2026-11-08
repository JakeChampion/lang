package e2ecompiler

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostULCUnit(t *testing.T) {
	target := e2eharness.HostSelfHostTarget()
	if target == "" {
		t.Skip("no native self-host target")
	}
	runULCUnit(t, target)
}

func TestSelfHostULCUnitWASI(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime is unavailable")
	}
	runULCUnit(t, e2eharness.TargetWasm32Wasi)
}

func runULCUnit(t *testing.T, target string) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "ulc-unit")
	cmd := e2eharness.SelfHostCompileCmd(t, target, e2eharness.RepoPath("examples", "ulc", "tests", "unit.fern"), bin)
	cmd.Env = e2eharness.SelfHostChildEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile codec units: %v\n%s", err, out)
	}
	run := exec.Command(bin)
	if target == e2eharness.TargetWasm32Wasi {
		run = e2eharness.RunWasmCore(t, bin)
	}
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("codec units: %v\n%s", err, out)
	}
}

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

func TestSelfHostULCAccuracy(t *testing.T) {
	target := e2eharness.HostSelfHostTarget()
	if target == "" {
		t.Skip("no native self-host target")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is unavailable")
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "ulc")
	reference := filepath.Join(dir, "reference64")
	for _, entry := range []struct{ source, output string }{
		{"ulc.fern", binary}, {"tests/reference64.fern", reference},
	} {
		cmd := e2eharness.SelfHostCompileCmd(t, target, e2eharness.RepoPath("examples", "ulc", entry.source), entry.output)
		cmd.Env = e2eharness.SelfHostChildEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("compile accuracy decoder: %v\n%s", err, out)
		}
	}
	run := exec.Command(python, e2eharness.RepoPath("examples", "ulc", "tests", "accuracy.py"))
	for _, value := range e2eharness.SelfHostChildEnv() {
		if !strings.HasPrefix(value, "ULC=") && !strings.HasPrefix(value, "ULC_REFERENCE64=") {
			run.Env = append(run.Env, value)
		}
	}
	run.Env = append(run.Env, "ULC="+binary, "ULC_REFERENCE64="+reference)
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("FFT accuracy: %v\n%s", err, out)
	}
}
