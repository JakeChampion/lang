package e2eselfhost

import (
	"strings"
	"testing"
)

// TestSelfHostEnumStructPayloadDropIRArm64 is the arm64 port of the Perceus
// enum-payload struct deep-drop (the x86 sibling is
// TestSelfHostEnumStructPayloadDropIRX86_64). The reclaim is emitted in the shared
// irlower lowering (an IR call_direct("__struct_drop_<Inner>")), so arm64 gets it for
// free — its backend collects the struct_drop need from the op exactly like x86 and
// emits the same __fn___struct_drop_Inner body (shipped in the slice-3 deep-drop).
//
// Under qemu the reclaim is proven by CORRECTNESS (a wrong free of a live buffer
// corrupts the read-back) plus an asm-shape assertion that the recursive
// `bl __fn___struct_drop_Inner` is emitted. The heavy heap-exhaustion churn is left
// to the x86 path (too slow under qemu). Variant constructors are UNQUALIFIED.
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
	asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(prog), "-target", "arm64")
	if len(asm) == 0 {
		t.Fatal("self-host arm64 compiler emitted 0 bytes")
	}
	if !strings.Contains(string(asm), "bl __fn___struct_drop_Inner") {
		t.Fatalf("emitted arm64 asm missing `bl __fn___struct_drop_Inner` — the enum struct payload did not deep-drop")
	}
	bin := buildBinArm64(t, arm64gcc, dir, "enum_struct_payload_arm64", string(asm))
	cmd := runArm64Bin(qemu, bin)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("enum struct payload exited %d, want 0 (f()=17) — reclaim corrupted the live buffer?", code)
	}
}
