package ir

import "testing"

// The array dec-on-overwrite has to walk the old buffer's elements
// wherever that buffer still owns them. The buffer-only __fern_arr_dec
// is right for exactly one RHS shape — the self-append, whose MOVE-grow
// helper transfers the elements into the new buffer without an inc —
// and was emitted for every shape, so `a = mk()` and `a = f(a)` through
// a cowing callee each stranded one element per overwrite.
//
// The same reclaim `var a = mk()` re-executed in a loop already gets
// right (emitVarReinitDropOld routes to the deep emitOwnedSlotDrop), so
// the two spellings of one thing disagreed.

const overwriteSrc = `
function mk(pad: string): string[] {
    var o: string[] = [];
    o = o.append(pad + "-0123456789abcdef");
    return o;
}
function via_with(a: string[], v: string): string[] { return a.with(0, v); }

function plain(pad: string): i32 {
    var a: string[] = mk(pad);
    a = mk(pad);
    return a[0].len();
}
function via_call(pad: string): i32 {
    var a: string[] = mk(pad);
    a = via_with(a, pad + "-fedcba9876543210");
    return a[0].len();
}
function self_append(pad: string): i32 {
    var a: string[] = mk(pad);
    a = a.append(pad + "-fedcba9876543210");
    return a[0].len() + a[1].len();
}
// The control: same local, same producer, no overwrite at all. Its
// __fern_drop_arr_str count is the exit sweep's, which every function
// here pays, so a shape that adds one has a deep OVERWRITE release and
// a shape that matches it does not.
function no_overwrite(pad: string): i32 {
    var a: string[] = mk(pad);
    return a[0].len();
}
function main(): i32 { return 0; }`

func TestArrayOverwriteWalksElementsExceptOnTheSelfAppend(t *testing.T) {
	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, overwriteSrc, ptrW)
		base := countCallDirect(findFunc(p, "no_overwrite").Ops, "__fern_drop_arr_str")
		if base == 0 {
			t.Fatalf("ptrW=%d: the control emits no exit sweep at all, so the counts "+
				"below compare nothing; ops:\n%s", ptrW, p)
		}
		for _, fn := range []string{"plain", "via_call"} {
			f := findFunc(p, fn)
			if n := countCallDirect(f.Ops, "__fern_drop_arr_str"); n <= base {
				t.Errorf("ptrW=%d: %s emits %d __fern_drop_arr_str against the control's %d "+
					"— the superseded buffer dies owning its elements and the buffer-only "+
					"dec strands them; ops:\n%s", ptrW, fn, n, base, p)
			}
		}
		// The self-append keeps the buffer-only dec: __fern_arr_push_grow_move_ptr
		// hands the old buffer's elements to the new one WITHOUT an inc, so
		// walking them here would release references the live buffer holds.
		f := findFunc(p, "self_append")
		if n := countCallDirect(f.Ops, "__fern_drop_arr_str"); n != base {
			t.Errorf("ptrW=%d: self_append emits %d __fern_drop_arr_str against the "+
				"control's %d — the move-grow transferred the elements, so the overwrite "+
				"must not walk them; ops:\n%s", ptrW, n, base, p)
		}
		if n := countCallDirect(f.Ops, "__fern_arr_dec"); n == 0 {
			t.Errorf("ptrW=%d: self_append emits no __fern_arr_dec — the grow orphan is "+
				"never reclaimed; ops:\n%s", ptrW, p)
		}
	}
}

// The identity guard: a callee can hand the local's OWN buffer back (the
// cow's rc==1 in-place path, a grow with room to spare, an argument
// flowed through unchanged). The old and new slot values are then the
// same LIVE buffer, so only the reference the call added may go — the
// shallow dec — and the deep drop must sit behind a pointer-changed
// test. Without the else arm the shallow dec was simply dropped and the
// buffer stayed one count above zero forever, which is a bigger leak
// than the one being fixed.
func TestArraySelfReassignThroughCalleeGuardsOnIdentity(t *testing.T) {
	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, overwriteSrc, ptrW)
		f := findFunc(p, "via_call")
		var sawNe, sawElse bool
		for _, op := range f.Ops {
			switch op.Kind {
			case OpNe:
				sawNe = true
			case OpElse:
				sawElse = true
			}
		}
		if !sawNe {
			t.Errorf("ptrW=%d: via_call has no pointer-changed test before its overwrite "+
				"release; ops:\n%s", ptrW, p)
		}
		if !sawElse {
			t.Errorf("ptrW=%d: via_call has no else arm — the same-buffer case must still "+
				"release the reference the call added; ops:\n%s", ptrW, p)
		}
		if n := countCallDirect(f.Ops, "__fern_arr_dec"); n == 0 {
			t.Errorf("ptrW=%d: via_call emits no __fern_arr_dec — the same-buffer arm is "+
				"missing its shallow release; ops:\n%s", ptrW, p)
		}
	}
}

// A plain overwrite from an unrelated producer cannot alias the old
// value, so it needs no runtime test at all.
func TestArrayPlainOverwriteNeedsNoIdentityTest(t *testing.T) {
	p := lowerSourceWith(t, overwriteSrc, 8)
	f := findFunc(p, "plain")
	for _, op := range f.Ops {
		if op.Kind == OpElse {
			t.Errorf("plain emits an identity-guarded release for a RHS that cannot "+
				"return the old buffer; ops:\n%s", p)
			break
		}
	}
}
