package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostWasmCoreExportsMain pins that the WASI core module (`-emit
// core-module` and the `-emit asm` text it is assembled from) exports `main`
// beside `_start`, so a host reads main's result with `wasmtime run --invoke
// main`. Through `_start` alone the result goes to proc_exit, which refuses a
// status outside [0..126), so an answer of 126 or more is unreadable that way.
func TestSelfHostWasmCoreExportsMain(t *testing.T) {
	cli := newStrictCLI(t)
	wat := cli.emit(t, "wasm32-wasi", "function main(): i32 { return 200; }")
	for _, want := range []string{`(export "main" (func $main))`, `(export "_start" (func $__fern_start))`} {
		if !strings.Contains(wat, want) {
			t.Fatalf("core module lacks %s:\n%s", want, wat)
		}
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	path := filepath.Join(t.TempDir(), "prog.wat")
	if err := os.WriteFile(path, []byte(wat), 0o644); err != nil {
		t.Fatal(err)
	}
	invoke := exec.Command("wasmtime", "run", "--invoke", "main", path)
	var stdout, stderr bytes.Buffer
	invoke.Stdout = &stdout
	invoke.Stderr = &stderr
	if err := invoke.Run(); err != nil {
		t.Fatalf("wasmtime --invoke main: %v\n%s", err, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "200" {
		t.Fatalf("--invoke main printed %q, want 200", got)
	}
	if code, _ := runWasm(t, wat); code == 200 {
		t.Fatalf("a bare run exited 200: proc_exit accepted a status outside [0..126), so the main export is no longer the only way to read it")
	}
}
