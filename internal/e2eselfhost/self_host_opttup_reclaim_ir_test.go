package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// optTupReclaimCases pin the #4365 Option[<tuple-with-array>] reclaim: a
// `var o: Option[(i32, i32[])] = Some((i, [i, i+1]))` consumed by a borrow-only
// match leaked its payload array buffer + tuple box + option box per iteration on
// the self-host IR path (native bounds it). The new "OPTTUP:" reclaim class
// (annotation-driven, mirroring OPTAARR since Option is not a struct-decl enum)
// admits a fresh Some((...)) consumed by exactly one borrow-only match, and inline-
// frees the box: tag-check (Some) -> type-driven tuple deep-drop
// (emit_tuple_type_child_drops: dec each array position, recurse nested tuples) ->
// tuple box dec -> option box dec, at the loop-rebind and exit sweep.
//
// SOUNDNESS: the Some-arm's payload use is checked by opttup_payload_escapes — a
// scalar field read (p.0), an indexed array-field read (p.1[i]) and p.1.len() are
// borrows (reclaim proceeds); a BARE array-field extraction (store/return/pass/
// alias/slice p.1) escapes and the local is left leak-safe (never over-released —
// the store-p.1 case previously double-freed).
var optTupReclaimCases = []struct {
	name string
	src  string
	want int
}{
	// Core churn: rebuilt per iteration, scalar read only.
	{"opttup-churn", `function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { var o: Option[(i32, i32[])] = Some((i, [i, i + 1])); match (o) { Some(p) => { acc = (acc + p.0) % 251; }, None => {} } i = i + 1; }
    var b1: i32 = __heap_bump_bytes();
    var j: i32 = 0;
    while (j < 5000) { var o2: Option[(i32, i32[])] = Some((j, [j, j + 1])); match (o2) { Some(p) => { acc = (acc + p.0) % 251; }, None => {} } j = j + 1; }
    var b2: i32 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// Full borrow set: scalar field (p.0), indexed array-field (p.1[i]) and
	// p.1.len() are all admitted — still reclaims (bounded).
	{"opttup-borrow-full", `function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { var o: Option[(i32, i32[])] = Some((i, [i, i + 1])); match (o) { Some(p) => { acc = (acc + p.0 + p.1[0] + p.1[1] + p.1.len()) % 251; }, None => {} } i = i + 1; }
    var b1: i32 = __heap_bump_bytes();
    var j: i32 = 0;
    while (j < 5000) { var o2: Option[(i32, i32[])] = Some((j, [j, j + 1])); match (o2) { Some(p) => { acc = (acc + p.1[1]) % 251; }, None => {} } j = j + 1; }
    var b2: i32 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// PAYLOAD-ESCAPE-STORE negative: `Some(p) => keep = p.1` extracts the array
	// out of the arm — the local is NOT credited (leak-safe), and MUST NOT be
	// over-released (this exact shape double-freed before opttup_payload_escapes).
	// Detector zero, value exact.
	{"opttup-escape-store-safe", `function main(): i32 {
    var keep: i32[] = [0, 0];
    var i: i32 = 0;
    while (i < 50) {
        var o: Option[(i32, i32[])] = Some((i, [i, i + 1]));
        match (o) { Some(p) => { keep = p.1; }, None => {} }
        i = i + 1;
    }
    var acc: i32 = keep[0] + keep[1];
    if (acc < 0) { return 97; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// PAYLOAD-ESCAPE-CALL negative: `take(p.1)` passes the array field to a call
	// (a retain) — un-credited, leak-safe, detector zero.
	{"opttup-escape-call-safe", `function take(xs: i32[]): i32 { return xs[0]; }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 50) {
        var o: Option[(i32, i32[])] = Some((i, [i, i + 1]));
        match (o) { Some(p) => { acc = (acc + take(p.1)) % 251; }, None => {} }
        i = i + 1;
    }
    if (acc < 0) { return 97; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// ESCAPE-VIA-FN negative: the option is passed to a function whose match
	// returns the payload array — ownership moves out, nothing freed, value exact.
	{"opttup-escape-fn-safe", `function pick(o: Option[(i32, i32[])]): i32[] {
    match (o) { Some(p) => { return p.1; }, None => {} }
    return [0];
}
function main(): i32 {
    var o: Option[(i32, i32[])] = Some((5, [6, 7]));
    var a = pick(o);
    if (__rc_underflow() != 0) { return 99; }
    return a[0] + a[1];
}`, 13},
	// STRING-element payload (#4353 item 2, probe p4): `Option[(i32, string)]`
	// with a fresh producer element — construction str_box's the element into
	// a fresh copy, the type-driven drop routes it through the rc-aware
	// __fern_str_free. Previously the string tag rejected admission and all
	// three levels leaked per iteration.
	{"opttup-string-elem-churn", `function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { var o: Option[(i32, string)] = Some((i, "v" + i.to_string())); match (o) { Some(p) => { acc = (acc + p.0 + p.1.len()) % 251; }, None => {} } i = i + 1; }
    var b1: i32 = __heap_bump_bytes();
    var j: i32 = 0;
    while (j < 5000) { var o2: Option[(i32, string)] = Some((j, "v" + j.to_string())); match (o2) { Some(p) => { acc = (acc + p.1.len()) % 251; }, None => {} } j = j + 1; }
    var b2: i32 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// Mixed string + array elements — both freed by the type-driven walk.
	{"opttup-string-arr-mixed-churn", `function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { var o: Option[(string, i32[])] = Some(("tag", [i, i + 1])); match (o) { Some(p) => { acc = (acc + p.0.len() + p.1[0]) % 251; }, None => {} } i = i + 1; }
    var b1: i32 = __heap_bump_bytes();
    var j: i32 = 0;
    while (j < 5000) { var o2: Option[(string, i32[])] = Some(("tag", [j, j + 1])); match (o2) { Some(p) => { acc = (acc + p.1[1]) % 251; }, None => {} } j = j + 1; }
    var b2: i32 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// IDENT-string-element negative: `Some((i, s))` aliases a live local at a
	// string position — admission (tuple_arg_payload_fresh) rejects, nothing
	// freed, s stays valid, detector zero.
	{"opttup-string-ident-elem-safe", `function main(): i32 {
    var s: string = "seven";
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { var o: Option[(i32, string)] = Some((i, s)); match (o) { Some(p) => { acc = (acc + p.1.len()) % 251; }, None => {} } i = i + 1; }
    if (s.len() != 5) { return 97; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// STRING-EXTRACTION negative: `Some(p) => keep = p.1` pulls the owned
	// string element out of the arm — the escape walker treats a bare
	// non-scalar p.N as an escape, the local is NOT credited (leak-safe),
	// keep stays valid.
	{"opttup-string-extract-safe", `function main(): i32 {
    var keep: string = "";
    var i: i32 = 0;
    while (i < 100) {
        var o: Option[(i32, string)] = Some((i, "k" + i.to_string()));
        match (o) { Some(p) => { keep = p.1; }, None => {} }
        i = i + 1;
    }
    if (keep.len() < 2) { return 97; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
}

// TestSelfHostOptTupReclaimIRX86_64 drives the cases through the self-hosted
// x86-64 compiler (asm_run), heap-bump + underflow guarded.
func TestSelfHostOptTupReclaimIRX86_64(t *testing.T) {
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

	for _, tc := range optTupReclaimCases {
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
				t.Errorf("%s = %d, want %d (98 = option leaked; 99 = over-release/underflow; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
