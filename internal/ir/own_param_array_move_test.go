package ir_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// rcOpTrace renders fn's rc-relevant calls in order, so a test can assert the
// SHAPE of the reference traffic rather than a count.
func rcOpTrace(fn *ir.Func) string {
	var parts []string
	for _, op := range fn.Ops {
		switch op.Str {
		case "__fern_arr_cow_inplace", "__fern_arr_dec", "__fern_rc_inc", "__fern_rc_dec":
			parts = append(parts, strings.TrimPrefix(op.Str, "__fern_"))
		}
	}
	return strings.Join(parts, " ")
}

func fnNamed(t *testing.T, ip *ir.Program, name string) *ir.Func {
	t.Helper()
	for _, fn := range ip.Funcs {
		if fn != nil && fn.Name == name {
			return fn
		}
	}
	t.Fatalf("no lowered function %q", name)
	return nil
}

// An `own` array param is a MOVE. Passing it to __fern_arr_cow_inplace with no
// pre-call inc hands the reference over — the helper returns the SAME pointer on
// its rc==1 branch and dec's the receiver itself on the copy branch — so the
// callee must not release it again. It used to, freeing a buffer the returned
// value still pointed at (#6013). The consuming site nulls the param's slot
// (`local.load S; const 0; local.store S` right before the cow call), so the
// exit sweep's arr_dec meets a null.
func TestOwnParamWithReceiverNulledAtConsume(t *testing.T) {
	ip := lowerForTest(t, `function wr(own buf: i32[], at: i32, w: i32): i32[] { return buf.with(at, w); }
function main(): i32 { var b: i32[] = [1, 2, 3]; b = wr(b, 0, 9); return b.len(); }`)
	wr := fnNamed(t, ip, "wr")
	if got, want := rcOpTrace(wr), "arr_cow_inplace arr_dec"; got != want {
		t.Errorf("wr rc trace = %q, want %q", got, want)
	}
	nulled := false
	for i, op := range wr.Ops {
		if op.Kind == ir.OpCallDirect && op.Str == "__fern_arr_cow_inplace" && i >= 4 &&
			wr.Ops[i-4].Kind == ir.OpLoadLocal && wr.Ops[i-3].Kind == ir.OpConstI32 && wr.Ops[i-3].I32 == 0 &&
			wr.Ops[i-2].Kind == ir.OpStoreLocal && wr.Ops[i-2].I32 == wr.Ops[i-4].I32 {
			nulled = true
		}
	}
	if !nulled {
		t.Errorf("wr does not null the own receiver's slot at the consuming `.with` — "+
			"the trailing arr_dec would then be the #6013 over-release; ops:\n%s", ip)
	}
}

// A BORROWED receiver is the contrast that proves the exclusion is keyed on
// ownership, not on `.with`: the pre-call inc forces cow_inplace down its copy
// path, so the param keeps its own reference and the exit dec is required.
func TestBorrowedParamWithReceiverStillIncsAndCopies(t *testing.T) {
	ip := lowerForTest(t, `function wr(buf: i32[], at: i32, w: i32): i32[] { return buf.with(at, w); }
function main(): i32 { var b: i32[] = [1, 2, 3]; b = wr(b, 0, 9); return b.len(); }`)
	got := rcOpTrace(fnNamed(t, ip, "wr"))
	if want := "rc_inc arr_cow_inplace"; got != want {
		t.Errorf("wr rc trace = %q, want %q (the inc is what forces the copy path)", got, want)
	}
}

// Caller side of the same move: `c = wr(c, …)` gives the callee the slot's only
// reference, so the assignment's overwrite-drop of the old `c` has nothing left
// to release. Emitting __fern_arr_dec there takes the live buffer the callee
// just returned from rc 1 to 0.
//
// Asserted on the WINDOW between the call and the store that consumes it — the
// overwrite drop is emitted exactly there. A whole-function dec count cannot see
// it: `step` legitimately holds two other decs (the var-decl re-init drop, a
// NULL no-op on a once-run `var`, and the exit sweep of the reference `c` ends up
// owning), and both survive the fix.
func decsBetweenCallAndStore(t *testing.T, fn *ir.Func, callee string) []string {
	t.Helper()
	for i, op := range fn.Ops {
		if op.Kind != ir.OpCallDirect || op.Str != callee {
			continue
		}
		// A self-reassigning call's overwrite release sits behind an
		// identity guard (#7914: a callee can hand the local's own buffer
		// back). The result is stashed first, so step over the stash to
		// keep the window this helper has always meant — the ops between
		// the call and the store that lands the value.
		j := i + 1
		if j+4 < len(fn.Ops) && fn.Ops[j].Kind == ir.OpStoreLocal &&
			fn.Ops[j+1].Kind == ir.OpLoadLocal && fn.Ops[j+2].Kind == ir.OpLoadLocal &&
			fn.Ops[j+3].Kind == ir.OpNe && fn.Ops[j+4].Kind == ir.OpIf {
			j += 5
		}
		var decs []string
		for _, after := range fn.Ops[j:] {
			if after.Kind == ir.OpStoreLocal {
				return decs
			}
			if strings.HasSuffix(after.Str, "_dec") {
				decs = append(decs, after.Str)
			}
		}
		t.Fatalf("no store consuming the result of %q", callee)
	}
	t.Fatalf("no call to %q in %s", callee, fn.Name)
	return nil
}

func TestSelfReassignIntoOwnParamSkipsOverwriteDrop(t *testing.T) {
	ip := lowerForTest(t, `function wr(own buf: i32[], at: i32, w: i32): i32[] { return buf.with(at, w); }
function step(): i32 {
	var c: i32[] = [1, 2, 3];
	c = wr(c, 0, 9);
	return c.len();
}
function main(): i32 { return step(); }`)
	if got := decsBetweenCallAndStore(t, fnNamed(t, ip, "step"), "wr"); len(got) != 0 {
		t.Errorf("overwrite drop %v emitted after `c = wr(c, …)`, but the reference\n"+
			"moved into wr's `own` param — dropping it here double-releases (#6013)", got)
	}
}

// A self-reassign whose result comes from a plain (non-own) callee still needs
// the overwrite drop: nothing was moved out, so the old buffer's reference is
// this slot's to release. This is the contrast that keeps the fix from becoming
// "never drop after a self-reassigning call", which would leak.
func TestSelfReassignFromBorrowingCalleeKeepsOverwriteDrop(t *testing.T) {
	ip := lowerForTest(t, `function wr(buf: i32[], at: i32, w: i32): i32[] { return buf.with(at, w); }
function step(): i32 {
	var c: i32[] = [1, 2, 3];
	c = wr(c, 0, 9);
	return c.len();
}
function main(): i32 { return step(); }`)
	if got := decsBetweenCallAndStore(t, fnNamed(t, ip, "step"), "wr"); len(got) == 0 {
		t.Error("no overwrite drop after `c = wr(c, …)`: the callee only borrowed, so\n" +
			"the old buffer's reference is this slot's to release or it leaks")
	}
}
