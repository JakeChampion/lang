package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const shadowedGenericProg = `function id[T](x: T): T { return x; }
function inc(x: i32): i32 { return x + 1; }
function twice(x: i32): i32 { return x * 2; }
function apply(v: i32, id: (i32) => i32): i32 { return id(v); }
function plain(v: i32): i32 { return id(v); }
function main(): i32 {
    let id: (i32) => i32 = twice;
    function via_capture(v: i32): i32 { return id(v); }
    return apply(7, inc) + id(10) + plain(3) + via_capture(4);
}
`

const shadowedUseProg = `function withRes(cb: (i32) => i32): i32 { return cb(4); }
function taker(f: (string) => i32): i32 { return f("hi"); }
function g(): i32 {
    let withRes = taker;
    use x <- withRes();
    if (x == "hi") { return 21; }
    return 0;
}
function main(): i32 { return g(); }
`

var shadowedCases = []struct {
	name string
	src  string
	want int
}{
	{"shadowed_generic", shadowedGenericProg, 39},
	{"shadowed_use", shadowedUseProg, 21},
}

func TestNativeShadowedGenericCall(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(p, []byte(shadowedGenericProg), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, code := runFixtureInterp(t, p, ""); code != 39 {
		t.Errorf("shadowed-generic interp = %d, want 39", code)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != 39 {
		t.Errorf("shadowed-generic x86-64 = %d, want 39", code)
	}
	if code := runWasm(t, shadowedGenericProg); code != 39 {
		t.Errorf("shadowed-generic wasm = %d, want 39", code)
	}
}

func TestNativeShadowedUseClauseCallee(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(p, []byte(shadowedUseProg), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, code := runFixtureInterp(t, p, ""); code != 21 {
		t.Errorf("shadowed-use interp = %d, want 21", code)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != 21 {
		t.Errorf("shadowed-use x86-64 = %d, want 21", code)
	}
	if code := runWasm(t, shadowedUseProg); code != 21 {
		t.Errorf("shadowed-use wasm = %d, want 21", code)
	}
}

func TestSelfHostShadowedGenericCallIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile(filepath.Join("../../../compiler", "drivers/asm_run.fern"))
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	for _, tc := range shadowedCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
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
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s self-host x86-64 = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostShadowedGenericCallIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host shadowed-generic wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range shadowedCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %s: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			runc := exec.Command("wasmtime", "run", watFile)
			_ = runc.Run()
			if runc.ProcessState == nil || !runc.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s:\n%s", tc.name, wat)
			}
			if code := runc.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s wasm IR = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
