package ir_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// A move claimed textually after a `?` is claimed for the whole function, but
// the `?` lowering leaves through the owned-local dec sweep — which skips
// locals marked moved — so on the Err path the local was neither moved nor
// released (#8442). Both move kinds the dominance guard admits are pinned: the
// bare-ident alias (computeMovedLocals' sawReturn gate) and the `own` argument
// (walkDominatingExprs). Each has a control with the move BEFORE the `?`,
// which does dominate every exit and must keep its claim — a predicate that
// refused every function containing a `?` would pass the first half forever.
func TestMoveAfterTryOpIsNotClaimed(t *testing.T) {
	dumps := map[string]string{}
	ir.RcPlanHook = func(fn, dump string) { dumps[fn] = dump }
	defer func() { ir.RcPlanHook = nil }()

	lowerForTest(t, `function take(own a: i32[]): i32 { return a[0]; }
function g(c: i32): Result[i32, i32] {
	if (c == 0) { return Err(7); }
	return Ok(c * 2);
}
function aliased(c: i32): Result[i32, i32] {
	var x: i32[] = [1, 2, 3];
	var r: i32 = g(c)?;
	var y: i32[] = x;
	return Ok(y[0] + r);
}
function aliased_before(c: i32): Result[i32, i32] {
	var x: i32[] = [1, 2, 3];
	var y: i32[] = x;
	var r: i32 = g(c)?;
	return Ok(y[0] + r);
}
function owned(own a: i32[], c: i32): Result[i32, i32] {
	var r: i32 = g(c)?;
	return Ok(take(a) + r);
}
function owned_before(own a: i32[], c: i32): Result[i32, i32] {
	var t: i32 = take(a);
	var r: i32 = g(c)?;
	return Ok(t + r);
}
function main(): i32 {
	var acc: i32 = 0;
	match (aliased(0)) { Ok(v) => { acc = acc + v; }, Err(e) => { acc = acc + e; } }
	match (aliased_before(0)) { Ok(v) => { acc = acc + v; }, Err(e) => { acc = acc + e; } }
	match (owned([4], 0)) { Ok(v) => { acc = acc + v; }, Err(e) => { acc = acc + e; } }
	match (owned_before([4], 0)) { Ok(v) => { acc = acc + v; }, Err(e) => { acc = acc + e; } }
	return acc;
}`)

	for _, tc := range []struct {
		fn, local string
		claimed   bool
	}{
		{"aliased", "x", false},
		{"aliased_before", "x", true},
		{"owned", "a", false},
		{"owned_before", "a", true},
	} {
		got, ok := dumps[tc.fn]
		if !ok {
			t.Errorf("%s: RcPlanHook never fired", tc.fn)
			continue
		}
		has := strings.Contains(got, "movedLocals: "+tc.local)
		switch {
		case has && !tc.claimed:
			t.Errorf("%s claims `%s` moved, but the move sits after a `?` whose Err path "+
				"leaves through a sweep that skips moved locals — neither moved nor released; dump:\n%s",
				tc.fn, tc.local, got)
		case !has && tc.claimed:
			t.Errorf("%s does NOT claim `%s` moved, but the move runs before the `?` and so "+
				"dominates every exit; dump:\n%s", tc.fn, tc.local, got)
		}
	}
}
