package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSelfHostStructArrReclaimIRX86_64 pins the #4355 struct-array element-box
// reclaim: a fresh, non-escaping `let g = [P { .. }, P { .. }]` had NO element
// reclaim on the self-host IR path — the slot is a plain is_arr array whose exit
// sweep did a SHALLOW __fern_rc_dec of the OUTER buffer only, leaking every
// element struct box per iteration (the outer buffer was freed; the P{} boxes
// were not). The reclaim routes such a slot through the EXISTING
// __fern_arrarr_free helper (one rc-guarded arr_dec per element pointer — which
// is exactly a struct-box free — then the outer buffer), so no new backend
// runtime is needed. Admission: every element must be a fresh no-base struct
// LITERAL ("STRUCTARR:"); a bare element bind (`let q = g[0]`) or a `for p in g`
// whose body lets `p` escape rejects the candidate (the element pointer would
// dangle). These fixtures use scalar-field structs. Every run must also leave a
// balanced census.
func TestSelfHostStructArrReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	t.Setenv("FERN_LEAKCHECK", "1")
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../compiler/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	run := func(t *testing.T, prog, name string, want int) {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", name)
		}
		bin := buildBin(t, gcc, dir, name, string(asm))
		stderr, code := hevRun(t, runner, bin)
		if code != want {
			t.Errorf("%s exited %d, want %d (98 = element boxes leaked; 99 = over-release; 88 = live value freed; 97 = value corrupted)", name, code, want)
		}
		allocs, frees, live := parseLeakcheck(t, name, stderr)
		if live != 0 || allocs != frees {
			t.Errorf("%s: allocs=%d frees=%d live_bytes=%d, want a balanced census", name, allocs, frees, live)
		}
	}

	// scalar-field struct[] churn (unannotated) — the slice target: fresh
	// element boxes freed per rebind, flat at detector zero.
	run(t, `struct P { x: i32, y: i32 }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let g = [P { x: i, y: i + 1 }, P { x: i + 2, y: i + 3 }];
        acc = acc + g.len() + g[0].x;
        i = i + 1;
    }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) {
        let g2 = [P { x: j, y: j + 1 }, P { x: j + 2, y: j + 3 }];
        acc = acc + g2.len() + g2[1].y;
        j = j + 1;
    }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, "structarr-scalar-flat", 0)

	// ANNOTATED struct[] churn — the `let g: P[] = [..]` spelling must reclaim
	// the same way as the inferred one.
	run(t, `struct P { x: i32, y: i32 }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let g: P[] = [P { x: i, y: i + 1 }, P { x: i + 2, y: i + 3 }];
        acc = acc + g.len() + g[0].y;
        i = i + 1;
    }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) {
        let g2: P[] = [P { x: j, y: j + 1 }, P { x: j + 2, y: j + 3 }];
        acc = acc + g2.len() + g2[0].x;
        j = j + 1;
    }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, "structarr-annotated-flat", 0)

	// TRANSIENT ITERATION admitted: `for p in g { acc += p.x }` borrows each
	// element only within the loop (dead before the rebind free), so the
	// candidate is still credited and stays flat.
	run(t, `struct P { x: i32, y: i32 }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let g = [P { x: i, y: i + 1 }, P { x: i + 2, y: i + 3 }];
        for p in g { acc = acc + p.x + p.y; }
        i = i + 1;
    }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) {
        let g2 = [P { x: j, y: j + 1 }, P { x: j + 2, y: j + 3 }];
        for p in g2 { acc = acc + p.x; }
        j = j + 1;
    }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, "structarr-iter-flat", 0)

	// ELEMENT ALIAS: `let q = g[1]` binds an element struct box, which must stay
	// a valid, correctly-valued box (never freed under it).
	run(t, `struct P { x: i32, y: i32 }
function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 500) {
        let g = [P { x: i, y: i + 1 }, P { x: i + 2, y: i + 3 }];
        let q = g[1];
        if (q.x != i + 2) { bad = 1; }
        if (q.y != i + 3) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "structarr-elem-alias-safe", 0)
}
