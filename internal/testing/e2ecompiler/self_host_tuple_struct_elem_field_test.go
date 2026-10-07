package e2ecompiler

import (
	"strings"
	"testing"
)

// A struct whose TUPLE field carries a RECLAIM-STRUCT element — `Holder { t:
// (i32, P), … }` where `P` has an rc-array field. Dropping the holder must
// release the tuple's struct element and its array, balancing at live_bytes 0.
// The leak matrix has no cell of this shape, so this test is the only thing
// that measures it.

func TestSelfHostTupleStructElemFieldX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	const src = `struct P { xs: i32[], k: i32 }
struct Holder { t: (i32, P), n: i32 }
function round(i: i32): i32 {
    let p: P = P { xs: [i, i + 1], k: i };
    let t: (i32, P) = (i, p);
    let h: Holder = Holder { t: t, n: i };
    return h.n + h.t.0;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { acc = acc + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`

	asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
	progBin := buildBin(t, gcc, dir, "tuplestructelem", asm)
	stderr, exit := hevRun(t, runner, progBin)
	if exit != 23 {
		t.Fatalf("exited %d, want 23 (99 = rc underflow; 139 = read freed memory)", exit)
	}
	summary := leakSummaryLine(stderr)
	if summary == "" {
		t.Fatal("no leakcheck summary")
	}
	var allocs, frees, live int64
	if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
		t.Fatalf("parse %q: %v", summary, err)
	}
	if allocs == 0 {
		t.Fatal("allocated nothing — the probe is not exercising the path")
	}
	if live != 0 || allocs != frees {
		t.Errorf("%s — must balance at live_bytes 0; a struct-element tuple field "+
			"leaks its whole chain when struct_routes_field_reclaim_at stops "+
			"admitting it", summary)
	}

	sanAsm := hevCompile(t, runner, driverBin, src, []string{"FERN_SANITIZE=1"})
	sanBin := buildBin(t, gcc, dir, "tuplestructelem_san", sanAsm)
	sanErr, sanExit := hevRun(t, runner, sanBin)
	if sanExit != 23 {
		t.Fatalf("sanitize leg exited %d, want 23 (124 = fatal sanitizer check)", sanExit)
	}
	if strings.Contains(sanErr, "rc over-release") || strings.Contains(sanErr, "use-after-free") {
		t.Fatalf("sanitize leg reported:\n%s", sanErr)
	}
}
