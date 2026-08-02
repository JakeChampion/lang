package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// compoundAssignWiderIRCases exercise compound assignment (`+= -= *= /= %=`)
// beyond the i32 path through the self-host IR path on x86-64 + wasm. A compound
// assignment is a read-modify-write: the lowering must load the local, apply the
// op, and store back with width-correct semantics — i64 / u32 / u8 (wrap) / f64
// in addition to the full i32 operator set, plus loop accumulation via `+=`.
//
// This pins the wider-type half of the "Compound assignment += -= *= …" audit
// row (docs/FEATURE-AUDIT.md), previously exercised on the i32 path. Each case
// is oracle-checked against the interpreter, routing-pinned to "ir", and returns
// a value <= 120 (wasmtime exit-code truncation, cf. #2908).
var compoundAssignWiderIRCases = []struct {
	name string
	main string
}{
	// i64 `+=` / `*=`.
	{"i64-plus-eq", `function main(): i32 { var n = 5 as i64; n += 3 as i64; return n as i32; }`},
	{"i64-mul-eq", `function main(): i32 { var n = 4 as i64; n *= 3 as i64; return n as i32; }`},
	// u32 `+=` -> 120 (<= wasm clamp).
	{"u32-plus-eq", `function main(): i32 { var n = 100 as u32; n += 20 as u32; return n as i32; }`},
	// u8 `+=` wraps mod 256: 250 + 10 -> 4.
	{"u8-wrap-eq", `function main(): i32 { var n = 250 as u8; n += 10 as u8; return n as i32; }`},
	// f64 `+=` / `*=`.
	{"f64-plus-eq", `function main(): i32 { var x = 2.5; x += 1.5; return x as i32; }`},
	{"f64-mul-eq", `function main(): i32 { var x = 3.0; x *= 4.0; return x as i32; }`},
	// The rest of the i32 operator set: `-=` / `/=` / `%=`.
	{"i32-minus-eq", `function main(): i32 { var n = 20; n -= 5; return n; }`},
	{"i32-div-eq", `function main(): i32 { var n = 20; n /= 4; return n; }`},
	{"i32-mod-eq", `function main(): i32 { var n = 23; n %= 5; return n; }`},
	// Loop accumulation into an i64 via `+=`: 0+1+2+3+4 = 10.
	{"loop-accum-i64", `function main(): i32 { var s = 0 as i64; var i = 0; while (i < 5) { s += i as i64; i = i + 1; } return s as i32; }`},
}

// TestSelfHostCompoundAssignWiderIRX86_64 routes each case through the
// self-hosted x86-64 IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostCompoundAssignWiderIRX86_64(t *testing.T) {
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

	for _, tc := range compoundAssignWiderIRCases {
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

// TestSelfHostCompoundAssignWiderIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostCompoundAssignWiderIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host compound-assign wasm IR e2e")
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

	for _, tc := range compoundAssignWiderIRCases {
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
			watFile := filepath.Join(dir, "compoundassign_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("compound-assign wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
