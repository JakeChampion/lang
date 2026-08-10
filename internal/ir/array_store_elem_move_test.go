package ir

import "testing"

// `xs.append(v)` / `xs.with(i, v)` store their element under the same
// `needsRcIncOnAlias && !moveSites` retain the container literals use, and
// computeFreeEligible's Array_push / Array_set arms taint a direct-Ident
// element on the stated grounds that "the moveSites shapes transfer instead of
// inc'ing". markConstructionMoves never marked those elements, so the
// assumption did not hold: the element took BOTH the retain and the taint, and
// the taint suppressed the release (emitVarReinitDropOld / the exit sweep)
// that would have balanced it.
//
// Bound-vs-inline was the discriminator. Inline (`xs.append(Val { … })`) names
// no local, takes no retain, and was already flat; hoisting the identical value
// into a local first leaked one box per push — 320 B a round with no plateau in
// a loop, and 16 B a round from straight-line code where the array happened to
// be declared BEFORE the element (declaration order alone flipped it, because
// the exit sweep runs in declaration order and the element's flat dec landing
// after the buffer's deep drop never reaches zero).
//
// The assertions compare the bound form's retain count against the inline
// form's: the move is exactly the claim that the two lower to the same rc
// traffic.

const boundPushSrc = `struct Val { kind: i32, kids: i32[] }
function build(n: i32): i32 {
    var vals: Val[] = [];
    var total: i32 = 0;
    for i in 0..n {
        var v = Val { kind: i, kids: [] };
        vals = vals.append(v);
        total = total + vals.len();
    }
    return total;
}`

const inlinePushSrc = `struct Val { kind: i32, kids: i32[] }
function build(n: i32): i32 {
    var vals: Val[] = [];
    var total: i32 = 0;
    for i in 0..n {
        vals = vals.append(Val { kind: i, kids: [] });
        total = total + vals.len();
    }
    return total;
}`

// The push nested in a struct literal's field — `d = Doc { ...d, vals:
// d.vals.append(v) }`, the shape an immutable accumulator threads through a
// loop. markConstructionMoves only ever looked one level down, so the nested
// push was invisible to it even after the push arm existed.
const nestedPushSrc = `struct Val { kind: i32, kids: i32[] }
struct Doc { vals: Val[], root: i32 }
function build(n: i32): i32 {
    var d = Doc { vals: [], root: 0 };
    var total: i32 = 0;
    for i in 0..n {
        var v = Val { kind: i, kids: [] };
        d = Doc { ...d, vals: d.vals.append(v) };
        total = total + d.vals.len();
    }
    return total;
}`

const nestedInlinePushSrc = `struct Val { kind: i32, kids: i32[] }
struct Doc { vals: Val[], root: i32 }
function build(n: i32): i32 {
    var d = Doc { vals: [], root: 0 };
    var total: i32 = 0;
    for i in 0..n {
        d = Doc { ...d, vals: d.vals.append(Val { kind: i, kids: [] }) };
        total = total + d.vals.len();
    }
    return total;
}`

func TestArrayPushBoundElemMovesLikeInline(t *testing.T) {
	for _, tc := range []struct{ name, bound, inline string }{
		{"direct", boundPushSrc, inlinePushSrc},
		{"nested in struct literal", nestedPushSrc, nestedInlinePushSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bound, inline := lowerSource(t, tc.bound), lowerSource(t, tc.inline)
			if findFunc(bound, "build") == nil || findFunc(inline, "build") == nil {
				t.Fatal("build not lowered")
			}
			if got, want := countRcIncs(bound, "build"), countRcIncs(inline, "build"); got != want {
				t.Errorf("bound form emits %d rc.inc, inline form %d — the bound "+
					"element took a retain the inline one does not, and its "+
					"escape taint suppresses the matching release", got, want)
			}
		})
	}
}

// The move must not fire for a local declared OUTSIDE the loop: it lives across
// iterations, so transferring its single reference into the first iteration's
// buffer would let that buffer's drop free a value the next iteration reads —
// a use-after-free, not a leak. markLoopBodyConstructionMoves' `allow` gate is
// what holds this, and the push arm rides on it.
func TestArrayPushOuterLocalElemNotMoved(t *testing.T) {
	src := `struct Val { kind: i32, kids: i32[] }
function build(n: i32): i32 {
    var vals: Val[] = [];
    var v = Val { kind: 7, kids: [] };
    var total: i32 = 0;
    for i in 0..n {
        vals = vals.append(v);
        total = total + vals.len() + v.kind;
    }
    return total;
}`
	p := lowerSource(t, src)
	if findFunc(p, "build") == nil {
		t.Fatal("build not lowered")
	}
	if countRcIncs(p, "build") == 0 {
		t.Error("no rc.inc: a loop-carried local pushed every iteration must be " +
			"retained per push, not moved into the first buffer")
	}
}
