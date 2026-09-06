package ir

import "testing"

// `a = a.with(i, v)` on a uniquely-owned buffer must not pay a call to be
// told the buffer is its own. emitCowInplace takes the mutate-or-copy
// decision in the IR — the same `rc == 1` test __fern_arr_cow_inplace makes
// internally — so the unique path is a bounds-checked store and nothing
// else, and the helper is left on the copy arm (#8530).
//
// The mirror half is the receiver the rc analysis says is still LIVE after
// the write: it is inc'd to force the copy, so an inline uniqueness test
// could only ever fail and the call stays unguarded.

const withInplaceSrc = `function accum(n: i32): i32 {
    var buf: u8[] = [];
    var k: i32 = 0;
    while (k < 8) { buf = buf.append(0u8); k = k + 1; }
    var i: i32 = 0;
    while (i < 8) { buf = buf.with(i, (i + n) as u8); i = i + 1; }
    return buf[0] as i32;
}
function keepsOriginal(n: i32): i32 {
    var a: i32[] = [1, 2, 3];
    var b: i32[] = a.with(0, n);
    return a[0] + b[0];
}
function main(): i32 { return 0; }`

// cowCallSites returns the index of every call to a CoW helper in ops.
func cowCallSites(ops []Op) []int {
	var out []int
	for i, op := range ops {
		if op.Kind == OpCallDirect && len(op.Str) >= 22 && op.Str[:22] == "__fern_arr_cow_inplace" {
			out = append(out, i)
		}
	}
	return out
}

// onCopyArm reports whether the call at index i is the one emitCowInplace
// leaves in the else-arm of its inline uniqueness test:
//
//	is_unique ; if ; else ; load recv ; const stride ; call
func onCopyArm(ops []Op, i int) bool {
	if i < 5 {
		return false
	}
	want := []OpKind{OpRcIsUnique, OpIf, OpElse, OpLoadLocal, OpConstI32}
	for k, kind := range want {
		if ops[i-5+k].Kind != kind {
			return false
		}
	}
	return true
}

func TestWithUniqueReceiverStoresWithoutTheCowCall(t *testing.T) {
	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, withInplaceSrc, ptrW)
		fn := findFunc(p, "accum")
		if fn == nil {
			t.Fatalf("ptrW=%d: no accum in the lowered program", ptrW)
		}
		sites := cowCallSites(fn.Ops)
		if len(sites) != 1 {
			t.Fatalf("ptrW=%d: accum has %d CoW calls, want 1; ops:\n%s", ptrW, len(sites), p)
		}
		if !onCopyArm(fn.Ops, sites[0]) {
			t.Errorf("ptrW=%d: the `.with` in accum still reaches the CoW helper on the "+
				"straight-line path — the uniquely-owned accumulator is paying a call to be "+
				"handed its own buffer back; ops:\n%s", ptrW, p)
		}
	}
}

func TestWithLiveReceiverKeepsTheUnguardedCopyCall(t *testing.T) {
	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, withInplaceSrc, ptrW)
		fn := findFunc(p, "keepsOriginal")
		if fn == nil {
			t.Fatalf("ptrW=%d: no keepsOriginal in the lowered program", ptrW)
		}
		sites := cowCallSites(fn.Ops)
		if len(sites) != 1 {
			t.Fatalf("ptrW=%d: keepsOriginal has %d CoW calls, want 1; ops:\n%s", ptrW, len(sites), p)
		}
		if onCopyArm(fn.Ops, sites[0]) {
			t.Errorf("ptrW=%d: a receiver that is live after the write was inc'd to rc >= 2, "+
				"so an is_unique guard on it can only fail — guarding it costs the copy path "+
				"a test that decides nothing; ops:\n%s", ptrW, p)
		}
		// It is the pre-call retain that makes the guard pointless, so it
		// must still be there: without it the copy would not happen at all
		// and `a` would be rewritten through `b`.
		if n := countCallDirect(fn.Ops, "__fern_rc_inc"); n < 1 {
			t.Errorf("ptrW=%d: keepsOriginal does not retain the receiver before the CoW, "+
				"so the write lands in the array `a` still holds; ops:\n%s", ptrW, p)
		}
	}
}
