package e2e

import "testing"

// A consuming match over an `own` enum parameter moves each pointer payload
// into its arm binding and frees the box shallow, so the binding is the
// payload's owner (#10872). Before, nothing released it, so the array `len`
// and `len_guarded` bind and the struct `keep` returns leaked per call, and
// so did the array of a box the caller still shares. `inc` is the
// reuse-paired traversal, whose bindings move into the rebuilt list.
const ownParamPayloadSrc = `enum Box { Arr(i32[]), Nil }
enum List { Cons(i32, List), Nil2 }
struct P { xs: i32[], n: i32 }
enum Holder { Has(P), Empty }
@noinline function len(own b: Box): i32 {
    match (b) { Arr(a) => { return a.len(); }, Nil => { return 0; } }
    return 0;
}
@noinline function len_guarded(own b: Box): i32 {
    match (b) { Arr(a) when a.len() > 5 => { return 100; }, Arr(a) => { return a.len(); }, Nil => { return 0; } }
    return 0;
}
@noinline function keep(own h: Holder): P {
    match (h) { Has(p) => { return p; }, Empty => { return P { xs: [], n: 0 }; } }
    return P { xs: [], n: 0 };
}
@noinline function inc(own l: List): List {
    match (l) { Cons(h, t) => { return Cons(h + 1, inc(t)); }, Nil2 => { return Nil2; } }
    return Nil2;
}
@noinline function sum(own l: List): i32 {
    match (l) { Cons(h, t) => { return h + sum(t); }, Nil2 => { return 0; } }
    return 0;
}
@noinline function shared_box(): i32 {
    var shared = Arr([4, 5]);
    var other = shared;
    var n = len(shared);
    match (other) { Arr(a) => { return n + a.len(); }, Nil => { return n; } }
    return n;
}
function main(): i32 {
    var total: i32 = 0;
    var i: i32 = 0;
    while (i < 10) {
        total = total + len(Arr([1, 2, 3]));
        total = total + shared_box();
        total = total + len_guarded(Arr([6, 7, 8, 9]));
        var p = keep(Has(P { xs: [1], n: 2 }));
        total = total + p.xs.len() + p.n;
        total = total + sum(inc(Cons(1, Cons(2, Nil2))));
        i = i + 1;
    }
    return total - 190;
}
`

func TestLeakCheckOwnParamPayloadX86_64(t *testing.T) {
	_, stderr, code := runLeakCheckX86_64(t, ownParamPayloadSrc)
	allocs, frees, live := leakSummaryIn(t, stderr)
	if code != 0 || allocs != frees || live != 0 {
		t.Fatalf("code=%d allocs=%d frees=%d live_bytes=%d: a payload moved out of an own parameter is not released", code, allocs, frees, live)
	}
}

func TestLeakCheckOwnParamPayloadArm64(t *testing.T) {
	_, stderr, code := runLeakCheckArm64(t, ownParamPayloadSrc)
	allocs, frees, live := leakSummaryIn(t, stderr)
	if code != 0 || allocs != frees || live != 0 {
		t.Fatalf("code=%d allocs=%d frees=%d live_bytes=%d: a payload moved out of an own parameter is not released", code, allocs, frees, live)
	}
}

func TestLeakCheckOwnParamPayloadWasm(t *testing.T) {
	_, stderr, code := runLeakCheckWasm(t, ownParamPayloadSrc, false)
	allocs, frees, live := leakSummaryIn(t, stderr)
	if code != 0 || allocs != frees || live != 0 {
		t.Fatalf("code=%d allocs=%d frees=%d live_bytes=%d: a payload moved out of an own parameter is not released", code, allocs, frees, live)
	}
}
