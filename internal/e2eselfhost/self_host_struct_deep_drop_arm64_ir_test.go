package e2eselfhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostStructDeepDropIRArm64 is the arm64 port of the Perceus slice-3
// DEEP-DROP (the x86 sibling is TestSelfHostStructDeepDropIRX86_64): a direct
// nested-struct field (`Outer { inner: Inner }`) whose inner is a LEAF struct
// carrying its OWN rc-array field is now RECURSIVELY reclaimed. When the inner box
// is uniquely owned, `__struct_drop_<Inner>` releases the inner's array buffers
// before the inner box is freed, instead of the shallow box-only free that leaked
// them (slice 3b).
//
// CYCLE SAFETY: deep-drop fires ONLY for a leaf inner (no nested-struct field of
// its own), so `__struct_drop_<Inner>` makes no further recursive struct_drop call
// — the recursion is depth-1 and cannot loop. A self-referential / tree struct
// necessarily carries a nested-struct field, so its field edge stays shallow.
//
// Under qemu the reclaim is proven by CORRECTNESS (a wrong free of a live buffer
// corrupts the read-back) plus an asm-shape assertion that the recursive
// `bl __fn___struct_drop_Inner` (and its is_unique gate) is emitted — the same
// signal the x86 churn-exhaustion test pins at runtime. Heavy 150M-iteration churn
// is left to the x86 path (too slow under qemu).
func TestSelfHostStructDeepDropIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	run := func(t *testing.T, prog, name string, want int, wantAsmSubstr string) {
		t.Helper()
		asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(prog), "-target", "arm64")
		if len(asm) == 0 {
			t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", name)
		}
		if wantAsmSubstr != "" && !strings.Contains(string(asm), wantAsmSubstr) {
			t.Fatalf("%s: emitted arm64 asm missing %q — the nested-struct field did not deep-drop", name, wantAsmSubstr)
		}
		bin := buildBinArm64(t, arm64gcc, dir, name, string(asm))
		cmd := runArm64Bin(qemu, bin)
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s exited %d, want %d", name, code, want)
		}
	}

	// DEEP-DROP shape + value: `o.inner` is a fresh struct LITERAL (sole-owned, rc 1),
	// so the is_unique gate passes and `__struct_drop_Inner` releases `inner.items`
	// before the inner box is freed. The inner is read back before the drop; a wrong
	// free of the live buffer would corrupt it. items[0..15] sum to 136, + tag 7 = 143.
	// Asserts the recursive `bl __fn___struct_drop_Inner` is emitted.
	run(t, `struct Inner { items: i32[] }
struct Outer { inner: Inner, tag: i32 }
function main(): i32 {
    var o: Outer = Outer { inner: Inner { items: [1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16] }, tag: 7 };
    var sum: i32 = 0; var j: i32 = 0;
    while (j < 16) { sum = sum + o.inner.items[j]; j = j + 1; }
    return sum + o.tag;
}`, "struct_deep_drop_arm64_value", 143, "bl __fn___struct_drop_Inner")

	// CYCLE SAFETY: a tree (`Node { kids: Node[] }`) must NOT infinitely recurse.
	// `kids` is an array-of-struct (the k_box element walk, shallow per element);
	// Node has no direct nested-struct field, so no deep-drop edge is created. A
	// churn building a 2-node tree each iteration stays correct + terminating.
	run(t, `struct Node { kids: Node[], v: i32 }
function mk(): i32 {
    var leaf: Node = Node { kids: [], v: 5 };
    var root: Node = Node { kids: [leaf], v: 3 };
    return root.v + root.kids[0].v;
}
function main(): i32 {
    var s: i32 = 0; var f: i32 = 0;
    while (f < 200000) { s = mk(); f = f + 1; }
    return s - 8;
}`, "struct_deep_drop_arm64_cyclic_safe", 0, "")
}
