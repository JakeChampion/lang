package e2ecompiler

import "testing"

// Updates before a read-only capture need no shared cell. Later assignments,
// loop backedges, closure writes, and deferred writes must stay observable.
const captureTimingProgram = `import "core/map";
@noinline function observe(f: () => i32): i32 { return f(); }
@noinline function observe_float(f: () => f64): f64 { return f(); }
@noinline function map_before_read(n: i32): i32 {
    let before: i64 = __heap_alloc_count();
    let m: Map[i32, i32] = Map { };
    let i: i32 = 0;
    while (i < n) { m = m.insert(i * 7, i); i = i + 1; }
    let allocations: i64 = __heap_alloc_count() - before;
    let result: i32 = observe((): i32 => m.get_or(7, 0));
    if (result != 1) { return 1; }
    if (allocations > 32i64) { return 2; }
    return 0;
}
@noinline function array_before_read(n: i32): i32 {
    if (n > 0) {
        let before: i64 = __heap_alloc_count();
        let xs: i32[] = [];
        let i: i32 = 0;
        while (i < n) { xs = xs.append(i); i = i + 1; }
        let allocations: i64 = __heap_alloc_count() - before;
        let result: i32 = observe((): i32 => xs.len() + xs[n - 1]);
        if (result != n + n - 1) { return 3; }
        if (allocations > 32i64) { return 4; }
    }
    return 0;
}
@noinline function fresh_loop(n: i32): i32 {
    let previous: () => i32 = (): i32 => 0;
    let turn: i32 = 0;
    while (turn < 3) {
        let before: i64 = __heap_alloc_count();
        let xs: i32[] = [];
        let i: i32 = 0;
        while (i < n + turn) { xs = xs.append(i); i = i + 1; }
        let allocations: i64 = __heap_alloc_count() - before;
        if (allocations > 32i64) { return 10; }
        if (turn > 0 && observe(previous) != n + turn - 1) { return 11; }
        previous = (): i32 => xs.len();
        turn = turn + 1;
    }
    if (observe(previous) != n + 2) { return 12; }
    return 0;
}
@noinline function later_assignment(): i32 {
    let xs: i32[] = [1];
    let read: () => i32 = (): i32 => xs[0] + xs.len();
    xs = xs.append(2);
    xs = xs.with(0, 9);
    if (observe(read) != 11) { return 5; }
    return 0;
}
@noinline function loop_backedge(): i32 {
    let x: i32 = 0;
    let previous: () => i32 = (): i32 => 0;
    let i: i32 = 0;
    while (i < 4) {
        x = i;
        if (i > 0 && observe(previous) != i) { return 6; }
        previous = (): i32 => x;
        i = i + 1;
    }
    return 0;
}
@noinline function deferred_reader(): () => i32 {
    let x: i32 = 1;
    defer { x = 7; }
    return (): i32 => x;
}
@noinline function shared_writes(): i32 {
    let x: i32 = 0;
    let read: () => i32 = (): i32 => x;
    let write: () => i32 = (): i32 => { x = x + 7; return x; };
    if (observe(write) != 7 || observe(read) != 7) { return 7; }
    return 0;
}
@noinline function wide_capture(): i32 {
    let x: f64 = 0.0;
    let i: i32 = 0;
    while (i < 4) { x = x + 0.5; i = i + 1; }
    let read: () => f64 = (): f64 => x;
    if (observe_float(read) != 2.0) { return 8; }
    return 0;
}
function main(): i32 {
    let result: i32 = map_before_read(512 + args().len());
    if (result != 0) { return result; }
    result = array_before_read(1024 + args().len());
    if (result != 0) { return result; }
    result = fresh_loop(512 + args().len());
    if (result != 0) { return result; }
    result = later_assignment();
    if (result != 0) { return result; }
    result = loop_backedge();
    if (result != 0) { return result; }
    if (observe(deferred_reader()) != 7) { return 9; }
    result = shared_writes();
    if (result != 0) { return result; }
    result = wide_capture();
    if (result != 0) { return result; }
    print("ok");
    return 0;
}
`

func TestSelfHostCaptureAfterLastWrite(t *testing.T) {
	runSemanticProgram(t, "capture-timing", captureTimingProgram,
		[]string{"observe", "observe_float", "map_before_read", "array_before_read", "fresh_loop", "later_assignment", "loop_backedge", "deferred_reader", "shared_writes", "wide_capture"},
		semInlineWants("ok\n"), "observe", "observe_float")
}
