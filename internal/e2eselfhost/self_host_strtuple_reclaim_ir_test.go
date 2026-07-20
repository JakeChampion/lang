package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
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
	{"str-tuple-concat-churn", `function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 200) { var t: (i32, string) = (w, "n" + w.to_string()); acc = (acc + t.1.len()) % 251; w = w + 1; }
    var b1: i32 = __heap_bump_bytes();
    var i: i32 = 0;
    while (i < 5000) { var t2: (i32, string) = (i, "n" + i.to_string()); acc = (acc + t2.1.len()) % 251; i = i + 1; }
    var b2: i32 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// Bare `.to_string()` element (the ExprCall producer arm).
	{"str-tuple-tostring-churn", `function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 200) { var t: (i32, string) = (w, w.to_string()); acc = (acc + t.1.len()) % 251; w = w + 1; }
    var b1: i32 = __heap_bump_bytes();
    var i: i32 = 0;
    while (i < 5000) { var t2: (i32, string) = (i, i.to_string()); acc = (acc + t2.1.len()) % 251; i = i + 1; }
    var b2: i32 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// A string LITERAL element next to a fresh array: previously the string
	// element rejected the whole tuple (all three allocations leaked); now the
	// array routes the deep drop and the immortal literal is skipped.
	{"str-tuple-lit-mixed", `function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 200) { var t: (string, i32[]) = ("tag", [w, w + 1]); acc = (acc + t.1[0]) % 251; w = w + 1; }
    var b1: i32 = __heap_bump_bytes();
    var i: i32 = 0;
    while (i < 5000) { var t2: (string, i32[]) = ("tag", [i, i + 1]); acc = (acc + t2.1[0]) % 251; i = i + 1; }
    var b2: i32 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// All-literal tuple `(i, "abc")`: takes the SHALLOW scalar-tuple path (box
	// freed, immortal element untouched) — previously rejected there too, so
	// even the box leaked per iteration.
	{"str-tuple-lit-shallow", `function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 200) { var t: (i32, string) = (w, "abc"); acc = (acc + t.0 + t.1.len()) % 251; w = w + 1; }
    var b1: i32 = __heap_bump_bytes();
    var i: i32 = 0;
    while (i < 5000) { var t2: (i32, string) = (i, "abc"); acc = (acc + t2.0 + t2.1.len()) % 251; i = i + 1; }
    var b2: i32 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
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
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// ESCAPE negative: the tuple is returned — ownership moves out, nothing
	// freed, values exact (no dangle in the caller's reads).
	{"str-tuple-escape-safe", `function mk(i: i32): (i32, string) {
    var t: (i32, string) = (i, "v" + i.to_string());
    return t;
}
function main(): i32 {
    var t = mk(5);
    if (t.0 != 5) { return 97; }
    if (t.1.len() != 2) { return 96; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// EXTRACTION negative: `keep = t.1` pulls the owned string out — the
	// annotated escape gate rejects the credit (string is pointer-shaped), so
	// the extracted alias stays valid after the rebind (leak-safe, no UAF).
	{"str-tuple-extract-escape-safe", `function main(): i32 {
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
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// DISCARDED statement `(w, "x" + w.to_string());` — the discarded-statement
	// arm takes the same deep drop (fresh string freed, then the box).
	{"str-tuple-discarded", `function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 200) { (w, "x" + w.to_string()); w = w + 1; }
    var b1: i32 = __heap_bump_bytes();
    var i: i32 = 0;
    while (i < 5000) { (i, "x" + i.to_string()); i = i + 1; }
    var b2: i32 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
}

// TestSelfHostStrTupleReclaimIRX86_64 drives the cases through the self-hosted
// x86-64 compiler (asm_run), heap-bump + underflow guarded.
func TestSelfHostStrTupleReclaimIRX86_64(t *testing.T) {
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

	for _, tc := range strTupleReclaimCases {
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
				t.Errorf("%s = %d, want %d (98 = string-tuple leaked; 99 = over-release/underflow; 97/96 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
