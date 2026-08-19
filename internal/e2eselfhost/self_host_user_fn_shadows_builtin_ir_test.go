package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// #3710: on the self-host IR path a user-defined free function whose name
// collides with a builtin (`len`, `chr`, the `str_*` free calls, …) was
// silently mis-lowered as the builtin op instead of being called. native +
// interp correctly resolve `name(args)` to the user function when one is
// declared; these cases pin the IR path to the same behaviour.
//
// Each case defines a free function that shadows a builtin and returns a value
// the builtin form could never produce (the repro `len` over an enum reads 0
// via op_arr_len; `chr`/`str_index_of` would route to the string runtime).
// Routing-pinned to "ir", oracle-checked against the interpreter.
type userFnShadowCase struct {
	name string
	src  string
}

var userFnShadowCases = []userFnShadowCase{
	// The filed repro: a user `len` over an enum linked list. The builtin
	// intercept lowered `len(t)` as op_arr_len on the (non-array) enum box → 0.
	{"len-enum", `
enum L { C(i32, L), N }
function len(l: L): i32 {
    match (l) { C(h, t) => { return 1 + len(t); }, N => { return 0; } }
}
function main(): i32 {
    var l: L = C(1, C(2, C(3, N)));
    return len(l);   // 3
}`},
	// A 1-arg value builtin (`chr`): the builtin would route to __fern_chr (a
	// string box); the user function returns an i32.
	{"chr-i32", `
function chr(n: i32): i32 { return n * 2; }
function main(): i32 { return chr(21); }   // 42`},
	// A str_* free-call sibling (`str_index_of`): the builtin would route to
	// lower_str_method expecting a string receiver.
	{"str_index_of-i32", `
function str_index_of(a: i32, b: i32): i32 { return a - b; }
function main(): i32 { return str_index_of(50, 8); }`},
}

func TestSelfHostUserFnShadowsBuiltinIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "ufs_driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "ufs_probe")

	for _, tc := range userFnShadowCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src)
			want := interpExit(t, interpBin, tc.src)
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

func TestSelfHostUserFnShadowsBuiltinIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host user-fn-shadow wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "ufs_wasm_driver")

	for _, tc := range userFnShadowCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src)
			want := interpExit(t, interpBin, tc.src)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "ufs_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("user-fn-shadow wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
