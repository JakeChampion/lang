package ir_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Drop-guided reuse selection (ast.RcReuseDropGuided, plan E3 — see
// rc_dropguided.go). Two halves:
//
//  1. PARITY — the strategies must agree on the shapes the PLDI pairing
//     already fires (mirrored from general_reuse_test.go): the token scan
//     is a claim-order change, not an eligibility change, within one list.
//  2. THE NEW SHAPE — a donor whose last use sits INSIDE a dominated
//     non-loop arm, claimed by a later construction in the same arm. Only
//     drop-guided pairs it; the pins below lock both directions (ON fires,
//     OFF does not) plus the conservative rejections (sibling-arm ref, use
//     after the statement, loop arms, match-scrutinee bindings).

func withDropGuided(t *testing.T, fn func()) {
	t.Helper()
	prev := ast.RcReuseDropGuided
	ast.RcReuseDropGuided = true
	defer func() { ast.RcReuseDropGuided = prev }()
	fn()
}

// reuseCountDG lowers src with the flag ON and returns main's
// __alloc_reuse count.
func reuseCountDG(t *testing.T, src string) int {
	t.Helper()
	n := -1
	withDropGuided(t, func() {
		f := funcByName(lowerForTest(t, src), "main")
		if f == nil {
			t.Fatal("no func main")
		}
		n = allocReuseCount(f)
	})
	return n
}

// --- Parity mirrors (same expectations as the flag-off tests) ---

func TestDropGuidedParityDeadLocal(t *testing.T) {
	src := `struct Point { x: i32, y: i32 }
function main(): i32 {
    var a: Point = Point { x: 1, y: 2 };
    var s: i32 = a.x + a.y;
    var b: Point = Point { x: s + 1, y: 9 };
    return b.x + b.y;
}`
	if got := reuseCountDG(t, src); got != 1 {
		t.Errorf("drop-guided should pair dead a -> b like the pairing, got %d", got)
	}
}

func TestDropGuidedParityLoopBody(t *testing.T) {
	src := `struct Point { x: i32, y: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 10) {
        var a: Point = Point { x: i, y: i + 1 };
        var s: i32 = a.x + a.y;
        var b: Point = Point { x: s, y: i };
        acc = acc + b.x + b.y;
        i = i + 1;
    }
    return acc;
}`
	if got := reuseCountDG(t, src); got != 1 {
		t.Errorf("drop-guided loop-body pairing should fire once, got %d", got)
	}
}

func TestDropGuidedParityCrossBlock(t *testing.T) {
	src := `struct Point { x: i32, y: i32 }
function main(): i32 {
    var a: Point = Point { x: 1, y: 2 };
    var s: i32 = a.x + a.y;
    var acc: i32 = 0;
    if (s > 0) {
        var b: Point = Point { x: s, y: 9 };
        acc = b.x + b.y;
    }
    return acc;
}`
	if got := reuseCountDG(t, src); got != 1 {
		t.Errorf("cross-block token flow should fire (dead a -> nested b), got %d", got)
	}
}

func TestDropGuidedParitySkipsLiveSource(t *testing.T) {
	src := `struct Point { x: i32, y: i32 }
function main(): i32 {
    var a: Point = Point { x: 1, y: 2 };
    var b: Point = Point { x: a.x + 1, y: 9 };
    return a.y + b.x;
}`
	if got := reuseCountDG(t, src); got != 0 {
		t.Errorf("a live at b — no token exists yet, got %d", got)
	}
}

func TestDropGuidedParityComposesLevels(t *testing.T) {
	src := `struct Point { x: i32, y: i32 }
function main(): i32 {
    var a: Point = Point { x: 1, y: 2 };
    var sa: i32 = a.x + a.y;
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 10) {
        var m: Point = Point { x: i, y: i };
        var sm: i32 = m.x + m.y;
        if (sm >= 0) {
            var c: Point = Point { x: sa + sm, y: i };
            acc = acc + c.x + c.y;
        }
        i = i + 1;
    }
    return acc;
}`
	if got := reuseCountDG(t, src); got != 2 {
		t.Errorf("expected two reuse sites (c<-m innermost, m<-a), got %d", got)
	}
}

// --- The drop-guided-only shape ---

// dgArmShapeSrc: a's LAST USE is inside the if arm, before b's construction
// in the same arm. The PLDI pairing structurally misses it (a is not
// declared in the arm's list, and the cross-block deadFrom sees a used
// inside the enclosing if), while the drop token born after `var s` flows
// straight to b.
const dgArmShapeSrc = `struct Point { x: i32, y: i32 }
function main(): i32 {
    var a: Point = Point { x: 1, y: 2 };
    var acc: i32 = 0;
    if (acc == 0) {
        var s: i32 = a.x + a.y;
        var b: Point = Point { x: s, y: 9 };
        acc = b.x + b.y;
    }
    return acc;
}`

func TestDropGuidedFiresArmDropShape(t *testing.T) {
	if got := reuseCountDG(t, dgArmShapeSrc); got != 1 {
		t.Errorf("drop-guided should pair a's in-arm drop with b, got %d", got)
	}
}

// The same source with the flag OFF must NOT fire — pins that the default
// selection is untouched by the drop-guided code paths.
func TestDropGuidedArmShapeOffBaseline(t *testing.T) {
	prev := ast.RcReuseDropGuided
	ast.RcReuseDropGuided = false
	defer func() { ast.RcReuseDropGuided = prev }()
	f := funcByName(lowerForTest(t, dgArmShapeSrc), "main")
	if got := allocReuseCount(f); got != 0 {
		t.Errorf("flag OFF must keep the PLDI pairing (misses the arm shape), got %d", got)
	}
}

// Rejects: a referenced in the SIBLING else arm — refs not confined to the
// claimed arm's prefix (conservative; the sibling is unreachable from C but
// the analysis does not prove it).
func TestDropGuidedSkipsSiblingArmRef(t *testing.T) {
	src := `struct Point { x: i32, y: i32 }
function main(): i32 {
    var a: Point = Point { x: 1, y: 2 };
    var acc: i32 = 0;
    if (acc == 0) {
        var s: i32 = a.x;
        var b: Point = Point { x: s, y: 9 };
        acc = b.x + b.y;
    } else {
        acc = a.y;
    }
    return acc;
}`
	if got := reuseCountDG(t, src); got != 0 {
		t.Errorf("sibling-arm ref must reject the token, got %d", got)
	}
}

// Rejects: a used AFTER the if — the token dies at the join it cannot
// cross (deadFrom(P, j+1) fails).
func TestDropGuidedSkipsUseAfterStatement(t *testing.T) {
	src := `struct Point { x: i32, y: i32 }
function main(): i32 {
    var a: Point = Point { x: 1, y: 2 };
    var acc: i32 = 0;
    if (acc == 0) {
        var s: i32 = a.x;
        var b: Point = Point { x: s, y: 9 };
        acc = b.x + b.y;
    }
    return acc + a.y;
}`
	if got := reuseCountDG(t, src); got != 0 {
		t.Errorf("use after the if must reject the token, got %d", got)
	}
}

// Rejects: the arm is a LOOP body — the next iteration re-reads a after a
// prior iteration's construction claimed the box. Tokens never cross a
// loop back-edge.
func TestDropGuidedSkipsLoopArm(t *testing.T) {
	src := `struct Point { x: i32, y: i32 }
function main(): i32 {
    var a: Point = Point { x: 1, y: 2 };
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 10) {
        var s: i32 = a.x + a.y;
        var b: Point = Point { x: s, y: i };
        acc = acc + b.x + b.y;
        i = i + 1;
    }
    return acc;
}`
	if got := reuseCountDG(t, src); got != 0 {
		t.Errorf("loop-body drop of an outer local must not pair, got %d", got)
	}
}

// Rejects: a is the MATCH SCRUTINEE — the arm's payload binding (xs) is a
// live uncounted view into a's box, so a construction in the arm must not
// take the box over. Scrutinee/guard refs are outside the allowed regions.
func TestDropGuidedSkipsMatchScrutinee(t *testing.T) {
	src := `enum Wrapper { Wrap(i32[]) }
function main(): i32 {
    var a: Wrapper = Wrap([1, 2]);
    var out: i32 = 0;
    match (a) {
        Wrap(xs) => {
            var b: Wrapper = Wrap([xs[0]]);
            out = match (b) { Wrap(ys) => ys[0] };
        }
    }
    return out;
}`
	if got := reuseCountDG(t, src); got != 0 {
		t.Errorf("match-scrutinee donor must not pair inside its own arm, got %d", got)
	}
}

// Fires in a MATCH arm when the donor is unrelated to the scrutinee: the
// token born after a's last use inside the arm flows to b.
func TestDropGuidedFiresMatchArmUnrelatedDonor(t *testing.T) {
	src := `enum Flag { On, Off }
struct Point { x: i32, y: i32 }
function main(): i32 {
    var a: Point = Point { x: 1, y: 2 };
    var f: Flag = On;
    var acc: i32 = 0;
    match (f) {
        On => {
            var s: i32 = a.x + a.y;
            var b: Point = Point { x: s, y: 9 };
            acc = b.x + b.y;
        },
        Off => {
            acc = 0;
        }
    }
    return acc;
}`
	if got := reuseCountDG(t, src); got != 1 {
		t.Errorf("unrelated donor dropped in a match arm should pair, got %d", got)
	}
}

// The arm shape composes with the loop precedent: a LOOP-BODY-level donor
// (re-declared each iteration) dropped inside an if arm within the loop is
// claimed by a construction later in that arm — every iteration.
func TestDropGuidedFiresArmDropInLoop(t *testing.T) {
	src := `struct Point { x: i32, y: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 10) {
        var a: Point = Point { x: i, y: i + 1 };
        if (i % 2 == 0) {
            var s: i32 = a.x + a.y;
            var b: Point = Point { x: s, y: i };
            acc = acc + b.x + b.y;
        }
        i = i + 1;
    }
    return acc;
}`
	if got := reuseCountDG(t, src); got != 1 {
		t.Errorf("loop-body donor dropped in the if arm should pair, got %d", got)
	}
}
