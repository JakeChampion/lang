package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// trmcConsumeCases pin #5333: the consume-safety half of TRMC. The rewrite itself
// landed in #4578 — a TRMC function walks its scrutinee with a hole-passing loop
// instead of recursion — but the loop only READ the cells it walked past, so the
// whole input list stayed live until the caller dropped it. Peak was input +
// output where it should be output alone.
//
// A consume-safe function now releases each cell as the loop leaves it, and the
// freed cell recycles through the freelist into the node the next iteration
// allocates. `inc_all` over `build(2000)`, self-host x86-64, `__heap_bump_bytes()`
// delta across the call:
//
//	without the verdict  187 KiB
//	with it               93 KiB
//
// A halving, which is the FBIP result: peak becomes the output. At 50 cells the
// leak census goes allocs=102 frees=0 live=4864 -> allocs=102 frees=50 live=2464.
//
// # Both halves or neither
//
// The loop's release is sound only because the CALL SITE retains an argument the
// caller still holds, so the loop's uniqueness check sees rc >= 2 on the first
// cell, decs once and stops — the rest of the list belongs to the caller. Ported
// without that retain the check is decoration: `is_unique` reports every cell
// unique and the traversal frees a list its caller is still using. That is
// `trmc-consume-shared-input-safe` below, which exits 95 on the callee-only
// build while every other case here still passes and the census still looks like
// a win. It is the only case that catches it, because the corruption is only
// visible by reading the ORIGINAL back after the call — values through the
// RESULT are all correct.
//
// So both halves read one registry (`FnSigs.trmc_consume_fns`) rather than
// re-deriving the verdict: a path holding the verdict must never free what a
// path lacking it never retained.
//
// The scan refuses anything whose cell needs more than a shallow free — the loop
// steals the tail out of the cell it is releasing, so a string or container
// payload would be stranded. `trmc-consume-string-head-refused` pins that
// exclusion at byte-identical numbers before and after.
var trmcConsumeCases = []struct {
	name string
	src  string
	want int
}{
	// PEAK: the consuming walk must recycle its input into its output. 93 KiB
	// with the verdict, 187 KiB without, so the 140 KiB gate sits between them.
	{"trmc-consume-halves-peak", `enum List { Cons(i32, List), Nil }
function inc_all(xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, inc_all(t)); },
        Nil => { return Nil; },
    }
}
function build(n: i32): List { var acc: List = Nil; var i: i32 = 0; while (i < n) { acc = Cons(1, acc); i = i + 1; } return acc; }
function sum(l: List): i32 { var acc: i32 = 0; var cur: List = l; var go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 {
    var warm: List = inc_all(build(10));
    if (sum(warm) != 20) { return 97; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var ys: List = inc_all(build(2000));
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (sum(ys) != 4000) { return 96; }
    if (__rc_underflow() != 0) { return 99; }
    if ((b2 - b1) / 1024 >= 140) { return 98; }
    return 0;
}`, 0},
	// VALUE: sum(1..50) = 1275 read back off the result, so a cell freed too
	// early is a wrong answer rather than only a byte count.
	{"trmc-consume-value-exact", `enum List { Cons(i32, List), Nil }
function inc_all(xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, inc_all(t)); },
        Nil => { return Nil; },
    }
}
function build(n: i32): List { var acc: List = Nil; var i: i32 = 0; while (i < n) { acc = Cons(i, acc); i = i + 1; } return acc; }
function sum(l: List): i32 { var acc: i32 = 0; var cur: List = l; var go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 {
    var ys: List = inc_all(build(50));
    if (sum(ys) != 1275) { return 96; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// THE ONE THAT CATCHES A MISSING RETAIN. `keep` is still live across the
	// call, so the walk must stop at the first cell and leave the list alone.
	// Reading `keep` back is the only thing that sees it — `ys` is correct
	// either way. Exits 95 without the call-site retain.
	{"trmc-consume-shared-input-safe", `enum List { Cons(i32, List), Nil }
function inc_all(xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, inc_all(t)); },
        Nil => { return Nil; },
    }
}
function build(n: i32): List { var acc: List = Nil; var i: i32 = 0; while (i < n) { acc = Cons(1, acc); i = i + 1; } return acc; }
function sum(l: List): i32 { var acc: i32 = 0; var cur: List = l; var go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 {
    var keep: List = build(30);
    var ys: List = inc_all(keep);
    if (sum(ys) != 60) { return 96; }
    if (sum(keep) != 30) { return 95; }
    if (sum(ys) != 60) { return 94; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// The same shape TWICE off one retained list: the second call must still see
	// an intact input, so the retain cannot be a one-shot.
	{"trmc-consume-shared-twice-safe", `enum List { Cons(i32, List), Nil }
function inc_all(xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, inc_all(t)); },
        Nil => { return Nil; },
    }
}
function build(n: i32): List { var acc: List = Nil; var i: i32 = 0; while (i < n) { acc = Cons(1, acc); i = i + 1; } return acc; }
function sum(l: List): i32 { var acc: i32 = 0; var cur: List = l; var go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 {
    var keep: List = build(20);
    var a: List = inc_all(keep);
    var b: List = inc_all(keep);
    if (sum(a) != 40) { return 96; }
    if (sum(b) != 40) { return 95; }
    if (sum(keep) != 20) { return 94; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// REFUSED: a string head cannot survive a shallow cell free, so the scan
	// declines and this function keeps the borrow model. Numbers here are
	// byte-identical before and after the change (14/2/368).
	{"trmc-consume-string-head-refused", `enum SList { SCons(string, SList), SNil }
function tag_all(xs: SList): SList {
    match (xs) {
        SCons(h, t) => { return SCons(h + "!", tag_all(t)); },
        SNil => { return SNil; },
    }
}
function len_all(l: SList): i32 { var acc: i32 = 0; var cur: SList = l; var go: boolean = true; while (go) { match (cur) { SCons(h, t) => { acc = acc + h.len(); cur = t; }, SNil => { go = false; } } } return acc; }
function main(): i32 {
    var xs: SList = SCons("ab", SCons("cde", SNil));
    var ys: SList = tag_all(xs);
    if (len_all(ys) != 7) { return 96; }
    if (len_all(xs) != 5) { return 95; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// REFUSED: the retain lives at the DIRECT call site, so a function reached
	// through a function VALUE would consume with nobody retaining — the same
	// use-after-free by a route the call site cannot see. Taking the address
	// anywhere costs the verdict, so this reads `frees=0` and `keep` survives.
	{"trmc-consume-fn-value-refused", `enum List { Cons(i32, List), Nil }
function inc_all(xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, inc_all(t)); },
        Nil => { return Nil; },
    }
}
function apply(f: (List) => List, l: List): List { return f(l); }
function build(n: i32): List { var acc: List = Nil; var i: i32 = 0; while (i < n) { acc = Cons(1, acc); i = i + 1; } return acc; }
function sum(l: List): i32 { var acc: i32 = 0; var cur: List = l; var go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 {
    var keep: List = build(20);
    var ys: List = apply(inc_all, keep);
    if (sum(ys) != 40) { return 96; }
    if (sum(keep) != 20) { return 95; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// The O(1)-stack case from the TRMC suite, now also consuming: 300k cells
	// must still complete with the right sum and no over-release.
	{"trmc-consume-deep-stack", `enum List { Cons(i32, List), Nil }
function inc_all(xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, inc_all(t)); },
        Nil => { return Nil; },
    }
}
function build(n: i32): List { var acc: List = Nil; var i: i32 = 0; while (i < n) { acc = Cons(1, acc); i = i + 1; } return acc; }
function sum(l: List): i32 { var acc: i32 = 0; var cur: List = l; var go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 {
    var ys: List = inc_all(build(300000));
    if (sum(ys) != 600000) { return 96; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
}

// TestSelfHostTrmcConsumeIRX86_64 drives the cases through the self-hosted x86-64
// compiler (asm_run), peak + value + over-release guarded.
func TestSelfHostTrmcConsumeIRX86_64(t *testing.T) {
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

	for _, tc := range trmcConsumeCases {
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
				t.Errorf("%s = %d, want %d (98 = input not recycled; 99 = over-release; 94-97 = value corrupted, 95 on the shared case = the call-site retain is missing)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostTrmcConsumeIRArm64 is the arm64 leg — the rewrite and the release
// are backend-independent, so both register backends carry the same guarantee.
func TestSelfHostTrmcConsumeIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range trmcConsumeCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = input not recycled; 99 = over-release; 94-97 = value corrupted, 95 on the shared case = the call-site retain is missing)", tc.name, code, tc.want)
			}
		})
	}
}
