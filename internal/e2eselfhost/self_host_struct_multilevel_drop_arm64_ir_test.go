package e2eselfhost

import (
	"testing"
)

// TestSelfHostStructMultiLevelDropIRArm64 is the arm64 port of the MULTI-LEVEL
// deep-drop (the x86 sibling is TestSelfHostStructMultiLevelDropIRX86_64). The
// reclaim decision lives in the shared `irtables.nested_field_deep_drop_ok` (now an
// acyclic-closure gate, not leaf-only), so arm64 inherits multi-level deep-drop
// through the same generic k_struct emission arm — `bl __fn___struct_drop_B` for a
// non-leaf inner B, which the old leaf gate never emitted.
//
// Under qemu the reclaim is proven by CORRECTNESS (a wrong free of a live buffer
// down the chain corrupts the read-back) plus a balanced arm64 census. Heavy
// churn is left to the x86 path (too slow under qemu).
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

	// MULTI-LEVEL shape + value: a 3-level chain `A -> B -> C{ items }`, every struct a
	// fresh sole-owned literal (rc 1). The deep value is read back before the drop; a
	// premature free of a live buffer would corrupt it. items[0..15] sum 136 + b.bt 2 +
	// a.at 7 = 145.
	// Both probes use a runtime element to keep the array and boxes on the heap.
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

	// LIGHT CHURN: a small alloc->drop loop confirming the multi-level chain terminates
	// (no runaway recursion) and stays correct under repetition. mk returns 26; exit 0.
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
