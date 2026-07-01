package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostStructArrElemDropIRX86_64 covers the Perceus ARRAY-ELEMENT deep-drop
// (#2649): a struct-ARRAY field `S { elems: Inner[] }` whose element struct is
// deep-drop-ok now recursively reclaims each ELEMENT's own rc fields, closing the
// "one-level array-element gap" where the k_box walk shallow-freed each element box
// and leaked its arrays.
//
// The reclaim is a per-element-type helper `__struct_arr_elems_drop_<Inner>(buffer)`
// emitted alongside `__struct_drop_<S>`: it is_unique-gates the buffer AND each
// element box, then calls `__struct_drop_<Inner>` per uniquely-owned element to free
// the element's own arrays — BEFORE the existing element/buffer arr_dec frees the
// boxes. The helper uses callee-saved rbx/r12 for buffer/index so they survive the
// nested __struct_drop_<Inner> call; the k_box caller reloads its box from 8(%rsp)
// afterwards.
//
// Runtime signal is heap exhaustion: a long churn that leaks each element's `items`
// buffer exhausts the bump heap and is SIGKILLed (137); with the element deep-drop
// the freed blocks recycle and the churn stays bounded (exit 0).
func TestSelfHostStructArrElemDropIRX86_64(t *testing.T) {
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
			t.Fatalf("%s: emitted asm missing %q — the struct-array element did not deep-drop", name, wantAsmSubstr)
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

	// ARRAY-ELEMENT DEEP-DROP + CHURN: `s.elems` is a 2-element `Inner[]`, each Inner a
	// fresh sole-owned literal (rc 1) holding an `items` buffer. The per-element helper
	// reclaims both `items` buffers each iteration. Asserts the helper call is emitted,
	// and 50M alloc->drop cycles stay bounded (exit 0); under the shallow k_box walk each
	// element's `items` leaked every call -> heap exhausted -> SIGKILL (137).
	run(t, `struct Inner { items: i32[] }
struct S { elems: Inner[], tag: i32 }
function mk(): i32 {
    var s: S = S { elems: [Inner { items: [1,2,3,4,5,6,7,8] }, Inner { items: [9,10,11,12,13,14,15,16] }], tag: 3 };
    return s.elems[0].items[0] + s.elems[1].items[7] + s.tag;
}
function main(): i32 {
    var acc: i32 = 0; var f: i32 = 0;
    while (f < 50000000) { acc = mk(); f = f + 1; }
    return acc - 20;
}`, "struct_arr_elem_drop_churn", 0, "call __fn___struct_arr_elems_drop_Inner")

	// VALUE-CORRECTNESS: every element's items are read back before the drop; a premature
	// free of a live element buffer would corrupt the read. Two Inners: items sum
	// (1..8)=36 and (9..16)=100, + tag 3 = 139.
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
}`, "struct_arr_elem_drop_value", 139, "")

	// MULTI-LEVEL x ARRAY-ELEMENT: the element struct is itself a nested chain
	// (`Inner { mid: Mid }`, `Mid { items: i32[] }`), so the element helper calls
	// __struct_drop_Inner which recurses into __struct_drop_Mid — array-element deep-drop
	// composed with the acyclic multi-level gate. 40M cycles stay bounded (exit 0).
	run(t, `struct Mid { items: i32[] }
struct Inner { mid: Mid, it: i32 }
struct S { elems: Inner[], tag: i32 }
function mk(): i32 {
    var s: S = S { elems: [Inner { mid: Mid { items: [1,2,3,4,5,6,7,8] }, it: 4 }], tag: 3 };
    return s.elems[0].mid.items[0] + s.elems[0].it + s.tag;
}
function main(): i32 {
    var acc: i32 = 0; var f: i32 = 0;
    while (f < 40000000) { acc = mk(); f = f + 1; }
    return acc - 8;
}`, "struct_arr_elem_drop_multilevel", 0, "call __fn___struct_drop_Mid")
}
