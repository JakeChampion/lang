package e2e

import "testing"

// PR5-wasm (docs/ASYNC-FUTURE-UNIFICATION.md): the std/async combinators
// now resolve `Pending` futures on wasm, because the generic `poll`
// builtin forwards to wasi:io/poll.poll (__fern_wasm_poll) over the i32
// tokens as pollable handles. These drive real wasm readiness through
// `gather` / `race` using timer pollables (wasm_timer_pollable) — the
// same Preview-2 path the wasm suite already runs under stock wasmtime
// (no Preview 3). A `Pending`'s token is a real pollable handle, so the
// poll never traps. (`with_deadline`'s deadline is native-only for now —
// `poll` ignores its timeout arg on wasm; gather/race don't need it.)

// gather over two timer-pollable futures: poll blocks in the host until
// each fires, and gather returns results in input order. The 10ms future
// carries 10, the 15ms carries 15 → [10, 15] → 42.
func TestAsyncWasmGatherTimers(t *testing.T) {
	src := `import "std/async";

function start_timer(ns: i64, label: i32): async.Future[i32] {
    var p: i32 = wasm_timer_pollable(ns);
    function resume(woken: i32): async.Future[i32] { return Ready(label); }
    return Pending(p, resume);
}

function main(): i32 {
    var fs: async.Future[i32][] = [start_timer(10000000, 10), start_timer(15000000, 15)];
    var r: i32[] = async.gather(fs, -1);
    if (r.len() != 2) { return 90; }
    if (r[0] != 10) { return 91; }
    if (r[1] != 15) { return 92; }
    return 42;
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("async.gather over wasm timer pollables: got %d, want 42", got)
	}
}

// race over two timer-pollable futures returns (winnerIndex, value) for
// the FIRST to fire. Fast (10ms → 10) at index 1, slow (5s → 20) at
// index 0: race returns index 1, value 10, short-circuiting the slow one.
//
// The gap is wide for the reason the native case's is: `__fern_wasm_poll`
// returns `ready[0]`, so index order is the tiebreak once both pollables
// have fired, and both are armed before `race` is entered. The slow one is
// short-circuited, so the wait is still the fast timer.
func TestAsyncWasmRaceTimers(t *testing.T) {
	src := `import "std/async";

function start_timer(ns: i64, label: i32): async.Future[i32] {
    var p: i32 = wasm_timer_pollable(ns);
    function resume(woken: i32): async.Future[i32] { return Ready(label); }
    return Pending(p, resume);
}

function main(): i32 {
    var fs: async.Future[i32][] = [start_timer(5000000000, 20), start_timer(10000000, 10)];
    var (winner, value) = async.race(fs, -1);
    if (value != 10) { return 91; }
    if (winner != 1) { return 92; }
    return 42;
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("async.race over wasm timer pollables: got %d, want 42", got)
	}
}

// with_deadline on wasm now ENFORCES the deadline (it appends a real
// wasm_timer_pollable to the poll set each round): a fast future (10ms) beats
// the 500ms budget and carries its result; a slow one (30s) misses and lands
// on_timeout. Proves the wasm host-timeout path (docs/ASYNC-FUTURE-UNIFICATION.md)
// — with_deadline honours its deadline on wasm, and monotonic_ns composes
// with the timer-pollable.
//
// Both gaps are wide deliberately. The fast future has to be SEEN inside the
// budget, and at 10ms against 60ms that margin was 50ms -- tighter than the
// 40ms that failed a quarter of native runs. The slow one has to still be
// Pending when the budget opens, and it is armed before with_deadline is
// entered. The case costs the budget, so the budget is 500ms rather than
// seconds: 526ms a run against 147ms.
func TestAsyncWasmWithDeadline(t *testing.T) {
	src := `import "std/async";

function start_timer(ns: i64, label: i32): async.Future[i32] {
    var p: i32 = wasm_timer_pollable(ns);
    function resume(woken: i32): async.Future[i32] {
        var d: i32 = wasm_pollable_drop(woken);
        return Ready(label);
    }
    return Pending(p, resume);
}

function main(): i32 {
    var fs: async.Future[i32][] = [start_timer(10000000, 7), start_timer(30000000000, 35)];
    var r: Option[i32][] = async.with_deadline(500, fs);
    if (r.len() != 2) { return 90; }
    match (r[0]) { Some(v) => { if (v != 7) { return 91; } }, None => { return 91; } }  // beat the 500ms deadline
    match (r[1]) { Some(v) => { return 92; }, None => { } }                              // missed it -> None
    return 42;
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("async.with_deadline on wasm: got %d, want 42", got)
	}
}

// gather over Future[string] on wasm — generic over T through the
// host-driven poll resumption, with string results threaded back.
func TestAsyncWasmGatherStrings(t *testing.T) {
	src := `import "std/async";
import "std/string";

function start_timer(ns: i64, label: string): async.Future[string] {
    var p: i32 = wasm_timer_pollable(ns);
    function resume(woken: i32): async.Future[string] { return Ready(label); }
    return Pending(p, resume);
}

function main(): i32 {
    var fs: async.Future[string][] = [start_timer(40000000, "lo"), start_timer(10000000, "hi")];
    var r: string[] = async.gather(fs, "");
    if (r.len() != 2) { return 1; }
    if (r[0] != "lo") { return 2; }
    if (r[1] != "hi") { return 3; }
    return 42;
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("async.gather over wasm timer pollables (string): got %d, want 42", got)
	}
}
