package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostWasmUserNamesDoNotShadowTheRuntime defines a user function under
// each name the wasm runtime once gave its own imports and entry points, then
// calls the builtins that reach those imports. The runtime's ids carry the
// __fern_ prefix, so neither side takes the other's definition: a user
// `fd_write` used to make every print call it with the import's four
// arguments.
func TestSelfHostWasmUserNamesDoNotShadowTheRuntime(t *testing.T) {
	cli := buildSelfHostCLI(t)
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	names := []string{
		"fd_write", "fd_read", "path_open", "fd_close", "proc_exit", "clock_time_get", "environ_get",
		"wall_now", "mono_now", "get_stdout", "bwf", "get_arguments", "get_environment",
		"cabi_realloc", "_start", "_lang_run",
	}
	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, "function %s(n: i32): i32 { if (n <= 0) { return 0; } return %s(n - 1) + 1; }\n", n, n)
	}
	b.WriteString("function main(): i32 {\n    let k: i32 = args().len();\n    let s: i32 = 0;\n")
	for _, n := range names {
		fmt.Fprintf(&b, "    s = s + %s(k);\n", n)
	}
	fmt.Fprintf(&b, `    print("hi");
    match (read_file("absent")) { Ok(_) => { return 100; }, Err(_) => {} }
    if (now_unix_ms() <= 0 as i64) { return 101; }
    match (env("FERN_NAMES")) { Some(v) => { if (v != "set") { return 102; } }, None => { return 103; } }
    return s - %d * k;
}
`, len(names))
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(t *testing.T, module string) {
		cmd := exec.Command(wasmtime, "run", "--dir=.", "--env", "FERN_NAMES=set", module)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("run: %v\n%s", err, out)
		}
		if string(out) != "hi\n" {
			t.Fatalf("output = %q, want %q", out, "hi\n")
		}
	}
	t.Run("core", func(t *testing.T) {
		run(t, cli.emit(t, src, "wasm32-wasi"))
	})
	t.Run("component", func(t *testing.T) {
		run(t, cli.wasmComponent(t, src))
	})
}
