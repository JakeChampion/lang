package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var dynCallReclaimCases = []struct {
	name string
	src  string
	exit int
}{
	{"call-result-scalar-concrete", `trait Shape { function area(self: Self): i32; }
struct Square { side: i32 }
impl Shape for Square { function area(self: Self): i32 { return self.side * self.side; } }
function mk(k: i32): Square { return Square { side: k }; }
function go(k: i32): i32 { let d: dyn Shape = mk(k); return d.area(); }
function churn(m: i32): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < m) { acc = (acc + go(3)) % 251; i = i + 1; } return acc; }
function main(): i32 { let w: i32 = churn(2000); let b1: i64 = __heap_bump_bytes(); let x: i32 = churn(2000); let b2: i64 = __heap_bump_bytes(); if (__rc_underflow_count() != 0) { return 99; } if (w != x) { return 97; } let per: i64 = (b2 - b1) / 2000; if (per > 95) { per = 95; } return (per as i32); }`, 0},

	{"call-result-rc-field-concrete", `trait Shape { function area(self: Self): i32; }
struct Circle { r: i32, tags: i32[] }
impl Shape for Circle { function area(self: Self): i32 { return self.r * self.r + self.tags[0]; } }
function mk(k: i32): Circle { return Circle { r: k, tags: [7, 8] }; }
function go(k: i32): i32 { let d: dyn Shape = mk(k); return d.area(); }
function churn(m: i32): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < m) { acc = (acc + go(3)) % 251; i = i + 1; } return acc; }
function main(): i32 { let w: i32 = churn(2000); let b1: i64 = __heap_bump_bytes(); let x: i32 = churn(2000); let b2: i64 = __heap_bump_bytes(); if (__rc_underflow_count() != 0) { return 99; } if (w != x) { return 97; } let per: i64 = (b2 - b1) / 2000; if (per > 95) { per = 95; } return (per as i32); }`, 0},

	{"method-call-result", `trait Shape { function area(self: Self): i32; }
struct Square { side: i32 }
impl Shape for Square { function area(self: Self): i32 { return self.side * self.side; } }
struct Factory { base: i32 }
function (self: Factory) make(k: i32): Square { return Square { side: k + self.base }; }
function go(k: i32): i32 { let f: Factory = Factory { base: 1 }; let d: dyn Shape = f.make(k); return d.area(); }
function churn(m: i32): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < m) { acc = (acc + go(3)) % 251; i = i + 1; } return acc; }
function main(): i32 { let w: i32 = churn(2000); let b1: i64 = __heap_bump_bytes(); let x: i32 = churn(2000); let b2: i64 = __heap_bump_bytes(); if (__rc_underflow_count() != 0) { return 99; } if (w != x) { return 97; } let per: i64 = (b2 - b1) / 2000; if (per > 95) { per = 95; } return (per as i32); }`, 0},

	{"alias-init-excluded", `trait Shape { function area(self: Self): i32; }
struct Square { side: i32 }
impl Shape for Square { function area(self: Self): i32 { return self.side * self.side; } }
function mk(k: i32): Square { return Square { side: k }; }
function go(k: i32): i32 { let s: Square = mk(k); let d: dyn Shape = s; let a: i32 = d.area(); return a + s.side; }
function churn(m: i32): i32 { let bad: i32 = 0; let i: i32 = 0; while (i < m) { if (go(3) != 12) { bad = 96; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } return v; }`, 0},

	{"identity-callee-excluded", `trait Shape { function area(self: Self): i32; }
struct Square { side: i32 }
impl Shape for Square { function area(self: Self): i32 { return self.side * self.side; } }
@noinline function ident(s: Square): Square { return s; }
function go(k: i32): i32 { let live: Square = Square { side: k }; let d: dyn Shape = ident(live); let a: i32 = d.area(); return a + live.side; }
function churn(m: i32): i32 { let bad: i32 = 0; let i: i32 = 0; while (i < m) { if (go(3) != 12) { bad = 96; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } return v; }`, 0},

	{"param-receiver-method-excluded", `trait Shape { function area(self: Self): i32; }
struct Square { side: i32 }
impl Shape for Square { function area(self: Self): i32 { return self.side * self.side; } }
struct Factory { base: i32 }
function (self: Factory) make(k: i32): Square { return Square { side: k + self.base }; }
function go(f: Factory, k: i32): i32 { let d: dyn Shape = f.make(k); let a: i32 = d.area(); return a + f.base; }
function churn(m: i32): i32 { let bad: i32 = 0; let i: i32 = 0; while (i < m) { let f: Factory = Factory { base: 1 }; if (go(f, 3) != 17) { bad = 96; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } return v; }`, 0},

	{"computed-prim-payload", `trait Show { function show(self: Self): i32; }
impl Show for i32 { function show(self: Self): i32 { return self + 1; } }
function go(k: i32): i32 { let d: dyn Show = k * 2; return d.show(); }
function churn(m: i32): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < m) { acc = (acc + go(3)) % 251; i = i + 1; } return acc; }
function main(): i32 { let w: i32 = churn(2000); let b1: i64 = __heap_bump_bytes(); let x: i32 = churn(2000); let b2: i64 = __heap_bump_bytes(); if (__rc_underflow_count() != 0) { return 99; } if (w != x) { return 97; } let per: i64 = (b2 - b1) / 2000; if (per > 95) { per = 95; } return (per as i32); }`, 0},

	{"prim-payload-from-local", `trait Show { function show(self: Self): i32; }
impl Show for i32 { function show(self: Self): i32 { return self + 1; } }
function go(k: i32): i32 { let n: i32 = k * 3; let d: dyn Show = n; let a: i32 = d.show(); return a + n; }
function churn(m: i32): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < m) { if (go(3) != 19) { return 96; } acc = (acc + go(3)) % 251; i = i + 1; } return acc; }
function main(): i32 { let w: i32 = churn(2000); if (w == 96) { return 96; } let b1: i64 = __heap_bump_bytes(); let x: i32 = churn(2000); let b2: i64 = __heap_bump_bytes(); if (__rc_underflow_count() != 0) { return 99; } if (w != x) { return 97; } let per: i64 = (b2 - b1) / 2000; if (per > 95) { per = 95; } return (per as i32); }`, 0},

	{"block-scoped-struct-payload", `trait Shape { function area(self: Self): i32; }
struct Square { side: i32 }
impl Shape for Square { function area(self: Self): i32 { return self.side * self.side; } }
function go(k: i32): i32 { let t: i32 = 0; if (k > 0) { let d: dyn Shape = Square { side: k }; t = d.area(); } else { t = 5; } return t; }
function churn(m: i32): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < m) { acc = (acc + go(3)) % 251; i = i + 1; } return acc; }
function main(): i32 { let w: i32 = churn(2000); let b1: i64 = __heap_bump_bytes(); let x: i32 = churn(2000); let b2: i64 = __heap_bump_bytes(); if (__rc_underflow_count() != 0) { return 99; } if (w != x) { return 97; } let per: i64 = (b2 - b1) / 2000; if (per > 95) { per = 95; } return (per as i32); }`, 0},

	{"block-scoped-untaken-branch", `trait Shape { function area(self: Self): i32; }
struct Square { side: i32 }
impl Shape for Square { function area(self: Self): i32 { return self.side * self.side; } }
function go(k: i32): i32 { let t: i32 = 0; if (k > 100) { let d: dyn Shape = Square { side: k }; t = d.area(); } else { t = 5; } return t; }
function churn(m: i32): i32 { let bad: i32 = 0; let i: i32 = 0; while (i < m) { if (go(3) != 5) { bad = 96; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } return v; }`, 0},

	{"string-payload-alias-excluded", `trait Show { function show(self: Self): i32; }
impl Show for string { function show(self: Self): i32 { return self.len(); } }
function go(k: i32): i32 { let s: string = "abcd"; let d: dyn Show = s; let a: i32 = d.show(); return a + s.len() + k - k; }
function churn(m: i32): i32 { let bad: i32 = 0; let i: i32 = 0; while (i < m) { if (go(3) != 8) { bad = 96; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } return v; }`, 0},

	{"escaping-dyn-excluded", `trait Shape { function area(self: Self): i32; }
struct Square { side: i32 }
impl Shape for Square { function area(self: Self): i32 { return self.side * self.side; } }
function mk(k: i32): Square { return Square { side: k }; }
function mkd(k: i32): dyn Shape { let d: dyn Shape = mk(k); return d; }
function go(k: i32): i32 { let e = mkd(k); return e.area(); }
function churn(m: i32): i32 { let bad: i32 = 0; let i: i32 = 0; while (i < m) { if (go(3) != 9) { bad = 96; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } return v; }`, 0},

	{"escaping-and-reassigned-prim-excluded", `trait Show { function show(self: Self): i32; }
impl Show for i32 { function show(self: Self): i32 { return self + 1; } }
function mkd(k: i32): dyn Show { let d: dyn Show = k * 2; return d; }
function reasg(k: i32): i32 { let d: dyn Show = k * 2; d = k * 3; return d.show(); }
function go(k: i32): i32 { return mkd(k).show() + reasg(k); }
function churn(m: i32): i32 { let bad: i32 = 0; let i: i32 = 0; while (i < m) { if (go(3) != 17) { bad = 96; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } return v; }`, 0},
}

func TestSelfHostDynCallReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range dynCallReclaimCases {
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
				t.Errorf("%s exited %d, want %d (a small non-zero code is the leaked bytes per round; 99 = over-release, 97 = churns disagreed, 96 = wrong answer)", tc.name, code, tc.exit)
			}
		})
	}
}

func TestSelfHostDynCallReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range dynCallReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			progBin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d (a small non-zero code is the leaked bytes per round; 99 = over-release, 97 = churns disagreed, 96 = wrong answer)", tc.name, code, tc.exit)
			}
		})
	}
}

func TestSelfHostDynCallReclaimIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host dyn call-result reclaim wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range dynCallReclaimCases {
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
				t.Errorf("%s exited %d, want %d (a small non-zero code is the leaked bytes per round; 99 = over-release, 97 = churns disagreed, 96 = wrong answer)", tc.name, code, tc.exit)
			}
		})
	}
}
