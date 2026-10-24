package e2e

import "testing"

func TestX86_64ArrayWithPtrElemRecycles(t *testing.T) {
	const src = `struct Op { a: i32, c: i32 }
struct R { ops: Op[], p: i32 }
function upd(r: R, i: i32, v: i32): R {
    return R { ops: r.ops.with(i, Op { a: v, c: 7 }), p: r.p };
}
function churn(n: i32): i32 {
    let ops: Op[] = [];
    let k: i32 = 0;
    while (k < 8) { ops = ops.append(Op { a: 0, c: 0 }); k = k + 1; }
    let r: R = R { ops: ops, p: 0 };
    let i: i32 = 0;
    while (i < n) { r = upd(r, i % 8, i); i = i + 1; }
    if (r.ops[0].a == 999999) { return 99; }
    if ((__heap_bump_bytes() as i32) < 1048576) { return 0; }
    return 1;
}
function main(): i32 { return churn(100000); }`
	if _, code := compileAndRunX86_64FreeOn(t, src); code != 0 {
		t.Errorf("pointer-element .with churn: got exit %d, want 0 (heap bump < 1 MiB — copied array spines + replaced elements must recycle)", code)
	}
}
