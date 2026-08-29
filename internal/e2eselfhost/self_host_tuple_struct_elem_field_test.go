package e2eselfhost

import (
	"strings"
	"testing"
)

// A struct whose TUPLE field carries a RECLAIM-STRUCT element — `Holder { t:
// (i32, P), … }` where `P` has an rc-array field. This is the shape that
// separates the two tuple admission predicates, and nothing else covers it.
//
// struct_routes_field_reclaim_at consults struct_has_reclaim_array_field
// first, whose tuple case (#7259) is TYPE-only: with no `structs` view it
// cannot classify a struct-typed element and deliberately bails on one. The
// struct_has_deep_tuple_field clause below it does take `structs` and admits
// the element via struct_has_reclaim_array_field(P). Neither predicate
// subsumes the other — theirs also admits Option/Result and f64[]/i64[]
// elements that this one does not.
//
// Measured both ways, because the leak matrix has NO cell of this shape and so
// reports nothing when the clause is removed:
//
//	clause live     400 allocs / 400 frees, 0 live
//	clause removed  400 allocs / 100 frees, 12800 live
//
// A knockout that reads only the matrix therefore looks like dead code. This
// test is what makes it move a number.

func TestSelfHostTupleStructElemFieldX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	const src = `struct P { xs: i32[], k: i32 }
struct Holder { t: (i32, P), n: i32 }
function round(i: i32): i32 {
    var p: P = P { xs: [i, i + 1], k: i };
    var t: (i32, P) = (i, p);
    var h: Holder = Holder { t: t, n: i };
    return h.n + h.t.0;
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
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
