package e2eharness

import (
	"bytes"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

func CompileAndRunWasmbinMain(t *testing.T, src string) int {
	t.Helper()
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	core := CompileSelfHostSource(t, TargetWasm32Wasi, src, nil)
	cmd := RunWasmCore(t, core)
	cmd.Args = append(cmd.Args[:2], append([]string{"--invoke", "main"}, cmd.Args[2:]...)...)
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	err := cmd.Run()
	if cmd.ProcessState == nil {
		t.Fatalf("wasmtime: %v\nsrc:\n%s", err, src)
	}
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
