package e2e

import "testing"

func TestAsyncWasmGatherTimers(t *testing.T) {
	src := `import "std/async";
import "std/clock";
import "std/time";

function start_timer(ns: i64, label: i32): async.Future[i32] {
    let none: i32[] = [];
    let at: Instant = clock.instant().add(time.duration_nanos(ns));
    return Pending(async.Wait { set: none, until: Some(at) }, (wake: async.Wake): async.Future[i32] => Ready(label));
}

function main(): i32 {
    let fs: async.Future[i32][] = [start_timer(10000000, 10), start_timer(15000000, 15)];
    let r: i32[] = async.block_on(async.gather(fs)).unwrap_or([]);
    if (r.len() != 2) { return 90; }
    if (r[0] != 10) { return 91; }
    if (r[1] != 15) { return 92; }
    return 42;
}`
	if got := runWasmResult(t, src); got != 42 {
		t.Errorf("async.gather over wasm deadlines: got %d, want 42", got)
	}
}

func TestAsyncWasmRaceTimers(t *testing.T) {
	src := `import "std/async";
import "std/clock";
import "std/time";

function start_timer(ns: i64, label: i32): async.Future[i32] {
    let none: i32[] = [];
    let at: Instant = clock.instant().add(time.duration_nanos(ns));
    return Pending(async.Wait { set: none, until: Some(at) }, (wake: async.Wake): async.Future[i32] => Ready(label));
}

function main(): i32 {
    let fs: async.Future[i32][] = [start_timer(5000000000, 20), start_timer(10000000, 10)];
    let won: (i32, i32) = async.block_on(async.race(fs)).unwrap_or((0 - 1, 0 - 1));
    if (won.1 != 10) { return 91; }
    if (won.0 != 1) { return 92; }
    return 42;
}`
	if got := runWasmResult(t, src); got != 42 {
		t.Errorf("async.race over wasm deadlines: got %d, want 42", got)
	}
}

func TestAsyncWasmWithDeadline(t *testing.T) {
	src := `import "std/async";
import "std/clock";
import "std/time";

function start_timer(ns: i64, label: i32): async.Future[i32] {
    let none: i32[] = [];
    let at: Instant = clock.instant().add(time.duration_nanos(ns));
    return Pending(async.Wait { set: none, until: Some(at) }, (wake: async.Wake): async.Future[i32] => Ready(label));
}

function main(): i32 {
    let fs: async.Future[i32][] = [start_timer(10000000, 7), start_timer(30000000000, 35)];
    let r: Option[i32][] = async.block_on(async.with_deadline(clock.instant().add(time.duration_millis(500)), fs)).unwrap_or([]);
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
import "std/clock";
import "std/time";
import "std/string";

function start_timer(ns: i64, label: string): async.Future[string] {
    let none: i32[] = [];
    let at: Instant = clock.instant().add(time.duration_nanos(ns));
    return Pending(async.Wait { set: none, until: Some(at) }, (wake: async.Wake): async.Future[string] => Ready(label));
}

function main(): i32 {
    let fs: async.Future[string][] = [start_timer(40000000, "lo"), start_timer(10000000, "hi")];
    let r: string[] = async.block_on(async.gather(fs)).unwrap_or([]);
    if (r.len() != 2) { return 1; }
    if (r[0] != "lo") { return 2; }
    if (r[1] != "hi") { return 3; }
    return 42;
}`
	if got := runWasmResult(t, src); got != 42 {
		t.Errorf("async.gather over wasm deadlines (string): got %d, want 42", got)
	}
}
