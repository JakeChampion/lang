package e2ecompiler

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostWriteRows(t *testing.T) {
	h := selfHostCLIForHost(t)
	targets := append([]ssaBackendTarget{}, h.targets...)
	if wasmtime, err := exec.LookPath("wasmtime"); err == nil {
		targets = append(targets, ssaBackendTarget{target: "wasm32-wasi", runner: []string{wasmtime, "run", "--env", "FERN_LEAKCHECK=1"}})
	} else {
		t.Run("wasm32-wasi", func(t *testing.T) { t.Skip("requires wasmtime") })
	}
	for _, target := range targets {
		t.Run(target.target, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "write-rows")
			entry := e2eharness.RepoPath("compiler", "drivers", "ssa_write_rows_run.fern")
			build := exec.Command(h.cli, "-target", target.target, "-sanitize", "-o", binary, entry, h.stdlib)
			build.Env = childEnv("FERN_IR_VERIFY=1", "FERN_STRICT_IR=1")
			if out, err := build.CombinedOutput(); err != nil {
				t.Fatalf("compile write rows: %v\n%s", err, out)
			}
			command := append(append([]string{}, target.runner...), binary)
			run := exec.Command(command[0], command[1:]...)
			run.Env = childEnv("FERN_LEAKCHECK=1")
			var stderr bytes.Buffer
			run.Stderr = &stderr
			out, err := run.Output()
			if err != nil || len(out) != 0 {
				t.Fatalf("write rows: %v\nstdout: %s\nstderr: %s", err, out, stderr.String())
			}
			assertBalancedCensus(t, stderr.String())
		})
	}
}
