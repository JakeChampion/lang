package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostDynPrimReclaimIRX86_64(t *testing.T) {
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

	run := func(t *testing.T, prog, name string, want int) {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", name)
		}
		bin := buildBin(t, gcc, dir, name, string(asm))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], bin)...)
		}
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s exited %d, want %d (98 = dyn cell leaked; 99 = over-release/UAF; 97 = value corrupted)", name, code, want)
		}
	}

	run(t, `trait Show { function show(self: Self): i32; }
impl Show for i32 { function show(self: Self): i32 { return self + 1; } }
function go(k: i32): i32 { let d: dyn Show = 41; return d.show() + k; }
function churn(m: i32): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < m) { acc = (acc + go(3)) % 251; i = i + 1; } return acc; }
function main(): i32 { let w: i32 = churn(3000); let b1: i32 = (__heap_bump_bytes() as i32); let x: i32 = churn(3000); let b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`,
		"dyn-prim-i32-reclaim-flat", 0)

	run(t, `trait Show { function show(self: Self): i32; }
impl Show for string { function show(self: Self): i32 { return self.len(); } }
function go(k: i32): i32 { let d: dyn Show = "hello"; return d.show() + k; }
function churn(m: i32): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < m) { acc = (acc + go(3)) % 251; i = i + 1; } return acc; }
function main(): i32 { let w: i32 = churn(3000); let b1: i32 = (__heap_bump_bytes() as i32); let x: i32 = churn(3000); let b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`,
		"dyn-prim-str-reclaim-flat", 0)

	run(t, `trait Show { function show(self: Self): i32; }
impl Show for i32 { function show(self: Self): i32 { return self + 1; } }
function mk(k: i32): dyn Show { let d: dyn Show = 41; return d; }
function go(k: i32): i32 { let e = mk(k); return e.show(); }
function churn(m: i32): i32 { let bad: i32 = 0; let i: i32 = 0; while (i < m) { if (go(3) != 42) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"dyn-prim-escaping-excluded", 0)

	run(t, `trait Show { function show(self: Self): i32; }
impl Show for i32 { function show(self: Self): i32 { return self + 1; } }
function go(k: i32): i32 { let d: dyn Show = 10; d = 41; return d.show(); }
function churn(m: i32): i32 { let bad: i32 = 0; let i: i32 = 0; while (i < m) { if (go(3) != 42) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"dyn-prim-reassigned-excluded", 0)

	run(t, `trait Show { function show(self: Self): i32; }
enum Op { Add(i32), Neg }
impl Show for Op { function show(self: Self): i32 { match (self) { Add(v) => { return v + 1; }, Neg => { return 0; } } } }
function go(k: i32): i32 { let d: dyn Show = Add(41); return d.show() + k; }
function churn(m: i32): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < m) { acc = (acc + go(3)) % 251; i = i + 1; } return acc; }
function main(): i32 { let w: i32 = churn(3000); let b1: i32 = (__heap_bump_bytes() as i32); let x: i32 = churn(3000); let b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`,
		"dyn-enum-payload-reclaim-flat", 0)

	run(t, `trait Show { function show(self: Self): i32; }
impl Show for string { function show(self: Self): i32 { return self.len(); } }
function go(k: i32): i32 { let s: string = "world"; let d: dyn Show = s; return d.show() + s.len(); }
function churn(m: i32): i32 { let bad: i32 = 0; let i: i32 = 0; while (i < m) { if (go(3) != 10) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"dyn-prim-aliased-str-excluded", 0)
}

func TestSelfHostDynPrimReclaimWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping dyn prim reclaim wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	cases := []struct {
		name     string
		src      string
		expected int
	}{
		{"dyn-prim-i32-reclaim-flat-wasm", `trait Show { function show(self: Self): i32; }
impl Show for i32 { function show(self: Self): i32 { return self + 1; } }
function go(k: i32): i32 { let d: dyn Show = 41; return d.show() + k; }
function churn(m: i32): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < m) { acc = (acc + go(3)) % 251; i = i + 1; } return acc; }
function main(): i32 { let w: i32 = churn(2000); let b1: i32 = (__heap_bump_bytes() as i32); let x: i32 = churn(2000); let b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`, 0},
		{"dyn-prim-str-reclaim-flat-wasm", `trait Show { function show(self: Self): i32; }
impl Show for string { function show(self: Self): i32 { return self.len(); } }
function go(k: i32): i32 { let d: dyn Show = "hello"; return d.show() + k; }
function churn(m: i32): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < m) { acc = (acc + go(3)) % 251; i = i + 1; } return acc; }
function main(): i32 { let w: i32 = churn(2000); let b1: i32 = (__heap_bump_bytes() as i32); let x: i32 = churn(2000); let b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`, 0},
		{"dyn-enum-payload-reclaim-flat-wasm", `trait Show { function show(self: Self): i32; }
enum Op { Add(i32), Neg }
impl Show for Op { function show(self: Self): i32 { match (self) { Add(v) => { return v + 1; }, Neg => { return 0; } } } }
function go(k: i32): i32 { let d: dyn Show = Add(41); return d.show() + k; }
function churn(m: i32): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < m) { acc = (acc + go(3)) % 251; i = i + 1; } return acc; }
function main(): i32 { let w: i32 = churn(2000); let b1: i32 = (__heap_bump_bytes() as i32); let x: i32 = churn(2000); let b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`, 0},
		{"dyn-prim-escaping-excluded-wasm", `trait Show { function show(self: Self): i32; }
impl Show for i32 { function show(self: Self): i32 { return self + 1; } }
function mk(k: i32): dyn Show { let d: dyn Show = 41; return d; }
function go(k: i32): i32 { let e = mk(k); return e.show(); }
function churn(m: i32): i32 { let bad: i32 = 0; let i: i32 = 0; while (i < m) { if (go(3) != 42) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(1000); if (__rc_underflow_count() != 0) { return 99; } return v; }`, 0},
	}
	for _, tc := range cases {
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
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.expected {
				t.Errorf("dyn prim reclaim wasm IR %q = %d, want %d (98 = leaked; 99 = over-release)", tc.name, got, tc.expected)
			}
		})
	}
}

func TestSelfHostDynPrimReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name string
		prog string
	}{
		{"dyn-prim-i32-reclaim-flat-arm64", `trait Show { function show(self: Self): i32; }
impl Show for i32 { function show(self: Self): i32 { return self + 1; } }
function go(k: i32): i32 { let d: dyn Show = 41; return d.show() + k; }
function churn(m: i32): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < m) { acc = (acc + go(3)) % 251; i = i + 1; } return acc; }
function main(): i32 { let w: i32 = churn(2000); let b1: i32 = (__heap_bump_bytes() as i32); let x: i32 = churn(2000); let b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`},
		{"dyn-prim-str-reclaim-flat-arm64", `trait Show { function show(self: Self): i32; }
impl Show for string { function show(self: Self): i32 { return self.len(); } }
function go(k: i32): i32 { let d: dyn Show = "hello"; return d.show() + k; }
function churn(m: i32): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < m) { acc = (acc + go(3)) % 251; i = i + 1; } return acc; }
function main(): i32 { let w: i32 = churn(2000); let b1: i32 = (__heap_bump_bytes() as i32); let x: i32 = churn(2000); let b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`},
	} {
		asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.prog), "-target", "arm64-linux")
		if len(asm) == 0 {
			t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
		}
		bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
		cmd := runArm64Bin(qemu, bin)
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 0 {
			t.Errorf("%s exited %d, want 0 (98 = leaked; 99 = over-release; 97 = corrupted)", tc.name, code)
		}
	}
}
