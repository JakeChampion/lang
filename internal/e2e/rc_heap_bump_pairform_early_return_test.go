package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// A PAIR-FORM match scrutinee's payload release was emitted only after the arm
// body, so an arm that LEFT the match early — `match (mk()) { Some(s) => {
// return s.len(); } … }`, the ordinary early-return spelling — branched
// straight past it and the payload leaked (#5339).
//
// The release is registered as a pending scrutinee drop now, so every exit path
// replays it (emitRcDecLocalsAtExitExcept), exactly as the heap-form box
// release already was.
//
// Scope, measured rather than assumed. This is the PAIR-FORM path only:
// Option[string] and Option[i32[]] are pair-form-eligible on the natives and
// are what these cases exercise. A mixed-shape `Result[string, i32]` and a
// three-variant user enum are NOT pair-form (isPairFormEligible), take the
// heap-box scrutinee path, and still leak — identically whether the arm returns
// or falls through, so that is a separate gap and not this one. The sibling
// file rc_heap_bump_match_scrutinee_test.go owns the box path and says so.

// Each source is SELF-CHECKING and returns 0 when flat: ten times the rounds
// must not cost more fresh memory. Returning the byte delta as the exit code
// cannot work here — an exit status is masked to 0..255, and a 256-byte delta
// reads as 0, which is how a leak would look like a pass.
func pairFormEarlyReturnBody(take string) string {
	return take + `
function main(): i32 {
    var b0: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 200) { acc = acc + take(i % 3); i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) { acc = acc + take(j % 3); j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (acc < 0) { return 97; }
    if ((b2 - b1) > (b1 - b0)) { return 98; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`
}

// Option[string] — pair-form on the natives' single-word string ABI.
var pairFormEarlyReturnStr = pairFormEarlyReturnBody(`function tag(v: i32): string { if (v == 0) { return "aa"; } if (v == 1) { return "bb"; } return "cc"; }
function mk(v: i32): Option[string] { if (v < 0) { return None; } return Some("pf-payload-" + tag(v)); }
function take(v: i32): i32 { match (mk(v)) { Some(s) => { return s.len(); }, None => { return 0; } } return 0; }`)

// Option[i32[]] — pair-form on every backend, so this is the case the wasm leg
// can carry (a two-word string payload is not pair-form there).
var pairFormEarlyReturnArr = pairFormEarlyReturnBody(`function mk(v: i32): Option[i32[]] { if (v < 0) { return None; } return Some([v, v + 1, v + 2]); }
function take(v: i32): i32 { match (mk(v)) { Some(a) => { return a.len(); }, None => { return 0; } } return 0; }`)

// The arm never MENTIONS the binding, so pairFormPayloadConfined passes
// vacuously. That is the case which falsifies the justification the old code
// carried — "a `return` inside the body … would have escaped anyway
// (pairFormPayloadConfined rejects a body that mentions the name in a return)".
// Confinement excuses an unused binding, so admission and a returning arm
// coexist and the payload was simply stranded.
var pairFormEarlyReturnUnused = pairFormEarlyReturnBody(`function mk(v: i32): Option[i32[]] { if (v < 0) { return None; } return Some([v, v + 1, v + 2]); }
function take(v: i32): i32 { match (mk(v)) { Some(a) => { return 1; }, None => { return 0; } } return 0; }`)

// CRITICAL soundness: `wrap` returns its PARAMETER inside the variant, so the
// payload is not fresh — returnsNoParamEscape is false and the reclaim must be
// refused outright. `b` is re-read after the loop, so releasing it would be a
// use-after-free rather than a byte count. 200 * 14 = 2800; b.len() == 14.
const pairFormReturningArmAliasedSafe = `function wrap(s: string): Option[string] { return Some(s); }
function take(s: string): i32 { match (wrap(s)) { Some(t) => { return t.len(); }, None => { return 0; } } return 0; }
function main(): i32 {
    var b: string = "shared-" + "payload";
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 200) {
        acc = acc + take(b);
        i = i + 1;
    }
    if (acc != 2800) { return 99; }
    if (b.len() != 14) { return 88; }
    return __rc_underflow_count();
}`

func checkPairFormReturningArmSafe(t *testing.T, run func(*testing.T, string) (string, int)) {
	t.Helper()
	if _, code := run(t, pairFormReturningArmAliasedSafe); code != 0 {
		t.Errorf("aliased pair-form payload: code=%d (99=value, 88=UAF/freed-early, >0=over-release)", code)
	}
}

var pairFormEarlyReturnNative = []string{pairFormEarlyReturnStr, pairFormEarlyReturnArr, pairFormEarlyReturnUnused}

func TestX86_64PairFormReturningArmReclaim(t *testing.T) {
	for _, src := range pairFormEarlyReturnNative {
		if _, code := compileAndRunX86_64FreeOn(t, src); code != 0 {
			t.Errorf("pair-form early-return payload: code=%d (98=grows, 99=over-release, 97=value)", code)
		}
	}
	checkPairFormReturningArmSafe(t, compileAndRunX86_64FreeOn)
}

func TestArm64PairFormReturningArmReclaim(t *testing.T) {
	// A string payload is not pair-form under arm64's two-word string ABI
	// (isPairFormPayloadShape), so it takes the heap-box path there and
	// carries no signal for this change; only the array sources run.
	for _, src := range []string{pairFormEarlyReturnArr, pairFormEarlyReturnUnused} {
		if _, code := compileAndRunArm64FreeOn(t, src); code != 0 {
			t.Errorf("pair-form early-return payload: code=%d (98=grows, 99=over-release, 97=value)", code)
		}
	}
}

func TestWASMPairFormReturningArmReclaim(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	for _, src := range []string{pairFormEarlyReturnArr, pairFormEarlyReturnUnused} {
		if got := runWasm(t, src); got != 0 {
			t.Errorf("pair-form early-return payload: code=%d (98=grows, 99=over-release, 97=value)", got)
		}
	}
}
