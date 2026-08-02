package e2eselfhost

import (
	"testing"
)

// TestSelfHostRcEnumCallInitIRArm64 is the arm64 port of
// TestSelfHostRcEnumCallInitIRX86_64 (#4355 slice 5): the RCENUM call-init
// admission plus the arm64-specific __struct_drop_<T> return fix — the arm64
// body restored its return from x10, which __fern_str_free and a nested
// __struct_drop_* bl clobber (the slice-2 field-reclaim lesson), so the
// enum-payload deep-drop consumed a stale pointer. The body now reloads the
// box from the stack arg ([sp, #16] under the stp frame). Lighter churn under
// qemu.
func TestSelfHostRcEnumCallInitIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	run := func(t *testing.T, prog, name string, want int) {
		t.Helper()
		asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(prog), "-target", "arm64")
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
    var base: string = "a";
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var e: E = mk(base, i);
        match (e) { A(s, k) => { acc = acc + k; }, B(x, y) => { acc = acc + x + y; } }
        i = i + 1;
    }
    var b1: i32 = __heap_bump_bytes();
    var j: i32 = 0;
    while (j < 1000) {
        var e2: E = mk(base, j);
        match (e2) { A(s, k) => { acc = acc + k; }, B(x, y) => { acc = acc + x + y; } }
        j = j + 1;
    }
    var b2: i32 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, "rcenum-call-init-str-flat-arm64", 0)

	// PARAM-EMBED exclusion pin.
	run(t, `struct S { name: string, n: i32 }
enum E { A(S, i32), B(i32, i32) }
function mk(nm: string, n: i32): E { return A(S { name: nm, n: n }, n); }
function main(): i32 {
    var keep: string = "aa" + "bb";
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
        var e: E = mk(keep, i);
        match (e) { A(s, k) => { acc = acc + k; }, B(x, y) => { acc = acc + x + y; } }
        i = i + 1;
    }
    if (keep.len() != 4) { return 88; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, "rcenum-call-init-param-embed-safe-arm64", 0)

	// RETURN-CLOBBER regression pin (direct-init struct payload, detector zero).
	run(t, `struct Inner { items: i32[] }
enum Box { Full(Inner), Empty }
function readit(): i32 {
    var b: Box = Full(Inner { items: [1,2,3,4] });
    var r: i32 = 0;
    match (b) { Full(inner) => { r = inner.items[0]; }, Empty => { r = 0; } }
    return r;
}
function main(): i32 {
    var s: i32 = 0;
    var f: i32 = 0;
    while (f < 1000) { s = s + readit(); f = f + 1; }
    if (s != 1000) { return 97; }
    return __rc_underflow();
}`, "rcenum-struct-payload-detector-zero-arm64", 0)
}
