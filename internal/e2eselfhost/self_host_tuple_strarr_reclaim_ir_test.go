package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// tupleStrArrReclaimCases pin the #4353 `string[]` ELEMENT POSITION of an rc-tuple.
// A `(i32, string[])` tuple was admitted for deep reclaim, but both walkers treated
// the position as a plain buffer: the literal-driven emit_tuple_child_drops
// __fern_rc_dec'd it (buffer only, every element string stranded) and the
// type-driven tuple_field_deep_droppable refused the type outright, so an
// `Option[(i32, string[])]` payload leaked the whole structure. Measured per round
// on the self-host IR path before the fix: 64 B (x86-64 / arm64) and 46 B (wasm)
// for the bare tuple, 120+ B / 110 B for the Option payload; native is flat on both.
//
// The position now takes the element-walking __fern_str_arr_free, gated by
// tuple_strarr_elem_fresh: an array LITERAL whose every element is a string literal
// or a fresh producer (concat / .to_string(), tuple_str_elem_fresh). One bare-ident
// element keeps the shallow buffer-only dec, so a live local's box is never freed.
var tupleStrArrReclaimCases = []struct {
	name string
	src  string
	want int
}{
	// Core churn: a loop-local (i32, string[]) rebuilt each iteration with fresh
	// concat elements. Pre-fix the two element boxes leaked per round → 98.
	{"tuple-strarr-churn", `function main(): i32 {
    var pre: string = "ab";
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { var t: (i32, string[]) = (i, [pre + "x", pre + "yy"]); acc = (acc + t.0 + t.1[0].len()) % 251; i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 5000) { var t: (i32, string[]) = (j, [pre + "x", pre + "yy"]); acc = (acc + t.0 + t.1[0].len()) % 251; j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// The TYPE-driven walker: an Option payload has no construction literal at the
	// drop site, so the release is read off the `(i32, string[])` type tag. Pre-fix
	// tuple_field_deep_droppable rejected the type and the payload leaked whole.
	{"tuple-strarr-opt-payload", `function main(): i32 {
    var pre: string = "ab";
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var o: Option[(i32, string[])] = Some((i, [pre + "x", pre + "yy"]));
        match (o) { Some(t) => { acc = (acc + t.0) % 251; }, None => {} }
        i = i + 1;
    }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 5000) {
        var o: Option[(i32, string[])] = Some((j, [pre + "x", pre + "yy"]));
        match (o) { Some(t) => { acc = (acc + t.0) % 251; }, None => {} }
        j = j + 1;
    }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// VALUE: the reclaimed tuple still reads correctly through both element
	// positions and the array length. 7 + 3 + 4 + 2 = 16.
	{"tuple-strarr-value", `function main(): i32 {
    var pre: string = "ab";
    var t: (i32, string[]) = (7, [pre + "x", pre + "yy"]);
    var v: i32 = t.0 + t.1[0].len() + t.1[1].len() + t.1.len();
    if (__rc_underflow() != 0) { return 99; }
    return v;
}`, 16},
	// ALIASED-ELEMENT negative: a bare string ident at an element position aliases
	// a live local, so the position must stay on the shallow buffer dec. The
	// element box is read after the tuple's reclaim point; a deep free here would
	// tick the over-release detector (99) or corrupt the read (97).
	{"tuple-strarr-alias-safe", `function main(): i32 {
    var pre: string = "ab";
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 50) {
        var s1: string = pre + "wide";
        var t: (i32, string[]) = (i, [s1, s1]);
        acc = (acc + t.0 + s1.len()) % 251;
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// PAYLOAD-ESCAPE negative: `keep = t.1` extracts the string array out of the
	// tuple, so the local is not credited and the extracted boxes stay live.
	// keep[0] = "abx" (3), keep[1] = "abyy" (4).
	{"tuple-strarr-escape-safe", `function main(): i32 {
    var pre: string = "ab";
    var keep: string[] = ["z"];
    var i: i32 = 0;
    while (i < 50) {
        var t: (i32, string[]) = (i, [pre + "x", pre + "yy"]);
        keep = t.1;
        i = i + 1;
    }
    var v: i32 = keep[0].len() + keep[1].len();
    if (__rc_underflow() != 0) { return 99; }
    if (v != 7) { return 97; }
    return 0;
}`, 0},
}

// TestSelfHostTupleStrArrReclaimIRX86_64 drives the cases through the self-hosted
// x86-64 compiler, heap-bump + underflow guarded.
func TestSelfHostTupleStrArrReclaimIRX86_64(t *testing.T) {
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

	for _, tc := range tupleStrArrReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
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
				t.Errorf("%s = %d, want %d (98 = string[] element positions leaked; 99 = over-release/underflow; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
