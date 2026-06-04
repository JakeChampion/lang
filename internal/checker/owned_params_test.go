package checker

import (
	"strings"
	"testing"
)

// Slice A of the owned/consuming-parameter feature: the affine use-after-move
// analysis (checkOwnedParams, E050). These tests pin the consume/borrow
// classification, branch + loop handling, and the parser's contextual `own`.

func wantE050(t *testing.T, name, src string) {
	t.Helper()
	err := checkSource(t, src)
	if err == nil {
		t.Fatalf("%s: expected an E050 use-after-move error, got none", name)
	}
	if !strings.Contains(err.Error(), "E050") && !strings.Contains(err.Error(), "owned parameter") {
		t.Errorf("%s: expected E050 / owned-parameter error, got: %v", name, err)
	}
}

func wantOK(t *testing.T, name, src string) {
	t.Helper()
	if err := checkSource(t, src); err != nil {
		t.Errorf("%s: expected no error, got: %v", name, err)
	}
}

const ownPrelude = `enum Lst { Cons(i32), Nil }
function sink(xs: i32[]): i32 { return xs[0]; }
function lsink(l: Lst): i32 { return 0; }
`

// --- E050 should FIRE ----------------------------------------------------

func TestOwnedUseAfterCallConsume(t *testing.T) {
	wantE050(t, "call-then-use", ownPrelude+`
function f(own xs: i32[]): i32 {
    var a: i32 = sink(xs);   // consume xs (whole-value arg)
    return sink(xs);         // E050: xs already moved
}`)
}

func TestOwnedDoubleConsumeOneStmt(t *testing.T) {
	wantE050(t, "f(x)+g(x)", ownPrelude+`
function f(own xs: i32[]): i32 {
    return sink(xs) + sink(xs);   // second xs is use-after-move
}`)
}

func TestOwnedUseAfterMatchConsume(t *testing.T) {
	wantE050(t, "match-then-use", ownPrelude+`
function f(own l: Lst): i32 {
    var r: i32 = match (l) { Cons(h) => h, Nil => 0 };   // match consumes l
    return r + lsink(l);                                 // E050
}`)
}

func TestOwnedConsumeInLoop(t *testing.T) {
	wantE050(t, "consume-in-loop", ownPrelude+`
function f(own xs: i32[]): i32 {
    var i: i32 = 0;
    while (i < 3) {
        var a: i32 = sink(xs);   // a later iteration would use xs after move
        i = i + 1;
    }
    return 0;
}`)
}

func TestOwnedConsumeInThenUsedAfterMerge(t *testing.T) {
	// then-branch consumes xs but does NOT diverge → after the if, xs may be
	// moved, so the later use is rejected.
	wantE050(t, "consume-in-nondiverging-then", ownPrelude+`
function f(own xs: i32[], c: boolean): i32 {
    var acc: i32 = 0;
    if (c) {
        acc = sink(xs);   // consume on the then-path, falls through
    }
    return acc + sink(xs);   // E050 on the path where the then-branch ran
}`)
}

// --- E050 should NOT fire ------------------------------------------------

func TestOwnedBorrowThenConsumeOK(t *testing.T) {
	wantOK(t, "borrow*-then-consume", ownPrelude+`
function f(own xs: i32[]): i32 {
    var a: i32 = xs[0];      // borrow (projection)
    var b: i32 = xs[1];      // borrow
    return a + b + sink(xs); // consume (last use)
}`)
}

func TestOwnedConsumeInBothDivergingBranchesOK(t *testing.T) {
	// Each branch consumes xs exactly once and diverges, so there is no path
	// that uses xs twice.
	wantOK(t, "diverging-branches", ownPrelude+`
function f(own xs: i32[], c: boolean): i32 {
    if (c) {
        return sink(xs);
    } else {
        return sink(xs) + 1;
    }
}`)
}

func TestOwnedNeverConsumedOK(t *testing.T) {
	wantOK(t, "borrow-only", ownPrelude+`
function f(own xs: i32[]): i32 {
    return xs[0];
}`)
}

func TestOwnedMethodReceiverIsBorrowOK(t *testing.T) {
	wantOK(t, "method-receiver-borrow", ownPrelude+`
function f(own xs: i32[]): i32 {
    var n: i32 = xs.len();   // receiver borrow
    return n + sink(xs);     // consume after borrow — fine
}`)
}

// --- parser: contextual `own` --------------------------------------------

func TestParamNamedOwnStillWorks(t *testing.T) {
	// `own: i32` is a param NAMED own (not a modifier — the next token is `:`).
	wantOK(t, "param-named-own", `
function f(own: i32): i32 { return own + 1; }
function main(): i32 { return f(41); }`)
}

func TestOwnModifierParses(t *testing.T) {
	wantOK(t, "own-modifier-parses", ownPrelude+`
function f(own xs: i32[]): i32 { return xs[0]; }
function main(): i32 { return f([1, 2]); }`)
}
