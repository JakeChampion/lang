// Package e2eharness holds the shared e2e test harness — driver builds,
// tooling discovery, caches — used by both internal/testing/e2e and
// internal/testing/e2ecompiler (#4398 part 3). Extracted verbatim from
// internal/testing/e2e/diff_oracle_test.go.
package e2eharness

import (
	"bytes"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// CompileAndRunWasmbinMain compiles src with the current self-host compiler
// to a WASI core module and runs it under wasmtime; main's result is the
// process exit code.
//
// Skips the test if wasmtime is not on PATH.
func CompileAndRunWasmbinMain(t *testing.T, src string) int {
	t.Helper()
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	core := CompileSelfHostSource(t, TargetWasm32Wasi, src, nil)
	cmd := RunWasmCore(t, core)
	// main's i32 is read from the invoke's stdout: a WASI exit carries only
	// 0..125, and the differential's programs return any byte.
	cmd.Args = append(cmd.Args[:2], append([]string{"--invoke", "main"}, cmd.Args[2:]...)...)
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	err := cmd.Run()
	if cmd.ProcessState == nil {
		t.Fatalf("wasmtime: %v\nsrc:\n%s", err, src)
	}
	// A program may eprint; only wasmtime's own trap or error report fails the run.
	if strings.Contains(se.String(), "wasm trap") || strings.Contains(se.String(), "Error: ") {
		t.Fatalf("WASM-RUN-FAIL: %v (exit %d)\nstderr:\n%s\nsrc:\n%s", err, cmd.ProcessState.ExitCode(), se.String(), src)
	}
	trimmed := strings.TrimSpace(so.String())
	if i := strings.LastIndex(trimmed, "\n"); i >= 0 {
		trimmed = strings.TrimSpace(trimmed[i+1:])
	}
	got, perr := strconv.Atoi(trimmed)
	if perr != nil {
		t.Fatalf("parse wasm invoke stdout %q: %v\nsrc:\n%s", trimmed, perr, src)
	}
	return got & 0xFF
}
