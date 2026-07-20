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

// `sink` CONSUMES its argument (`own`), so passing an owned value to it is a
// move — the consume tests below rely on that. `peek` BORROWS, so passing an
// owned value to it is a read, not a move (the precise affine model: only an
// `own` position consumes).
const ownPrelude = `enum Lst { Cons(i32), Nil }
function sink(own xs: i32[]): i32 { return xs[0]; }
function peek(xs: i32[]): i32 { return xs[0]; }
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

// Precise affine model: passing an owned value to a BORROWED parameter is a
// read, not a move — so it can be passed to a borrowing helper repeatedly and
// still consumed at the end. (Strict-affine over-approximation would have
// flagged the second `peek(xs)` as a use-after-move.) This is the idiom the
// self-host's `own`-threaded builders rely on (`contains_str(out, x)` then
// `out = out.append(x)`).
func TestOwnedBorrowArgIsNotConsumeOK(t *testing.T) {
	wantOK(t, "borrow-arg-not-consume", ownPrelude+`
function f(own xs: i32[]): i32 {
    var a: i32 = peek(xs);   // borrow (arg to a borrowed param)
    var b: i32 = peek(xs);   // still a borrow — not use-after-move
    return a + b + sink(xs); // consume at the end
}`)
}

// Read-form borrows: an `own` value READ through a slice, a comparison, a cast,
// etc. is borrowed (not consumed), so it can still be consumed at the end. The
// affine walk now classifies those read positions as borrows; without it each
// was a false use-after-move E050 (the gap that blocked tracking owned locals,
// which are read through casts / comparisons / slices pervasively).
func TestOwnedReadFormsAreBorrows(t *testing.T) {
	wantOK(t, "slice-and-compare-reads", ownPrelude+`
function takesl(s: [i32]): i32 { return s.len(); }
function f(own xs: i32[]): i32 {
    var n: i32 = takesl(xs[0:1]);     // slice read (borrow)
    var c: boolean = xs.len() == 3;   // method-then-compare (reads)
    if (c) { return n; }
    return n + sink(xs);              // consume at the end
}`)
	wantOK(t, "string-concat-read", ownPrelude+`
function slen(s: string): i32 { return 0; }
function f(own s: string): i32 {
    var t: i32 = slen(s + "!");       // string-concat operand read (borrow)
    var u: boolean = s == "x";        // comparison read (borrow)
    if (u) { return t; }
    return t + slen(s);
}`)
}

// --- E051: call-site ownership guard -------------------------------------

const ownConsumer = `enum Box { Wrap(i32[]) }
struct Pair { items: i32[], n: i32 }
function consume(own xs: i32[]): i32 { return xs[0]; }
function consumeBox(own b: Box): i32 { return 0; }
`

func wantE051(t *testing.T, name, src string) {
	t.Helper()
	err := checkSource(t, src)
	if err == nil {
		t.Fatalf("%s: expected an E051 ownership error, got none", name)
	}
	if !strings.Contains(err.Error(), "E051") && !strings.Contains(err.Error(), "owned parameter must be an owned value") {
		t.Errorf("%s: expected E051 / owned-value error, got: %v", name, err)
	}
}

func TestOwnGuardRejectsBorrowedParam(t *testing.T) {
	wantE051(t, "borrowed-param-arg", ownConsumer+`
function f(xs: i32[]): i32 {   // xs BORROWED
    return consume(xs);        // E051: can't transfer a borrowed value
}`)
}

func TestOwnGuardRejectsFieldRead(t *testing.T) {
	wantE051(t, "field-read-arg", ownConsumer+`
function f(p: Pair): i32 {
    return consume(p.items);   // E051: a projection is a borrow
}`)
}

func TestOwnGuardRejectsPlainLocal(t *testing.T) {
	wantE051(t, "plain-local-arg", ownConsumer+`
function f(): i32 {
    var xs: i32[] = [1, 2];
    return consume(xs);        // E051: a plain local isn't tracked as owned yet
}`)
}

func TestOwnGuardAllowsConstruction(t *testing.T) {
	wantOK(t, "construction-arg", ownConsumer+`
function f(): i32 {
    return consume([1, 2]);    // fresh construction → owned
}`)
}

func TestOwnGuardAllowsOwnParam(t *testing.T) {
	wantOK(t, "own-param-arg", ownConsumer+`
function f(own ys: i32[]): i32 {
    return consume(ys);        // ys is owned → transfer OK (consumed once)
}`)
}

func TestOwnGuardAllowsVariantCall(t *testing.T) {
	wantOK(t, "variant-call-arg", ownConsumer+`
function f(): i32 {
    return consumeBox(Wrap([1, 2]));   // fresh enum value → owned
}`)
}

// A call to a function whose EVERY pointer parameter is `own` returns a
// freshly-owned result (the callee consumed each pointer input, so it can't
// hand back a borrowed one) — so it passes the E051 transfer guard. This is
// the self-host `consume(build(own ops, s))` shape: a threaded `own` array
// param grown and returned is owned by the caller, transferable onward.
func TestOwnGuardAllowsAllOwnPtrParamCallResult(t *testing.T) {
	wantOK(t, "all-own-ptr-param-result", ownConsumer+`
function build(own ops: i32[], x: i32): i32[] { return ops.append(x); }
function f(): i32 {
    return consume(build([1, 2], 3));   // build's result is freshly owned → transfer OK
}`)
}

// The dual: a function with a BORROWED pointer parameter could return it
// (`id(xs) -> xs`), so its result is NOT provably owned — transferring it stays
// E051.
func TestOwnGuardRejectsBorrowedPtrParamCallResult(t *testing.T) {
	wantE051(t, "borrowed-ptr-param-result", ownConsumer+`
function pick(a: i32[], b: i32[]): i32[] { return a; }
function f(): i32 {
    return consume(pick([1, 2], [3, 4]));   // pick may return a borrowed input → E051
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

func TestOwnedSiblingMatchBindingNameReuseOK(t *testing.T) {
	// Two sibling matches on OWNED scrutinees whose arm bindings share a name.
	// The first arm consumes its binding; that must NOT make the second match's
	// same-named binding look already-moved. Regression: the arm-local binding's
	// consumed-state leaked through the non-diverging join into the parent set,
	// so the second `e` was flagged E050. An `own` func is present so the
	// analysis is active (mirrors a program that imports std/sort, whose `own`
	// in-place sorts now activate the guard for every function). Arms must NOT
	// diverge (no `return`) so the buggy join path is exercised.
	wantOK(t, "sibling-match-binding-name-reuse", ownConsumer+`
function mkBox(): Box { return Wrap([1, 2]); }
function f(): i32 {
    var a: i32 = 0;
    match (mkBox()) { Wrap(e) => { a = consumeBox(Wrap(e)); } }
    match (mkBox()) { Wrap(e) => { a = a + consumeBox(Wrap(e)); } }
    return a;
}`)
}

// --- consuming methods (`own self`) ---------------------------------------

func TestConsumingMethodsAccepted(t *testing.T) {
	// An inherent (receiver-clause) consuming method — the recursive `map`
	// shape. `own self` parses, the receiver hoists to an `own` Params[0], and
	// the method-call transfer makes `t.inc()` consume the owned binding `t`.
	wantOK(t, "inherent-own-self", `enum List { Cons(i32, List), Nil }
function (own xs: List) inc(): List { match (xs) { Cons(h, t) => { return Cons(h + 1, t.inc()); }, Nil => { return Nil; } } }
function main(): i32 { var ys: List = Cons(1, Nil).inc(); return 0; }`)

	// A consuming method's receiver still goes through the E051 call-site guard:
	// a BORROWED receiver can't be transferred.
	wantE051(t, "borrowed-receiver-to-consuming-method", `enum List { Cons(i32, List), Nil }
function (own xs: List) inc(): List { match (xs) { Cons(h, t) => { return Cons(h + 1, t.inc()); }, Nil => { return Nil; } } }
function f(borrowed: List): i32 { var ys: List = borrowed.inc(); return 0; }`)
}

// --- E051 self-reassign move admission (#4873 step 0) --------------------

// A LOCAL passed exactly once, directly, in an `own` position of its own
// reassignment's RHS is a transfer: the old binding dies at the
// assignment, so E051 admits it (SelfReassignOwnMoveArg — the IR's
// callConsumesIdent overwrite-dec skip pairs with the same shape).
func TestOwnGuardAllowsSelfReassignMove(t *testing.T) {
	if err := checkSource(t, ownConsumer+`
struct B { items: i32[] }
function grow(own b: B, x: i32): B { return B { items: b.items.append(x) }; }
function f(): i32 {
    var a = B { items: [] };
    a = grow(a, 1);
    a = grow(a, 2);
    return a.items.len();
}`); err != nil {
		t.Errorf("self-reassign own move should check, got: %v", err)
	}
}

// The admission is ONLY the self-reassign shape: binding the result to a
// DIFFERENT name keeps the old binding alive — still E051.
func TestOwnGuardRejectsKeptAliveLocal(t *testing.T) {
	wantE051(t, "kept-alive-local", ownConsumer+`
struct B { items: i32[] }
function grow(own b: B, x: i32): B { return B { items: b.items.append(x) }; }
function f(): i32 {
    var a = B { items: [] };
    var c = grow(a, 1);
    return c.items.len();
}`)
}

// A SECOND read of the local anywhere in the same RHS would observe the
// consumed value — exactly-once is required, so this stays E051.
func TestOwnGuardRejectsSelfReassignSecondRead(t *testing.T) {
	wantE051(t, "self-reassign-second-read", ownConsumer+`
struct B { items: i32[] }
function grow(own b: B, x: i32): B { return B { items: b.items.append(x) }; }
function f(): i32 {
    var a = B { items: [7] };
    a = grow(a, a.items[0]);
    return a.items.len();
}`)
}
