package ir_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// allocReuseCount counts the __alloc_reuse calls a function lowers —
// the marker that the Phase 5b constructor-reuse (FBIP) path fired.
func allocReuseCount(fn *ir.Func) int {
	n := 0
	for _, op := range fn.Ops {
		if op.Kind == ir.OpCallDirect && op.Str == "__alloc_reuse" {
			n++
		}
	}
	return n
}

// A self-overwrite of an owned, all-scalar struct local with a literal
// of the same type lowers through __alloc_reuse (reuse the old box in
// place when uniquely owned).
func TestStructReuseFiresForSelfOverwrite(t *testing.T) {
	ip := lowerForTest(t, `struct Point { x: i32, y: i32 }
function churn(n: i32): i32 {
    var p: Point = Point { x: 0, y: 0 };
    var i: i32 = 0;
    while (i < n) {
        p = Point { x: p.x + 1, y: p.y };
        i = i + 1;
    }
    return p.x;
}
function main(): i32 { return churn(3); }`)
	f := funcByName(ip, "churn")
	if f == nil {
		t.Fatal("no func churn")
	}
	if got := allocReuseCount(f); got != 1 {
		t.Errorf("self-overwrite struct reuse should emit one __alloc_reuse, got %d", got)
	}
}

// A struct with a pointer-shaped (rc-tracked) field is deferred to the
// field-store-elision slice (5d) — the reuse path must not fire, so its
// per-field rc isn't mishandled.
func TestStructReuseSkipsPointerField(t *testing.T) {
	ip := lowerForTest(t, `struct Named { id: i32, name: string }
function churn(n: i32): i32 {
    var p: Named = Named { id: 0, name: "a" };
    var i: i32 = 0;
    while (i < n) {
        p = Named { id: p.id + 1, name: p.name };
        i = i + 1;
    }
    return p.id;
}
function main(): i32 { return churn(3); }`)
	f := funcByName(ip, "churn")
	if f == nil {
		t.Fatal("no func churn")
	}
	if got := allocReuseCount(f); got != 0 {
		t.Errorf("pointer-field struct must not reuse (deferred to 5d), got %d", got)
	}
}

// A borrowed parameter can be rc==1 while the caller still holds it, so
// reusing its box in place would corrupt the caller's value. The
// freeEligible gate (params are never eligible) keeps reuse off.
func TestStructReuseSkipsBorrowedParam(t *testing.T) {
	ip := lowerForTest(t, `struct Point { x: i32, y: i32 }
function bump(p: Point): Point {
    p = Point { x: p.x + 1, y: p.y };
    return p;
}
function main(): i32 { return bump(Point { x: 1, y: 2 }).x; }`)
	f := funcByName(ip, "bump")
	if f == nil {
		t.Fatal("no func bump")
	}
	if got := allocReuseCount(f); got != 0 {
		t.Errorf("borrowed param must not reuse in place, got %d", got)
	}
}

// Wide / float scalar fields need width-correct temp slots the first
// cut doesn't emit (the reuse temps are i32), so an i64 field falls
// back to the normal fresh-alloc path.
func TestStructReuseSkipsWideScalarField(t *testing.T) {
	ip := lowerForTest(t, `struct Wide { x: i64, y: i32 }
function churn(n: i32): i64 {
    var p: Wide = Wide { x: 0, y: 0 };
    var i: i32 = 0;
    while (i < n) {
        p = Wide { x: p.x + 1, y: p.y };
        i = i + 1;
    }
    return p.x;
}
function main(): i32 { return 0; }`)
	f := funcByName(ip, "churn")
	if f == nil {
		t.Fatal("no func churn")
	}
	if got := allocReuseCount(f); got != 0 {
		t.Errorf("wide-scalar (i64) field must not reuse yet, got %d", got)
	}
}
