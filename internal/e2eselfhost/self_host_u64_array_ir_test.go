package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// u64ArrayIRCases widen the self-host IR subset to u64[] arrays. i64[] / f64[]
// already rode the 8-byte-element path (op_arr_make_i64 + the i64arr element-width
// mark); u64[] was deferred. The fix rides the SAME 8-byte path and marks the slot
// u64 for UNSIGNED element arithmetic — and crucially is_i64_slot now excludes
// array (pointer) slots, so a u64[] local stays an i32 pointer (the wasm verifier
// rejects an i32 array pointer stored into an i64 local). u64[] as a struct field
// still bails for now (field-tag width dispatch — a separate increment).
//
// Each case is routing-pinned to "ir", oracle-checked against the interpreter,
// and returns a value <= 120 (cf. the wasmtime exit-code gap #2908).
var u64ArrayIRCases = []struct {
	name string
	main string
}{
	{"len", `function main(): i32 { var xs: u64[] = [1 as u64, 2 as u64, 3 as u64]; return xs.len(); }`},
	{"index", `function main(): i32 { var xs: u64[] = [10 as u64, 20 as u64, 30 as u64]; return xs[1] as i32; }`},
	{"iterate", `function main(): i32 { var xs: u64[] = [1 as u64, 2 as u64, 3 as u64, 4 as u64]; var s: u64 = 0 as u64; for x in xs { s = s + x; } return s as i32; }`},
	{"alias", `function main(): i32 { var xs: u64[] = [7 as u64, 8 as u64]; var ys: u64[] = xs; return ys[0] as i32; }`},
	{"wide-value", `function main(): i32 { var xs: u64[] = [5000000007 as u64]; return (xs[0] % 1000 as u64) as i32; }`},
	{"as-param", `function total(xs: u64[]): u64 { var s: u64 = 0 as u64; for x in xs { s = s + x; } return s; }
function main(): i32 { var xs: u64[] = [5 as u64, 6 as u64, 7 as u64]; return total(xs) as i32; }`},
	{"i64-regress", `function main(): i32 { var xs: i64[] = [10, 20, 30]; var s: i64 = 0; for x in xs { s = s + x; } return s as i32; }`},
}

func TestSelfHostU64ArrayIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")
	for _, tc := range u64ArrayIRCases {
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

func TestSelfHostU64ArrayIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host u64[] wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")
	for _, tc := range u64ArrayIRCases {
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
			watFile := filepath.Join(dir, "u64_array_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("u64[] wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
