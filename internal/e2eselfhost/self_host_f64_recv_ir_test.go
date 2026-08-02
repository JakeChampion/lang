package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// f64RecvIRCases exercise scalar f64 VALUE-receiver methods (`(x: f64) m(): f64`,
// called `a.m()`) through the self-host IR path on x86-64 + wasm.
//
// The bug: expr_is_f64 classified a method call `recv.m()` as f64 only when the
// receiver was a STRUCT (expr_struct_type), so a scalar f64 value receiver fell
// through as "not f64". A following `a.m() as i32` then took the integer cast
// path and masked the DOUBLE's low 32 bits (→ 0) instead of truncating via
// f64_to_i32. The fix resolves a scalar receiver's primitive type
// (expr_recv_prim_type) as a fallback so an f64-returning method `<f64>.m` is
// recognised as an f64 value. (i64 value-receiver methods are unaffected — i32
// truncation of an i64 result happens to equal the low-32-bit mask the integer
// path already used; their separate wasm legacy-AST gap is out of scope.)
//
// Each case casts its f64 result to i32 and returns a non-negative value kept
// <= 126 (the wasmtime exit-code truncation gap, cf. #2908), oracle-checked
// against the interpreter and routing-pinned to "ir".
var f64RecvIRCases = []struct {
	name string
	main string
}{
	// Receiver in f64 arithmetic: 3.5 + 3.5 = 7.
	{"arith", `function (x: f64) dbl(): f64 { return x + x; }
function main(): i32 { var a: f64 = 3.5; return a.dbl() as i32; }`},
	// Identity receiver: the value flows straight back out as f64.
	{"id", `function (x: f64) id(): f64 { return x; }
function main(): i32 { var a: f64 = 5.5; return a.id() as i32; }`},
	// Division in the method body: 9.0 / 2.0 = 4.5 -> 4.
	{"div", `function (x: f64) half(): f64 { return x / 2.0; }
function main(): i32 { var a: f64 = 9.0; return a.half() as i32; }`},
	// Method result feeding further f64 arithmetic: (4+1) + 2 = 7.
	{"chain", `function (x: f64) inc(): f64 { return x + 1.0; }
function main(): i32 { var a: f64 = 4.0; var r: f64 = a.inc() + 2.0; return r as i32; }`},
	// Method body calls an f64 math intrinsic: sqrt(16) = 4.
	{"intrinsic", `function (x: f64) sq(): f64 { return __sqrt_f64(x); }
function main(): i32 { var a: f64 = 16.0; return a.sq() as i32; }`},
}

// TestSelfHostF64RecvIRX86_64 routes each case through the self-hosted x86-64 IR
// driver, oracle-checked, with routing pinned to the "ir" path.
func TestSelfHostF64RecvIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range f64RecvIRCases {
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

// TestSelfHostF64RecvIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostF64RecvIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host f64-receiver wasm IR e2e")
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

	for _, tc := range f64RecvIRCases {
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
			watFile := filepath.Join(dir, "f64recv_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("f64-receiver wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
