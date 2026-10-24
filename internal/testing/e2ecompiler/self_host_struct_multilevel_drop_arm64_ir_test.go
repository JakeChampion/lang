package e2ecompiler

import (
	"testing"
)

func TestSelfHostStructMultiLevelDropIRArm64(t *testing.T) {
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

	run(t, `struct C { items: i32[] }
struct B { c: C, bt: i32 }
struct A { b: B, at: i32 }
@noinline function runtime(n: i32): i32 { return n; }
function main(): i32 {
    let a: A = A { b: B { c: C { items: [runtime(1),2,3,4,5,6,7,8,9,10,11,12,13,14,15,16] }, bt: 2 }, at: 7 };
    let sum: i32 = 0; let j: i32 = 0;
    while (j < 16) { sum = sum + a.b.c.items[j]; j = j + 1; }
    return sum + a.b.bt + a.at;
}`, "struct_multilevel_drop_arm64_value", 145)

	run(t, `struct C { items: i32[] }
struct B { c: C, bt: i32 }
struct A { b: B, at: i32 }
@noinline function runtime(n: i32): i32 { return n; }
function mk(): i32 {
    let a: A = A { b: B { c: C { items: [runtime(1),2,3,4,5,6,7,8,9,10,11,12,13,14,15,16] }, bt: 2 }, at: 7 };
    return a.b.c.items[0] + a.b.c.items[15] + a.b.bt + a.at;
}
function main(): i32 {
    let s: i32 = 0; let f: i32 = 0;
    while (f < 200000) { s = mk(); f = f + 1; }
    return s - 26;
}`, "struct_multilevel_drop_arm64_churn", 0)
}
