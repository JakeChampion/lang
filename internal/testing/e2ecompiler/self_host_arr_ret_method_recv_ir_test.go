package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var arrRetMethodRecvCases = []struct {
	name string
	src  string
	exit int
}{
	{"struct-arr-method-len", structArrPrelude + `function main(): i32 { let h: Holder = mkh(); return h.get().len(); }`, 2},
	{"struct-arr-method-index-field", structArrPrelude + `function main(): i32 { let h: Holder = mkh(); return h.get()[0].k; }`, 3},
	{"struct-arr-method-index-nested-len", structArrPrelude + `function main(): i32 { let h: Holder = mkh(); return h.get()[1].ys.len(); }`, 1},
	{"struct-arr-method-foreach", structArrPrelude + `function main(): i32 { let h: Holder = mkh(); let t: i32 = 0; for x in h.get() { t = t + x.k; } return t; }`, 7},
	{"struct-arr-method-bound-first", structArrPrelude + `function main(): i32 { let h: Holder = mkh(); let tmp: Inner[] = h.get(); return tmp.len(); }`, 2},

	{"enum-arr-method-len", `enum Col { R, G } struct EH { es: Col[] } function (h: EH) get(): Col[] { return h.es; } function main(): i32 { let h: EH = EH { es: [Col.R, Col.G] }; return h.get().len(); }`, 2},
	{"strarr-method-len", strArrPrelude + `function main(): i32 { let h: SH = SH { ss: ["a", "b", "c"] }; return h.get().len(); }`, 3},
	{"strarr-method-elem-len", strArrPrelude + `function main(): i32 { let h: SH = SH { ss: ["abcd", "b"] }; return h.get()[0].len(); }`, 4},
	{"i32arr-method-len", i32ArrPrelude + `function main(): i32 { let h: IH = IH { xs: [7, 8] }; return h.get().len(); }`, 2},
	{"i32arr-method-index", i32ArrPrelude + `function main(): i32 { let h: IH = IH { xs: [7, 8] }; return h.get()[1]; }`, 8},

	{"free-fn-arr-ret-len", freeFnArrPrelude + `function main(): i32 { return mk().len(); }`, 2},
	{"free-fn-arr-ret-index-field", freeFnArrPrelude + `function main(): i32 { return mk()[1].k; }`, 4},

	{"struct-arr-loop-no-underflow", structArrPrelude + `function main(): i32 { let i: i32 = 0; while (i < 30) { let h: Holder = mkh(); if (h.get().len() != 2) { return 90; } i = i + 1; } return __rc_underflow_count(); }`, 0},
	{"struct-arr-index-loop-no-underflow", structArrPrelude + `function main(): i32 { let i: i32 = 0; while (i < 30) { let h: Holder = mkh(); if (h.get()[1].k != 4) { return 90; } i = i + 1; } return __rc_underflow_count(); }`, 0},
	{"struct-arr-foreach-loop-no-underflow", structArrPrelude + `function main(): i32 { let i: i32 = 0; while (i < 30) { let h: Holder = mkh(); let t: i32 = 0; for x in h.get() { t = t + x.k; } if (t != 7) { return 90; } i = i + 1; } return __rc_underflow_count(); }`, 0},
	{"strarr-loop-no-underflow", strArrPrelude + `function main(): i32 { let i: i32 = 0; while (i < 30) { let h: SH = SH { ss: ["a", "b"] }; if (h.get().len() != 2) { return 90; } i = i + 1; } return __rc_underflow_count(); }`, 0},
}

const (
	structArrPrelude = `struct Inner { k: i32, ys: i32[] } struct Holder { xs: Inner[] } ` +
		`function (h: Holder) get(): Inner[] { return h.xs; } ` +
		`function mkh(): Holder { return Holder { xs: [Inner { k: 3, ys: [1, 2] }, Inner { k: 4, ys: [5] }] }; } `
	strArrPrelude    = `struct SH { ss: string[] } function (h: SH) get(): string[] { return h.ss; } `
	i32ArrPrelude    = `struct IH { xs: i32[] } function (h: IH) get(): i32[] { return h.xs; } `
	freeFnArrPrelude = `struct Inner2 { k: i32 } function mk(): Inner2[] { return [Inner2 { k: 3 }, Inner2 { k: 4 }]; } `
)

func TestSelfHostArrRetMethodRecvIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range arrRetMethodRecvCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src))
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

func TestSelfHostArrRetMethodRecvIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range arrRetMethodRecvCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

func TestSelfHostArrRetMethodRecvIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host array-returning-method receiver wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range arrRetMethodRecvCases {
		t.Run(tc.name, func(t *testing.T) {
			wat := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src))
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s", tc.name)
			}
			if code := run.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
