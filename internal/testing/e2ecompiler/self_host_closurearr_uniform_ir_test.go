package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var closureArrUniformCases = []struct {
	name string
	src  string
	exit int
}{
	{"cross-return-plain-and-passthrough", `function id[T](x: T): T { return x; } function gen(c: boolean, p1: i32): ((i32) => i32)[] { return if (c) { [((x: i32) => x)] } else { [id(((y: i32) => (y + p1)))] }; } function main(): i32 { let fs: ((i32) => i32)[] = gen(false, 5i32); return fs[0i32](1i32) & 63i32; }`, 6},
	{"arm-array-passthrough-element", `function pick[T](cond: boolean, a: T, b: T): T { return if (cond) { a } else { b }; } function main(): i32 { let p: i32 = 4i32; let fs: ((i32) => i32)[] = (if (true) { [pick(true, ((x: i32) => (x + p)), ((y: i32) => y))] } else { [((z: i32) => z)] }); return fs[0i32](1i32) & 63i32; }`, 5},
	{"all-plain-returns", `function gen(c: boolean): ((i32) => i32)[] { return if (c) { [((x: i32) => x)] } else { [((y: i32) => (y + 1i32))] }; } function main(): i32 { let fs: ((i32) => i32)[] = gen(false); return fs[0i32](1i32) & 63i32; }`, 2},

	{"with-lambda-into-plain-fn-array", `function main(): i32 { let s: ((i32) => i32)[] = [((a: i32) => a), ((c: i32) => (c + 2i32))]; let w: ((i32) => i32)[] = s.with(0i32, ((b: i32) => (b + 1i32))); return ((w[0i32](5i32) + w[1i32](5i32) + s[0i32](5i32)) & 63i32); }`, 18},
	{"with-capturing-lambda-into-plain-fn-array", `function main(): i32 { let n: i32 = 3i32; let s: ((i32) => i32)[] = [((a: i32) => a), ((c: i32) => (c + 2i32))]; let w: ((i32) => i32)[] = s.with(0i32, ((b: i32) => (b + n))); return ((w[0i32](5i32) + w[1i32](5i32)) & 63i32); }`, 15},
	{"with-boxed-local-into-plain-fn-array", `function main(): i32 { let s: ((i32) => i32)[] = [((a: i32) => a), ((c: i32) => (c + 2i32))]; let f = ((b: i32) => (b + 1i32)); let w: ((i32) => i32)[] = s.with(0i32, f); return ((w[0i32](5i32) + f(1i32)) & 63i32); }`, 8},
	{"with-fn-name-into-closure-array", `function bump(x: i32): i32 { return (x + 1i32); } function main(): i32 { let n: i32 = 2i32; let s: ((i32) => i32)[] = [((a: i32) => a), ((c: i32) => (c + n))]; let w: ((i32) => i32)[] = s.with(0i32, bump); return ((w[0i32](5i32) + w[1i32](5i32)) & 63i32); }`, 13},
	{"with-element-of-closure-array", `function main(): i32 { let n: i32 = 2i32; let s: ((i32) => i32)[] = [((a: i32) => a), ((c: i32) => (c + n))]; let w: ((i32) => i32)[] = s.with(0i32, s[1i32]); return ((w[0i32](5i32) + w[1i32](5i32)) & 63i32); }`, 14},
	{"append-capturing-lambda-to-plain-fn-array", `function main(): i32 { let n: i32 = 4i32; let s: ((i32) => i32)[] = [((a: i32) => a)]; let w: ((i32) => i32)[] = s.append(((b: i32) => (b + n))); return ((w[0i32](1i32) + w[1i32](5i32)) & 63i32); }`, 10},
	{"non-fn-with-beside-fn-array", `function main(): i32 { let xs: i32[] = [1i32, 2i32]; let ys: i32[] = xs.with(0i32, 7i32); let s: ((i32) => i32)[] = [((a: i32) => a), ((c: i32) => (c + 2i32))]; return ((s[0i32](ys[0i32]) + s[1i32](ys[1i32])) & 63i32); }`, 11},
}

func TestSelfHostClosureArrUniformIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range closureArrUniformCases {
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
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

func TestSelfHostClosureArrUniformIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range closureArrUniformCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

func TestSelfHostClosureArrUniformWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host closure-array uniformity wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range closureArrUniformCases {
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
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := run.ProcessState.ExitCode(); got != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, got, tc.exit)
			}
		})
	}
}
