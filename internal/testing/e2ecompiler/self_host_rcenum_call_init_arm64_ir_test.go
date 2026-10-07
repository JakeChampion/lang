package e2ecompiler

import (
	"testing"
)

// TestSelfHostRcEnumCallInitIRArm64 is the arm64 port of
// TestSelfHostRcEnumCallInitIRX86_64 (#4355): an rc-payload enum local
// initialised from a call is released at scope exit, payload chain included.
// The payload's drop calls further helpers, so a box pointer held in a
// clobbered register across them would release a stale pointer; the cases pin
// flat churn, no underflow, and live values left intact. Lighter churn under
// qemu.
func TestSelfHostRcEnumCallInitIRArm64(t *testing.T) {
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
			t.Errorf("%s exited %d, want %d (98 = chain leaked; 99 = rc underflow / over-release; 88 = live value freed; 97 = value corrupted)", name, code, want)
		}
	}

	// CALL-INIT, STRING-field payload struct — churn flat at detector zero
	// (exercises the str_free x10-clobber reload).
	run(t, `struct S { name: string, n: i32 }
enum E { A(S, i32), B(i32, i32) }
function mk(nm: string, n: i32): E { return A(S { name: nm + "x", n: n }, n); }
function main(): i32 {
    let base: string = "a";
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let e: E = mk(base, i);
        match (e) { A(s, k) => { acc = acc + k; }, B(x, y) => { acc = acc + x + y; } }
        i = i + 1;
    }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 1000) {
        let e2: E = mk(base, j);
        match (e2) { A(s, k) => { acc = acc + k; }, B(x, y) => { acc = acc + x + y; } }
        j = j + 1;
    }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, "rcenum-call-init-str-flat-arm64", 0)

	// PARAM-EMBED exclusion pin.
	run(t, `struct S { name: string, n: i32 }
enum E { A(S, i32), B(i32, i32) }
function mk(nm: string, n: i32): E { return A(S { name: nm, n: n }, n); }
function main(): i32 {
    let keep: string = "aa" + "bb";
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 500) {
        let e: E = mk(keep, i);
        match (e) { A(s, k) => { acc = acc + k; }, B(x, y) => { acc = acc + x + y; } }
        i = i + 1;
    }
    if (keep.len() != 4) { return 88; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, "rcenum-call-init-param-embed-safe-arm64", 0)

	// RETURN-CLOBBER regression pin (direct-init struct payload, detector zero).
	run(t, `struct Inner { items: i32[] }
enum Box { Full(Inner), Empty }
function readit(): i32 {
    let b: Box = Full(Inner { items: [1,2,3,4] });
    let r: i32 = 0;
    match (b) { Full(inner) => { r = inner.items[0]; }, Empty => { r = 0; } }
    return r;
}
function main(): i32 {
    let s: i32 = 0;
    let f: i32 = 0;
    while (f < 1000) { s = s + readit(); f = f + 1; }
    if (s != 1000) { return 97; }
    return __rc_underflow_count();
}`, "rcenum-struct-payload-detector-zero-arm64", 0)
}
