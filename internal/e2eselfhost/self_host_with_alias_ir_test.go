package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// withAliasIRCases exercise the in-place `a = a.with(i, v)` self-reassign through
// the self-host IR path when the array `a` has a lasting LOCAL alias (#3599).
//
// The single-owner / leak model lowered the self-reassign as an in-place arr_set,
// which is UNSOUND once `a` is aliased (`var b = a`, captured into a struct
// literal, …): the in-place write mutates the buffer the alias still reads, so
// `b` observes the change. The interpreter and the native (Perceus) backend both
// copy-on-write and leave the alias unchanged. The fix detects the alias at
// lower_func time (aliased_array_names_of) and routes the aliased self-reassign
// through the value-producing clone (lower_arr_with_value) instead of the
// in-place store. The unaliased "no-alias" case still takes the in-place path.
//
// Each case is oracle-checked against the interpreter, routing-pinned to "ir",
// and returns a non-negative value <= 126.
var withAliasIRCases = []struct {
	name string
	main string
}{
	// The issue's minimal repro: a plain `var b = a` alias, mutate a, read both.
	// In-place mutation made b[0] == 9 too (9+9=18); copy-on-write keeps b[0]==1.
	{"basic", `function main(): i32 { var a = [1, 2, 3]; var b = a; a = a.with(0, 9); return a[0] + b[0]; }`},
	// Two live aliases of the same array; neither must see a's later mutation.
	{"two-alias", `function main(): i32 { var a = [1, 2]; var b = a; var c = a; a = a.with(0, 7); return b[0] + c[0]; }`},
	// The mutation is conditional (inside an if) but the alias is live across it.
	{"cond", `function main(): i32 { var a = [1, 2, 3]; var b = a; if (a[0] == 1) { a = a.with(1, 8); } return a[1] + b[1]; }`},
	// Alias captured into a struct field literal (`H { xs: a }`): the field still
	// references the original buffer, so the in-place mutate would corrupt it.
	{"struct-field-alias", `struct H { xs: i32[] }
function main(): i32 { var a = [1, 2, 3]; var h = H { xs: a }; a = a.with(0, 9); return a[0] + h.xs[0]; }`},
	// Nested array: `var h = g` aliases the outer array; replacing g[0] must not
	// disturb h[0][0].
	{"nested", `function main(): i32 { var g = [[1, 2], [3, 4]]; var h = g; g = g.with(0, [9, 9]); return g[0][0] + h[0][0]; }`},
	// REGRESSION: no alias — the in-place fast path must still apply and be
	// correct (a sole-owned array's .with returns the mutated value).
	{"no-alias", `function main(): i32 { var a = [1, 2, 3]; a = a.with(0, 9); return a[0] + a[1]; }`},
}

// TestSelfHostWithAliasIRX86_64 routes each case through the self-hosted x86-64 IR
// driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostWithAliasIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range withAliasIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
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

// TestSelfHostWithAliasIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostWithAliasIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host with-alias wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range withAliasIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
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
			watFile := filepath.Join(dir, "withalias_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("with-alias wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
