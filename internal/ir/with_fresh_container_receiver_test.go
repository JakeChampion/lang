package ir

import "testing"

// `.with` on a value read out of a FRESH owned container. The read already
// retains the value and deep-drops the container (#6401 / #6475), so the
// expression holds the only reference and __fern_arr_cow_inplace may take
// it. Classifying it with the other projections forced the #2832 pre-call
// inc, which bought a copy AND leaked the original — nothing holds it, so
// no slot release could ever reach it: 64 B a round for
// `mk_box().items.with(…)`, unbounded, where `mk_box().items.len()` was
// flat.
//
// The mirror halves are the projections that really are borrows: a field of
// a live struct and an element of a live array must still copy, or the
// container is mutated through the receiver.

const withFreshContainerSrc = `struct S { xs: i32[], tag: i32 }
function mk(): S { return S { xs: [1, 2], tag: 0 }; }
function mkxs(): i32[][] { return [[1, 2], [3, 4]]; }
function freshField(i: i32): i32 {
    var b: i32[] = mk().xs.with(0, i);
    return b[0] + b[1];
}
function freshElem(i: i32): i32 {
    var b: i32[] = mkxs()[0].with(0, i);
    return b[0] + b[1];
}
function borrowedField(s: S, i: i32): i32 {
    var b: i32[] = s.xs.with(0, i);
    return b[0] + s.xs[0];
}
function borrowedElem(a: i32[][], i: i32): i32 {
    var b: i32[] = a[0].with(0, i);
    return b[0] + a[0][0];
}
function main(): i32 { return 0; }`

func TestWithOnFreshContainerReadTakesTheReference(t *testing.T) {
	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, withFreshContainerSrc, ptrW)
		for _, name := range []string{"freshField", "freshElem"} {
			fn := findFunc(p, name)
			// One retain only: the container read's own, which the
			// container's deep drop nets back to a single reference.
			if n := countCallDirect(fn.Ops, "__fern_rc_inc"); n != 1 {
				t.Errorf("ptrW=%d: %s emitted %d retains, want 1 — a second one forces "+
					"cow to copy and strands the original, which no slot can release; ops:\n%s",
					ptrW, name, n, p)
			}
		}
	}
}

func TestWithOnBorrowedProjectionStillCopies(t *testing.T) {
	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, withFreshContainerSrc, ptrW)
		for _, name := range []string{"borrowedField", "borrowedElem"} {
			fn := findFunc(p, name)
			if n := countCallDirect(fn.Ops, "__fern_rc_inc"); n != 1 {
				t.Errorf("ptrW=%d: %s emitted %d retains, want 1 — the container is live, "+
					"so cow must copy rather than mutate through the receiver; ops:\n%s",
					ptrW, name, n, p)
			}
		}
	}
}
