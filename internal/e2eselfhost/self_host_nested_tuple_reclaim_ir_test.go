package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// nestedTupleReclaimCases pin the #4365 nested-tuple-with-inner-array reclaim: a
// tuple whose element is ITSELF a tuple literal carrying a fresh array
// (`((i, [i, i+1]), i)`) leaked all three levels — the inner array buffer, the
// inner tuple box, and the outer box — per loop iteration / per discard on the
// self-host IR path (native bounds it). The TUPRC: admission
// (tuple_lit_rc_reclaimable) now recurses into nested tuple-literal elements
// (tuple_lit_has_array), and the deep-drop (emit_tuple_child_drops) frees each
// fresh array buffer and nested tuple box depth-first before the outer box. A
// bare-ident element (nested or flat) aliases a live local and is skipped
// (leak-safe); a returned tuple escapes and is never freed. Also fixes a latent
// bug: expr_scalar_leaf classified a tuple element as scalar, so the shallow
// scalar-tuple discard admitted a nested tuple and leaked its inner box.
var nestedTupleReclaimCases = []struct {
	name string
	src  string
	want int
}{
	// Core churn: nested-tuple loop-local rebuilt per iteration, len/scalar read.
	{"nested-tuple-churn", `function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 200) { var t: ((i32, i32[]), i32) = ((w, [w, w + 1]), w); acc = (acc + t.1) % 251; w = w + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < 5000) { var t2: ((i32, i32[]), i32) = ((i, [i, i + 1]), i); acc = (acc + t2.1) % 251; i = i + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// Nested-read: the inner array is BORROW-read through `t.0.1[i]` before the
	// rebind free — reads precede the drop, values exact, still bounded.
	{"nested-tuple-read", `function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 5000) {
        var t: ((i32, i32[]), i32) = ((i, [i, i + 1]), i);
        acc = (acc + t.0.1[0] + t.0.1[1]) % 251;
        i = i + 1;
    }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 5000) {
        var t2: ((i32, i32[]), i32) = ((j, [j, j + 1]), j);
        acc = (acc + t2.0.1[0]) % 251;
        j = j + 1;
    }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// ESCAPE negative: the nested tuple is returned — ownership moves out, the
	// local is non-reclaimable, nothing freed (values exact, no dangle).
	{"nested-tuple-escape-safe", `function mk(i: i32): ((i32, i32[]), i32) {
    var t: ((i32, i32[]), i32) = ((i, [i, i + 1]), i);
    return t;
}
function main(): i32 {
    var t = mk(5);
    var v: i32 = t.0.1[0] + t.0.1[1] + t.1;
    if (v != 16) { return 97; }
    if (__rc_underflow() != 0) { return 99; }
    return v;
}`, 16},
	// IDENT-inner negative: the nested tuple's array element is a bare ident
	// (`((w, xs), w)`) aliasing a live local — skipped by the deep-drop (only the
	// fresh inner box is freed, xs stays valid), no double-free at detector zero.
	{"nested-tuple-ident-elem-safe", `function main(): i32 {
    var xs: i32[] = [7, 8];
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 100) { var t: ((i32, i32[]), i32) = ((w, xs), w); acc = (acc + t.0.1[0]) % 251; w = w + 1; }
    var ok: i32 = xs[0] + xs[1];
    if (ok != 15) { return 97; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// Deeper nesting (three tuple levels) — the recursion frees every level.
	{"nested-tuple-deep", `function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 200) { var t: (((i32, i32[]), i32), i32) = (((w, [w, w + 1]), w), w); acc = (acc + t.1) % 251; w = w + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < 5000) { var t2: (((i32, i32[]), i32), i32) = (((i, [i, i + 1]), i), i); acc = (acc + t2.1) % 251; i = i + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// Mixed: a fresh array in the OUTER tuple alongside a nested-tuple element —
	// both the outer array and the nested inner array are freed.
	{"nested-tuple-mixed", `function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 200) { var t: ((i32, i32[]), i32[]) = ((w, [w, w + 1]), [w, w]); acc = (acc + t.1[0]) % 251; w = w + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < 5000) { var t2: ((i32, i32[]), i32[]) = ((i, [i, i + 1]), [i, i]); acc = (acc + t2.1[0]) % 251; i = i + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// DISCARDED nested tuple statement (`((w, [w, w+1]), w);`) — the discarded-
	// statement arm takes the same recursive deep-drop. (Regression guard for the
	// expr_scalar_leaf fix — a shallow scalar-tuple discard leaks the inner box.)
	{"nested-tuple-discarded", `function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 200) { ((w, [w, w + 1]), w); w = w + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < 5000) { ((i, [i, i + 1]), i); i = i + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
}

// TestSelfHostNestedTupleReclaimIRX86_64 drives the cases through the self-hosted
// x86-64 compiler (asm_run), heap-bump + underflow guarded.
func TestSelfHostNestedTupleReclaimIRX86_64(t *testing.T) {
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

	for _, tc := range nestedTupleReclaimCases {
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
				t.Errorf("%s = %d, want %d (98 = nested tuple leaked; 99 = over-release/underflow; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
