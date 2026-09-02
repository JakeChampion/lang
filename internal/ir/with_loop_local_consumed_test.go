package ir

import (
	"strings"
	"testing"
)

// A loop-body `var` whose LAST use is a `.with` receiver has its reference
// taken over by __fern_arr_cow_inplace, so the slot must hold nothing when
// the next iteration re-declares it: releasing it again freed the buffer
// the RESULT still pointed at — SIGSEGV on both natives for a struct-element
// array and a silent double free for a scalar one (#6877).
//
// The consuming site NULLS the receiver's slot once the value is on the
// operand stack (emitArraySet, arraySetConsumedSites), so the re-init drop
// and the exit sweep are emitted for it like any other local and meet a
// null — while a path that never reaches the `.with` still releases what
// the slot holds. The assertions pin the null store at the consuming site,
// and its absence on a receiver that is read again.

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

// cowReceiverNulled reports whether every __fern_arr_cow_inplace* call in fn
// is preceded by a null store into the slot the receiver was just loaded
// from: `local.load S; const.i32 0; local.store S; const.i32 stride; call`.
// A function with no cow call reports false.
func cowReceiverNulled(fn *Func) bool {
	found := false
	for i, op := range fn.Ops {
		if op.Kind != OpCallDirect || !strings.HasPrefix(op.Str, "__fern_arr_cow_inplace") {
			continue
		}
		found = true
		if i < 4 || fn.Ops[i-1].Kind != OpConstI32 ||
			fn.Ops[i-2].Kind != OpStoreLocal || fn.Ops[i-3].Kind != OpConstI32 || fn.Ops[i-3].I32 != 0 ||
			fn.Ops[i-4].Kind != OpLoadLocal || fn.Ops[i-4].I32 != fn.Ops[i-2].I32 {
			return false
		}
	}
	return found
}

// `it` is transferred into the `.with`: its slot is nulled at the call, and
// both locals keep the same release sites the borrowed twin has (one re-init
// drop and one exit sweep each) — the null is what makes `it`'s inert.
func TestWithLoopLocalConsumedNullsReceiver(t *testing.T) {
	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, withLoopConsumedSrc, ptrW)
		fn := findFunc(p, "consumed")
		if !cowReceiverNulled(fn) {
			t.Errorf("ptrW=%d: consumed does not null the `.with` receiver's slot at the "+
				"consuming site — releasing it again frees the buffer the result points at; ops:\n%s",
				ptrW, p)
		}
		if n, want := countCallDirect(fn.Ops, "__drop_arr_struct_P"), countCallDirect(findFunc(p, "borrowed").Ops, "__drop_arr_struct_P"); n != want {
			t.Errorf("ptrW=%d: consumed emitted %d deep array drops, want the borrowed twin's %d — every path releases what the slot holds; ops:\n%s",
				ptrW, n, want, p)
		}
	}
}

// A SCALAR element array is the same bug with nothing left to corrupt: it
// exited 0 with the right answer while FERN_LEAKCHECK reported allocs=20
// frees=39, live_bytes=-304. Nothing observable at the exit code pins it, so
// the null store is what stops the fix being narrowed to pointer elements.
func TestWithLoopLocalConsumedScalarNullsReceiver(t *testing.T) {
	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, withLoopConsumedSrc, ptrW)
		if !cowReceiverNulled(findFunc(p, "consumedScalar")) {
			t.Errorf("ptrW=%d: consumedScalar does not null the `.with` receiver's slot; ops:\n%s",
				ptrW, p)
		}
	}
}

// The mirror: a receiver read AFTER the call keeps its own reference (the
// #2832 pre-call inc forces the copy), so its slot is not nulled and both
// slots owe a release at both sites.
func TestWithLoopLocalBorrowedKeepsReinitDrop(t *testing.T) {
	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, withLoopConsumedSrc, ptrW)
		fn := findFunc(p, "borrowed")
		if cowReceiverNulled(fn) {
			t.Errorf("ptrW=%d: borrowed nulls a receiver that is read again after the call; ops:\n%s", ptrW, p)
		}
		if n := countCallDirect(fn.Ops, "__drop_arr_struct_P"); n != 4 {
			t.Errorf("ptrW=%d: borrowed emitted %d deep array drops, want 4 — a "+
				"receiver still read after the call owns its reference and both "+
				"slots must be released; ops:\n%s", ptrW, n, p)
		}
	}
}

// A `.with` nested in an `if` is still a consuming site — its null store
// sits inside the branch — and the iterations that skip it keep their
// reference for the re-init drop and the exit sweep, both of which are
// therefore emitted for `it` as for `a` (the transferring iterations meet
// the null there).
func TestWithLoopLocalConditionalKeepsReinitDrop(t *testing.T) {
	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, withLoopConsumedSrc, ptrW)
		fn := findFunc(p, "conditional")
		if !cowReceiverNulled(fn) {
			t.Errorf("ptrW=%d: conditional does not null the receiver at its consuming `.with`; ops:\n%s", ptrW, p)
		}
		if n := countCallDirect(fn.Ops, "__drop_arr_struct_P"); n != 4 {
			t.Errorf("ptrW=%d: conditional emitted %d deep array drops, want 4 — the "+
				"iterations that skip the transfer release through the re-init drop "+
				"and the exit sweep; ops:\n%s", ptrW, n, p)
		}
	}
}
