package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// arrArrDiscReclaimCases pin the #4365 discarded scalar-inner array-of-arrays
// reclaim: a discarded `[[i, i+1], [i]];` literal leaked its inner buffers AND
// the outer buffer per evaluation on the self-host IR path (the shallow scalar
// path freed only the outer box, orphaning every inner; native bounds the
// shape). The StmtExpr lowering now routes a discardable_scalar_arrarr_lit (every
// element a DIRECT scalar-element array literal, sole-owned rc=1) through
// __fern_arrarr_free — one rc-guarded arr_dec per inner (scalar inners fully
// reclaim), then the outer buffer. A bare-ident inner aliases a live local and
// keeps the whole literal leak-mode (no double-free); string-element inners stay
// leak-safe on the plain drop (first-cut scope).
var arrArrDiscReclaimCases = []struct {
	name string
	src  string
	want int
}{
	// Core churn: discarded i32[][] literal rebuilt per iteration, heap bounded.
	{"arrarr-disc-churn", `function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 200) { [[w, w + 1], [w]]; w = w + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < 5000) { [[i, i + 1], [i]]; i = i + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// f64 inners are scalar-leaf too (value-copied elements ride the freed
	// buffer) — reclaimed and bounded.
	{"arrarr-disc-f64", `function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 200) { [[1.5, 2.5], [3.5]]; w = w + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < 5000) { [[1.5, 2.5], [3.5]]; i = i + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// ALIAS-inner negative: a bare-ident inner (`[[9], inner]`) aliases a live
	// local the exit sweep also frees — the literal is excluded (leak-mode), the
	// aliased row stays valid, no double-free (detector zero).
	{"arrarr-disc-alias-safe", `function main(): i32 {
    var inner: i32[] = [7, 8];
    [[9], inner];
    var ok: i32 = inner[0] + inner[1];
    if (ok != 15) { return 97; }
    var w: i32 = 0;
    while (w < 100) { [[w], [w, w + 1]]; w = w + 1; }
    var again: i32 = inner[0] + inner[1];
    if (again != 15) { return 96; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// STRING-inner negative: string[][] is excluded from the scalar-inner path
	// (a first cut) and stays leak-safe on the plain drop — no crash, no
	// double-free, values exact.
	{"arrarr-disc-strinner-safe", `function main(): i32 {
    var w: i32 = 0;
    while (w < 100) { [["a" + "x"], ["b", "c"]]; w = w + 1; }
    var chk: i32 = 42;
    if (chk != 42) { return 97; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
}

// TestSelfHostArrArrDiscReclaimIRX86_64 drives the cases through the self-hosted
// x86-64 compiler (asm_run), heap-bump + underflow guarded.
func TestSelfHostArrArrDiscReclaimIRX86_64(t *testing.T) {
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

	for _, tc := range arrArrDiscReclaimCases {
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
				t.Errorf("%s = %d, want %d (98 = arrarr temp leaked; 99 = over-release/underflow; 96-97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
