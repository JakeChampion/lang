package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var fnValueCaptureIRCases = []struct {
	name string
	src  string
	exit int
}{
	{"nested-local-fn-captured", `function mk(): (i32) => i32 {
    function lf(x: i32): i32 { return x + 1i32; }
    return ((x0: i32) => (lf(x0) * 2i32));
}
function main(): i32 { return mk()(3i32) & 63i32; }`, 8},
	{"annotated-fn-local-captured", `function mk(): (i32) => i32 {
    let lf: (i32) => i32 = ((x: i32) => (x + 1i32));
    return ((x0: i32) => (lf(x0) * 2i32));
}
function main(): i32 { return mk()(3i32) & 63i32; }`, 8},
	{"fn-and-scalar-captured", `function mk(n: i32): (i32) => i32 {
    let lf: (i32) => i32 = ((x: i32) => (x * 2i32));
    return ((x0: i32) => (lf(x0) + n));
}
function main(): i32 { return mk(5i32)(4i32) & 63i32; }`, 13},
	{"capturing-closure-captured", `function mk(n: i32): (i32) => i32 {
    let inner: (i32) => i32 = ((y: i32) => (y + n));
    return ((x0: i32) => (inner(x0) * 2i32));
}
function main(): i32 { return mk(3i32)(4i32) & 63i32; }`, 14},
	{"fn-capture-in-closure-array", `function main(): i32 {
    function lf(x: i32): i32 { return x + 1i32; }
    let fs: ((i32) => i32)[] = [((x0: i32) => (lf(x0) * 2i32)), ((x1: i32) => x1)];
    return fs[0i32](3i32) & 63i32;
}`, 8},
	{"passthrough-arg-captures-fn-local", `function pick[T](cond: boolean, a: T, b: T): T { return if (cond) { a } else { b }; }
function main(): i32 {
    function lf(x: i32): boolean { return false; }
    let v1: (i32) => i32 = pick(true, ((x0: i32) => (if (lf(4i32)) { x0 } else { 34i32 })), ((x1: i32) => 5i32));
    return v1(3i32) & 255i32;
}`, 34},
	{"call-only-fn-local-unchanged", `function main(): i32 {
    let f: (i32) => i32 = ((x: i32) => (x + 1i32));
    return f(3i32) & 63i32;
}`, 4},
	{"call-only-capturing-local-unchanged", `function main(): i32 {
    let n: i32 = 3i32;
    let f: (i32) => i32 = ((x: i32) => (x + n));
    return f(4i32) & 63i32;
}`, 7},
	{"iife-call-only-fn-local-unchanged", `function main(): i32 {
    function lf(p: i32): i64 { return 501i64; }
    let v1: i64[] = (if (true) { [806i64, lf(1i32), 155i64] } else { [452i64] });
    return (v1[1i32] as i32) & 63i32;
}`, 53},
	{"wide-fn-param-called-directly", `function mk(lf: (i64) => boolean): i32 {
    return (if (lf(4i64)) { 7i32 } else { 34i32 });
}
function main(): i32 { return mk(((y: i64) => (y > 3i64))) & 63i32; }`, 7},
	{"wide-fn-param-captured", `function mk(lf: (i64) => boolean): (i32) => i32 {
    return ((x0: i32) => (if (lf(4i64)) { x0 } else { 34i32 }));
}
function main(): i32 { return mk(((y: i64) => (y > 9i64)))(3i32) & 63i32; }`, 34},
	{"wide-fn-ret-captured", `function mk(lf: (i32) => i64): (i32) => i32 {
    return ((x0: i32) => ((lf(x0) + 5i64) as i32));
}
function main(): i32 { return mk(((y: i32) => ((y * 3i32) as i64)))(7i32) & 63i32; }`, 26},
	{"wide-fn-local-multi-param-captured", `function mk(): (i32) => i32 {
    let lf: (i64, i32) => i32 = ((a: i64, b: i32) => ((a as i32) + b));
    return ((x0: i32) => (lf(11i64, x0) * 2i32));
}
function main(): i32 { return mk()(4i32) & 63i32; }`, 30},
	{"f64-fn-param-captured", `function mk(lf: (f64) => i32): (i32) => i32 {
    return ((x0: i32) => (lf(2.5) + x0));
}
function main(): i32 { return mk(((y: f64) => ((y * 4.0) as i32)))(1i32) & 63i32; }`, 11},
	{"f32-fn-param-captured", `function mk(lf: (f32) => i32): (i32) => i32 {
    return ((x0: i32) => (lf(2.5f32) + x0));
}
function main(): i32 { return mk(((y: f32) => ((y * 4.0f32) as i32)))(1i32) & 63i32; }`, 11},
	{"f32-fn-param-called-directly", `function mk(lf: (f32) => i32): i32 {
    return lf(2.5f32) + 1i32;
}
function main(): i32 { return mk(((y: f32) => ((y * 4.0f32) as i32))) & 63i32; }`, 11},
	{"nested-wide-fn-local-captured", `function mk(): (i32) => i32 {
    function lf(p: i64): boolean { return p > 3i64; }
    return ((x0: i32) => (if (lf(4i64)) { x0 * 5i32 } else { 34i32 }));
}
function main(): i32 { return mk()(3i32) & 63i32; }`, 15},
}

func TestSelfHostFnValueCaptureIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range fnValueCaptureIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src))
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

func TestSelfHostFnValueCaptureWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host fn-value-capture wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "wasm_ir.fern", "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range fnValueCaptureIRCases {
		t.Run(tc.name, func(t *testing.T) {
			wat := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src))
			if len(wat) == 0 {
				t.Fatal("self-host wasm compiler emitted 0 bytes")
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			cmd := exec.Command("wasmtime", "run", watFile)
			_ = cmd.Run()
			if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got := cmd.ProcessState.ExitCode(); got != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, got, tc.exit)
			}
		})
	}
}

func TestSelfHostFnValueCaptureIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range fnValueCaptureIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
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
