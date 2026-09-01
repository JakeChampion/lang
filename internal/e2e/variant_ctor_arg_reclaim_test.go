package e2e

import (
	"strconv"
	"testing"
)

// A VARIANT CONSTRUCTOR at a call-argument position (`sink(E.A(mk(...)))`)
// allocates a fresh rc=1 box exactly as a struct or tuple literal does, but
// `freshOwnedRcTempType` classified only the literal node kinds — a variant
// construction is spelled as a Call, and that switch excludes Calls because a
// method call can alias its receiver. So the box AND everything under it was
// stranded on every call, while the same value bound to a `var` first, and the
// struct and tuple literals in the identical position, were all reclaimed
// (#7867 slice 3, half two).
//
// Measured on the reported shape: 200 allocs / 0 frees / 8000 live bytes over
// 100 rounds, 400 / 0 / 16000 over 200 — two blocks per round (the box and its
// string payload), linear and unbounded.
//
// Each case is loop-resident so a leak scales with the round count, and is
// measured at three counts: a per-round leak is what one count cannot tell
// apart from a fixed startup cost. The exit code folds in
// `__rc_underflow_count()` because the census is structurally blind to an
// OVER-release — allocs == frees at live_bytes 0 is also what a double free
// into the freelist looks like, and over-release is the real hazard of
// admitting a new shape to a deep drop.
//
// `@noinline` is on both the callee and the payload producer throughout:
// `internal/ir/inline.go` inlines a single-reference callee, and a loop call
// site lifts its size cap, so an inlined probe has no argument temp to reclaim
// and reads clean whether or not the bug is present (docs/TEST-GATES.md).
// The payload is built by concatenation from a shared left operand so it clears
// the 7-byte inline-string threshold and cannot grow in place.

const variantReclaimPrelude = `enum E { A(string), B }
@noinline
function sink(e: E): i32 {
    match (e) {
        E.A(s) => { return s.len(); },
        E.B => { return 0; },
    }
}
@noinline
function mkpayload(base: string): string { return base + "-payloadpayload"; }
`

// variantCtorArgSrc: the reported shape — a fresh variant construction handed
// straight to a borrowing callee.
func variantCtorArgSrc(rounds int) string {
	return variantReclaimPrelude + `
function round(i: i32): i32 {
    var base: string = "shared-left-operand";
    return sink(E.A(mkpayload(base)));
}
` + variantChurnMain(rounds, rounds*34)
}

// variantCtorArgLivePayloadSrc: the payload is a local that is READ AFTER the
// call, so no move applies and the construction's alias inc is what keeps it
// alive. If the new reclaim released the payload the callee borrowed, this
// reads freed memory — caught by the value check and the underflow counter.
func variantCtorArgLivePayloadSrc(rounds int) string {
	return variantReclaimPrelude + `
function round(i: i32): i32 {
    var base: string = "shared-left-operand";
    var live: string = mkpayload(base);
    var n: i32 = sink(E.A(live));
    return n + live.len() - 34;
}
` + variantChurnMain(rounds, rounds*34)
}

// variantCtorReturnedSrc: the callee HANDS THE ARGUMENT BACK (`keepf(e) -> e`).
// The return-transfer inc puts the box at rc >= 2, so the caller's
// is_unique-gated drop must decrement rather than free — the shape that decides
// whether admitting variant constructions is sound at all.
func variantCtorReturnedSrc(rounds int) string {
	return variantReclaimPrelude + `
@noinline
function keepf(e: E): E { return e; }
function round(i: i32): i32 {
    var base: string = "shared-left-operand";
    var r: E = keepf(E.A(mkpayload(base)));
    match (r) {
        E.A(s) => { return s.len(); },
        E.B => { return 0; },
    }
}
` + variantChurnMain(rounds, rounds*34)
}

// variantPayloadlessArgSrc: a payloadless variant lowers to a SHARED STATIC
// sentinel rather than a fresh box (emitEnumNew), so there is nothing to
// reclaim and nothing may be dec'd as though there were — a sentinel released
// as if it were fresh corrupts a value every later construction shares.
//
// The sentinel call is paired with a payload-carrying one in the same round
// because a payloadless variant allocates nothing at all, and leakCounts
// rejects a probe whose census is empty (it cannot distinguish "nothing
// leaked" from "nothing ran"). The pairing gives the census something to
// measure and keeps the underflow counter — the assertion that actually
// carries here — meaningful.
func variantPayloadlessArgSrc(rounds int) string {
	return variantReclaimPrelude + `
function round(i: i32): i32 {
    var base: string = "shared-left-operand";
    return sink(E.B) + sink(E.A(mkpayload(base)));
}
` + variantChurnMain(rounds, rounds*34)
}

// variantCtorNestedSrc: the construction is one argument among several and its
// payload is itself a fresh call result, so the stash has to keep the operand
// order intact as well as reclaim.
func variantCtorNestedSrc(rounds int) string {
	return variantReclaimPrelude + `
@noinline
function sink2(a: i32, e: E, b: i32): i32 {
    match (e) {
        E.A(s) => { return s.len() + a + b; },
        E.B => { return a + b; },
    }
}
function round(i: i32): i32 {
    var base: string = "shared-left-operand";
    return sink2(1, E.A(mkpayload(base)), 2) - 3;
}
` + variantChurnMain(rounds, rounds*34)
}

// variantChurnMain drives `round` and returns 0 only when the accumulated value
// matches and no rc over-release was counted.
func variantChurnMain(rounds, want int) string {
	return `function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < ` + strconv.Itoa(rounds) + `) { acc = acc + round(r); r = r + 1; }
    if (acc != ` + strconv.Itoa(want) + `) { return 1; }
    if (__rc_underflow_count() != 0) { return 2; }
    return 0;
}`
}

func TestX86_64VariantCtorArgReclaim(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  func(int) string
	}{
		{"variant_ctor_at_arg", variantCtorArgSrc},
		{"variant_ctor_live_payload", variantCtorArgLivePayloadSrc},
		{"variant_ctor_returned_by_callee", variantCtorReturnedSrc},
		{"variant_payloadless_at_arg", variantPayloadlessArgSrc},
		{"variant_ctor_nested_args", variantCtorNestedSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, rounds := range []int{100, 200, 400} {
				name := tc.name + "/" + strconv.Itoa(rounds)
				allocs, frees, live := leakCounts(t, name, tc.src(rounds), 0)
				if live != 0 || allocs != frees {
					t.Errorf("%s: allocs=%d frees=%d live_bytes=%d, want a balanced census — "+
						"a fresh variant-constructor temp at an argument position must be reclaimed "+
						"once the callee has borrowed it",
						name, allocs, frees, live)
				}
			}
		})
	}
}
