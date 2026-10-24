package e2e

import "testing"

func TestAsyncWasmGatherTimers(t *testing.T) {
	src := `import "std/async";

function start_timer(ns: i64, label: i32): async.Future[i32] {
    let p: i32 = wasm_timer_pollable(ns);
    function resume(woken: i32): async.Future[i32] { return Ready(label); }
    return Pending(p, resume);
}

function main(): i32 {
    let fs: async.Future[i32][] = [start_timer(10000000, 10), start_timer(15000000, 15)];
    let r: i32[] = async.gather(fs, -1);
    if (r.len() != 2) { return 90; }
    if (r[0] != 10) { return 91; }
    if (r[1] != 15) { return 92; }
    return 42;
}`
	if got := runWasmResult(t, src); got != 42 {
		t.Errorf("async.gather over wasm timer pollables: got %d, want 42", got)
	}
}

func TestAsyncWasmRaceTimers(t *testing.T) {
	src := `import "std/async";

function start_timer(ns: i64, label: i32): async.Future[i32] {
    let p: i32 = wasm_timer_pollable(ns);
    function resume(woken: i32): async.Future[i32] { return Ready(label); }
    return Pending(p, resume);
}

function main(): i32 {
    let fs: async.Future[i32][] = [start_timer(5000000000, 20), start_timer(10000000, 10)];
    let (winner, value) = async.race(fs, -1);
    if (value != 10) { return 91; }
    if (winner != 1) { return 92; }
    return 42;
}`
	if got := runWasmResult(t, src); got != 42 {
		t.Errorf("async.race over wasm timer pollables: got %d, want 42", got)
	}
}

func TestAsyncWasmWithDeadline(t *testing.T) {
	src := `import "std/async";
import "std/time";

function start_timer(ns: i64, label: i32): async.Future[i32] {
    let p: i32 = wasm_timer_pollable(ns);
    function resume(woken: i32): async.Future[i32] {
        let d: i32 = wasm_pollable_drop(woken);
        return Ready(label);
    }
    return Pending(p, resume);
}

function main(): i32 {
    let fs: async.Future[i32][] = [start_timer(10000000, 7), start_timer(30000000000, 35)];
    let r: Option[i32][] = async.with_deadline(time.duration_millis(500), fs);
    if (r.len() != 2) { return 90; }
    match (r[0]) { Some(v) => { if (v != 7) { return 91; } }, None => { return 91; } }  // beat the 500ms deadline
    match (r[1]) { Some(v) => { return 92; }, None => { } }                              // missed it -> None
    return 42;
}`
	if got := runWasmResult(t, src); got != 42 {
		t.Errorf("async.with_deadline on wasm: got %d, want 42", got)
	}
}

func TestAsyncWasmGatherStrings(t *testing.T) {
	src := `import "std/async";
import "std/string";

function start_timer(ns: i64, label: string): async.Future[string] {
    let p: i32 = wasm_timer_pollable(ns);
    function resume(woken: i32): async.Future[string] { return Ready(label); }
    return Pending(p, resume);
}

function main(): i32 {
    let fs: async.Future[string][] = [start_timer(40000000, "lo"), start_timer(10000000, "hi")];
    let r: string[] = async.gather(fs, "");
    if (r.len() != 2) { return 1; }
    if (r[0] != "lo") { return 2; }
    if (r[1] != "hi") { return 3; }
    return 42;
}`
	if got := runWasmResult(t, src); got != 42 {
		t.Errorf("async.gather over wasm timer pollables (string): got %d, want 42", got)
	}
}
