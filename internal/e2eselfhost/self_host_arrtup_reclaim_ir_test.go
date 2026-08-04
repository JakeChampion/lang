package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// arrtupReclaimCases pin the #4365 `(<tuple-with-array>)[]` array-of-tuples reclaim:
// a `var xs: (i32, i32[])[] = [(i, [i, i+1]), ...]` local — an array whose ELEMENTS
// are tuples each carrying a fresh inner array — leaked all three levels (the
// per-element inner array buffers, the element tuple boxes, and the outer buffer)
// per loop iteration on the self-host IR path (native bounds it). The new "ARRTUP:"
// class credits a fresh array of fresh tuple literals consumed borrow-only, and
// releases it with a COUNTED ELEMENT WALK (emit_arrtup_deep_free): for each element
// the type-driven tuple deep-drop (emit_tuple_type_child_drops: dec each array field,
// recurse nested tuples) + tuple box dec, then the outer buffer — at the loop-rebind
// and the exit sweep. No runtime helper; the loop lowers through backend-common IR
// ops (block/loop/arr_len/arr_get/rc_dec), so all three backends share it.
//
// SOUNDNESS: the element payload use is checked by arrtup_elem_payload_escapes — a
// scalar field read (xs[i].0), an indexed array-field read (xs[i].1[j]) and
// xs[i].1.len() are borrows (reclaim proceeds); a BARE array-field extraction
// (store / return / pass / alias / slice xs[i].1) OR a bound element (var t = xs[i] /
// for t in xs, via arrarr_row_escapes) escapes and the local is left leak-safe
// (never over-released).
var arrtupReclaimCases = []struct {
	name string
	src  string
	want int
}{
	// Core churn: rebuilt per iteration, scalar read only.
	{"arrtup-churn", `function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { var xs: (i32, i32[])[] = [(i, [i, i + 1]), (i + 1, [i + 2, i + 3])]; acc = (acc + xs[0].0) % 251; i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 5000) { var ys: (i32, i32[])[] = [(j, [j, j + 1]), (j + 1, [j + 2, j + 3])]; acc = (acc + ys[0].0) % 251; j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// Full borrow set: scalar field (xs[i].0), indexed array-field (xs[i].1[j]) and
	// xs[i].1.len() are all admitted — still reclaims (bounded).
	{"arrtup-borrow-full", `function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 5000) {
        var xs: (i32, i32[])[] = [(i, [i, i + 1]), (i + 1, [i + 2, i + 3])];
        acc = (acc + xs[0].0 + xs[1].1[0] + xs[0].1.len()) % 251;
        i = i + 1;
    }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 5000) { var ys: (i32, i32[])[] = [(j, [j, j + 1])]; acc = (acc + ys[0].1[1]) % 251; j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// PAYLOAD-ESCAPE-STORE negative: `keep = xs[0].1` extracts the array out of an
	// element — the local is NOT credited (leak-safe), and MUST NOT be over-released.
	{"arrtup-escape-store-safe", `function main(): i32 {
    var keep: i32[] = [0, 0];
    var i: i32 = 0;
    while (i < 50) {
        var xs: (i32, i32[])[] = [(i, [i, i + 1])];
        keep = xs[0].1;
        i = i + 1;
    }
    var acc: i32 = keep[0] + keep[1];
    if (acc < 0) { return 97; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// PAYLOAD-ESCAPE-CALL negative: `take(xs[0].1)` passes the array field to a call
	// (a retain) — un-credited, leak-safe, detector zero.
	{"arrtup-escape-call-safe", `function take(xs: i32[]): i32 { return xs[0]; }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 50) {
        var xs: (i32, i32[])[] = [(i, [i, i + 1])];
        acc = (acc + take(xs[0].1)) % 251;
        i = i + 1;
    }
    if (acc < 0) { return 97; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// ESCAPE-VIA-FN negative: the array-of-tuples is returned — ownership moves out,
	// nothing freed, value exact (a[0].0 + a[0].1[0] + a[1].1[1] = 5 + 5 + 8 = 18).
	{"arrtup-escape-fn-safe", `function mk(n: i32): (i32, i32[])[] {
    var xs: (i32, i32[])[] = [(n, [n, n + 1]), (n + 1, [n + 2, n + 3])];
    return xs;
}
function main(): i32 {
    var a = mk(5);
    var v: i32 = a[0].0 + a[0].1[0] + a[1].1[1];
    if (__rc_underflow() != 0) { return 99; }
    return v;
}`, 18},
}

// TestSelfHostArrTupReclaimIRX86_64 drives the cases through the self-hosted x86-64
// compiler (asm_run), heap-bump + underflow guarded.
func TestSelfHostArrTupReclaimIRX86_64(t *testing.T) {
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

	for _, tc := range arrtupReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = array-of-tuples leaked; 99 = over-release/underflow; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
