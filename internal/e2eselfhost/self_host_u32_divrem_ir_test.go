package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// u32DivRemIRCases guard a real correctness bug: u32 `/` and `%` were lowered as
// SIGNED div/rem on the self-host IR path, so a u32 numerator >= 2^31 (which reads
// as signed-negative in a 32-bit slot) produced the wrong quotient on x86-64 and
// wasm (arm64 happened to be right because its 64-bit register held the value
// zero-extended). The interpreter computes unsigned. The fix remaps u32 div_s/rem_s
// to div_u/rem_u (the same treatment u32 ordering compares and u64 div already get)
// and adds i32.div_u/i32.rem_u to the wasm backend.
//
// Each case is oracle-checked against the interpreter, routing-pinned to "ir", and
// returns a value <= 126 (the high-bit-set quotients are reduced to a small code via
// an equality branch, cf. #2908).
var u32DivRemIRCases = []struct {
	name string
	main string
}{
	// 3e9 / 3 = 1e9 (unsigned). Signed div of 3e9-as-i32 (negative) differs.
	{"div-highbit", `function main(): i32 { var u: u32 = 3000000000 as u32; if (u / (3 as u32) == (1000000000 as u32)) { return 5; } return 9; }`},
	// 3000000003 % 10 = 3 (unsigned).
	{"rem-highbit", `function main(): i32 { var u: u32 = 3000000003 as u32; if (u % (10 as u32) == (3 as u32)) { return 5; } return 9; }`},
	// 4e9 / 2 = 2e9 (unsigned).
	{"div-4e9", `function main(): i32 { var u: u32 = 4000000000 as u32; if (u / (2 as u32) == (2000000000 as u32)) { return 5; } return 9; }`},
	// Low-value u32 div still works and fits the exit code directly: 100/4 = 25.
	{"div-low", `function main(): i32 { var u: u32 = 100 as u32; return (u / (4 as u32)) as i32; }`},
	// u32 rem low value: 100 % 7 = 2.
	{"rem-low", `function main(): i32 { var u: u32 = 100 as u32; return (u % (7 as u32)) as i32; }`},
}

// TestSelfHostU32DivRemIRX86_64 routes each case through the self-hosted x86-64 IR
// driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostU32DivRemIRX86_64(t *testing.T) {
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

	for _, tc := range u32DivRemIRCases {
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

// TestSelfHostU32DivRemIRWasm runs the same cases through the wasm IR backend (one
// of the two backends the signed-div bug affected).
func TestSelfHostU32DivRemIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host u32 div/rem wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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

	for _, tc := range u32DivRemIRCases {
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
			watFile := filepath.Join(dir, "u32divrem_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("u32 div/rem wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
