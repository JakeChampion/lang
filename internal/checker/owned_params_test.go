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

// A local handed over where it dies is a move (#9541): nothing reads the
// binding again. The admission is CallArgDeaths', the analysis the IR moves by.
func TestOwnGuardAllowsLocalAtLastUse(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"returned-call", `
function f(): i32 {
    var xs: i32[] = [1, 2];
    return consume(xs);
}`},
		{"call-initialised-last-read", `
function mk(): i32[] { return [1, 2]; }
function f(): i32 {
    var xs: i32[] = mk();
    var n: i32 = consume(xs);
    return n;
}`},
		{"last-read-on-the-returning-path", `
function mk(): Box { return Wrap([1]); }
function f(c: boolean): i32 {
    var b: Box = mk();
    if (c) {
        var n: i32 = consumeBox(b);
        return n;
    }
    match (b) { Wrap(xs) => { return xs.len(); } }
}`},
		{"two-statement-rebind", `
function grow(own xs: i32[], v: i32): i32[] { return xs.append(v); }
function f(): i32 {
    var xs: i32[] = [1];
    var ys: i32[] = grow(xs, 2);
    xs = ys;
    return xs.len();
}`},
	} {
		wantOK(t, tc.name, ownConsumer+tc.body)
	}
}

// Every way the binding can still be read after the call keeps the refusal.
func TestOwnGuardRejectsLocalStillLive(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"read-again", `
function mk(): i32[] { return [1, 2]; }
function f(): i32 {
    var xs: i32[] = mk();
    var n: i32 = consume(xs);
    return n + xs.len();
}`},
		{"inside-a-loop", `
function mk(): i32[] { return [1, 2]; }
function f(): i32 {
    var xs: i32[] = mk();
    var n: i32 = 0;
    while (n < 3) { n = n + consume(xs); }
    return n;
}`},
		{"read-by-a-defer", `
function mk(): i32[] { return [1, 2]; }
function f(): i32 {
    var xs: i32[] = mk();
    defer { var k: i32 = xs.len(); }
    return consume(xs);
}`},
		{"captured-by-a-lambda", `
function mk(): i32[] { return [1, 2]; }
function f(): i32 {
    var xs: i32[] = mk();
    var g = (): i32 => xs.len();
    return consume(xs) + g();
}`},
		// A literal-initialised local is admitted at a return, not at an
		// ordinary last read: CallArgDeaths cannot rule out that it aliases.
		{"literal-local-not-returned", `
function f(): i32 {
    var xs: i32[] = [1, 2];
    var n: i32 = consume(xs);
    return n;
}`},
	} {
		wantE051(t, tc.name, ownConsumer+tc.body)
	}
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

// A parameter or local that shadows an `own` function's name is a function
// VALUE. Its consuming positions come from its own TYPE, not from the global
// declaration it happens to share a name with — the stdlib's
// `filter(xs, keep)` was being checked against a user's `keep(own …)` and
// refusing to compile (#9532).
// Every argument here is a POINTER, deliberately: a scalar argument is excused
// by `scalarArgs` regardless of the callee, so a scalar case passes whether or
// not the shadowing rule works and proves nothing.
func TestOwnGuardIgnoresShadowingParameter(t *testing.T) {
	wantOK(t, "shadowing-param", ownConsumer+`
function filterish(rows: i32[][], consume: (i32[]) => i32): i32 {
    var n: i32 = 0;
    for r in rows { n = n + consume(r); }
    return n;
}
function main(): i32 { return filterish([[1, 2]], (v: i32[]) => v[0]); }`)
}

func TestOwnGuardIgnoresShadowingLocal(t *testing.T) {
	wantOK(t, "shadowing-local", ownConsumer+`
function f(xs: i32[]): i32 {
    var consume: (i32[]) => i32 = (v: i32[]) => v[0];
    return consume(xs);
}`)
}

func TestOwnGuardIgnoresShadowingLambdaParameter(t *testing.T) {
	wantOK(t, "shadowing-lambda-param", ownConsumer+`
function f(xs: i32[]): i32 {
    var run: ((i32[]) => i32) = (consume: i32[]) => consume.len();
    return run(xs);
}`)
}

// The consume classifier reads the same table, so a shadowed callee must not
// mark its argument MOVED either — `xs` is still live on the next line. This is
// the E050 half of the same bug, and it fires on the exact #9532 shape once the
// argument is an owned name rather than a scalar.
func TestOwnGuardShadowedCalleeDoesNotConsume(t *testing.T) {
	wantOK(t, "shadowing-param-no-consume", ownConsumer+`
function f(own xs: i32[], consume: (i32[]) => i32): i32 {
    var r: i32 = consume(xs);
    return r + xs.len();
}`)
}

// A `for` variable shadows for its body, like a parameter or a local.
func TestOwnGuardIgnoresShadowingLoopVariable(t *testing.T) {
	wantOK(t, "shadowing-loop-var", ownConsumer+`
function f(fns: ((i32[]) => i32)[], xs: i32[]): i32 {
    var n: i32 = 0;
    for consume in fns { n = n + consume(xs); }
    return n;
}`)
}

// A local shadows from its declaration to the end of its block — not before it,
// and not outside it.
func TestOwnGuardLocalShadowsOnlyAfterItsDeclaration(t *testing.T) {
	wantE051(t, "local-shadow-after-call", ownConsumer+`
function f(xs: i32[]): i32 {
    var a: i32 = consume(xs);
    var consume: (i32[]) => i32 = (v: i32[]) => v[0];
    return a + consume(xs);
}`)
}

func TestOwnGuardLocalShadowStaysInItsBlock(t *testing.T) {
	wantE051(t, "local-shadow-inner-block", ownConsumer+`
function f(xs: i32[], c: boolean): i32 {
    var n: i32 = 0;
    if (c) { var consume: (i32[]) => i32 = (v: i32[]) => v[0]; n = consume(xs); }
    return n + consume(xs);
}`)
}

// A match binder shadows for its arm whether or not the scrutinee is owned —
// the binding exists either way; only the ownership transfer depends on it.
func TestOwnGuardArmBinderShadowsOnABorrowedScrutinee(t *testing.T) {
	wantOK(t, "arm-binder-borrowed-scrutinee", `enum Box { B((i32[]) => i32) }
function consume(own xs: i32[]): i32 { return xs[0]; }
function f(b: Box, xs: i32[]): i32 {
    var n: i32 = 0;
    match (b) { B(consume) => { n = consume(xs); } }
    return n;
}`)
}

// A binding shadows inside ITS OWN scope. A lambda parameter elsewhere in the
// function does not make the real call to the global own-func safe, and a
// whole-function set of shadowed names would silence it.
func TestOwnGuardShadowElsewhereStillGuardsRealCall(t *testing.T) {
	wantE051(t, "shadow-elsewhere", ownConsumer+`
function apply(g: ((i32[]) => i32), zs: i32[]): i32 { return g(zs); }
function f(xs: i32[]): i32 {
    var a: i32 = apply((consume: i32[]) => consume.len(), xs);
    return a + consume(xs);
}`)
}

// The shadowing rule must not disarm the guard for the real function.
func TestOwnGuardStillFiresForTheRealFunction(t *testing.T) {
	wantE051(t, "unshadowed-still-guarded", ownConsumer+`
function g(xs: i32[]): i32 {
    return consume(xs);
}`)
}

// A payload-less variant is spelled as a bare name, so it arrives as an Ident
// where `Wrap([1, 2])` above arrives as a Call. Both are fresh enum values, and
// the guard admitted only the Call shape (#9517).
func TestOwnGuardAllowsPayloadlessVariant(t *testing.T) {
	wantOK(t, "payloadless-variant-arg", `enum Span { Empty, Wide(i32[]) }
function eat(own sp: Span): i32 { return 0; }
function f(): i32 {
    return eat(Empty);                 // fresh enum value → owned
}`)
}

// The qualified spelling stays a FieldAccess rather than being rewritten to an
// Ident, so it needs its own admission.
func TestOwnGuardAllowsQualifiedPayloadlessVariant(t *testing.T) {
	wantOK(t, "qualified-payloadless-variant-arg", `enum Span { Empty, Wide(i32[]) }
function eat(own sp: Span): i32 { return 0; }
function f(): i32 {
    return eat(Span.Empty);            // fresh enum value → owned
}`)
}

// The admission is for VARIANTS, not for any bare name that happens to match a
// declaration: a local of enum type read after the call is still a borrow.
func TestOwnGuardRejectsEnumLocal(t *testing.T) {
	wantE051(t, "enum-local-arg", `enum Span { Empty, Wide(i32[]) }
function eat(own sp: Span): i32 { return 0; }
function peek(sp: Span): i32 { return 0; }
function f(): i32 {
    var sp: Span = Empty;
    var n: i32 = eat(sp);              // read again below, so not a move
    return n + peek(sp);
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

// --- nested function bodies (#7452) --------------------------------------
//
// A lambda body and a local function body are STATEMENTS. The walk that
// reaches them as one flat expression sees the calls but not the assignments
// around them, so the self-reassign admission — a statement-level fact — went
// missing exactly one nesting level in.

func TestOwnGuardAllowsSelfReassignMoveInLambda(t *testing.T) {
	wantOK(t, "self-reassign-in-lambda", ownConsumer+`
struct B { items: i32[] }
function grow(own b: B, x: i32): B { return B { items: b.items.append(x) }; }
function f(): i32 {
    var lam = (): i32 => {
        var a = B { items: [] };
        a = grow(a, 1);
        a = grow(a, 2);
        return a.items.len();
    };
    return lam();
}`)
}

func TestOwnGuardAllowsSelfReassignMoveInLocalFunc(t *testing.T) {
	wantOK(t, "self-reassign-in-local-func", ownConsumer+`
struct B { items: i32[] }
function grow(own b: B, x: i32): B { return B { items: b.items.append(x) }; }
function f(): i32 {
    function inner(): i32 {
        var a = B { items: [] };
        a = grow(a, 1);
        return a.items.len();
    }
    return inner();
}`)
}

// A local function's OWN `own` parameter is owned inside its body, so it may
// be transferred onward just like a top-level one's.
func TestOwnGuardAllowsLocalFuncOwnParamTransfer(t *testing.T) {
	wantOK(t, "local-func-own-param-transfer", ownConsumer+`
function f(): i32 {
    function inner(own b: i32[]): i32 { return consume(b); }
    return inner([1]);
}`)
}

// The rejections still apply inside a nested body — reaching the statements
// is not the same as exempting them.
func TestOwnGuardRejectsKeptAliveLocalInLambda(t *testing.T) {
	wantE051(t, "kept-alive-local-in-lambda", ownConsumer+`
struct B { items: i32[] }
function grow(own b: B, x: i32): B { return B { items: b.items.append(x) }; }
function f(): i32 {
    var lam = (): i32 => {
        var a = B { items: [] };
        var c = grow(a, 1);
        return c.items.len();
    };
    return lam();
}`)
}

func TestOwnedUseAfterConsumeInLambda(t *testing.T) {
	wantE050(t, "double-consume-in-lambda", ownPrelude+`
function f(own xs: i32[]): i32 {
    var lam = (): i32 => {
        var a: i32 = sink(xs);
        return a + sink(xs);   // E050: xs already moved
    };
    return lam();
}`)
}

// A nested parameter SHADOWS an outer owned name it repeats: the inner `xs` is
// a borrowed lambda parameter, so consuming it neither transfers (E051 stands)
// nor marks the outer `xs` moved.
func TestOwnedNestedParamShadowsOuter(t *testing.T) {
	src := ownPrelude + `
function f(own xs: i32[]): i32 {
    var lam = (xs: i32[]): i32 => { return sink(xs); };
    return lam([1]) + sink(xs);
}`
	err := checkSource(t, src)
	if err == nil {
		t.Fatalf("shadowed-nested-param: expected E051 on the borrowed lambda parameter, got none")
	}
	if !strings.Contains(err.Error(), "owned parameter must be an owned value") {
		t.Errorf("shadowed-nested-param: expected E051, got: %v", err)
	}
	if strings.Contains(err.Error(), "after it was consumed") {
		t.Errorf("shadowed-nested-param: the outer owned xs is untouched by the shadowing parameter, got: %v", err)
	}
}

// --- E051 superseded-field move admission (#8186) ------------------------
//
// `a = S { ...a, f: g(.., a.f, ..) }` with `a` an `own` param or a local: the
// store supersedes the one field the call consumes, and nothing can read it
// in between, so the field transfers (SupersededFieldOwnMoveArgs — the IR's
// computeFieldOwnMoves keys on the same shape).

const fieldMovePrelude = `struct Cfi { rules: i32[], n: i32 }
struct Asm { code: i32[], cfi: Cfi }
function record(own s: Cfi, v: i32): Cfi { return Cfi { rules: s.rules.append(v), n: s.n + 1 }; }
`

func TestOwnGuardAllowsSupersededFieldMoveOnOwnBase(t *testing.T) {
	wantOK(t, "field-move-own-base", fieldMovePrelude+`
function step(own a: Asm, v: i32): Asm {
    a = Asm { ...a, cfi: record(a.cfi, v) };
    return a;
}`)
}

func TestOwnGuardAllowsSupersededFieldMoveOnLocal(t *testing.T) {
	wantOK(t, "field-move-local-base", fieldMovePrelude+`
function build(): i32 {
    var a: Asm = Asm { code: [], cfi: Cfi { rules: [], n: 0 } };
    a = Asm { ...a, cfi: record(a.cfi, 1) };
    a = Asm { ...a, cfi: record(a.cfi, a.code.len()) };
    return a.cfi.n;
}`)
}

func TestOwnGuardAllowsSupersededFieldMoveOnReturn(t *testing.T) {
	wantOK(t, "field-move-return-form", fieldMovePrelude+`
function step(own a: Asm, v: i32): Asm {
    return Asm { ...a, cfi: record(a.cfi, v) };
}`)
}

// A BORROWED base keeps its field for the caller — still E051.
func TestOwnGuardRejectsSupersededFieldMoveOnBorrowedBase(t *testing.T) {
	wantE051(t, "field-move-borrowed-base", fieldMovePrelude+`
function step(a: Asm, v: i32): Asm {
    a = Asm { ...a, cfi: record(a.cfi, v) };
    return a;
}`)
	wantE051(t, "field-move-borrowed-base-return", fieldMovePrelude+`
function step(a: Asm, v: i32): Asm {
    return Asm { ...a, cfi: record(a.cfi, v) };
}`)
}

// The base passed to the callee by a SECOND route would read the emptied
// field slot — still E051.
func TestOwnGuardRejectsSupersededFieldMoveWithAliasedBase(t *testing.T) {
	wantE051(t, "field-move-base-also-passed", fieldMovePrelude+`
function record2(own s: Cfi, a: Asm): Cfi { return Cfi { rules: s.rules.append(a.code.len()), n: s.n + 1 }; }
function step(own a: Asm): Asm {
    a = Asm { ...a, cfi: record2(a.cfi, a) };
    return a;
}`)
}

// A second read of the field anywhere in the literal observes the moved
// value — still E051.
func TestOwnGuardRejectsSupersededFieldMoveSecondRead(t *testing.T) {
	wantE051(t, "field-move-second-read", fieldMovePrelude+`
function step(own a: Asm): Asm {
    a = Asm { ...a, cfi: record(a.cfi, a.cfi.n) };
    return a;
}`)
}

// Storing the call's result into a DIFFERENT field leaves the moved field
// carried by the spread — still E051.
func TestOwnGuardRejectsSupersededFieldMoveIntoOtherField(t *testing.T) {
	wantE051(t, "field-move-other-field", `struct Cfi { rules: i32[], n: i32 }
struct Asm { code: i32[], cfi: Cfi, cfi2: Cfi }
function record(own s: Cfi, v: i32): Cfi { return Cfi { rules: s.rules.append(v), n: s.n + 1 }; }
function step(own a: Asm): Asm {
    a = Asm { ...a, cfi2: record(a.cfi, 1) };
    return a;
}`)
}

// Binding the literal to a DIFFERENT name keeps the old base alive — still
// E051.
func TestOwnGuardRejectsSupersededFieldMoveKeptAliveBase(t *testing.T) {
	wantE051(t, "field-move-kept-alive-base", fieldMovePrelude+`
function build(): i32 {
    var a: Asm = Asm { code: [], cfi: Cfi { rules: [], n: 0 } };
    var b: Asm = Asm { ...a, cfi: record(a.cfi, 1) };
    return b.cfi.n + a.cfi.n;
}`)
}

// A nested function's borrowed parameter shadows an outer local of the same
// name for the body's extent — still E051 inside it.
func TestOwnGuardRejectsSupersededFieldMoveOnShadowingBorrowedParam(t *testing.T) {
	wantE051(t, "field-move-nested-borrowed-param", fieldMovePrelude+`
function build(): i32 {
    var a: Asm = Asm { code: [], cfi: Cfi { rules: [], n: 0 } };
    function inner(a: Asm): Asm {
        a = Asm { ...a, cfi: record(a.cfi, 1) };
        return a;
    }
    return inner(a).cfi.n;
}`)
}

// A call's result is owned when the callee cannot have borrowed it from the
// caller. Which functions those are is inferred from their returns, so a
// factory that takes a reference and builds something new is admitted where
// reading its parameter list alone refused it (#9538).

func TestOwnGuardAcceptsFreshResultFromABorrowingFactory(t *testing.T) {
	wantOK(t, "fresh-from-borrowing-factory", ownConsumer+`
function build(tag: string): i32[] { return [tag.len()]; }
function main(): i32 { return consume(build("xy")); }`)
}

func TestOwnGuardAcceptsFreshResultFromAPointerArgument(t *testing.T) {
	wantOK(t, "fresh-from-pointer-argument", ownConsumer+`
function sized(xs: i32[]): i32[] { return [xs.len()]; }
function main(): i32 { return consume(sized([1, 2])); }`)
}

func TestOwnGuardRejectsAResultThatIsTheBorrowedParameter(t *testing.T) {
	wantE051(t, "result-is-the-parameter", ownConsumer+`
function passthru(xs: i32[]): i32[] { return xs; }
function main(): i32 { return consume(passthru([1, 2])); }`)
}

func TestOwnGuardRejectsAResultBorrowedThroughALocal(t *testing.T) {
	wantE051(t, "result-borrowed-through-a-local", ownConsumer+`
function hop(xs: i32[]): i32[] {
    var y: i32[] = xs;
    return y;
}
function main(): i32 { return consume(hop([1, 2])); }`)
}

func TestOwnGuardRejectsAResultBorrowedThroughAField(t *testing.T) {
	wantE051(t, "result-borrowed-through-a-field", ownConsumer+`
function inner(p: Pair): i32[] { return p.items; }
function main(): i32 { return consume(inner(Pair { items: [1, 2], n: 2 })); }`)
}

func TestOwnGuardRejectsAResultBorrowedThroughACallChain(t *testing.T) {
	wantE051(t, "result-borrowed-through-a-chain", ownConsumer+`
function passthru(xs: i32[]): i32[] { return xs; }
function relay(ys: i32[]): i32[] { return passthru(ys); }
function main(): i32 { return consume(relay([1, 2])); }`)
}

// A chain that ends in a construction is still fresh, however many borrowing
// signatures it passes through.
func TestOwnGuardAcceptsAFreshResultThroughACallChain(t *testing.T) {
	wantOK(t, "fresh-through-a-chain", ownConsumer+`
function sized(xs: i32[]): i32[] { return [xs.len()]; }
function relay(ys: i32[]): i32[] { return sized(ys); }
function main(): i32 { return consume(relay([1, 2])); }`)
}

// Recursion is seeded clean rather than pessimistically, so a recursive
// function that returns only constructions is still a fresh owner.
func TestOwnGuardAcceptsAFreshResultFromARecursiveFunction(t *testing.T) {
	wantOK(t, "fresh-from-recursion", ownConsumer+`
function grow(xs: i32[], k: i32): i32[] {
    if (k <= 0) { return [xs.len()]; }
    return grow(xs, k - 1);
}
function main(): i32 { return consume(grow([1, 2], 3)); }`)
}

// ...but a recursive function that can also return its borrowed parameter is
// marked through the cycle.
func TestOwnGuardRejectsARecursiveResultThatCanBeTheParameter(t *testing.T) {
	wantE051(t, "recursive-result-is-the-parameter", ownConsumer+`
function last(xs: i32[], k: i32): i32[] {
    if (k <= 0) { return xs; }
    return last(xs, k - 1);
}
function main(): i32 { return consume(last([1, 2], 3)); }`)
}

// An `own` parameter is not a borrow: the callee took ownership, so threading
// it out is still a value the caller can transfer.
func TestOwnGuardAcceptsAThreadedOwnParameter(t *testing.T) {
	wantOK(t, "threaded-own-parameter", ownConsumer+`
function thread(own xs: i32[], k: i32): i32[] {
    if (k <= 0) { return xs; }
    return [k];
}
function main(): i32 { return consume(thread([1, 2], 3)); }`)
}
