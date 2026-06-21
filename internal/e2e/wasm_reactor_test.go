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
