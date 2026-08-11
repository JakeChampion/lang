package e2eselfhost

import (
	"os/exec"
	"testing"
)

// f64ToStringProgram formats a spread of f64 values one per line through
// std/float's `.to_string()`. It imports the module explicitly: the register
// f64 formatting is std/float's shortest-round-trip formatter on every
// backend (there is no builtin `.to_string()` — #5826 removed the hand-asm
// k=15 fixed-precision one), the same one
// the Go backend compiles and the interpreter runs, so an import-free
// `.to_string()` on an f64 is an unresolved method (native reports E043) —
// exactly as it already was on wasm, which never had the builtin.
//
// The expected output is therefore SHORTEST round-trip, not k=15. The
// difference is visible and is the point of the change: k=15 rendered
// 123456.789 as "123456.789000000004307" and 1/3 as "0.333333333333333"
// (15 digits), where shortest gives the fewest digits that still parse back
// to the same f64. The last line pins the #5363 default that a bare
// unsuffixed literal is f64 — 1.0/3.0 renders at f64 precision, not f32's.
//
// The `var fx: float` line this program used to carry is GONE, not moved:
// with std/float actually imported, a `float`-declared receiver dispatches
// to that module's f32 `.to_string()` and renders "0.33333334" (#5882).
// That is a pre-existing self-host dispatch bug — a pre-deletion driver
// emits the same `__fn_f32__to_string` call — which was invisible here only
// because this test ran import-free, where no f32 method existed and the
// now-deleted builtin caught f32 and f64 alike. Restore the line as part of
// fixing #5882 rather than pinning it to the wrong value here.
const f64ToStringProgram = `import "./float";
function main(): i32 {
    print((3.5 as f64).to_string());
    print((0.0 as f64 - 2.25).to_string());
    print((0.0 as f64).to_string());
    print((1.0 as f64).to_string());
    print((100.0 as f64).to_string());
    print((123456.789 as f64).to_string());
    print((0.1 as f64).to_string());
    print((0.5 as f64).to_string());
    print((9999999.99 as f64).to_string());
    print((0.0 as f64 - 0.000125).to_string());
    print((1.0 / 3.0).to_string());
    return 0;
}`

// Byte-for-byte the native interpreter's output for the same program —
// std/float is the single formatter now, so self-host and native agree by
// construction rather than by a hand-maintained transcription contract.
const f64ToStringWant = "3.5\n" +
	"-2.25\n" +
	"0\n" +
	"1\n" +
	"100\n" +
	"123456.789\n" +
	"0.1\n" +
	"0.5\n" +
	"9999999.99\n" +
	"-0.000125\n" +
	"0.3333333333333333\n"

// f64ToStringMods vendors std/float alone. Its own `import "std/i32"` /
// `"std/i64"` are skipped as unresolved by the loader (the same set the old
// marker bundle hand-picked), which is correct here: the only integer method
// float's body calls is an i32 `.to_string()` (__float_sig_core renders its
// decimal exponent with one), and that is still a register-backend builtin.
// Vendoring i32/i64 instead FAILS — they import core/int, which this helper
// does not carry, leaving `int__*` calls undefined.
var f64ToStringMods = []string{"float"}

// TestSelfHostF64ToStringX86_64 compiles the float-formatting program with
// the self-hosted x86-64 emitter and checks its stdout against the native
// interpreter's shortest-round-trip output.
func TestSelfHostF64ToStringX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)

	asm, progDir := compileStdProgModload(t, runner, driverBin, f64ToStringMods, f64ToStringProgram)
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes")
	}
	progBin := buildBin(t, gcc, progDir, "f64prog", asm)

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), progBin)...)
	}
	out, _ := cmd.Output()
	if string(out) != f64ToStringWant {
		t.Errorf("f64.to_string output mismatch:\n got: %q\nwant: %q", string(out), f64ToStringWant)
	}
}

// TestSelfHostF64ToStringArm64 is the ARM64 counterpart: same program
// through the self-hosted ARM64 emitter, run under qemu-aarch64.
// CI-gated; skips cleanly without the cross toolchain.
func TestSelfHostF64ToStringArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	_, x86runner, driverBin := buildModloadArm64DriverX86(t)

	asm, progDir := compileStdProgModload(t, x86runner, driverBin, f64ToStringMods, f64ToStringProgram, "-target", "arm64-linux")
	if len(asm) == 0 {
		t.Fatal("self-host arm64 compiler emitted 0 bytes for the f64 program")
	}
	f64Bin := buildBin(t, arm64gcc, progDir, "f64prog", asm)

	cmd := runArm64Bin(qemu, f64Bin)
	out, _ := cmd.Output()
	if string(out) != f64ToStringWant {
		t.Errorf("f64.to_string (arm64) output mismatch:\n got: %q\nwant: %q", string(out), f64ToStringWant)
	}
}
