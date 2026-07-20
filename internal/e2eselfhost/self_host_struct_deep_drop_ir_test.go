package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostStructDeepDropIRX86_64 covers the Perceus slice-3 DEEP-DROP: a
// direct nested-struct field (`Outer { inner: Inner }`) whose inner carries its
// own rc-array field is RECURSIVELY reclaimed — when the inner box is uniquely
// owned, `__struct_drop_<Inner>` releases the inner's array buffers before the
// inner box is freed, instead of the shallow box-only free that leaked them
// (slices 3a/b/c).
//
// DEPTH: `nested_field_deep_drop_ok` / `nddo_reach` admits ARBITRARY acyclic depth,
// so `__struct_drop_<Inner>` may itself recurse into Inner's own deep-drop-ok
// nested-struct fields (the depth-2+ cases below): the emitted call graph is a DAG
// bounded by the struct-type count, and the per-type bodies are emitted for the
// whole transitive closure (asm_ir's index-driven `struct_drop:` need loop / wasm's
// `struct_drop_types` transitive walk / arm64 mirror).
//
// CYCLE SAFETY: a back-edge on the nested-struct closure (a self-referential / tree
// struct — `Node { kids: Node[] }`) poisons the whole chain, so `nddo_reach` returns
// the cyclic sentinel and the field edge stays SHALLOW — the recursion cannot loop.
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

	// DEPTH-2 DEEP-DROP + CHURN (#5336): `Outer { mid: Mid }`, `Mid { inner: Inner }`,
	// `Inner { items: i32[] }`. `__struct_drop_Outer` must call `__struct_drop_Mid`
	// which must call `__struct_drop_Inner` (transitive closure), releasing the
	// depth-2 `inner.items` buffer. Asserts BOTH transitive calls are emitted, and
	// that 150M alloc→drop cycles stay bounded (exit 0); a depth-1-only deep-drop
	// leaks `inner.items` every call → heap exhausted → SIGKILL (137). items[0]+
	// items[15]=17, +mid.m 2 +tag 7 = 26, so s-26==0.
	run(t, `struct Inner { items: i32[] }
struct Mid { inner: Inner, m: i32 }
struct Outer { mid: Mid, tag: i32 }
function mk(): i32 {
    var o: Outer = Outer { mid: Mid { inner: Inner { items: [1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16] }, m: 2 }, tag: 7 };
    return o.mid.inner.items[0] + o.mid.inner.items[15] + o.mid.m + o.tag;
}
function main(): i32 {
    var s: i32 = 0; var f: i32 = 0;
    while (f < 150000000) { s = mk(); f = f + 1; }
    return s - 26;
}`, "struct_deep_drop_depth2_churn", 0, "call __fn___struct_drop_Mid")

	// DEPTH-3 with a STRING leaf field (also reclaimable via nddo_reach's #4297 A2
	// string credit): `A { b: B }`, `B { c: C }`, `C { name: string, xs: i32[] }`.
	// The full chain __struct_drop_A → _B → _C must be emitted; the depth-3 `name`
	// string + `xs` buffer are reclaimed each iteration. Bounded (exit 0) after,
	// unbounded (137) if any level short-circuits. xs[0]+xs[1]=1, +name.len() 3 = 4.
	run(t, `struct C { name: string, xs: i32[] }
struct B { c: C, y: i32 }
struct A { b: B, z: i32 }
function mk(): i32 {
    var a: A = A { b: B { c: C { name: "abc", xs: [0, 1] }, y: 9 }, z: 4 };
    return a.b.c.xs[0] + a.b.c.xs[1] + a.b.c.name.len();
}
function main(): i32 {
    var s: i32 = 0; var f: i32 = 0;
    while (f < 100000000) { s = mk(); f = f + 1; }
    return s - 4;
}`, "struct_deep_drop_depth3_str_churn", 0, "call __fn___struct_drop_C")

	// DEPTH-2 VALUE-CORRECTNESS: read the whole depth-2 chain back before the drop; a
	// wrong free of a live buffer would corrupt the sum. items[0..15] sum 136 + mid.m
	// 2 + tag 7 = 145.
	run(t, `struct Inner { items: i32[] }
struct Mid { inner: Inner, m: i32 }
struct Outer { mid: Mid, tag: i32 }
function main(): i32 {
    var o: Outer = Outer { mid: Mid { inner: Inner { items: [1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16] }, m: 2 }, tag: 7 };
    var sum: i32 = 0; var j: i32 = 0;
    while (j < 16) { sum = sum + o.mid.inner.items[j]; j = j + 1; }
    return sum + o.mid.m + o.tag;
}`, "struct_deep_drop_depth2_value", 145, "")
}
