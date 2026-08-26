package ir_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/modload"
)

// Cross-branch reuse sharing (#4402 opt 3a): one dead donor D feeds a
// construction in EVERY arm of a branch, not just the first the pairing
// walk reaches. Only one arm runs per pass, so the single token is claimed
// at most once.

// Both arms of an if/else construct the donor's type — both reuse D's box.
func TestCrossBranchReuseBothArmsShareDonor(t *testing.T) {
	ip := lowerForTest(t, `struct Point { x: i32, y: i32 }
function main(): i32 {
    var a: Point = Point { x: 1, y: 2 };
    var s: i32 = a.x + a.y;          // a's last use
    var acc: i32 = 0;
    if (s > 0) {
        var b: Point = Point { x: s, y: 9 };
        acc = b.x + b.y;
    } else {
        var c: Point = Point { x: 7, y: s };
        acc = c.x + c.y;
    }
    return acc;
}`)
	if got := allocReuseCount(funcByName(ip, "main")); got != 2 {
		t.Errorf("both arms should reuse the dead donor's box, got %d __alloc_reuse", got)
	}
}

// Three match arms, one donor: every arm gets the token.
func TestCrossBranchReuseMatchArmsShareDonor(t *testing.T) {
	ip := lowerForTest(t, `struct Point { x: i32, y: i32 }
function main(): i32 {
    var a: Point = Point { x: 1, y: 2 };
    var s: i32 = a.x + a.y;
    var acc: i32 = 0;
    match (s) {
        3 => { var p: Point = Point { x: 1, y: 1 }; acc = p.x + p.y; },
        4 => { var q: Point = Point { x: 2, y: 2 }; acc = q.x + q.y; },
        _ => { var r: Point = Point { x: 3, y: 3 }; acc = r.x + r.y; },
    }
    return acc;
}`)
	if got := allocReuseCount(funcByName(ip, "main")); got != 3 {
		t.Errorf("every match arm should reuse the dead donor's box, got %d __alloc_reuse", got)
	}
}

// SEQUENTIAL constructions inside one arm are NOT exclusive: the donor's box
// is gone after the first claim, so the second must not take it again.
func TestCrossBranchReuseDeclinesSequentialClaimants(t *testing.T) {
	ip := lowerForTest(t, `struct Point { x: i32, y: i32 }
function main(): i32 {
    var a: Point = Point { x: 1, y: 2 };
    var s: i32 = a.x + a.y;
    var acc: i32 = 0;
    if (s > 0) {
        var b: Point = Point { x: s, y: 9 };
        var c: Point = Point { x: 7, y: 1 };
        acc = b.x + c.y;          // both live to here, so c can't pair with b
    }
    return acc;
}`)
	if got := allocReuseCount(funcByName(ip, "main")); got != 1 {
		t.Errorf("only the first claimant in an arm may take the donor, got %d __alloc_reuse", got)
	}
}

// Cross-CLASS pairing (#4402 opt 3b): D and C pair on equal freelist class
// whatever KIND each is. A dead two-scalar TUPLE hands its box to a
// two-scalar STRUCT construction — the old gate declined it for the kind
// mismatch alone, though the emit path already releases D's fields through
// D's own layout.
func TestCrossKindReuseTupleDonorStructRecipient(t *testing.T) {
	ip := lowerForTest(t, `struct Point { x: i32, y: i32 }
function main(): i32 {
    var t: (i32, i32) = (1, 2);
    var s: i32 = t.0 + t.1;          // t's last use
    var b: Point = Point { x: s, y: 9 };
    return b.x + b.y;
}`)
	if got := allocReuseCount(funcByName(ip, "main")); got != 1 {
		t.Errorf("a dead tuple of the same class should donate to a struct, got %d __alloc_reuse", got)
	}
}

// The class gate still bites: a wider donor cannot hand its box to a
// narrower construction (the runtime class check would decline anyway).
func TestCrossKindReuseDeclinesClassMismatch(t *testing.T) {
	ip := lowerForTest(t, `struct Wide { a: i32, b: i32, c: i32, d: i32, e: i32, f: i32 }
struct Point { x: i32, y: i32 }
function main(): i32 {
    var w: Wide = Wide { a: 1, b: 2, c: 3, d: 4, e: 5, f: 6 };
    var s: i32 = w.a + w.f;
    var p: Point = Point { x: s, y: 9 };
    return p.x + p.y;
}`)
	if got := allocReuseCount(funcByName(ip, "main")); got != 0 {
		t.Errorf("a different-class donor must not pair, got %d __alloc_reuse", got)
	}
}

// A donor whose FIELD reaches a user `core/mem.Drop` never pairs. Reuse
// releases the donor's old fields where the recipient takes the box over,
// which is a different point from where the donor would otherwise have died —
// and box classes are computed from the target's pointer width, so the same
// program pairs on one backend and not another. A user-visible finalizer must
// not fire in a target-dependent order. (Loaded through modload: the impl
// needs `core/mem` resolved, which the bare parse+check helper cannot do.)
func TestReuseDeclinesDonorWithUserDropField(t *testing.T) {
	src := `import "core/mem";
struct W { n: i32 }
impl mem.Drop for W {
    function drop(self: Self): void { print("drop"); }
}
struct Holder { w: W }
struct Other { w: W }
function main(): i32 {
    var h: Holder = Holder { w: W { n: 3 } };
    var s: i32 = h.w.n;                          // h's last use
    var o: Other = Other { w: W { n: 4 } };      // same class, must not pair
    return s + o.w.n;
}`
	prog, _, err := modload.LoadSource(src)
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	ip, err := ir.LowerWith(prog, info, 8)
	ast.RcFreeEnabled = prev
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	if got := allocReuseCount(funcByName(ip, "main")); got != 0 {
		t.Errorf("a donor holding a user-Drop value must not pair, got %d __alloc_reuse", got)
	}
}
