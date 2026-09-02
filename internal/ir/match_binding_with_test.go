package ir

import "testing"

// A match-arm binding of a NON-consuming match is a borrow of the scrutinee
// box's payload (the arm loads the pointer with no retain), so `.with` on an
// array bound that way must take the copy path: computeArraySetIncs forces
// the receiver inc exactly as it does for a borrowed parameter. Without it
// the cow ran in place at the box's rc==1, rewriting the payload of an enum a
// snapshot still held, and the box's later drop released the stored element
// (use-after-free under -sanitize; the persistent HAMT's `Branch(bm, kids)
// => Branch(bm, kids.with(i, child))` shape).
//
// A consuming match keeps the in-place path: its bindings own their payload
// (moved out of an `own` scrutinee), so the write adds no inc — that is the
// zero-alloc FBIP traversal the reuse contract promises.
func TestArraySetOnBorrowedMatchBindingForcesCopy(t *testing.T) {
	src := `enum N { L(i32), B(i32, N[]) }
function borrowed(n: N, i: i32, v: i32): N {
    match (n) {
        L(x) => { return n; },
        B(c, kids) => { return B(c + 1, kids.with(i, L(v))); },
    }
}
function consuming(own n: N, i: i32, v: i32): N {
    match (n) {
        L(x) => { return L(x); },
        B(c, kids) => { return B(c + 1, kids.with(i, L(v))); },
    }
}
function control(own n: N, i: i32, v: i32): N {
    match (n) {
        L(x) => { return L(x); },
        B(c, kids) => { return B(c + 1, kids); },
    }
}
function main(): i32 { return 0; }`
	for _, ptrW := range []int{4, 8} {
		prog := lowerSourceWith(t, src, ptrW)
		if n := countRcIncs(prog, "borrowed"); n == 0 {
			t.Errorf("ptrW=%d: borrowed emitted no rc-inc — `.with` on a match binding of a borrowed scrutinee mutates the box's array in place", ptrW)
		}
		// The own-scrutinee match retains its bindings on the SHARED branch of
		// the consuming release (#6720) whether or not the arm writes, so the
		// `.with` must add nothing over the same arm without it.
		if got, ctl := countRcIncs(prog, "consuming"), countRcIncs(prog, "control"); got > ctl {
			t.Errorf("ptrW=%d: consuming emitted %d rc-incs, control %d — the own-scrutinee binding owns its payload and must keep the in-place path", ptrW, got, ctl)
		}
	}
}
