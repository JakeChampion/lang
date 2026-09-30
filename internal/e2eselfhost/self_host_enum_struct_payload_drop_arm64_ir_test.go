package e2eselfhost

import (
	"testing"
)

// TestSelfHostEnumStructPayloadDropIRArm64 is the arm64 port of the Perceus
// enum-payload struct deep-drop (the x86 sibling is
// TestSelfHostEnumStructPayloadDropIRX86_64). Under qemu the reclaim is proven by
// the census balancing at live_bytes 0, and a wrong free of the live buffer
// corrupts the read-back. Variant constructors are UNQUALIFIED.
func TestSelfHostEnumStructPayloadDropIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	// bound-borrow-only payload: the arm reads inner.items before the post-arm reclaim
	// deep-drops it. items[0]+items[15] = 1 + 16 = 17. A wrong/double free corrupts it.
	prog := `struct Inner { items: i32[] }
enum Box { Full(Inner), Empty }
function f(): i32 {
    var b: Box = Full(Inner { items: [1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16] });
    var r: i32 = 0;
    match (b) {
        Full(inner) => { r = inner.items[0] + inner.items[15]; },
        Empty => { r = 0; },
    }
    return r;
}
function main(): i32 { return f() - 17; }`
	if code := arm64CensusRun(t, x86runner, driverBin, arm64gcc, qemu, "enum_struct_payload_arm64", prog); code != 0 {
		t.Errorf("enum struct payload exited %d, want 0 (f()=17) — reclaim corrupted the live buffer?", code)
	}
}
