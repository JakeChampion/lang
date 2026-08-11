package e2eselfhost

import (
	"testing"
)

// TestSelfHostEnumStrPayloadReclaimIRArm64 is the arm64 port of the #4355
// slice-2 enum/Option STRING-payload release (x86 sibling:
// TestSelfHostEnumStrPayloadReclaimIRX86_64). Under qemu the reclaim is proven
// by the same bounded high-water assertion at a lighter churn plus the
// over-release detector; heavy churn stays on the x86 path.
func TestSelfHostEnumStrPayloadReclaimIRArm64(t *testing.T) {
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
			t.Errorf("%s exited %d, want %d (98 = payload leaked; 99 = over-release; 97 = value corrupted)", name, code, want)
		}
	}

	// ENUM string payload consumed by match — flat across the second churn.
	run(t, `enum Tok { Word(string), Num(i32) }
function go(pre: string): i32 { var x = Word(pre + "abc"); var r = 0; match (x) { Word(s) => { r = s.len(); }, Num(n) => { r = n; }, } return r; }
function churn(n: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < n) { acc = (acc + go(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(2000); var b1: i32 = (__heap_bump_bytes() as i32); var x: i32 = churn(2000); var b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`,
		"enum-str-payload-flat-arm64", 0)

	// OPTION string payload consumed by match — flat.
	run(t, `function go(pre: string): i32 { var o: Option[string] = Some(pre + "xyz"); var r = 0; match (o) { Some(s) => { r = s.len(); }, None => { r = 1; }, } return r; }
function churn(n: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < n) { acc = (acc + go(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(2000); var b1: i32 = (__heap_bump_bytes() as i32); var x: i32 = churn(2000); var b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`,
		"option-str-payload-flat-arm64", 0)
}
