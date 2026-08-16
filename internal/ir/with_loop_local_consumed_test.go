package ir

import "testing"

// A loop-body `var` whose LAST use is a `.with` receiver has its reference
// taken over by __fern_arr_cow_inplace, so the slot is empty when the next
// iteration re-declares it. The exit sweep already knew that (#6013); the
// per-iteration re-init drop did not, and released the buffer the RESULT
// still pointed at — SIGSEGV on both natives for a struct-element array and
// a silent double free for a scalar one (#6877).
//
// The assertions count the array release sites — one re-init drop per
// loop-body `var` that still owns its slot, plus one exit sweep per such
// local. A count with the receiver's re-init drop in it is the bug.

const withLoopConsumedSrc = `struct P { a: i32, b: i32 }
function mk_arr(): P[] { return [P{a:0,b:0}, P{a:1,b:1}]; }
function consumed(n: i32): i32 {
    var t: i32 = 0;
    for i in 0..n {
        var it: P[] = mk_arr();
        var a: P[] = it.with(0, P{a:i,b:i});
        t = t + a[0].a;
    }
    return t;
}
function borrowed(n: i32): i32 {
    var t: i32 = 0;
    for i in 0..n {
        var it: P[] = mk_arr();
        var a: P[] = it.with(0, P{a:i,b:i});
        t = t + a[0].a + it[1].b;
    }
    return t;
}
function conditional(n: i32, c: i32): i32 {
    var t: i32 = 0;
    for i in 0..n {
        var it: P[] = mk_arr();
        t = t + it[0].a;
        if (c > 0) { var a: P[] = it.with(0, P{a:i,b:i}); t = t + a[0].a; }
    }
    return t;
}
function mk_ints(): i32[] { return [0, 1]; }
function consumedScalar(n: i32): i32 {
    var t: i32 = 0;
    for i in 0..n {
        var it: i32[] = mk_ints();
        var a: i32[] = it.with(0, i);
        t = t + a[0];
    }
    return t;
}
function main(): i32 { return 0; }`

// `it` is transferred into the `.with`, so only `a` is released per
// iteration — one deep drop in the loop plus the one the exit sweep emits
// for `a`.
func TestWithLoopLocalConsumedSkipsReinitDrop(t *testing.T) {
	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, withLoopConsumedSrc, ptrW)
		fn := findFunc(p, "consumed")
		if n := countCallDirect(fn.Ops, "__drop_arr_struct_P"); n != 2 {
			t.Errorf("ptrW=%d: consumed emitted %d deep array drops, want 2 — the "+
				"receiver's reference went to __fern_arr_cow_inplace, so releasing "+
				"its slot as well frees the buffer the result points at; ops:\n%s",
				ptrW, n, p)
		}
	}
}

// A SCALAR element array is the same bug with nothing left to corrupt: it
// exited 0 with the right answer while FERN_LEAKCHECK reported allocs=20
// frees=39, live_bytes=-304. Nothing observable at the exit code pins it, so
// the drop count is what stops the fix being narrowed to pointer elements.
func TestWithLoopLocalConsumedScalarSkipsReinitDrop(t *testing.T) {
	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, withLoopConsumedSrc, ptrW)
		fn := findFunc(p, "consumedScalar")
		if n := countCallDirect(fn.Ops, "__fern_arr_dec"); n != 2 {
			t.Errorf("ptrW=%d: consumedScalar emitted %d array decs, want 2; ops:\n%s",
				ptrW, n, p)
		}
	}
}

// The mirror: a receiver read AFTER the call keeps its own reference (the
// #2832 pre-call inc forces the copy), so both slots owe a release and the
// re-init drop must still fire.
func TestWithLoopLocalBorrowedKeepsReinitDrop(t *testing.T) {
	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, withLoopConsumedSrc, ptrW)
		fn := findFunc(p, "borrowed")
		if n := countCallDirect(fn.Ops, "__drop_arr_struct_P"); n != 4 {
			t.Errorf("ptrW=%d: borrowed emitted %d deep array drops, want 4 — a "+
				"receiver still read after the call owns its reference and both "+
				"slots must be released; ops:\n%s", ptrW, n, p)
		}
	}
}

// The transfer has to be unconditional between one execution of the
// declaration and the next: a `.with` nested in an `if` leaves the
// iterations that skipped it holding a reference the re-init drop is the
// only site to release, so that drop stays.
func TestWithLoopLocalConditionalKeepsReinitDrop(t *testing.T) {
	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, withLoopConsumedSrc, ptrW)
		fn := findFunc(p, "conditional")
		if n := countCallDirect(fn.Ops, "__drop_arr_struct_P"); n != 3 {
			t.Errorf("ptrW=%d: conditional emitted %d deep array drops, want 3 — a "+
				"conditional transfer cannot claim the per-iteration release; ops:\n%s",
				ptrW, n, p)
		}
	}
}
