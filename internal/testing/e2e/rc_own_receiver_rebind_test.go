package e2e

import (
	"testing"
)

const ownReceiverRebindSrc = `struct Acc { out: i32[], n: i32 }
pub function (own s: Acc) emit(x: i32): Acc {
    let ys = s.out.append(x);
    return Acc { out: ys, n: s.n + 1 };
}
function emit_two(s: Acc, a: i32, b: i32): Acc {
    s = s.emit(a);
    s = s.emit(b);
    return s;
}
function churn_local(n: i32): i32 {
    let s = Acc { out: [], n: 0 };
    let i: i32 = 0;
    while (i < n) { s = s.emit(i); i = i + 1; }
    if (s.n != n || s.out.len() != n) { return 1; }
    if (s.out[0] != 0 || s.out[n - 1] != n - 1) { return 2; }
    return 0;
}
function churn_threaded(n: i32): i32 {
    let s = Acc { out: [], n: 0 };
    let i: i32 = 0;
    while (i < n) { s = emit_two(s, i, i + 1); i = i + 1; }
    if (s.n != 2 * n || s.out.len() != 2 * n) { return 3; }
    if (s.out[2 * n - 1] != n) { return 4; }
    return 0;
}
function keep_alive(): i32 {
    let s = Acc { out: [7, 8], n: 2 };
    let c = emit_two(s, 1, 2);
    // s's box must still be owned by THIS frame after the inner frame's
    // admitted move (the entry-inc materialised the callee's reference).
    if (s.n != 2 || s.out.len() < 2) { return 5; }
    if (c.n != 4) { return 6; }
    return 0;
}
function main(): i32 {
    let r: i32 = churn_local(200);
    if (r != 0) { return r; }
    r = churn_threaded(100);
    if (r != 0) { return r; }
    let k: i32 = 0;
    while (k < 50) {
        r = keep_alive();
        if (r != 0) { return r; }
        k = k + 1;
    }
    return __rc_underflow_count();
}`

func TestX86_64OwnReceiverRebind(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, ownReceiverRebindSrc); code != 0 {
		t.Errorf("own-receiver move-and-rebind: got %d, want 0", code)
	}
}

func TestArm64OwnReceiverRebind(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, ownReceiverRebindSrc); code != 0 {
		t.Errorf("own-receiver move-and-rebind: got %d, want 0", code)
	}
}

func TestWASMOwnReceiverRebind(t *testing.T) {
	if got := runWasm(t, ownReceiverRebindSrc); got != 0 {
		t.Errorf("own-receiver move-and-rebind: got %d, want 0", got)
	}
}
