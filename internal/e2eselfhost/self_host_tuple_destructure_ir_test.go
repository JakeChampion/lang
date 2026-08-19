package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// tupleDestructureIRCases pin `var (a, b) = E` / `let (a, b) = E` tuple
// destructuring on the IR path. The destructure already lowers fully through IR
// (irlower.fern's StmtVar arm emits op_tuple_get reads into the freshly-bound
// locals — no bail), but the existing TestSelfHostTupleDestructure* assert only
// exit codes, which the legacy AST emitter also satisfies. So a silent regression
// that kicked destructuring off the IR path would pass undetected — and the
// `let (a, b)` form has a documented history of hanging the self-host compiler,
// exactly the kind of frontier that warrants a hard IR-path pin.
//
// Each program declares a fresh, non-escaping struct temp whose IR-only reclaim
// free (`call __fn___fern_arr_dec`) proves the module took the IR path — the AST
// fallback is leak-only and emits none. `t.x - t.y` pads 0 into every result, so
// exit codes still pin the destructured values. Every tuple stays scalar
// `(i32, i32)` (the confirmed-lowering shape); the struct only appears as the
// separate pad temp, never as a tuple element. Mirrors self_host_if_let_ir_test.go.
var tupleDestructureIRCases = []struct {
	name string
	src  string
	exit int
}{
	{"from-fn-return", "struct Point { x: i32, y: i32 } function swap(a: i32, b: i32): (i32, i32) { return (b, a); } function main(): i32 { var t: Point = Point { x: 1, y: 1 }; var pad: i32 = t.x - t.y; var (x, y) = swap(10, 32); return x + y + pad; }", 42},
	{"from-local", "struct Point { x: i32, y: i32 } function main(): i32 { var t: Point = Point { x: 1, y: 1 }; var pad: i32 = t.x - t.y; var p: (i32, i32) = (15, 27); var (a, b) = p; return a + b + pad; }", 42},
	{"first-only", "struct Point { x: i32, y: i32 } function mk(): (i32, i32) { return (42, 7); } function main(): i32 { var t: Point = Point { x: 1, y: 1 }; var pad: i32 = t.x - t.y; var (a, b) = mk(); return a + pad; }", 42},
	{"second-only", "struct Point { x: i32, y: i32 } function mk(): (i32, i32) { return (7, 42); } function main(): i32 { var t: Point = Point { x: 1, y: 1 }; var pad: i32 = t.x - t.y; var (a, b) = mk(); return b + pad; }", 42},
	{"let-from-fn-return", "struct Point { x: i32, y: i32 } function swap(a: i32, b: i32): (i32, i32) { return (b, a); } function main(): i32 { var t: Point = Point { x: 1, y: 1 }; var pad: i32 = t.x - t.y; let (x, y) = swap(10, 32); return x + y + pad; }", 42},
	{"let-from-local", "struct Point { x: i32, y: i32 } function main(): i32 { var t: Point = Point { x: 1, y: 1 }; var pad: i32 = t.x - t.y; var p: (i32, i32) = (15, 27); let (a, b) = p; return a + b + pad; }", 42},
}

// TestSelfHostTupleDestructureIRX86_64 compiles each case through the self-hosted
// x86-64 driver (asm_run, IR default-on), asserts the IR path was taken (the
// reclaimed-temp struct free), and asserts the destructured exit code.
func TestSelfHostTupleDestructureIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range tupleDestructureIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			if bytes.Count(asm, []byte("call __fn___fern_arr_dec")) == 0 {
				t.Fatalf("%s: no struct free emitted — the tuple-destructure IR path was NOT exercised", tc.name)
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
