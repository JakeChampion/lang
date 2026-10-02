package e2e

import "testing"

// A loop threading a record of arrays through a helper that hands it back
// inside a tuple (#10679). The helper's `return (add(c, fd), true)` is where
// its `c` dies, so add grows the arrays in place; when only a call that WAS
// the whole returned value counted as that death, add copied all eleven
// arrays per append and the loop's bump grew quadratically — 1,346,400
// bytes over 600 events on x86-64, against 17,072 now. The program exits 42
// when 600 events bump under 100,000 bytes.
const tupleReturnHelperHeapSrc = `
struct C { a: i32[], b: i32[], c: i32[], d: i32[], e: i32[], f: i32[], g: i32[], h: i32[], i: i32[], j: i32[], k: i32[] }

function add(c: C, fd: i32): C {
    return C { a: c.a.append(fd), b: c.b.append(fd), c: c.c.append(fd), d: c.d.append(fd), e: c.e.append(fd), f: c.f.append(fd), g: c.g.append(fd), h: c.h.append(fd), i: c.i.append(fd), j: c.j.append(fd), k: c.k.append(fd) };
}

function intake(listener: i32, c: C, fd: i32): (C, boolean) {
    if (fd == listener) { return (add(c, fd), true); }
    return (c, false);
}

function main(): i32 {
    var c: C = C { a: [], b: [], c: [], d: [], e: [], f: [], g: [], h: [], i: [], j: [], k: [] };
    var h0: i64 = __heap_bump_bytes() as i64;
    var i: i32 = 0;
    while (i < 600) {
        var fd: i32 = 7;
        if (i % 3 != 0) { fd = 8; }
        var r: (C, boolean) = intake(7, c, fd);
        c = r.0;
        i = i + 1;
    }
    var grew: i64 = (__heap_bump_bytes() as i64) - h0;
    if (c.a.len() != 200 || c.k.len() != 200) { return 1; }
    if (grew >= 100000 as i64) { return 2; }
    return 42;
}
`

func TestTupleReturnHelperHeapX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, tupleReturnHelperHeapSrc); got != 42 {
		t.Fatalf("x86-64 exited %d, want 42 (2 = each append copied the record's arrays)", got)
	}
}

func TestTupleReturnHelperHeapArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, tupleReturnHelperHeapSrc); got != 42 {
		t.Fatalf("arm64 exited %d, want 42 (2 = each append copied the record's arrays)", got)
	}
}

func TestTupleReturnHelperHeapWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, tupleReturnHelperHeapSrc); got != 42 {
		t.Fatalf("wasm exited %d, want 42 (2 = each append copied the record's arrays)", got)
	}
}
