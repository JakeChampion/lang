package e2eselfhost

import (
	"strings"
	"testing"
)

// TestSelfHostStructArrElemDropIRArm64 is the arm64 port of the ARRAY-ELEMENT deep-drop
// (the x86 sibling is TestSelfHostStructArrElemDropIRX86_64). The k_box arm calls
// __struct_arr_elems_drop_<Inner> (x19=buffer, x20=index callee-saved across the nested
// __struct_drop_<Inner>), reloading its box from [sp, #16] afterwards. Proven under qemu
// by CORRECTNESS (a wrong free of a live element buffer corrupts the read-back) plus an
// asm-shape assertion that the helper call `bl __fn___struct_arr_elems_drop_Inner` is
// emitted. Heavy churn is left to the x86 path (too slow under qemu).
func TestSelfHostStructArrElemDropIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	run := func(t *testing.T, prog, name string, want int, wantAsmSubstr string) {
		t.Helper()
		asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(prog), "-target", "arm64")
		if len(asm) == 0 {
			t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", name)
		}
		if wantAsmSubstr != "" && !strings.Contains(string(asm), wantAsmSubstr) {
			t.Fatalf("%s: emitted arm64 asm missing %q — the struct-array element did not deep-drop", name, wantAsmSubstr)
		}
		bin := buildBinArm64(t, arm64gcc, dir, name, string(asm))
		cmd := runArm64Bin(qemu, bin)
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s exited %d, want %d", name, code, want)
		}
	}

	// ARRAY-ELEMENT shape + value: two `Inner` elements each holding an `items` buffer,
	// read back before the drop. items sums (1..8)=36 and (9..16)=100, + tag 3 = 139.
	// Asserts the recursive `bl __fn___struct_arr_elems_drop_Inner` is emitted.
	run(t, `struct Inner { items: i32[] }
struct S { elems: Inner[], tag: i32 }
function main(): i32 {
    var s: S = S { elems: [Inner { items: [1,2,3,4,5,6,7,8] }, Inner { items: [9,10,11,12,13,14,15,16] }], tag: 3 };
    var sum: i32 = 0; var e: i32 = 0;
    while (e < 2) {
        var j: i32 = 0;
        while (j < 8) { sum = sum + s.elems[e].items[j]; j = j + 1; }
        e = e + 1;
    }
    return sum + s.tag;
}`, "struct_arr_elem_drop_arm64_value", 139, "bl __fn___struct_arr_elems_drop_Inner")

	// LIGHT CHURN: confirms the element helper terminates + stays correct under repetition
	// (a register-clobber bug in the helper's x19/x20 save/restore or the box reload would
	// corrupt the loop / heap). mk returns 20; exit 0.
	run(t, `struct Inner { items: i32[] }
struct S { elems: Inner[], tag: i32 }
function mk(): i32 {
    var s: S = S { elems: [Inner { items: [1,2,3,4,5,6,7,8] }, Inner { items: [9,10,11,12,13,14,15,16] }], tag: 3 };
    return s.elems[0].items[0] + s.elems[1].items[7] + s.tag;
}
function main(): i32 {
    var acc: i32 = 0; var f: i32 = 0;
    while (f < 200000) { acc = mk(); f = f + 1; }
    return acc - 20;
}`, "struct_arr_elem_drop_arm64_churn", 0, "")
}
