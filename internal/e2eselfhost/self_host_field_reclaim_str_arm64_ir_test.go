package e2eselfhost

import (
	"testing"
)

// TestSelfHostFieldReclaimStrIRArm64 is the arm64 port of the #4355
// replaced-STRING-field reclaim (x86 sibling:
// TestSelfHostFieldReclaimStrIRX86_64): the widened
// emit_arm64_field_reclaim_one body frees a replaced string field via the
// rc-aware __fern_str_free under the same cow + snap guards. Lighter churn
// under qemu.
func TestSelfHostFieldReclaimStrIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

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
			t.Errorf("%s exited %d, want %d (98 = string field leaked; 99 = over-release; 97/96 = value corrupted; 88 = aliased read freed)", name, code, want)
		}
	}

	// Consume-rebind churn — the replaced string field recycles, flat.
	run(t, `struct S { xs: i32[], name: string, n: i32 }
function step(s: S): S { return S { xs: [s.n, s.n + 1], name: s.name + "x", n: s.n + 1 }; }
function main(): i32 {
    var s: S = S { xs: [1, 2], name: "a" + "b", n: 0 };
    var i: i32 = 0;
    while (i < 200) { s = step(s); i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 1000) { s = S { xs: [1, 2], name: "a" + "b", n: 0 }; var k: i32 = 0; while (k < 3) { s = step(s); k = k + 1; } j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (s.n != 3) { return 97; }
    return 0;
}`, "field-reclaim-str-flat-arm64", 0)

	// Carried field (functional update) stays readable, no double-free.
	run(t, `struct S { xs: i32[], name: string, n: i32 }
function bump(s: S): S { return S { ...s, n: s.n + 1 }; }
function main(): i32 {
    var s: S = S { xs: [1, 2], name: "ab" + "cd", n: 0 };
    var i: i32 = 0;
    while (i < 1000) { s = bump(s); i = i + 1; }
    if (__rc_underflow() != 0) { return 99; }
    if (s.name.len() != 4) { return 97; }
    if (s.n != 1000) { return 96; }
    return 0;
}`, "field-reclaim-str-carried-safe-arm64", 0)

	// STRING-ONLY struct (#4355 slice 3) — routed via STRFLDOK, churn flat.
	run(t, `struct B { name: string, n: i32 }
function step(b: B): B { return B { name: b.name + "x", n: b.n + 1 }; }
function main(): i32 {
    var b: B = B { name: "a" + "b", n: 0 };
    var i: i32 = 0;
    while (i < 200) { b = step(b); i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 1000) { b = B { name: "a" + "b", n: 0 }; var k: i32 = 0; while (k < 3) { b = step(b); k = k + 1; } j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (b.n != 3) { return 97; }
    return 0;
}`, "field-reclaim-str-only-flat-arm64", 0)

	// Aliased read (`var t = s.name`) survives the rebind's field free.
	run(t, `struct S { xs: i32[], name: string, n: i32 }
function step(s: S): S { return S { xs: [s.n], name: s.name + "x", n: s.n + 1 }; }
function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
        var s: S = S { xs: [1], name: "a" + "b", n: 0 };
        var t: string = s.name;
        s = step(s);
        s = step(s);
        if (t.len() != 2) { bad = 1; }
        if (s.name.len() != 4) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "field-reclaim-str-aliased-read-safe-arm64", 0)
}
