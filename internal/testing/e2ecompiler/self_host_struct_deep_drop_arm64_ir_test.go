package e2ecompiler

import (
	"testing"
)

func TestSelfHostStructDeepDropIRArm64(t *testing.T) {
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

	run(t, `struct Inner { items: i32[] }
struct Outer { inner: Inner, tag: i32 }
@noinline function runtime(n: i32): i32 { return n; }
function main(): i32 {
    let o: Outer = Outer { inner: Inner { items: [runtime(1),2,3,4,5,6,7,8,9,10,11,12,13,14,15,16] }, tag: 7 };
    let sum: i32 = 0; let j: i32 = 0;
    while (j < 16) { sum = sum + o.inner.items[j]; j = j + 1; }
    return sum + o.tag;
}`, "struct_deep_drop_arm64_value", 143)

	run(t, `struct Node { kids: Node[], v: i32 }
@noinline function id(xs: i32[]): i32[] { return xs; }
function mk(): i32 {
    let leaf: Node = Node { kids: [], v: id([5])[0] };
    let root: Node = Node { kids: [leaf], v: 3 };
    return root.v + root.kids[0].v;
}
function main(): i32 {
    let s: i32 = 0; let f: i32 = 0;
    while (f < 200000) { s = mk(); f = f + 1; }
    return s - 8;
}`, "struct_deep_drop_arm64_cyclic_safe", 0)
}
