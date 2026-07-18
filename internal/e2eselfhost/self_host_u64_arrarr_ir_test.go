package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// u64ArrArrIRCases exercise a nested u64[][] element read (m[i][j]) chained in
// an unsigned op. The arrarr layer records the inner element kind as a string;
// before #5206 a u64[][] inner element was classified "i64" (its width only),
// so expr_is_u64's nested-ExprIndex arm couldn't recover the sign and the read
// used a SIGNED shift / compare — diverging once bit 63 was set. The fix keeps a
// distinct "u64" arrarr tag (8-byte WIDTH still shared with "i64" via
// arr_index_is_i64) so shr_u / lt_u are selected.
//
// Routing-pinned to "ir", oracle-checked against the interpreter, values <= 120
// (the wasmtime exit-code gap #2908). The wide element 18000000000000000000 has
// bit 63 set, so signed vs unsigned shift/compare give DIFFERENT results — the
// case would pass trivially on the old signed path only if they coincided.
var u64ArrArrIRCases = []struct {
	name string
	main string
}{
	// m[0][0] >> 58: unsigned = 0xF9CCD8A1C5080000 >> 58 = 62; signed (arith) = 254.
	{"nested-shr", `function main(): i32 { var m: u64[][] = [[18000000000000000000, 1], [2, 3]]; var r: u64 = m[0][0] >> 58; return r as i32; }`},
	// m[0][0] > 100: unsigned true (7); signed (negative) false (9).
	{"nested-cmp", `function main(): i32 { var m: u64[][] = [[18000000000000000000, 1], [2, 3]]; if (m[0][0] > (100 as u64)) { return 7; } return 9; }`},
	// Alias a u64[][] local, then nested-index the alias: the "u64" arrarr tag
	// must propagate across the aliasing bind.
	{"nested-alias", `function main(): i32 { var m: u64[][] = [[18000000000000000000, 1], [2, 3]]; var n: u64[][] = m; var r: u64 = n[0][0] >> 58; return r as i32; }`},
	// i64[][] width regression: the shared 8-byte width path must still read the
	// full element (not truncate) — value fits so signed/unsigned agree here.
	{"i64-width-regress", `function main(): i32 { var m: i64[][] = [[10, 20], [30, 40]]; return m[1][0] as i32; }`},
}

func TestSelfHostU64ArrArrIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asm_run.fern", "asm_pathprobe_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")
	for _, tc := range u64ArrArrIRCases {
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

func TestSelfHostU64ArrArrIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host u64[][] wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")
	for _, tc := range u64ArrArrIRCases {
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
			watFile := filepath.Join(dir, "u64_arrarr_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("u64[][] wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
