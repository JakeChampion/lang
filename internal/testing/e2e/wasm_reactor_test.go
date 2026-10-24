package e2e

import "testing"

func TestWasmReactorTimerBlock(t *testing.T) {
	src := `function main(): i32 {
    let p: i32 = wasm_timer_pollable(1000000);
    let r: i32 = wasm_block(p);
    if (r != 0) { return 1; }
    return 42;
}`
	if got := runWasmResult(t, src); got != 42 {
		t.Errorf("wasm reactor timer: got %d, want 42", got)
	}
}

func TestWasmReactorTimerWithStdout(t *testing.T) {
	src := `function main(): i32 {
    let p: i32 = wasm_timer_pollable(1000000);
    wasm_block(p);
    print("tick\n");
    return 0;
}`
	if got := stripMainResult(runResultStdout(t, src, runOpts{})); got != "tick" {
		t.Errorf("wasm reactor timer + stdout: got %q, want %q", got, "tick")
	}
}

func TestWasmReactorPollFirstReady(t *testing.T) {
	idx1 := `function main(): i32 {
    let a: i32 = wasm_timer_pollable(200000000);
    let b: i32 = wasm_timer_pollable(10000000);
    let ps: i32[] = [a, b];
    return wasm_poll(ps);
}`
	if got := runWasmResult(t, idx1); got != 1 {
		t.Errorf("wasm_poll first-ready (short at idx 1): got %d, want 1", got)
	}
	idx0 := `function main(): i32 {
    let a: i32 = wasm_timer_pollable(10000000);
    let b: i32 = wasm_timer_pollable(200000000);
    let ps: i32[] = [a, b];
    return wasm_poll(ps);
}`
	if got := runWasmResult(t, idx0); got != 0 {
		t.Errorf("wasm_poll first-ready (short at idx 0): got %d, want 0", got)
	}
}

func TestWasmReactorTimerBlockDrop(t *testing.T) {
	src := `function main(): i32 {
    let p: i32 = wasm_timer_pollable(1000000);
    wasm_block(p);
    wasm_pollable_drop(p);
    return 42;
}`
	if got := runWasmResult(t, src); got != 42 {
		t.Errorf("wasm timer block drop: got %d, want 42", got)
	}
}
