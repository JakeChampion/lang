package e2eselfhost

import (
	"testing"
)

// TestSelfHostStructStrLiteralFieldReclaimIRArm64 is the arm64 port of the #6127
// string-LITERAL struct-field reclaim (x86 sibling:
// TestSelfHostStructStrLiteralFieldReclaimIRX86_64). arm64's const_str boxes a
// literal through __fern_str_box exactly as x86-64 does, so the same fresh rc=1
// box was pinned at rc=2 by the construction retain; and its __fern_str_free
// carries the same __fern_heap_base lower-bound guard, so releasing the box
// leaves the .rodata bytes alone. Lighter churn under qemu.
//
// There is deliberately no wasm port: on wasm a literal lowers to a
// data-section pointer with no heap box at all, so the leak does not exist
// there and the change is inert — a wasm port would pass on an unfixed
// compiler and gate nothing.
func TestSelfHostStructStrLiteralFieldReclaimIRArm64(t *testing.T) {
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
			t.Errorf("%s exited %d, want %d (98 = literal box leaked; 99 = over-release; 97/96 = value corrupted; 88 = escaped read freed under its holder)", name, code, want)
		}
	}

	// SINGLE BIND, no rebind — the shape the leak was measured on.
	run(t, `struct B { name: string, n: i32 }
function round(i: i32): i32 { var b: B = B { name: "abc", n: i }; return b.n; }
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { t = t + round(i); i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) { t = t + round(j); j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (t <= 0) { return 97; }
    return 0;
}`, "str-literal-field-single-bind-flat-arm64", 0)

	// Carried field (functional update) stays readable, no double-free.
	run(t, `struct B { name: string, n: i32 }
function bump(b: B): B { return B { ...b, n: b.n + 1 }; }
function main(): i32 {
    var b: B = B { name: "abcd", n: 0 };
    var i: i32 = 0;
    while (i < 1000) { b = bump(b); i = i + 1; }
    if (__rc_underflow() != 0) { return 99; }
    if (b.name.len() != 4) { return 97; }
    if (b.n != 1000) { return 96; }
    return 0;
}`, "str-literal-field-carried-safe-arm64", 0)

	// ESCAPE — container store. The whole-program scan must exclude B, leaving
	// every stored string readable (the hazard class that made the sibling
	// nested-struct release a use-after-free).
	run(t, `struct B { name: string, n: i32 }
function mk(i: i32): B { return B { name: "leaf", n: i }; }
function main(): i32 {
    var acc: string[] = [];
    var i: i32 = 0;
    while (i < 200) { var b: B = mk(i); acc = acc.append(b.name); i = i + 1; }
    var bad: i32 = 0;
    var k: i32 = 0;
    while (k < acc.len()) { if (acc[k].len() != 4) { bad = 1; } k = k + 1; }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "str-literal-field-container-escape-safe-arm64", 0)
}
