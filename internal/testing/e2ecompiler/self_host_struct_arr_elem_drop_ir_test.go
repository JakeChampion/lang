package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostStructArrElemDropIRX86_64 covers the Perceus ARRAY-ELEMENT deep-drop
// (#2649): a struct-ARRAY field `S { elems: Inner[] }` releases each element's own
// rc fields, not just each element box, when its last owner drops it.
//
// Runtime signal is heap exhaustion: a long churn that leaks each element's `items`
// buffer exhausts the bump heap and is SIGKILLed (137); with the element deep-drop
// the freed blocks recycle and the churn stays bounded (exit 0).
func TestSelfHostStructArrElemDropIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	run := func(t *testing.T, prog, name string, want int) {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", name)
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
	// fresh sole-owned literal (rc 1) holding an `items` buffer. Both `items` buffers
	// are released each iteration, so 50M alloc->drop cycles stay bounded (exit 0); a
	// leaked element buffer exhausts the heap -> SIGKILL (137). items goes through id
	// so it is built on the heap rather than placed as a constant.
	run(t, `struct Inner { items: i32[] }
@noinline function id(xs: i32[]): i32[] { return xs; }
struct S { elems: Inner[], tag: i32 }
function mk(): i32 {
    let s: S = S { elems: [Inner { items: id([1,2,3,4,5,6,7,8]) }, Inner { items: id([9,10,11,12,13,14,15,16]) }], tag: 3 };
    return s.elems[0].items[0] + s.elems[1].items[7] + s.tag;
}
function main(): i32 {
    let acc: i32 = 0; let f: i32 = 0;
    while (f < 50000000) { acc = mk(); f = f + 1; }
    return acc - 20;
}`, "struct_arr_elem_drop_churn", 0)

	// VALUE-CORRECTNESS: every element's items are read back before the drop; a premature
	// free of a live element buffer would corrupt the read. Two Inners: items sum
	// (1..8)=36 and (9..16)=100, + tag 3 = 139. items goes through id so it is built on
	// the heap rather than placed as a constant.
	run(t, `struct Inner { items: i32[] }
@noinline function id(xs: i32[]): i32[] { return xs; }
struct S { elems: Inner[], tag: i32 }
function main(): i32 {
    let s: S = S { elems: [Inner { items: id([1,2,3,4,5,6,7,8]) }, Inner { items: id([9,10,11,12,13,14,15,16]) }], tag: 3 };
    let sum: i32 = 0; let e: i32 = 0;
    while (e < 2) {
        let j: i32 = 0;
        while (j < 8) { sum = sum + s.elems[e].items[j]; j = j + 1; }
        e = e + 1;
    }
    return sum + s.tag;
}`, "struct_arr_elem_drop_value", 139)

	// MULTI-LEVEL x ARRAY-ELEMENT: the element struct is itself a nested chain
	// (`Inner { mid: Mid }`, `Mid { items: i32[] }`), so each element's release
	// recurses through Mid down to items. 40M cycles stay bounded (exit 0).
	// items goes through id so the chain is built on the heap rather than placed as a
	// constant.
	run(t, `struct Mid { items: i32[] }
@noinline function id(xs: i32[]): i32[] { return xs; }
struct Inner { mid: Mid, it: i32 }
struct S { elems: Inner[], tag: i32 }
function mk(): i32 {
    let s: S = S { elems: [Inner { mid: Mid { items: id([1,2,3,4,5,6,7,8]) }, it: 4 }], tag: 3 };
    return s.elems[0].mid.items[0] + s.elems[0].it + s.tag;
}
function main(): i32 {
    let acc: i32 = 0; let f: i32 = 0;
    while (f < 40000000) { acc = mk(); f = f + 1; }
    return acc - 8;
}`, "struct_arr_elem_drop_multilevel", 0)
}
