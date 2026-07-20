package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Move-and-rebind through an `own`-RECEIVER method — `s = s.emit(x)` — the
// method sibling of rc_own_struct_rebind_test.go's plain-call shape. A method
// call reaches the rc layer as a plain Call on the mangled method name with
// the receiver in Args[0], so callConsumesIdent must treat the receiver
// position exactly like a plain `own` argument and suppress the reassign's
// overwrite-dec (the callee's receiver drop already released that reference).
//
// Before the fix callConsumesIdent EXCLUDED method calls, so the dec fired on
// top of the callee-side drop — net -1 per call:
//   - a LOCAL receiver's box was freed by the callee (rc==1 unique path) and
//     the overwrite-dec then wrote through the freed header, corrupting the
//     freelist next pointer (surfaced as target-dependent rc-underflow counts:
//     the freed word's low half read as "rc" differs by heap layout);
//   - a threaded BORROWED-PARAM receiver (consumedParams entry-inc) ended at
//     rc 0 while the OUTER caller still held it — a use-after-free one frame
//     up, invisible to value checks until the block was recycled.
//
// Three shapes, each ending in `__rc_underflow_count()` (0 == balanced):
// a local-receiver churn loop, the borrowed-param threading shape
// (`emit_two(s: Acc, ..)` self-reassigning through the own-receiver method,
// with the outer loop rebinding through the plain call), and a
// keep-alive-in-caller variant where the outer frame's binding must survive
// the inner frame's admitted move (old box read back rc-intact via len).
const ownReceiverRebindSrc = `struct Acc { out: i32[], n: i32 }
pub function (own s: Acc) emit(x: i32): Acc {
    var ys = s.out.append(x);
    return Acc { out: ys, n: s.n + 1 };
}
function emit_two(s: Acc, a: i32, b: i32): Acc {
    s = s.emit(a);
    s = s.emit(b);
    return s;
}
function churn_local(n: i32): i32 {
    var s = Acc { out: [], n: 0 };
    var i: i32 = 0;
    while (i < n) { s = s.emit(i); i = i + 1; }
    if (s.n != n || s.out.len() != n) { return 1; }
    if (s.out[0] != 0 || s.out[n - 1] != n - 1) { return 2; }
    return 0;
}
function churn_threaded(n: i32): i32 {
    var s = Acc { out: [], n: 0 };
    var i: i32 = 0;
    while (i < n) { s = emit_two(s, i, i + 1); i = i + 1; }
    if (s.n != 2 * n || s.out.len() != 2 * n) { return 3; }
    if (s.out[2 * n - 1] != n) { return 4; }
    return 0;
}
function keep_alive(): i32 {
    var s = Acc { out: [7, 8], n: 2 };
    var c = emit_two(s, 1, 2);
    // s's box must still be owned by THIS frame after the inner frame's
    // admitted move (the entry-inc materialised the callee's reference).
    if (s.n != 2 || s.out.len() < 2) { return 5; }
    if (c.n != 4) { return 6; }
    return 0;
}
function main(): i32 {
    var r: i32 = churn_local(200);
    if (r != 0) { return r; }
    r = churn_threaded(100);
    if (r != 0) { return r; }
    var k: i32 = 0;
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
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, ownReceiverRebindSrc); got != 0 {
		t.Errorf("own-receiver move-and-rebind: got %d, want 0", got)
	}
}
