package ir_test

import "testing"

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
