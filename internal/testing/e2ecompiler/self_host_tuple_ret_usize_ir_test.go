package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var tupleRetUsizeCases = []struct {
	name string
	src  string
	exit int
}{
	{"usize-tuple-ret-dot", `function mk(): (usize, usize) { let a: usize = __alloc(64); let b: usize = __alloc(64); __store_i32(a, 11); __store_i32(b, 22); return (a, b); }
function main(): i32 { let t = mk(); return __load_i32(t.0) + __load_i32(t.1); }`, 33},
	{"usize-tuple-ret-annotated", `function mk(): (usize, usize) { let a: usize = __alloc(64); __store_i32(a, 6); let b: usize = __alloc(64); __store_i32(b, 7); return (a, b); }
function main(): i32 { let t: (usize, usize) = mk(); return __load_i32(t.0) + __load_i32(t.1); }`, 13},
	{"usize-tuple-ret-destructure", `function mk(): (usize, usize) { let a: usize = __alloc(64); let b: usize = __alloc(64); __store_i32(a, 4); __store_i32(b, 5); return (a, b); }
function main(): i32 { let (p, q) = mk(); return __load_i32(p) * __load_i32(q); }`, 20},
	{"usize-tuple-ret-mixed", `function mk(n: i32): (usize, i32) { let a: usize = __alloc(64); __store_i32(a, n); return (a, n + 1); }
function main(): i32 { let t = mk(9); return __load_i32(t.0) + t.1; }`, 19},
	{"usize-tuple-elem-offset", `function mk(): (usize, usize) { let a: usize = __alloc(256); let b: usize = __alloc(256); return (a, b); }
function main(): i32 { let t = mk(); __store_i32(t.0 + 32 as usize, 41); __store_i32(t.1 + 64 as usize, 1); if (__load_i32(t.0 + 32 as usize) != 41) { return 90; } if (__load_i32(t.1 + 64 as usize) != 1) { return 91; } return 7; }`, 7},
	{"usize-struct-field", `struct C { p: usize, n: i32, q: usize }
function mk(): C { let a: usize = __alloc(64); __store_i32(a, 3); let b: usize = __alloc(64); __store_i32(b, 4); return C { p: a, n: 5, q: b }; }
function main(): i32 { let c = mk(); return __load_i32(c.p) + c.n + __load_i32(c.q); }`, 12},
	{"char-tuple-ret", `function mk(): (char, i32) { let c: char = 'A'; return (c, 1); }
function main(): i32 { let t = mk(); if (t.0 == 'A') { return 6; } return t.1; }`, 6},
	{"char-struct-field", `struct K { c: char, n: i32 }
function mk(): K { return K { c: 'z', n: 3 }; }
function main(): i32 { let k = mk(); if (k.c == 'z') { return k.n + 4; } return 0; }`, 7},
}

func TestSelfHostTupleRetUsizeIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	for _, tc := range tupleRetUsizeCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
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
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

func TestSelfHostTupleRetUsizeIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range tupleRetUsizeCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

func TestSelfHostTupleRetUsizeIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host tuple-ret-usize wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range tupleRetUsizeCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %s: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
