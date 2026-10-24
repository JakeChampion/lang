package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var dynEnumIRCases = []struct {
	name     string
	src      string
	expected int
}{
	{"enum-local-coerce",
		`trait Show { function show(self: Self): i32; } enum Op { Add(i32), Neg } impl Show for Op { function show(self: Self): i32 { match (self) { Add(v) => { return v + 1; }, Neg => { return 0; } } } } function go(k: i32): i32 { let e: Op = Add(k); let d: dyn Show = e; return d.show(); } function main(): i32 { return go(3); }`, 4},
	{"enum-direct-init",
		`trait Show { function show(self: Self): i32; } enum Op { Add(i32), Neg } impl Show for Op { function show(self: Self): i32 { match (self) { Add(v) => { return v + 1; }, Neg => { return 0; } } } } function main(): i32 { let d: dyn Show = Add(41); return d.show(); }`, 42},
	{"enum-local-to-param",
		`trait Show { function show(self: Self): i32; } enum Op { Add(i32), Neg } impl Show for Op { function show(self: Self): i32 { match (self) { Add(v) => { return v + 1; }, Neg => { return 0; } } } } function run(s: dyn Show): i32 { return s.show(); } function main(): i32 { let e: Op = Add(9); return run(e); }`, 10},
	{"enum-unit-variant",
		`trait Show { function show(self: Self): i32; } enum Op { Add(i32), Neg } impl Show for Op { function show(self: Self): i32 { match (self) { Add(v) => { return v + 1; }, Neg => { return 7; } } } } function go(): i32 { let e: Op = Neg; let d: dyn Show = e; return d.show(); } function main(): i32 { return go(); }`, 7},
	{"enum-method-arg",
		`trait Sc { function sc(self: Self, k: i32): i32; } enum Op { Add(i32), Neg } impl Sc for Op { function sc(self: Self, k: i32): i32 { match (self) { Add(v) => { return v * k; }, Neg => { return 0 - k; } } } } function f(s: dyn Sc): i32 { return s.sc(3); } function main(): i32 { let e: Op = Add(5); return f(e); }`, 15},
	{"enum-two-enums",
		`trait Show { function show(self: Self): i32; } enum Op { Add(i32), Neg } enum Col { Red, Blue } impl Show for Op { function show(self: Self): i32 { match (self) { Add(v) => { return v + 1; }, Neg => { return 0; } } } } impl Show for Col { function show(self: Self): i32 { match (self) { Red => { return 10; }, Blue => { return 20; } } } } function run(s: dyn Show): i32 { return s.show(); } function main(): i32 { let a: Op = Add(3); let b: Col = Blue; return run(a) + run(b); }`, 24},
	{"enum-struct-mixed-array",
		`trait Show { function show(self: Self): i32; } enum Op { Add(i32), Neg } struct Circle { r: i32 } impl Show for Op { function show(self: Self): i32 { match (self) { Add(v) => { return v + 1; }, Neg => { return 0; } } } } impl Show for Circle { function show(self: Self): i32 { return self.r * self.r; } } function sum(xs: dyn Show[]): i32 { let t: i32 = 0; for x in xs { t = t + x.show(); } return t; } function main(): i32 { let e: Op = Add(4); let xs: dyn Show[] = [Circle { r: 3 }, e]; return sum(xs); }`, 14},
}

func TestSelfHostDynEnumIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_run.fern", "drivers/asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range dynEnumIRCases {
		t.Run(tc.name, func(t *testing.T) {
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, []byte(tc.src))))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
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
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

func TestSelfHostDynEnumIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host dyn-enum wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range dynEnumIRCases {
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
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			watFile := filepath.Join(dir, "dynenum_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("dyn-enum wasm IR %q = %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

func TestSelfHostDynEnumIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range dynEnumIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, "dynenum-"+tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("dyn-enum arm64 IR %q = %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}
