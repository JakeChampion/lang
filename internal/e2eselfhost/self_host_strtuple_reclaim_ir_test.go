package e2eselfhost

import (
	"os/exec"
	"testing"
)

// strTupleReclaimCases pin the #4353 item-1 string-element tuple reclaim: a
// tuple literal carrying a FRESH-string element (`(i, "x" + s)` concat /
// `(i, i.to_string())`) leaked the string box AND the tuple box per loop
// iteration / per discard on the self-host IR path (native bounds it) — the
// TUPRC: admission rejected any string element outright. The admission
// (tuple_lit_rc_reclaimable) now admits string LITERAL elements (immortal box,
// nothing to free) and fresh-string producers (tuple_str_elem_fresh: concat
// with a string-literal operand / 0-arg `.to_string()`), and the deep-drop
// (emit_tuple_child_drops) routes producer elements through the rc-aware
// __fern_str_free. A bare string IDENT element aliases a live local and is
// still excluded (leak-safe); an extracted element (`keep = t.1`) rejects the
// credit via the annotated escape gate (string is pointer-shaped there).
var strTupleReclaimCases = []struct {
	name string
	src  string
	want int
}{
	// Core churn: concat-element tuple rebuilt per iteration, len read.
	// Literal⊕literal concat isolates the TUPLE-side reclaim: a fresh-call
	// concat operand (`"n" + i.to_string()`) leaks its operand TEMP in the
	// concat lowering itself — a pre-existing gap independent of tuples.
	{"str-tuple-concat-churn", `function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 200) { var t: (i32, string) = (w, "n" + "x"); acc = (acc + t.1.len()) % 251; w = w + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < 5000) { var t2: (i32, string) = (i, "n" + "x"); acc = (acc + t2.1.len()) % 251; i = i + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// Bare `.to_string()` element (the ExprCall producer arm).
	{"str-tuple-tostring-churn", `import "std/i32";
function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 200) { var t: (i32, string) = (w, w.to_string()); acc = (acc + t.1.len()) % 251; w = w + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < 5000) { var t2: (i32, string) = (i, i.to_string()); acc = (acc + t2.1.len()) % 251; i = i + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// A string LITERAL element next to a fresh array: the array routes the deep
	// drop and the immortal literal is skipped. Rejecting the whole tuple on the
	// string element leaks all three allocations.
	{"str-tuple-lit-mixed", `function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 200) { var t: (string, i32[]) = ("tag", [w, w + 1]); acc = (acc + t.1[0]) % 251; w = w + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < 5000) { var t2: (string, i32[]) = ("tag", [i, i + 1]); acc = (acc + t2.1[0]) % 251; i = i + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// All-literal tuple `(i, "abc")`: construction str_box's the literal into
	// a fresh copy, so the tuple routes the DEEP path (element copy freed,
	// then the box). Rejecting the tuple on its string element leaks every
	// level per iteration.
	{"str-tuple-lit-shallow", `function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 200) { var t: (i32, string) = (w, "abc"); acc = (acc + t.0 + t.1.len()) % 251; w = w + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < 5000) { var t2: (i32, string) = (i, "abc"); acc = (acc + t2.0 + t2.1.len()) % 251; i = i + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// IDENT-element negative: `(w, s)` aliases a live local — the tuple is
	// excluded from both classes (leak-safe), s stays valid, detector zero.
	{"str-tuple-ident-elem-safe", `function main(): i32 {
    var s: string = "seven";
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 100) { var t: (i32, string) = (w, s); acc = (acc + t.1.len()) % 251; w = w + 1; }
    if (s.len() != 5) { return 97; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	// ESCAPE negative: the tuple is returned — ownership moves out, nothing
	// freed, values exact (no dangle in the caller's reads).
	{"str-tuple-escape-safe", `import "std/i32";
function mk(i: i32): (i32, string) {
    var t: (i32, string) = (i, "v" + i.to_string());
    return t;
}
function main(): i32 {
    var t = mk(5);
    if (t.0 != 5) { return 97; }
    if (t.1.len() != 2) { return 96; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	// EXTRACTION negative: `keep = t.1` pulls the owned string out — the
	// annotated escape gate rejects the credit (string is pointer-shaped), so
	// the extracted alias stays valid after the rebind (leak-safe, no UAF).
	{"str-tuple-extract-escape-safe", `import "std/i32";
function main(): i32 {
    var acc: i32 = 0;
    var keep: string = "";
    var w: i32 = 0;
    while (w < 100) {
        var t: (i32, string) = (w, "k" + w.to_string());
        keep = t.1;
        acc = (acc + keep.len()) % 251;
        w = w + 1;
    }
    if (keep.len() < 2) { return 97; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	// DISCARDED statement `(w, "x" + "y");` — the discarded-statement arm
	// takes the same deep drop (fresh element copy freed, then the box).
	// Literal⊕literal concat for the same reason as the churn case.
	{"str-tuple-discarded", `function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 200) { (w, "x" + "y"); w = w + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < 5000) { (i, "x" + "y"); i = i + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
}

// TestSelfHostStrTupleReclaimIRX86_64 drives the cases through the self-hosted
// x86-64 compiler (asm_load_run), heap-bump + underflow guarded.
func TestSelfHostStrTupleReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	l := newStdlibLoader(t)
	dir := t.TempDir()

	for _, tc := range strTupleReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := []byte(l.emit(t, tc.src+"\n"))
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
				t.Errorf("%s = %d, want %d (98 = string-tuple leaked; 99 = over-release/underflow; 97/96 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
