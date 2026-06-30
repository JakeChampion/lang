package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostStructDeepDropIRX86_64 covers the Perceus slice-3 DEEP-DROP: a
// direct nested-struct field (`Outer { inner: Inner }`) whose inner is a LEAF
// struct carrying its own rc-array field is now RECURSIVELY reclaimed — when the
// inner box is uniquely owned, `__struct_drop_<Inner>` releases the inner's array
// buffers before the inner box is freed, instead of the shallow box-only free that
// leaked them (slices 3a/b/c).
//
// CYCLE SAFETY: deep-drop fires ONLY for a leaf inner (no nested-struct field of
// its own), so `__struct_drop_<Inner>` makes no further recursive struct_drop call
// — the recursion is depth-1 and cannot loop. A self-referential / tree struct
// necessarily carries a nested-struct field, so its field edge stays shallow.
//
// The leak/reclaim signal is heap exhaustion: a long churn that leaks the inner's
// array buffer each iteration exhausts the bump heap and is SIGKILLed (exit 137);
// with the deep-drop reclaiming it the freed blocks recycle and the churn stays
// bounded (exit 0) — the same differential the field-reclaim IR test uses.
func TestSelfHostStructDeepDropIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	run := func(t *testing.T, prog, name string, want int, wantAsmSubstr string) {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", name)
		}
		if wantAsmSubstr != "" && !strings.Contains(string(asm), wantAsmSubstr) {
			t.Fatalf("%s: emitted asm missing %q — the nested-struct field did not deep-drop", name, wantAsmSubstr)
		}
		bin := buildBin(t, gcc, dir, name, string(asm))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], bin)...)
		}
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s exited %d, want %d", name, code, want)
		}
	}

	// DEEP-DROP + CHURN: `o.inner` is a fresh struct LITERAL (sole-owned, rc 1), so
	// the is_unique gate passes and `__struct_drop_Inner` releases `inner.items`
	// before the inner box is freed. Asserts the recursive call is emitted, and
	// that 150M alloc→drop cycles stay bounded (exit 0); under the slice-3 shallow
	// drop `inner.items` leaked every call → heap exhausted → SIGKILL (137).
	run(t, `struct Inner { items: i32[] }
struct Outer { inner: Inner, tag: i32 }
function mk(): i32 {
    var o: Outer = Outer { inner: Inner { items: [1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16] }, tag: 7 };
    return o.inner.items[0] + o.inner.items[15] + o.tag;
}
function main(): i32 {
    var s: i32 = 0; var f: i32 = 0;
    while (f < 150000000) { s = mk(); f = f + 1; }
    return s - 24;
}`, "struct_deep_drop_churn", 0, "call __fn___struct_drop_Inner")

	// VALUE-CORRECTNESS: the inner is read back before the drop; a wrong free of a
	// live buffer would corrupt it. o.inner.items[0..15] sum to 136, + tag 7 = 143.
	run(t, `struct Inner { items: i32[] }
struct Outer { inner: Inner, tag: i32 }
function main(): i32 {
    var o: Outer = Outer { inner: Inner { items: [1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16] }, tag: 7 };
    var sum: i32 = 0; var j: i32 = 0;
    while (j < 16) { sum = sum + o.inner.items[j]; j = j + 1; }
    return sum + o.tag;
}`, "struct_deep_drop_value", 143, "")

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
    while (f < 1000000) { s = mk(); f = f + 1; }
    return s - 8;
}`, "struct_deep_drop_cyclic_safe", 0, "")
}
