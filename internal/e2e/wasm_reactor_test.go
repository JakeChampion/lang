package e2e

import "testing"

// The wasm reactor's timer primitive, end-to-end through the component
// composer + wasmtime. `wasm_timer_pollable(ns)` returns a
// wasi:io/poll pollable (via wasi:clocks/monotonic-clock.subscribe-
// duration) that becomes ready after `ns` nanoseconds; `wasm_block(p)`
// blocks on it until ready, then returns 0.
//
// This exercises the standalone clocks + io/poll composition — the
// pollable resource crossing the clock/poll instance boundary with NO
// socket in play, the piece docs/WASM-REACTOR-PLAN.md called the
// composer blocker. Pollables are Preview 2, so this runs on the
// stock wasmtime the rest of the wasm suite uses (no Preview 3 / no
// wasmtime upgrade needed).
func TestWasmReactorTimerBlock(t *testing.T) {
	// 1ms timer: subscribe, block until ready, then return 42 so the
	// harness can read a non-zero sentinel proving the path ran.
	src := `function main(): i32 {
    var p: i32 = wasm_timer_pollable(1000000);
    var r: i32 = wasm_block(p);
    if (r != 0) { return 1; }
    return 42;
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("wasm reactor timer: got %d, want 42", got)
	}
}

// A timer pollable composed alongside other capabilities (here
// stdout) must still compose — the Timer request path is independent
// of the CLI / stream surfaces, so a program that both blocks on a
// timer and prints works. Guards against the standalone-clocks
// composition clobbering or being clobbered by the io/streams union.
func TestWasmReactorTimerWithStdout(t *testing.T) {
	src := `function main(): i32 {
    var p: i32 = wasm_timer_pollable(1000000);
    wasm_block(p);
    print("tick\n");
    return 0;
}`
	// runWasmCapturingStdout trims the trailing newline + result line.
	if got := runWasmCapturingStdout(t, src); got != "tick" {
		t.Errorf("wasm reactor timer + stdout: got %q, want %q", got, "tick")
	}
}

// The reactor multiplexer: wasm_poll(pollables) blocks until the FIRST
// pollable in the array is ready and returns its index. Two timers
// (200ms, 10ms) — poll must return the index of the short one and
// short-circuit (not wait for the long one). The wasm analog of the
// native poll(fds); exercises wasi:io/poll.poll(list<pollable>) ->
// list<u32> with the list-in / list-out canonical-ABI marshalling.
func TestWasmReactorPollFirstReady(t *testing.T) {
	// Short timer at index 1 → poll returns 1.
	idx1 := `function main(): i32 {
    var a: i32 = wasm_timer_pollable(200000000);
    var b: i32 = wasm_timer_pollable(10000000);
    var ps: i32[] = [a, b];
    return wasm_poll(ps);
}`
	if got := runWasm(t, idx1); got != 1 {
		t.Errorf("wasm_poll first-ready (short at idx 1): got %d, want 1", got)
	}
	// Short timer at index 0 → poll returns 0.
	idx0 := `function main(): i32 {
    var a: i32 = wasm_timer_pollable(10000000);
    var b: i32 = wasm_timer_pollable(200000000);
    var ps: i32[] = [a, b];
    return wasm_poll(ps);
}`
	if got := runWasm(t, idx0); got != 0 {
		t.Errorf("wasm_poll first-ready (short at idx 0): got %d, want 0", got)
	}
}

// wasm_pollable_drop frees a consumed pollable. A timer → block → drop
// sequence composes (the standalone [resource-drop]pollable lowering)
// and runs clean. Guards the resource-drop path the reactor uses to
// avoid leaking fired timer pollables.
func TestWasmReactorTimerBlockDrop(t *testing.T) {
	src := `function main(): i32 {
    var p: i32 = wasm_timer_pollable(1000000);
    wasm_block(p);
    wasm_pollable_drop(p);
    return 42;
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("wasm timer block drop: got %d, want 42", got)
	}
}

// std/wasm_reactor.run drives generic pollable-tagged stackless tasks
// (Step[T]) to completion over wasm_poll — the wasm twin of
// std/reactor.run_io. Two timer tasks overlap on one thread; each
// resumes to Done(val). Results come back in TASK order, not
// completion order: task 0 is the slow (50ms) timer carrying 35, task 1
// the fast (10ms) one carrying 7, so results must be [35, 7] even
// though task 1 finishes first.
func TestWasmReactorRunSchedulerI32(t *testing.T) {
	src := `import "std/wasm_reactor";
function start(ns: i64, val: i32): wasm_reactor.Step[i32] {
    function resume(p: i32): wasm_reactor.Step[i32] { return Done(val); }
    return Wait(wasm_timer_pollable(ns), resume);
}
function main(): i32 {
    var tasks: wasm_reactor.Step[i32][] = [start(50000000, 35), start(10000000, 7)];
    var r: i32[] = wasm_reactor.run(tasks, -1);
    if (r.len() != 2) { return 90; }
    if (r[0] != 35) { return 91; }  // task-order, not completion-order
    if (r[1] != 7) { return 92; }
    return r[0] + r[1];
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("wasm_reactor.run i32: got %d, want 42", got)
	}
}

// std/wasm_reactor.select drives tasks until the FIRST completes, then
// returns its value and cancels the rest — the race / first-wins
// counterpart to run (which waits for all). Two timers (10ms, 200ms):
// select returns the fast one's value regardless of its position.
func TestWasmReactorSelect(t *testing.T) {
	// Fast task at index 1 → select returns its value (7), not the slow
	// one's (35), and short-circuits.
	src := `import "std/wasm_reactor";
function start(ns: i64, val: i32): wasm_reactor.Step[i32] {
    function resume(p: i32): wasm_reactor.Step[i32] { return Done(val); }
    return Wait(wasm_timer_pollable(ns), resume);
}
function main(): i32 {
    var tasks: wasm_reactor.Step[i32][] = [start(200000000, 35), start(10000000, 7)];
    return wasm_reactor.select(tasks, 0 - 1);
}`
	if got := runWasm(t, src); got != 7 {
		t.Errorf("wasm_reactor.select (fast at idx 1): got %d, want 7", got)
	}
	// Fast task at index 0 → returns 7 too.
	src0 := `import "std/wasm_reactor";
function start(ns: i64, val: i32): wasm_reactor.Step[i32] {
    function resume(p: i32): wasm_reactor.Step[i32] { return Done(val); }
    return Wait(wasm_timer_pollable(ns), resume);
}
function main(): i32 {
    var tasks: wasm_reactor.Step[i32][] = [start(10000000, 7), start(200000000, 35)];
    return wasm_reactor.select(tasks, 0 - 1);
}`
	if got := runWasm(t, src0); got != 7 {
		t.Errorf("wasm_reactor.select (fast at idx 0): got %d, want 7", got)
	}
}

// std/wasm_reactor.run_deadline bounds the whole fan-out by a
// wall-clock deadline (a timer pollable added to every poll round) —
// the wasm twin of std/reactor.run_io_deadline. A task that beats the
// deadline carries its result; one that doesn't is abandoned and takes
// `not_ready`.
func TestWasmReactorRunDeadline(t *testing.T) {
	// Fast task (10ms → 7) beats the 60ms deadline; slow task (300ms →
	// 35) does not, so it lands not_ready (-1).
	mixed := `import "std/wasm_reactor";
function start(ns: i64, val: i32): wasm_reactor.Step[i32] {
    function resume(p: i32): wasm_reactor.Step[i32] { return Done(val); }
    return Wait(wasm_timer_pollable(ns), resume);
}
function main(): i32 {
    var tasks: wasm_reactor.Step[i32][] = [start(10000000, 7), start(300000000, 35)];
    var r: i32[] = wasm_reactor.run_deadline(tasks, 60000000, 0 - 1);
    if (r.len() != 2) { return 90; }
    if (r[0] != 7) { return 91; }       // beat the deadline
    if (r[1] != (0 - 1)) { return 92; } // abandoned at the deadline
    return 42;
}`
	if got := runWasm(t, mixed); got != 42 {
		t.Errorf("wasm_reactor.run_deadline mixed: got %d, want 42", got)
	}
	// Both tasks beat a generous deadline → both carry their result.
	intime := `import "std/wasm_reactor";
function start(ns: i64, val: i32): wasm_reactor.Step[i32] {
    function resume(p: i32): wasm_reactor.Step[i32] { return Done(val); }
    return Wait(wasm_timer_pollable(ns), resume);
}
function main(): i32 {
    var tasks: wasm_reactor.Step[i32][] = [start(5000000, 35), start(10000000, 7)];
    var r: i32[] = wasm_reactor.run_deadline(tasks, 500000000, 0 - 1);
    return r[0] + r[1];
}`
	if got := runWasm(t, intime); got != 42 {
		t.Errorf("wasm_reactor.run_deadline in-time: got %d, want 42", got)
	}
}

// The scheduler is generic in T: a Step[string] fan-out returns string
// results. Exercises the generic-variant inference (T recovered through
// the function-typed Wait payload) end-to-end on wasm, plus string
// results threaded through wasm_poll-driven resumption.
func TestWasmReactorRunSchedulerString(t *testing.T) {
	src := `import "std/wasm_reactor";
import "std/string";
function start(ns: i64, val: string): wasm_reactor.Step[string] {
    function resume(p: i32): wasm_reactor.Step[string] { return Done(val); }
    return Wait(wasm_timer_pollable(ns), resume);
}
function main(): i32 {
    var tasks: wasm_reactor.Step[string][] = [start(40000000, "lo"), start(10000000, "hi")];
    var r: string[] = wasm_reactor.run(tasks, "");
    if (r.len() != 2) { return 1; }
    if (r[0] != "lo") { return 2; }
    if (r[1] != "hi") { return 3; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("wasm_reactor.run string: got %d, want 0", got)
	}
}
