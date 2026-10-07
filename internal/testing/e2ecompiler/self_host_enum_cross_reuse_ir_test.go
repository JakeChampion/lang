package e2ecompiler

import (
	"testing"
)

// General enum->enum cross-local box reuse (Perceus FBIP): a `let c = V([..])`
// construction of a uniform-layout array-payload enum reuses the heap box of an
// EARLIER, same-enum donor `a = W([..])` that is DEAD, non-escaping, and not
// aliased by the construction site. Instead of allocating a fresh box for c,
// the reuse RELEASES a's old payload arrays, re-shapes
// a's box to V in place, writes V's fresh array payloads into it, binds c to it, and
// zeroes a's slot — so the box is freed exactly once, via c. Net: one fewer alloc +
// one fewer free per such construction.
//
// Each case embeds a value check (returns 90/91/99 on mismatch) and then returns
// __rc_underflow_count() — so want=0 means BOTH the reused value is correct AND no
// over-release occurred. A mis-balanced old-payload release or a bad reshape would
// double-free (detector > 0); a mis-freed donor payload poisoning the recycled buffer
// would surface as a wrong value (especially the -probe case that allocs after reuse).
var enumCrossReuseIRCases = []struct {
	name string
	src  string
	want int
}{
	// FIRES + value: dead donor `a = A([1,2])` (used once via a wildcard match, then
	// dead) reused for `c = B([3,4])`. c's payload [3,4] reads back through the box
	// (v = 3+4 = 7); a was variant A (t = 5). 5 + 7 = 12.
	{"fires-value", `enum E { A(i32[]), B(i32[]) }
function f(): i32 { let a: E = A([1, 2]); let t: i32 = 0; match (a) { A(_) => { t = 5; }, B(_) => { t = 6; } } let c: E = B([3, 4]); let v: i32 = 0; match (c) { A(w) => { v = w[0]; }, B(w) => { v = w[0] + w[1]; } } return t + v; }
function main(): i32 { return f(); }`, 12},
	// FIRES + detector (THE soundness gate): same reuse, assert value then read the
	// over-release detector. A mis-balanced old-payload release / bad reshape would
	// double-free -> detector > 0.
	{"fires-detector", `enum E { A(i32[]), B(i32[]) }
function f(): i32 { let a: E = A([1, 2]); let t: i32 = 0; match (a) { A(_) => { t = 5; }, B(_) => { t = 6; } } let c: E = B([3, 4]); let v: i32 = 0; match (c) { A(w) => { v = w[0]; }, B(w) => { v = w[0] + w[1]; } } if (t + v != 12) { return 99; } return __rc_underflow_count(); }
function main(): i32 { return f(); }`, 0},
	// CORRUPTION PROBE: a FRESH array allocated AFTER the reuse must read back intact
	// — a mis-freed donor payload (or a double-free recycling the block early) would
	// poison the recycled buffer. fresh = [11,22,33] -> 66; value still 12.
	{"corruption-probe-detector", `enum E { A(i32[]), B(i32[]) }
function f(): i32 { let a: E = A([1, 2]); let t: i32 = 0; match (a) { A(_) => { t = 5; }, B(_) => { t = 6; } } let c: E = B([3, 4]); let fresh: i32[] = [11, 22, 33]; let fs: i32 = fresh[0] + fresh[1] + fresh[2]; let v: i32 = 0; match (c) { A(w) => { v = w[0]; }, B(w) => { v = w[0] + w[1]; } } if (t + v != 12) { return 90; } if (fs != 66) { return 91; } return __rc_underflow_count(); }
function main(): i32 { return f(); }`, 0},
	// DONOR LIVE -> NO REUSE: the donor `a` is used AFTER c's construction (its
	// wildcard match sits after c), so a is NOT dead at c and the reuse must NOT fire.
	// The value stays correct via the normal fresh-alloc path; detector 0. t=3 (A),
	// v=8 (B) -> 11.
	{"donor-live-no-reuse-detector", `enum E { A(i32[]), B(i32[]) }
function f(): i32 { let a: E = A([1, 2]); let c: E = B([3, 4]); let t: i32 = 0; match (a) { A(_) => { t = 3; }, B(_) => { t = 4; } } let v: i32 = 0; match (c) { A(_) => { v = 7; }, B(_) => { v = 8; } } if (t + v != 11) { return 99; } return __rc_underflow_count(); }
function main(): i32 { return f(); }`, 0},
	// RAGGED ENUM -> NO REUSE: variants have differing field counts (A:1, B:2), so the
	// box is NOT uniform-layout (enum_all_variants_same_field_count fails) and the
	// reshape would be size-unsafe — reuse must NOT fire (falls back to fresh alloc).
	// Value correct, detector 0. t=5 (A), v=3+6=9 -> 14.
	{"ragged-no-reuse-detector", `enum E { A(i32[]), B(i32[], i32[]) }
function f(): i32 { let a: E = A([1, 2]); let t: i32 = 0; match (a) { A(_) => { t = 5; }, B(_, _) => { t = 6; } } let c: E = B([3, 4], [5, 6]); let v: i32 = 0; match (c) { A(w) => { v = w[0]; }, B(w, x) => { v = w[0] + x[1]; } } if (t + v != 14) { return 99; } return __rc_underflow_count(); }
function main(): i32 { return f(); }`, 0},
}

// TestSelfHostEnumCrossReuseIR runs each case through the self-host CLI on
// x86-64, asserting the embedded value check plus __rc_underflow_count() == 0.
func TestSelfHostEnumCrossReuseIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range enumCrossReuseIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src
			for _, target := range []string{"x86-64-linux"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
