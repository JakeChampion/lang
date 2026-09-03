package ir

import "testing"

// #7867 class C: a fresh array temp handed to a callee that appends to its
// parameter and RETURNS the result without reassigning the parameter
// (`return xs.append(s)`) was refused by every admission — paramCountedRetain
// read false for the position, so the caller neither released the temp nor
// credited the binding of the result, and the round leaked everything.
//
// The receiver of an append is a counted occurrence: __fern_arr_push_grow
// bumps the buffer to rc 2 before handing it back in place, and the copy path
// leaves it at its incoming count. So a temp at that position is at rc 2 on
// the identity path and rc 1 on the fresh path, and the caller's unconditional
// post-call dec nets it to exactly one owner either way — no identity guard.

func TestArrayParamAppendReceiverIsCounted(t *testing.T) {
	for _, tc := range []struct {
		name, src string
	}{
		{"scalar elements", `function acc(xs: i32[], s: i32): i32[] { return xs.append(s); }
function main(): i32 { return 0; }`},
		{"string elements", `function acc(xs: string[], s: string): string[] { return xs.append(s); }
function main(): i32 { return 0; }`},
		{"chained appends", `function acc(xs: i32[], s: i32): i32[] { return xs.append(s).append(s + 1); }
function main(): i32 { return 0; }`},
		{"bound then returned", `function acc(xs: i32[], s: i32): i32[] {
    var ys: i32[] = xs.append(s);
    return ys;
}
function main(): i32 { return 0; }`},
	} {
		got := paramCountedFor(t, tc.src, "acc")
		if len(got) != 2 || !got[0] {
			t.Errorf("%s: paramCountedRetain[acc] = %v, want [true _] — the append's "+
				"receiver is a counted occurrence (in place bumps to rc 2, a copy leaves it alone)",
				tc.name, got)
		}
	}
}

func TestArrayParamAppendReceiverRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, src, why string
	}{
		{"reassigned", `function acc(xs: i32[], s: i32): i32[] { xs = xs.append(s); return xs; }
function main(): i32 { return 0; }`,
			"a reassigned parameter is the consumed-threaded class (its own protocol, #7995)"},
		{"bare return on another path", `function acc(xs: i32[], s: i32): i32[] {
    if (s < 0) { return xs; }
    return xs.append(s);
}
function main(): i32 { return 0; }`,
			"the bare `return xs` is the refused bare-parameter return"},
		{"with receiver", `function acc(xs: i32[], s: i32): i32[] { return xs.with(0, s); }
function main(): i32 { return 0; }`,
			"__fern_arr_cow_inplace hands the receiver back at rc 1, an uncounted identity"},
	} {
		got := paramCountedFor(t, tc.src, "acc")
		if len(got) == 2 && got[0] {
			t.Errorf("%s: paramCountedRetain[acc] = %v, want [false _]: %s", tc.name, got, tc.why)
		}
	}
}

// arrayReleasesIn counts the array releases `round` emits for a caller of
// `acc`, so a credited callee can be compared against the refused control
// (the same callee with a bare `return xs` on a dead path) rather than
// against an absolute count that the binding's reinit drops would inflate.
func arrayReleasesIn(t *testing.T, callee, caller string, ptrW int) int {
	t.Helper()
	p := lowerSourceWith(t, callee+"\n"+caller+"\nfunction main(): i32 { return round(1); }", ptrW)
	return countCallDirect(findFunc(p, "round").Ops, "__fern_arr_dec")
}

const appendRecvCredited = `function acc(xs: i32[], s: i32): i32[] { return xs.append(s); }`
const appendRecvRefused = `function acc(xs: i32[], s: i32): i32[] {
    if (s < -99) { return xs; }
    return xs.append(s);
}`

// The op-level effect at the call site, both halves: the fresh temp is stashed
// and released right after the call (unguarded — no pointer test, because the
// in-place path already counted the identity), and the binding of the result
// is freeEligible so it releases at exit — and, being eligible, also drops
// the slot's prior value at its declaration (emitVarReinitDropOld). Three
// more array releases than the refused control emits.
func TestAppendReceiverParamArgTempReleasedAndResultCredited(t *testing.T) {
	round := `function round(i: i32): i32 {
    var ys: i32[] = acc([], i);
    return ys.len();
}`
	for _, ptrW := range []int{4, 8} {
		credited := arrayReleasesIn(t, appendRecvCredited, round, ptrW)
		refused := arrayReleasesIn(t, appendRecvRefused, round, ptrW)
		if credited != refused+3 {
			t.Errorf("ptrW=%d: round releases %d array references against the refused "+
				"control's %d, want +3 (the `[]` temp after the call, `ys` at its "+
				"declaration and at exit)", ptrW, credited, refused)
		}
		p := lowerSourceWith(t, appendRecvCredited+"\n"+round+"\nfunction main(): i32 { return round(1); }", ptrW)
		for _, op := range findFunc(p, "round").Ops {
			if op.Kind == OpNe || op.Kind == OpIf {
				t.Errorf("ptrW=%d: the temp's release is pointer-guarded (%s) — the "+
					"in-place grow returns the temp at rc 2, so a guarded dec strands it; ops:\n%s",
					ptrW, op.Kind, p)
				break
			}
		}
	}
}

// A LIVE caller local at the position keeps value semantics by the #4873
// bracket (the callee's grow copies at rc 2), so the result is fresh and the
// binding is creditable; the local itself is not a temp and is not released
// here — `a`'s two releases more than the control, no temp release.
func TestAppendReceiverParamLiveLocalResultCredited(t *testing.T) {
	round := `function round(i: i32): i32 {
    var g: i32[] = [1, 2, 3];
    var a: i32[] = acc(g, i);
    return g.len() + a.len();
}`
	credited := arrayReleasesIn(t, appendRecvCredited, round, 8)
	refused := arrayReleasesIn(t, appendRecvRefused, round, 8)
	if credited != refused+2 {
		t.Errorf("round releases %d array references against the refused control's %d, "+
			"want +2 (`a` at its declaration and at exit; `g` is a live local, not a temp)",
			credited, refused)
	}
	p := lowerSourceWith(t, appendRecvCredited+"\n"+round+"\nfunction main(): i32 { return round(1); }", 8)
	if n := countCallDirect(findFunc(p, "round").Ops, "__fern_rc_inc"); n != 1 {
		t.Errorf("round brackets g across the call with %d incs, want 1; ops:\n%s", n, p)
	}
}

// A chain's intermediate is an owned temp the outer append consumes: released
// after the outer grow, where before it was stranded at rc 2 (in place) or
// rc 1 (copied) with nothing naming it.
func TestAppendChainReleasesTheIntermediate(t *testing.T) {
	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, `function acc(xs: i32[], s: i32): i32[] { return xs.append(s).append(s + 1); }
function main(): i32 { return acc([], 1).len(); }`, ptrW)
		if n := countCallDirect(findFunc(p, "acc").Ops, "__fern_arr_dec"); n != 1 {
			t.Errorf("ptrW=%d: acc releases %d array references, want 1 (the inner append's "+
				"result, consumed by the outer append); ops:\n%s", ptrW, n, p)
		}
		p = lowerSourceWith(t, `function acc(xs: string[], s: string): string[] { return xs.append(s).append(s + "!"); }
function main(): i32 { return acc([], "a").len(); }`, ptrW)
		if n := countCallDirect(findFunc(p, "acc").Ops, "__fern_drop_arr_str"); n != 1 {
			t.Errorf("ptrW=%d: acc releases %d string-array references, want 1 — the "+
				"outer grow's copy path retains the elements, so the deep drop is right; ops:\n%s",
				ptrW, n, p)
		}
	}
}
