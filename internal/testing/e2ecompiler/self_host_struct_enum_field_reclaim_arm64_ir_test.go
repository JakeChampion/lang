package e2ecompiler

import (
	"testing"
)

func TestSelfHostStructEnumFieldReclaimIRArm64(t *testing.T) {
	boxedProbes(t)
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	run := func(t *testing.T, prog, name string, want int) {
		t.Helper()
		if code := arm64CensusRun(t, x86runner, driverBin, arm64gcc, qemu, name, prog); code != want {
			t.Errorf("%s exited %d, want %d", name, code, want)
		}
	}

	run(t, `enum Shape { Circle, Square, Rect(i32) }
struct Tagged { e: Shape, n: i32 }
@noinline function runtime(n: i32): i32 { return n; }
function main(): i32 {
    let t: Tagged = Tagged { e: Rect(runtime(7)), n: 5 };
    let r: i32 = 0;
    match (t.e) { Rect(v) => { r = v; }, _ => { r = 0; } }
    return r + t.n;
}`, "struct_enum_field_arm64_shape", 12)

	run(t, `enum Shape { Circle, Square, Rect(i32) }
struct Tagged { e: Shape, n: i32 }
@noinline function runtime(n: i32): i32 { return n; }
function churn(n: i32): i32 {
    let bad: i32 = 0; let i: i32 = 0;
    while (i < n) {
        let s: Shape = Rect(runtime(9));
        let t: Tagged = Tagged { e: s, n: 1 };
        match (t.e) { Rect(v) => { if (v != 9) { bad = 1; } }, _ => { bad = 1; } }
        i = i + 1;
    }
    return bad;
}
function main(): i32 { return churn(20000); }`, "struct_enum_field_arm64_churn", 0)
}
