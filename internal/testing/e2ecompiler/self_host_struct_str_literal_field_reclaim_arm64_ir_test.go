package e2ecompiler

import (
	"testing"
)

func TestSelfHostStructStrLiteralFieldReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	run := func(t *testing.T, prog, name string, want int) {
		t.Helper()
		asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(prog), "-target", "arm64-linux")
		if len(asm) == 0 {
			t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", name)
		}
		bin := buildBinArm64(t, arm64gcc, dir, name, string(asm))
		cmd := runArm64Bin(qemu, bin)
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s exited %d, want %d (98 = literal box leaked; 99 = over-release; 97/96 = value corrupted; 88 = escaped read freed under its holder)", name, code, want)
		}
	}

	run(t, `struct B { name: string, n: i32 }
function round(i: i32): i32 { let b: B = B { name: "abc", n: i }; return b.n; }
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { t = t + round(i); i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { t = t + round(j); j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (t <= 0) { return 97; }
    return 0;
}`, "str-literal-field-single-bind-flat-arm64", 0)

	run(t, `struct B { name: string, n: i32 }
function bump(b: B): B { return B { ...b, n: b.n + 1 }; }
function main(): i32 {
    let b: B = B { name: "abcd", n: 0 };
    let i: i32 = 0;
    while (i < 1000) { b = bump(b); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    if (b.name.len() != 4) { return 97; }
    if (b.n != 1000) { return 96; }
    return 0;
}`, "str-literal-field-carried-safe-arm64", 0)

	run(t, `struct B { name: string, n: i32 }
function mk(i: i32): B { return B { name: "leaf", n: i }; }
function main(): i32 {
    let acc: string[] = [];
    let i: i32 = 0;
    while (i < 200) { let b: B = mk(i); acc = acc.append(b.name); i = i + 1; }
    let bad: i32 = 0;
    let k: i32 = 0;
    while (k < acc.len()) { if (acc[k].len() != 4) { bad = 1; } k = k + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "str-literal-field-container-escape-safe-arm64", 0)
}
