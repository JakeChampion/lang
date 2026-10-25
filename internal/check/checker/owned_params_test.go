package checker

import (
	"strings"
	"testing"
)

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
function sink(own xs: i32[]): i32 { return xs[0]; }
function peek(xs: i32[]): i32 { return xs[0]; }
function lsink(l: Lst): i32 { return 0; }
`

func TestOwnedUseAfterCallConsume(t *testing.T) {
	wantE050(t, "call-then-use", ownPrelude+`
function f(own xs: i32[]): i32 {
    let a: i32 = sink(xs);   // consume xs (whole-value arg)
    return sink(xs);         // E050: xs already moved
}`)
}

func TestOwnedDoubleConsumeOneStmt(t *testing.T) {
	wantE050(t, "f(x)+g(x)", ownPrelude+`
function f(own xs: i32[]): i32 {
    return sink(xs) + sink(xs);   // second xs is use-after-move
}`)
}

const ownBoxPrelude = `enum Box { Str(string), Arr(i32[]), Nil }
function bsink(b: Box): i32 { return 0; }
`

func TestOwnedUseAfterMatchConsume(t *testing.T) {
	wantE050(t, "match-then-use", ownBoxPrelude+`
function f(own b: Box): i32 {
    let r: i32 = match (b) { Str(s) => s.len(), Arr(a) => a.len(), Nil => 0 };   // takes s / a
    return r + bsink(b);                                                       // E050
}`)
	wantE050(t, "match-stmt-then-use", ownBoxPrelude+`
function f(own b: Box): i32 {
    match (b) { Str(s) => { return s.len(); }, Arr(_) => { }, Nil => { } }
    return bsink(b);
}`)
}

func TestOwnedUseAfterTagOnlyMatchOK(t *testing.T) {
	wantOK(t, "scalar-payload-match-then-use", ownPrelude+`
function f(own l: Lst): i32 {
    let r: i32 = match (l) { Cons(h) => h, Nil => 0 };
    return r + lsink(l);
}`)
	wantOK(t, "tag-only-match-then-match", ownBoxPrelude+`
function f(own b: Box): i32 {
    let t: i32 = 0;
    match (b) { Str(_) => { t = 1; }, Arr(_) => { t = 2; }, Nil => { t = 3; } }
    match (b) { Str(s) => { return t + s.len(); }, Arr(a) => { return t + a.len(); }, Nil => { return t; } }
}`)
}

func TestOwnedConsumeInLoop(t *testing.T) {
	wantE050(t, "consume-in-loop", ownPrelude+`
function f(own xs: i32[]): i32 {
    let i: i32 = 0;
    while (i < 3) {
        let a: i32 = sink(xs);   // a later iteration would use xs after move
        i = i + 1;
    }
    return 0;
}`)
}

func TestOwnedConsumeInThenUsedAfterMerge(t *testing.T) {
	wantE050(t, "consume-in-nondiverging-then", ownPrelude+`
function f(own xs: i32[], c: boolean): i32 {
    let acc: i32 = 0;
    if (c) {
        acc = sink(xs);   // consume on the then-path, falls through
    }
    return acc + sink(xs);   // E050 on the path where the then-branch ran
}`)
}

func TestOwnedBorrowThenConsumeOK(t *testing.T) {
	wantOK(t, "borrow*-then-consume", ownPrelude+`
function f(own xs: i32[]): i32 {
    let a: i32 = xs[0];      // borrow (projection)
    let b: i32 = xs[1];      // borrow
    return a + b + sink(xs); // consume (last use)
}`)
}

func TestOwnedConsumeInBothDivergingBranchesOK(t *testing.T) {
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
    let n: i32 = xs.len();   // receiver borrow
    return n + sink(xs);     // consume after borrow — fine
}`)
}

func TestOwnedBorrowArgIsNotConsumeOK(t *testing.T) {
	wantOK(t, "borrow-arg-not-consume", ownPrelude+`
function f(own xs: i32[]): i32 {
    let a: i32 = peek(xs);   // borrow (arg to a borrowed param)
    let b: i32 = peek(xs);   // still a borrow — not use-after-move
    return a + b + sink(xs); // consume at the end
}`)
}

func TestOwnedReadFormsAreBorrows(t *testing.T) {
	wantOK(t, "slice-and-compare-reads", ownPrelude+`
function takesl(s: [i32]): i32 { return s.len(); }
function f(own xs: i32[]): i32 {
    let n: i32 = takesl(xs[0:1]);     // slice read (borrow)
    let c: boolean = xs.len() == 3;   // method-then-compare (reads)
    if (c) { return n; }
    return n + sink(xs);              // consume at the end
}`)
	wantOK(t, "string-concat-read", ownPrelude+`
function slen(s: string): i32 { return 0; }
function f(own s: string): i32 {
    let t: i32 = slen(s + "!");       // string-concat operand read (borrow)
    let u: boolean = s == "x";        // comparison read (borrow)
    if (u) { return t; }
    return t + slen(s);
}`)
}

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

func TestOwnGuardAllowsLocalAtLastUse(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"returned-call", `
function f(): i32 {
    let xs: i32[] = [1, 2];
    return consume(xs);
}`},
		{"call-initialised-last-read", `
function mk(): i32[] { return [1, 2]; }
function f(): i32 {
    let xs: i32[] = mk();
    let n: i32 = consume(xs);
    return n;
}`},
		{"last-read-on-the-returning-path", `
function mk(): Box { return Wrap([1]); }
function f(c: boolean): i32 {
    let b: Box = mk();
    if (c) {
        let n: i32 = consumeBox(b);
        return n;
    }
    match (b) { Wrap(xs) => { return xs.len(); } }
}`},
		{"tuple-destructured-last-read", `
function step(): (i32[], i32) { return ([1], 2); }
function f(): i32 {
    let (xs, k) = step();
    let n: i32 = consume(xs);
    return n + k;
}`},
		{"struct-destructured-last-read", `
function mk(): Pair { return Pair { items: [1], n: 2 }; }
function f(): i32 {
    let Pair { items, n } = mk();
    return consume(items) + n;
}`},
		{"two-statement-rebind", `
function grow(own xs: i32[], v: i32): i32[] { return xs.append(v); }
function f(): i32 {
    let xs: i32[] = [1];
    let ys: i32[] = grow(xs, 2);
    xs = ys;
    return xs.len();
}`},
		{"declared-in-the-loop-body", `
function mk(n: i32): i32[] { return [n]; }
function f(): i32 {
    let n: i32 = 0;
    while (n < 3) {
        let xs: i32[] = mk(n);
        n = n + consume(xs);
    }
    return n;
}`},
	} {
		wantOK(t, tc.name, ownConsumer+tc.body)
	}
}

func TestOwnGuardRejectsLocalStillLive(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"read-again", `
function mk(): i32[] { return [1, 2]; }
function f(): i32 {
    let xs: i32[] = mk();
    let n: i32 = consume(xs);
    return n + xs.len();
}`},
		{"inside-a-loop", `
function mk(): i32[] { return [1, 2]; }
function f(): i32 {
    let xs: i32[] = mk();
    let n: i32 = 0;
    while (n < 3) { n = n + consume(xs); }
    return n;
}`},
		{"declared-in-an-outer-loop", `
function mk(n: i32): i32[] { return [n]; }
function f(): i32 {
    let n: i32 = 0;
    while (n < 3) {
        let xs: i32[] = mk(n);
        while (n < 2) { n = n + consume(xs); }
        n = n + 1;
    }
    return n;
}`},
		{"read-by-a-defer", `
function mk(): i32[] { return [1, 2]; }
function f(): i32 {
    let xs: i32[] = mk();
    defer { let k: i32 = xs.len(); }
    return consume(xs);
}`},
		{"destructured-read-again", `
function step(): (i32[], i32) { return ([1], 2); }
function f(): i32 {
    let (xs, k) = step();
    let n: i32 = consume(xs);
    return n + xs.len() + k;
}`},
		{"destructured-whole-named", `
function step(): (i32[], i32) { return ([1], 2); }
function f(): i32 {
    let t @ (xs, k) = step();
    let n: i32 = consume(xs);
    return n + t.1;
}`},
		{"destructured-borrowed-param", `
function f(p: Pair): i32 {
    let Pair { items, n } = p;
    let r: i32 = consume(items);
    return r + n;
}`},
		{"captured-by-a-lambda", `
function mk(): i32[] { return [1, 2]; }
function f(): i32 {
    let xs: i32[] = mk();
    let g = (): i32 => xs.len();
    return consume(xs) + g();
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

func TestOwnGuardIgnoresShadowingParameter(t *testing.T) {
	wantOK(t, "shadowing-param", ownConsumer+`
function filterish(rows: i32[][], consume: (i32[]) => i32): i32 {
    let n: i32 = 0;
    for r in rows { n = n + consume(r); }
    return n;
}
function main(): i32 { return filterish([[1, 2]], (v: i32[]) => v[0]); }`)
}

func TestOwnGuardIgnoresShadowingLocal(t *testing.T) {
	wantOK(t, "shadowing-local", ownConsumer+`
function f(xs: i32[]): i32 {
    let consume: (i32[]) => i32 = (v: i32[]) => v[0];
    return consume(xs);
}`)
}

func TestOwnGuardIgnoresShadowingLambdaParameter(t *testing.T) {
	wantOK(t, "shadowing-lambda-param", ownConsumer+`
function f(xs: i32[]): i32 {
    let run: ((i32[]) => i32) = (consume: i32[]) => consume.len();
    return run(xs);
}`)
}

func TestOwnGuardShadowedCalleeDoesNotConsume(t *testing.T) {
	wantOK(t, "shadowing-param-no-consume", ownConsumer+`
function f(own xs: i32[], consume: (i32[]) => i32): i32 {
    let r: i32 = consume(xs);
    return r + xs.len();
}`)
}

func TestOwnGuardIgnoresShadowingLoopVariable(t *testing.T) {
	wantOK(t, "shadowing-loop-var", ownConsumer+`
function f(fns: ((i32[]) => i32)[], xs: i32[]): i32 {
    let n: i32 = 0;
    for consume in fns { n = n + consume(xs); }
    return n;
}`)
}

func TestOwnGuardLocalShadowsOnlyAfterItsDeclaration(t *testing.T) {
	wantE051(t, "local-shadow-after-call", ownConsumer+`
function f(xs: i32[]): i32 {
    let a: i32 = consume(xs);
    let consume: (i32[]) => i32 = (v: i32[]) => v[0];
    return a + consume(xs);
}`)
}

func TestOwnGuardLocalShadowStaysInItsBlock(t *testing.T) {
	wantE051(t, "local-shadow-inner-block", ownConsumer+`
function f(xs: i32[], c: boolean): i32 {
    let n: i32 = 0;
    if (c) { let consume: (i32[]) => i32 = (v: i32[]) => v[0]; n = consume(xs); }
    return n + consume(xs);
}`)
}

func TestOwnGuardArmBinderShadowsOnABorrowedScrutinee(t *testing.T) {
	wantOK(t, "arm-binder-borrowed-scrutinee", `enum Box { B((i32[]) => i32) }
function consume(own xs: i32[]): i32 { return xs[0]; }
function f(b: Box, xs: i32[]): i32 {
    let n: i32 = 0;
    match (b) { B(consume) => { n = consume(xs); } }
    return n;
}`)
}

func TestOwnGuardShadowElsewhereStillGuardsRealCall(t *testing.T) {
	wantE051(t, "shadow-elsewhere", ownConsumer+`
function apply(g: ((i32[]) => i32), zs: i32[]): i32 { return g(zs); }
function f(xs: i32[]): i32 {
    let a: i32 = apply((consume: i32[]) => consume.len(), xs);
    return a + consume(xs);
}`)
}

func TestOwnGuardStillFiresForTheRealFunction(t *testing.T) {
	wantE051(t, "unshadowed-still-guarded", ownConsumer+`
function g(xs: i32[]): i32 {
    return consume(xs);
}`)
}

func TestOwnGuardAllowsPayloadlessVariant(t *testing.T) {
	wantOK(t, "payloadless-variant-arg", `enum Extent { Empty, Wide(i32[]) }
function eat(own sp: Extent): i32 { return 0; }
function f(): i32 {
    return eat(Empty);                 // fresh enum value → owned
}`)
}

func TestOwnGuardAllowsQualifiedPayloadlessVariant(t *testing.T) {
	wantOK(t, "qualified-payloadless-variant-arg", `enum Extent { Empty, Wide(i32[]) }
function eat(own sp: Extent): i32 { return 0; }
function f(): i32 {
    return eat(Extent.Empty);            // fresh enum value → owned
}`)
}

func TestOwnGuardRejectsEnumLocal(t *testing.T) {
	wantE051(t, "enum-local-arg", `enum Extent { Empty, Wide(i32[]) }
function eat(own sp: Extent): i32 { return 0; }
function peek(sp: Extent): i32 { return 0; }
function f(): i32 {
    let sp: Extent = Empty;
    let n: i32 = eat(sp);              // read again below, so not a move
    return n + peek(sp);
}`)
}

func TestOwnGuardAllowsEnumLocalAtLastUse(t *testing.T) {
	wantOK(t, "enum-local-last-use", `enum Extent { Empty, Wide(i32[]) }
function eat(own sp: Extent): i32 { return 0; }
function f(): i32 {
    let sp: Extent = Empty;
    return eat(sp);                    // dead after the call → moved
}`)
}

func TestOwnGuardAllowsAllOwnPtrParamCallResult(t *testing.T) {
	wantOK(t, "all-own-ptr-param-result", ownConsumer+`
function build(own ops: i32[], x: i32): i32[] { return ops.append(x); }
function f(): i32 {
    return consume(build([1, 2], 3));   // build's result is freshly owned → transfer OK
}`)
}

func TestOwnGuardRejectsBorrowedPtrParamCallResult(t *testing.T) {
	wantE051(t, "borrowed-ptr-param-result", ownConsumer+`
function pick(a: i32[], b: i32[]): i32[] { return a; }
function f(): i32 {
    return consume(pick([1, 2], [3, 4]));   // pick may return a borrowed input → E051
}`)
}

func TestParamNamedOwnStillWorks(t *testing.T) {
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
	wantOK(t, "sibling-match-binding-name-reuse", ownConsumer+`
function mkBox(): Box { return Wrap([1, 2]); }
function f(): i32 {
    let a: i32 = 0;
    match (mkBox()) { Wrap(e) => { a = consumeBox(Wrap(e)); } }
    match (mkBox()) { Wrap(e) => { a = a + consumeBox(Wrap(e)); } }
    return a;
}`)
}

func TestConsumingMethodsAccepted(t *testing.T) {
	wantOK(t, "inherent-own-self", `enum List { Cons(i32, List), Nil }
function (own xs: List) inc(): List { match (xs) { Cons(h, t) => { return Cons(h + 1, t.inc()); }, Nil => { return Nil; } } }
function main(): i32 { let ys: List = Cons(1, Nil).inc(); return 0; }`)

	wantE051(t, "borrowed-receiver-to-consuming-method", `enum List { Cons(i32, List), Nil }
function (own xs: List) inc(): List { match (xs) { Cons(h, t) => { return Cons(h + 1, t.inc()); }, Nil => { return Nil; } } }
function f(borrowed: List): i32 { let ys: List = borrowed.inc(); return 0; }`)
}

func TestOwnGuardAllowsSelfReassignMove(t *testing.T) {
	if err := checkSource(t, ownConsumer+`
struct B { items: i32[] }
function grow(own b: B, x: i32): B { return B { items: b.items.append(x) }; }
function f(): i32 {
    let a = B { items: [] };
    a = grow(a, 1);
    a = grow(a, 2);
    return a.items.len();
}`); err != nil {
		t.Errorf("self-reassign own move should check, got: %v", err)
	}
}

func TestOwnGuardRejectsSelfReassignSecondRead(t *testing.T) {
	wantE051(t, "self-reassign-second-read", ownConsumer+`
struct B { items: i32[] }
function grow(own b: B, x: i32): B { return B { items: b.items.append(x) }; }
function f(): i32 {
    let a = B { items: [7] };
    a = grow(a, a.items[0]);
    return a.items.len();
}`)
}

func TestOwnGuardAllowsSelfReassignMoveThroughTry(t *testing.T) {
	wantOK(t, "self-reassign-through-try", ownConsumer+`
struct B { items: i32[] }
function grow(own b: B, x: i32): Result[B, string] { return Ok(B { items: b.items.append(x) }); }
function (k: B) into(own b: B): Result[B, string] { return Ok(b); }
function f(k: B): Result[i32, string] {
    let a = B { items: [] };
    a = grow(a, 1)?;
    a = k.into(a)?;
    return Ok(a.items.len());
}`)
}

func TestOwnGuardRejectsSelfReassignThroughTrySecondRead(t *testing.T) {
	wantE051(t, "self-reassign-through-try-second-read", ownConsumer+`
struct B { items: i32[] }
function grow(own b: B, x: i32): Result[B, string] { return Ok(B { items: b.items.append(x) }); }
function f(): Result[i32, string] {
    let a = B { items: [7] };
    a = grow(a, a.items[0])?;
    return Ok(a.items.len());
}`)
}

func TestOwnGuardAllowsATryResult(t *testing.T) {
	wantOK(t, "try-result-into-own", ownConsumer+`
function make(n: i32): Result[i32[], string] { return Ok([n]); }
function f(): Result[i32, string] {
    let a: i32 = consume(make(1)?);
    let xs: i32[] = make(2)?;
    return Ok(a + consume(xs));
}`)
}

func TestOwnGuardAllowsAFieldUnpackedFromAnOwnParam(t *testing.T) {
	wantOK(t, "own-param-field-unpack", ownConsumer+`
function f(own p: Pair): i32 {
    let n: i32 = p.n;
    let xs: i32[] = p.items;
    let k: i32 = consume(xs);
    return k + n;
}`)
}

func TestOwnGuardRejectsAFieldOfAnOwnParamReadTwice(t *testing.T) {
	wantE051(t, "own-param-field-read-twice", ownConsumer+`
function f(own p: Pair): i32 {
    let xs: i32[] = p.items;
    let k: i32 = consume(xs);
    return k + p.items.len();
}`)
}

func TestOwnGuardRejectsAFieldOfABorrowedParam(t *testing.T) {
	wantE051(t, "borrowed-param-field-unpack", ownConsumer+`
function f(p: Pair): i32 {
    let xs: i32[] = p.items;
    let k: i32 = consume(xs);
    return k;
}`)
}

func TestOwnGuardAllowsSelfReassignMoveInLambda(t *testing.T) {
	wantOK(t, "self-reassign-in-lambda", ownConsumer+`
struct B { items: i32[] }
function grow(own b: B, x: i32): B { return B { items: b.items.append(x) }; }
function f(): i32 {
    let lam = (): i32 => {
        let a = B { items: [] };
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
        let a = B { items: [] };
        a = grow(a, 1);
        return a.items.len();
    }
    return inner();
}`)
}

func TestOwnGuardAllowsLocalFuncOwnParamTransfer(t *testing.T) {
	wantOK(t, "local-func-own-param-transfer", ownConsumer+`
function f(): i32 {
    function inner(own b: i32[]): i32 { return consume(b); }
    return inner([1]);
}`)
}

func TestOwnGuardRejectsKeptAliveLocalInLambda(t *testing.T) {
	wantE051(t, "kept-alive-local-in-lambda", ownConsumer+`
struct B { items: i32[] }
function grow(own b: B, x: i32): B { return B { items: b.items.append(x) }; }
function f(): i32 {
    let lam = (): i32 => {
        let a = B { items: [] };
        let c = grow(a, 1);
        return c.items.len();
    };
    return lam();
}`)
}

func TestOwnedUseAfterConsumeInLambda(t *testing.T) {
	wantE050(t, "double-consume-in-lambda", ownPrelude+`
function f(own xs: i32[]): i32 {
    let lam = (): i32 => {
        let a: i32 = sink(xs);
        return a + sink(xs);   // E050: xs already moved
    };
    return lam();
}`)
}

func TestOwnedNestedParamShadowsOuter(t *testing.T) {
	src := ownPrelude + `
function f(own xs: i32[]): i32 {
    let lam = (xs: i32[]): i32 => { return sink(xs); };
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
    let a: Asm = Asm { code: [], cfi: Cfi { rules: [], n: 0 } };
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

func TestOwnGuardRejectsSupersededFieldMoveWithAliasedBase(t *testing.T) {
	wantE051(t, "field-move-base-also-passed", fieldMovePrelude+`
function record2(own s: Cfi, a: Asm): Cfi { return Cfi { rules: s.rules.append(a.code.len()), n: s.n + 1 }; }
function step(own a: Asm): Asm {
    a = Asm { ...a, cfi: record2(a.cfi, a) };
    return a;
}`)
}

func TestOwnGuardRejectsSupersededFieldMoveSecondRead(t *testing.T) {
	wantE051(t, "field-move-second-read", fieldMovePrelude+`
function step(own a: Asm): Asm {
    a = Asm { ...a, cfi: record(a.cfi, a.cfi.n) };
    return a;
}`)
}

func TestOwnGuardRejectsSupersededFieldMoveIntoOtherField(t *testing.T) {
	wantE051(t, "field-move-other-field", `struct Cfi { rules: i32[], n: i32 }
struct Asm { code: i32[], cfi: Cfi, cfi2: Cfi }
function record(own s: Cfi, v: i32): Cfi { return Cfi { rules: s.rules.append(v), n: s.n + 1 }; }
function step(own a: Asm): Asm {
    a = Asm { ...a, cfi2: record(a.cfi, 1) };
    return a;
}`)
}

func TestOwnGuardRejectsSupersededFieldMoveKeptAliveBase(t *testing.T) {
	wantE051(t, "field-move-kept-alive-base", fieldMovePrelude+`
function build(): i32 {
    let a: Asm = Asm { code: [], cfi: Cfi { rules: [], n: 0 } };
    let b: Asm = Asm { ...a, cfi: record(a.cfi, 1) };
    return b.cfi.n + a.cfi.n;
}`)
}

func TestOwnGuardRejectsSupersededFieldMoveOnShadowingBorrowedParam(t *testing.T) {
	wantE051(t, "field-move-nested-borrowed-param", fieldMovePrelude+`
function build(): i32 {
    let a: Asm = Asm { code: [], cfi: Cfi { rules: [], n: 0 } };
    function inner(a: Asm): Asm {
        a = Asm { ...a, cfi: record(a.cfi, 1) };
        return a;
    }
    return inner(a).cfi.n;
}`)
}

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
    let y: i32[] = xs;
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

func TestOwnGuardAcceptsAFreshResultThroughACallChain(t *testing.T) {
	wantOK(t, "fresh-through-a-chain", ownConsumer+`
function sized(xs: i32[]): i32[] { return [xs.len()]; }
function relay(ys: i32[]): i32[] { return sized(ys); }
function main(): i32 { return consume(relay([1, 2])); }`)
}

func TestOwnGuardAcceptsAFreshResultWrittenWithACast(t *testing.T) {
	wantOK(t, "fresh-written-with-a-cast", ownConsumer+`
function widen(xs: i64[]): i32[] {
    let out: i32[] = [0, 0];
    out = out.with(0, xs[0] as i32);
    return out;
}
function main(): i32 { return consume(widen([1, 2])); }`)
}

func TestOwnGuardAcceptsAResultBuiltFromAViewsValues(t *testing.T) {
	wantOK(t, "built-from-a-views-values", ownConsumer+`
function bytes(own out: i32[], b: [i32]): i32[] {
    let i: i32 = 0;
    while (i < b.len()) { out = out.append(b[i]); i = i + 1; }
    return out;
}
function main(): i32 { let a: i32[] = [1, 2]; return consume(bytes(bytes([], a), a)); }`)
}

func TestOwnGuardRejectsAResultHoldingAPointerElement(t *testing.T) {
	wantE051(t, "holding-a-pointer-element", `function keep(own ys: string[]): i32 { return ys.len(); }
function firsts(xs: string[]): string[] {
    let out: string[] = [];
    out = out.append(xs[0]);
    return out;
}
function main(): i32 { return keep(firsts(["a"])); }`)
}

func TestOwnGuardAcceptsAFreshResultFromARecursiveFunction(t *testing.T) {
	wantOK(t, "fresh-from-recursion", ownConsumer+`
function grow(xs: i32[], k: i32): i32[] {
    if (k <= 0) { return [xs.len()]; }
    return grow(xs, k - 1);
}
function main(): i32 { return consume(grow([1, 2], 3)); }`)
}

func TestOwnGuardRejectsARecursiveResultThatCanBeTheParameter(t *testing.T) {
	wantE051(t, "recursive-result-is-the-parameter", ownConsumer+`
function last(xs: i32[], k: i32): i32[] {
    if (k <= 0) { return xs; }
    return last(xs, k - 1);
}
function main(): i32 { return consume(last([1, 2], 3)); }`)
}

func TestOwnGuardAcceptsAThreadedOwnParameter(t *testing.T) {
	wantOK(t, "threaded-own-parameter", ownConsumer+`
function thread(own xs: i32[], k: i32): i32[] {
    if (k <= 0) { return xs; }
    return [k];
}
function main(): i32 { return consume(thread([1, 2], 3)); }`)
}

func TestOwnGuardAllowsLiteralBuiltLocalAtLastUse(t *testing.T) {
	prelude := `struct W { d: i64[], tag: string }
function take(own xs: i64[]): i32 { return xs.len() as i32; }
function eat(own w: W): i32 { return w.tag.len() as i32; }
function takes(own s: string): i32 { return s.len() as i32; }
`
	wantOK(t, "grown-array-in-binary", prelude+`function f(): i32 {
    let xs: i64[] = [];
    xs = xs.append(1);
    return take(xs) + 0;
}`)
	wantOK(t, "grown-array-in-var", prelude+`function f(): i32 {
    let xs: i64[] = [];
    xs = xs.append(1);
    let r: i32 = take(xs);
    return r;
}`)
	wantOK(t, "struct-literal", prelude+`function f(): i32 {
    let w: W = W { d: [1], tag: "t" };
    let r: i32 = eat(w);
    return r;
}`)
	wantOK(t, "string-literal", prelude+`function f(): i32 {
    let s: string = "ab";
    s = s + "c";
    let r: i32 = takes(s);
    return r;
}`)
	wantE051(t, "spread-literal", prelude+`function f(v: W): i32 {
    let w: W = W { ...v, tag: "u" };
    let r: i32 = eat(w);
    return r;
}`)
}

func TestOwnGuardAllowsStoreAfterAGap(t *testing.T) {
	prelude := `struct Pair { a: i32[], b: boolean[] }
function step(own xs: i32[], own flags: boolean[]): Pair { return Pair { a: xs, b: flags }; }
function take(own flags: boolean[]): i32 { return flags.len(); }
`
	wantOK(t, "second-of-two-stores", prelude+`function f(): i32 {
    let xs: i32[] = [];
    let flags: boolean[] = [false];
    let i: i32 = 0;
    while (i < 3) {
        let p: Pair = step(xs, flags);
        xs = p.a;
        flags = p.b;
        i = i + 1;
    }
    return xs.len() + flags.len();
}`)
	wantOK(t, "unrelated-var-between", prelude+`function f(): i32 {
    let flags: boolean[] = [false];
    let n: i32 = take(flags);
    let m: i32 = 3;
    flags = [true];
    return n + m + flags.len();
}`)
	wantE051(t, "read-between", prelude+`function f(): i32 {
    let flags: boolean[] = [false];
    let n: i32 = take(flags);
    let m: i32 = flags.len();
    flags = [true];
    return n + m + flags.len();
}`)
	wantE051(t, "branch-between", prelude+`function f(k: i32): i32 {
    let flags: boolean[] = [false];
    let n: i32 = take(flags);
    if (k > 0) { k = k + 1; }
    flags = [true];
    return n + k + flags.len();
}`)
}

const memberUnionPrelude = `struct Node { xs: i32[], tag: i32 }
struct Leaf { v: i32 }
type Tree = Leaf | Node;
function grow(own xs: i32[]): i32[] { return xs.append(1); }
function consume(own n: Node): i32 { return n.xs.len(); }
`

func TestOwnGuardMemberBindingOfOwnedScrutinee(t *testing.T) {
	wantOK(t, "member-field-move", memberUnionPrelude+`
function bump(own t: Tree): Tree {
    match (t) {
        Leaf(l) => { return Leaf { ...l, v: l.v + 1 }; },
        Node(n) => { return Node { ...n, xs: grow(n.xs) }; }
    }
}`)
	wantOK(t, "member-bare", memberUnionPrelude+`
function bump(own t: Tree): i32 {
    match (t) {
        Leaf(l) => { return l.v; },
        Node(n) => { return consume(n); }
    }
}`)
	wantOK(t, "member-literal-returned-as-union", memberUnionPrelude+`
function bump(own n: Node): Tree { return Node { ...n, xs: grow(n.xs) }; }`)
	wantE051(t, "member-literal-stored-under-another-name", memberUnionPrelude+`
function bump(own n: Node): Tree {
    let t: Tree = Leaf { v: 0 };
    t = Node { ...n, xs: grow(n.xs) };
    return t;
}`)
}

func TestOwnGuardMemberBindingOfBorrowedScrutinee(t *testing.T) {
	wantE051(t, "member-field-move", memberUnionPrelude+`
function bump(t: Tree): Tree {
    match (t) {
        Leaf(l) => { return Leaf { ...l, v: l.v + 1 }; },
        Node(n) => { return Node { ...n, xs: grow(n.xs) }; }
    }
}`)
	wantE051(t, "member-bare", memberUnionPrelude+`
function bump(t: Tree): i32 {
    match (t) {
        Leaf(l) => { return l.v; },
        Node(n) => { return consume(n); }
    }
}`)
	wantE051(t, "payload-field-move", `struct Cfi { rules: i32[] }
enum Box { Full(Cfi), Empty }
function grow(own xs: i32[]): i32[] { return xs.append(1); }
function bump(b: Box): Box {
    match (b) {
        Full(c) => { return Full(Cfi { ...c, rules: grow(c.rules) }); },
        Empty => { return Empty; }
    }
}`)
}

const byteConsumer = `function take(own xs: u8[]): i32 { return xs.len(); }
`

func TestOwnGuardAcceptsAByteKernelResult(t *testing.T) {
	wantOK(t, "byte-kernel-result", byteConsumer+`
function main(): i32 {
    let v: u8[] = [1, 2, 3];
    return take(__bytes_owned(v)) + take(__bytes_range(v, 0, 2));
}`)
}

func TestOwnGuardAcceptsAFunctionReturningAByteKernelResult(t *testing.T) {
	wantOK(t, "byte-kernel-result-through-a-function", byteConsumer+`
function copy(b: [u8]): u8[] { return __bytes_owned(b); }
function main(): i32 {
    let v: u8[] = [1, 2, 3];
    return take(copy(v));
}`)
}
