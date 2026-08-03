package ir

import (
	"testing"
)

// Regression for the #4838 CI OOM: the reused-after forced copy in
// emitArrayPush (the #4827 value-semantics fix) must NOT fire for the two
// shapes inPlacePushes exempts — a borrowed-param self-reassign
// (`acc = acc.append(v)`) and a single-occurrence RETURN-position append
// (`return acc.append(v)`). Both rebind or exit before any intra-function
// read could observe an in-place grow, so forcing the copy path there is
// pure waste — and in the self-host compiler's leak-mode accumulator
// walkers it was O(n²) arena bytes, which blew the per-module emit past
// the bump-arena ceiling (exit 137) and OOM-killed the 16 GB CI runners
// on every PR.
//
// The forced copy is a bare rc-inc/rc-dec pair bracketing the grow call.
// `retpos` / `reused` have no other rc-inc source (i32 elements need no
// element retain, and neither RETURNS the param itself — a returned borrow
// gets its own transfer inc), so a whole-function OpRcInc count pins them
// target-independently at both pointer widths.
//
// `selfp` REASSIGNS its param, so since #6021 it is a consumed-threaded
// param: computeConsumedParams promotes it and the prologue carries one
// entry retain (without which the reassignment's overwrite-dec releases a
// reference the caller never handed over — the latent double-free). That
// retain is emitted once, in the prologue; a forced copy would be emitted
// per iteration, INSIDE the loop. So the O(n²) guard for this shape counts
// rc-incs at or after the first OpLoop rather than over the whole body.
func TestAppendForcedCopyExemptions(t *testing.T) {
	src := `function retpos(acc: i32[], x: i32): i32[] {
    if (x > 0) { return acc.append(x); }
    return acc.append(0 - x);
}
function selfp(acc: i32[], n: i32): i32 {
    var i: i32 = 0;
    while (i < n) { acc = acc.append(i); i = i + 1; }
    return acc.len();
}
function reused(acc: i32[]): i32 {
    var a: i32 = acc.append(1).len();
    var b: i32 = acc.append(2).len();
    return a * 10 + b;
}
function main(): i32 { return 0; }`

	for _, ptrW := range []int{4, 8} {
		prog := lowerSourceWith(t, src, ptrW)
		// Exempt: return-position (first return's operand is NOT its last
		// occurrence — the second return's is — yet nothing intra-function
		// runs after a return).
		if n := countRcIncs(prog, "retpos"); n != 0 {
			t.Errorf("ptrW=%d: retpos emitted %d rc-incs, want 0 (return-position append force-copied — the #4838 O(n²) accumulator regression)", ptrW, n)
		}
		// Exempt: consumed-threaded param self-reassign (outside
		// isSelfArrayPushLocal's local/own scope, but the rebind means no later
		// read of the old value). The prologue entry retain is expected; nothing
		// inside the loop may be.
		if n := countRcIncsInLoop(prog, "selfp"); n != 0 {
			t.Errorf("ptrW=%d: selfp emitted %d in-loop rc-incs, want 0 (borrowed-param self-reassign append force-copied)", ptrW, n)
		}
		if n := countRcIncs(prog, "selfp"); n != 1 {
			t.Errorf("ptrW=%d: selfp emitted %d rc-incs, want 1 (the consumed-param prologue entry retain)", ptrW, n)
		}
		// Still forced: a reused-after append in expression position — the
		// #4827 bug shape. The first append must keep the copy-forcing inc.
		if n := countRcIncs(prog, "reused"); n == 0 {
			t.Errorf("ptrW=%d: reused emitted no rc-inc — the #4827 reused-after forced copy regressed", ptrW)
		}
	}
}

// countRcIncs counts the OpRcInc ops in a function — the copy-forcing
// bracket emitArrayPush emits around the grow for a reused-after operand.
func countRcIncs(p *Program, fnName string) int {
	n := 0
	for _, fn := range p.Funcs {
		if fn.Name != fnName {
			continue
		}
		for _, op := range fn.Ops {
			if op.Kind == OpRcInc {
				n++
			}
		}
	}
	return n
}

// countRcIncsInLoop counts the OpRcInc ops at or after a function's first
// OpLoop — the per-iteration copy-forcing bracket, as distinct from a
// once-per-call prologue retain.
func countRcIncsInLoop(p *Program, fnName string) int {
	n := 0
	for _, fn := range p.Funcs {
		if fn.Name != fnName {
			continue
		}
		inLoop := false
		for _, op := range fn.Ops {
			if op.Kind == OpLoop {
				inLoop = true
			}
			if inLoop && op.Kind == OpRcInc {
				n++
			}
		}
	}
	return n
}
