package ir

import "testing"

// The RESULT of `xs.with(i, v)` on a BORROWED receiver is a fresh buffer this
// frame owns, and it must be freeEligible (#9299).
//
// computeFreeEligible's `__method_Array_set` arm reads the result as an alias
// of the receiver, which is true only on __fern_arr_cow_inplace's rc == 1 arm.
// computeArraySetIncs incs a borrowed receiver precisely to make that arm
// unreachable — emitArraySet says so where it skips the inline rc test, "just
// inc'd above: rc >= 2, so the helper's rc==1 arm is unreachable" — and the
// copy path allocates a fresh rc 1 buffer and decs the receiver back, so "the
// balance is: receiver keeps rc 1, the fresh copy is rc 1". The frame owns the
// copy outright and nothing was releasing it: one whole buffer stranded per
// call, unbounded. Measured at 256 rounds of a 1024-element array,
// `FERN_LEAKCHECK` read allocs=2816 frees=2560 live_bytes=1576960 against
// 2816/2816/0 after.
//
// The two verdicts are one predicate (arraySetReceiverBorrowed) because they
// have to agree: the credit here is sound only where the inc fired. Each case
// asserts BOTH, so a change that moves one and not the other fails.
func TestWithOnBorrowedReceiverYieldsFreeEligibleResult(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		// pos is the `.with` call's "line:col" in the rcPlan dump.
		pos string
		// forcedInc is computeArraySetIncs' verdict, and eligible is
		// computeFreeEligible's for the result binding `w`. They move
		// together: a receiver whose inc is NOT forced may get the receiver
		// itself back, so crediting the result would double-free it.
		forcedInc bool
		eligible  bool
	}{
		{
			// The filed shape: a callee that borrows an array parameter and
			// binds the updated copy.
			name: "borrowed array param receiver",
			src: `function probe(xs: i32[]): i32 {
    var w: i32[] = xs.with(0, 99);
    return w[0];
}
function main(): i32 { var a: i32[] = [1, 2, 3]; return probe(a); }`,
			pos: "2:27", forcedInc: true, eligible: true,
		},
		{
			// A non-consuming match binding reads the box's payload in
			// place, so it is a borrow on the same terms — the other half
			// of arraySetReceiverBorrowed.
			name: "borrowed match binding receiver",
			src: `function probe(o: Option[i32[]]): i32 {
    match (o) {
        Some(xs) => {
            var w: i32[] = xs.with(0, 99);
            return w[0];
        },
        None => { return 0; }
    }
}
function main(): i32 { var a: i32[] = [1, 2, 3]; return probe(Some(a)); }`,
			pos: "4:35", forcedInc: true, eligible: true,
		},
		{
			// THE REFUSAL. A local aliasing the borrowed param is not a
			// param and not a binding, so the inc is not forced — the
			// receiver is at its last use and cow_inplace takes the rc == 1
			// arm and hands the SAME buffer back. Crediting the result here
			// would free the caller's array. The taint stays.
			name: "local aliasing a borrowed param is refused",
			src: `function probe(xs: i32[]): i32 {
    var ys: i32[] = xs;
    var w: i32[] = ys.with(0, 99);
    return w[0];
}
function main(): i32 { var a: i32[] = [1, 2, 3]; return probe(a); }`,
			pos: "3:27", forcedInc: false, eligible: false,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			dumps := map[string]string{}
			RcPlanHook = func(fn, dump string) { dumps[fn] = dump }
			defer func() { RcPlanHook = nil }()
			lowerSourceWith(t, c.src, 8)
			plan := dumps["probe"]

			wantInc := c.pos + "=false"
			if c.forcedInc {
				wantInc = c.pos + "=true"
			}
			if !hasPlanName(plan, "arraySetInc", wantInc) {
				t.Errorf("arraySetInc is not %q — the freeEligible credit below is sound only where the inc forces the COPY path; plan:\n%s",
					wantInc, plan)
			}
			got := hasPlanName(plan, "freeEligible", "w")
			if got != c.eligible {
				if c.eligible {
					t.Errorf("the `.with` result w is not freeEligible — the fresh copy is stranded, one buffer per call; plan:\n%s", plan)
				} else {
					t.Errorf("the `.with` result w is freeEligible, but the receiver's inc was not forced — cow_inplace hands back the receiver itself, so this frees a buffer it does not own; plan:\n%s", plan)
				}
			}
		})
	}
}
