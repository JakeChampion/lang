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
	if !hasCode(err, "E050") {
		t.Errorf("%s: expected an E050 error, got: %v", name, err)
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

// ownHandle declares a struct with a method that consumes its receiver, so
// the struct is a user-defined handle.
const ownHandle = `struct Res { v: i32[], n: i32 }
function (own r: Res) step(): (Res, boolean) { return (r, false); }
`

func wantE051(t *testing.T, name, src string) {
	t.Helper()
	err := checkSource(t, src)
	if err == nil {
		t.Fatalf("%s: expected an E051 ownership error, got none", name)
	}
	if !hasCode(err, "E051") {
		t.Errorf("%s: expected an E051 error, got: %v", name, err)
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
		// Two loops declare the same names. The reads in the second loop
		// belong to its own bindings (#14100).
		{"same-name-in-sibling-loops", `
function step(own xs: i32[]): (i32[], i32) { let n: i32 = xs.len(); return (xs, n); }
function f(): i32 {
    let t: i32 = 0;
    let g: i32[] = [1];
    for i in 0..2 {
        let (a, u) = step(g);
        let (b, v) = step(a);
        g = b;
        t = t + u + v;
    }
    for j in 0..2 {
        let (a, u) = step(g);
        let (b, v) = step(a);
        g = b;
        t = t + u + v;
    }
    return t + consume(g);
}`},
		{"same-name-array-in-sibling-blocks", `
function f(c: boolean): i32 {
    let t: i32 = 0;
    if (c) {
        let xs: i32[] = [1];
        t = t + consume(xs);
    }
    if (!c) {
        let xs: i32[] = [2];
        t = t + consume(xs);
    }
    return t;
}`},
		// The fields of an own parameter that a destructure takes are owned,
		// also as a method receiver (#14101).
		{"destructured-field-as-receiver", `
struct Holder { box: Box, n: i32 }
function (own b: Box) open(): i32 { return 0; }
function f(own h: Holder): i32 {
    let Holder { box, n } = h;
    return box.open() + n;
}`},
		// The fields that a destructure takes from a fresh local at its
		// last use are owned too (#14144).
		{"destructured-fresh-local-at-last-use", `
struct Holder { box: Box, n: i32 }
function mk(): Holder { return Holder { box: Wrap([1]), n: 2 }; }
function (own b: Box) open(): i32 { return 0; }
function f(): i32 {
    let h: Holder = mk();
    let Holder { box, n } = h;
    return box.open() + n;
}`},
		{"destructured-field-given-a-fresh-local", `
struct Holder { box: Box, n: i32 }
function step(own b: Box): (Box, i32) { return (b, 1); }
function f(own h: Holder, c: boolean): i32 {
    let Holder { box, n } = h;
    if (c) {
        let (b2, k) = step(box);
        box = b2;
    }
    return consumeBox(box) + n;
}`},
		// A scalar result is a copy, so the array built from it is fresh
		// (#14104).
		{"fresh-array-from-a-scalar-helper", `
struct Wv { v: i32[] }
function at(w: Wv, j: i32): i32 { return w.v[j]; }
function copyOf(w: Wv): i32[] {
    let out: i32[] = [];
    for j in 0..w.v.len() {
        out = out.append(at(w, j));
    }
    return out;
}
function f(w: Wv): i32 {
    return consume(copyOf(w));
}`},
		// An alias that takes a local at its last use can move a handle type,
		// after a self-update of that local (#14109).
		{"alias-after-a-self-update", ownHandle + `
function f(own r0: Res): i32 {
    let r1: Res = r0;
    r1 = Res { ...r1, n: 1 };
    let r2: Res = r1;
    let (r3, k) = r2.step();
    r2 = r3;
    return r2.n;
}`},
		{"alias-of-an-unpack-after-a-self-update", ownHandle + `
function split(own r: Res): (Res, boolean) { return (r, true); }
function f(own r0: Res): i32 {
    let (r1, k) = split(r0);
    if (k) { r1 = Res { ...r1, n: r1.n + 1 }; }
    let r2: Res = r1;
    if (r2.n > 0) {
        let (r3, j) = r2.step();
        r2 = r3;
    }
    let (r4, m) = r2.step();
    return r4.n;
}`},
		{"inner-binding-shadows-the-outer", `
function f(c: boolean): i32 {
    let xs: i32[] = [1];
    let t: i32 = 0;
    if (c) {
        let xs: i32[] = [2];
        t = consume(xs);
    }
    return t + xs.len();
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
		// The call is in a block that later shadows the name, but the read
		// after the block is of the outer binding (#14100).
		{"outer-binding-read-after-the-shadowing-block", `
function f(c: boolean): i32 {
    let xs: i32[] = [1];
    let t: i32 = 0;
    if (c) {
        t = consume(xs);
        let xs: i32[] = [2];
        t = t + xs.len();
    }
    return t + xs.len();
}`},
		// The helper gives back the array of its parameter, which the
		// caller still holds (#14104).
		{"helper-result-is-the-parameter-array", `
struct Wv { v: i32[] }
function inner(w: Wv): i32[] { return w.v; }
function f(w: Wv): i32 {
    return consume(inner(w));
}`},
		// An alias of a handle type moves only when it takes its source at
		// the last use and is given only owned values (#14109).
		{"alias-whose-source-is-read-again", ownHandle + `
function f(own r0: Res): i32 {
    let r1: Res = r0;
    r1 = Res { ...r1, n: 1 };
    let r2: Res = r1;
    let (r3, k) = r2.step();
    return r3.n + r1.n;
}`},
		{"alias-given-a-borrowed-value", ownHandle + `
struct Hold { r: Res }
function f(own r0: Res, h: Hold): i32 {
    let r1: Res = r0;
    r1 = Res { ...r1, n: 1 };
    let r2: Res = r1;
    r2 = h.r;
    let (r3, k) = r2.step();
    return r3.n;
}`},
		{"destructured-read-again", `
function step(): (i32[], i32) { return ([1], 2); }
function f(): i32 {
    let (xs, k) = step();
    let n: i32 = consume(xs);
    return n + xs.len() + k;
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

func TestOwnGuardAllowsAPayloadBoundInALoop(t *testing.T) {
	wantOK(t, "payload-bound-in-loop", ownConsumer+`
enum Next { Part(i32[]), Done }
function step(n: i32): Next { return Part([n]); }
function f(): i32 {
    let n: i32 = 0;
    while (n < 3) {
        match (step(n)) {
            Part(r1) => {
                n = n + consume(r1);
            },
            Done => {
                return n;
            }
        }
    }
    return n;
}`)
}

func TestOwnGuardAllowsATryPayloadBoundInALoop(t *testing.T) {
	wantOK(t, "try-payload-bound-in-loop", ownConsumer+`
enum Next { Part(i32[]), Done }
function step(n: i32): Result[Next, string] { return Ok(Part([n])); }
function f(): Result[i32, string] {
    let n: i32 = 0;
    while (n < 3) {
        match (step(n)?) {
            Part(r1) => {
                n = n + consume(r1);
            },
            Done => {
                return Ok(n);
            }
        }
    }
    return Ok(n);
}`)
}

// A handle-free payload is a fresh local, so a move inside a loop is not at
// its last use.
func TestOwnGuardRejectsATryPayloadBoundBeforeALoop(t *testing.T) {
	wantE051(t, "try-payload-bound-before-loop", ownConsumer+`
enum Next { Part(i32[]), Done }
function step(n: i32): Result[Next, string] { return Ok(Part([n])); }
function f(): Result[i32, string] {
    match (step(1)?) {
        Part(r1) => {
            let n: i32 = 0;
            while (n < 3) {
                n = n + consume(r1);
            }
            return Ok(n);
        },
        Done => {
            return Ok(0);
        }
    }
    return Ok(0);
}`)
}

const freshResultPrelude = ownConsumer + `
function make(n: i32): Result[i32[], string] { return Ok([n]); }
function opt(n: i32): Option[i32[]] { return Some([n]); }
function keepStr(own s: string): i32 { return s.len(); }
`

func TestOwnGuardAllowsAFreshResultPayload(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"match", `
function f(): i32 {
    match (make(1)) {
        Ok(xs) => { return consume(xs); },
        Err(e) => { return keepStr(e); }
    }
    return 0;
}`},
		{"let-in-arm", `
function f(): i32 {
    match (make(1)) {
        Ok(xs) => {
            let k: i32 = consume(xs);
            return k;
        },
        Err(_) => { return 0; }
    }
    return 0;
}`},
		{"let-else", `
function f(): i32 {
    let Ok(xs) = make(2) else { return 0; };
    let k: i32 = consume(xs);
    return k;
}`},
		{"if-let-some-in-loop", `
function f(): i32 {
    let t: i32 = 0;
    for i in 0..3 {
        if let Some(xs) = opt(i) {
            t = t + consume(xs);
        }
    }
    return t;
}`},
		// The in-place AEAD tests' shape: a fresh buffer handed to a method's own parameter.
		{"into-an-own-method-param", `
struct K { n: i32 }
function (k: K) open(own buf: u8[]): i32 { return buf.len() + k.n; }
function sealed(seq: u64): Result[u8[], string] { return Ok([seq as u8]); }
function f(k: K): i32 {
    let Ok(buf) = sealed(1 as u64) else { return 0; };
    return k.open(buf);
}`},
		{"string-compared-then-used", `
function rs(n: i64): Result[string, string] { return Ok("x"); }
function f(): string {
    match (rs(1 as i64)) {
        Ok(h) => {
            if (h != "y") {
                return "a" + h;
            }
        },
        Err(_) => { return ""; }
    }
    return "";
}`},
	} {
		wantOK(t, "fresh-result-payload-"+tc.name, freshResultPrelude+tc.body)
	}
}

func TestOwnGuardAllowsABuiltinVariantConstruction(t *testing.T) {
	const keepers = `
function keepR(own r: Result[i32[], string]): i32 { return 0; }
function keepO(own o: Option[i32[]]): i32 { return 0; }
`
	for _, tc := range []struct{ name, body string }{
		{"ok", `function f(): i32 { return keepR(Ok([1])); }`},
		{"err", `function f(): i32 { return keepR(Err("e")); }`},
		{"some", `function f(): i32 { return keepO(Some([1])); }`},
		{"none", `function f(): i32 { return keepO(None); }`},
		{"scrutinee", `
function f(): i32 {
    match (Some([1])) {
        Some(xs) => { return consume(xs); },
        None => { return 0; }
    }
    return 0;
}`},
	} {
		wantOK(t, "builtin-variant-"+tc.name, freshResultPrelude+keepers+tc.body)
	}
}

func TestOwnGuardReadsThePayloadOfTheScrutineesEnum(t *testing.T) {
	const enums = `
enum Opt { Some(i32), None }
enum A { V(i32) }
enum B { V(i32[]) }
function opt(n: i32): Option[i32[]] { return Option.Some([n]); }
`
	wantOK(t, "builtin-payload-beside-a-user-variant", ownConsumer+enums+`
function f(): i32 {
    match (opt(2)) { Some(xs) => { return consume(xs); }, None => { return 0; } }
    return 0;
}`)
	wantOK(t, "scalar-payload-beside-a-pointer-variant", ownConsumer+enums+`
function f(own a: A): i32 {
    match (a) { V(n) => { return n; } }
    let again: A = a;
    return 0;
}`)
	wantE050(t, "own-builtin-matched-beside-a-user-variant", ownConsumer+enums+`
function f(own o: Option[i32[]]): i32 {
    match (o) { Some(xs) => { return consume(xs); }, None => { return 0; } }
    let again: Option[i32[]] = o;
    return 0;
}`)
}

func TestOwnGuardRejectsAResultPayloadThatMayBorrow(t *testing.T) {
	wantE051(t, "result-payload-through-a-borrowing-callee", freshResultPrelude+`
function finish(own w: Writer): i32 { return 0; }
function relay(r: Result[Writer, string]): Result[Writer, string] { return r; }
function f(r: Result[Writer, string]): i32 {
    let Ok(w) = relay(r) else { return 0; };
    return finish(w);
}`)
	wantOK(t, "handle-free-payload-through-a-borrowing-callee", freshResultPrelude+`
function relay(r: Result[i32[], string]): Result[i32[], string] { return r; }
function f(r: Result[i32[], string]): i32 {
    let Ok(xs) = relay(r) else { return 0; };
    return consume(xs);
}`)
}

// A payload that can hold no handle is a fresh local, so it moves only at its
// last use, as a let local does.
func TestOwnGuardRejectsAFreshResultPayloadMovedBeforeItsLastUse(t *testing.T) {
	wantE051(t, "fresh-result-payload-moved-twice", freshResultPrelude+`
function f(): i32 {
    let Ok(xs) = make(2) else { return 0; };
    let a: i32 = consume(xs);
    return a + consume(xs);
}`)
	wantE050(t, "fresh-handle-payload-closed-twice", `
function f(path: string): i32 {
    let Ok(r) = open_reader(path) else { return 0; };
    r.close();
    r.close();
    return 0;
}`)
}

// Issues #14089 and #14075: a payload bound from an owned scrutinee whose
// type cannot hold a handle is a local like any other let. A copy keeps both
// names, a store keeps the name readable, and a move must be its last use.
func TestOwnGuardHandleFreePayloadIsALocal(t *testing.T) {
	const prelude = `function make(): Result[u8[], string] { return Ok([1, 2]); }
function keep(own xs: u8[]): i32 { return xs[0] as i32; }
struct T { aux: string }
function take(t: T, n: i32): i32 { return t.aux.len() + n; }
function pick(xs: string[]): Option[string] { for x in xs { return Some(x); } return None; }
`
	wantOK(t, "let-else-copied-then-updated", prelude+`
function f(): i32 {
    let Ok(first) = make() else { return 1; };
    let retained = first;
    first = first.with(0, 77);
    return (retained[0] + first[0]) as i32;
}`)
	wantOK(t, "if-let-copied-then-updated", prelude+`
function f(): i32 {
    if let Ok(first) = make() {
        let retained = first;
        first = first.with(0, 77);
        return (retained[0] + first[0]) as i32;
    }
    return 1;
}`)
	wantOK(t, "stored-then-read", prelude+`
function f(xs: string[]): i32 {
    if let Some(u) = pick(xs) {
        let t = T { aux: u };
        return take(t, u.len());
    }
    return 0;
}`)
	wantOK(t, "copied-then-moved-at-its-last-use", prelude+`
function f(): i32 {
    let Ok(xs) = make() else { return 1; };
    let a = xs;
    let k: i32 = keep(xs);
    return k + a.len();
}`)
	wantOK(t, "handle-payload-closed-after-a-lambda-captured-it", `
function span(r: Reader, offset: i64, len: i64): Option[u8[]] { return None; }
function ranges(f: (i64, i64) => Option[u8[]]): Option[u8[]] { return f(0 as i64, 1 as i64); }
function f(path: string): Option[u8[]] {
    match (open_reader(path)) {
        Ok(r) => {
            let got: Option[u8[]] = ranges((offset: i64, len: i64): Option[u8[]] => span(r, offset, len));
            r.close();
            return got;
        },
        Err(e) => { return None; }
    }
    return None;
}`)
}

func TestOwnGuardCopiesFreshPatternPayload(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"let-else", `function f(): i32 {
    let Ok(xs) = make(2) else { return 0; };
    let retained = xs;
    xs = xs.with(0, 77);
    return retained[0] + xs[0];
}`},
		{"statement-arm", `function f(): i32 {
    match (make(2)) {
        Ok(xs) => { let retained = xs; xs = xs.with(0, 77); return retained[0] + xs[0]; },
        Err(_) => { return 0; }
    }
    return 0;
}`},
		{"if-let", `function f(): i32 {
    if let Ok(xs) = make(2) {
        let retained = xs;
        xs = xs.with(0, 77);
        return retained[0] + xs[0];
    }
    return 0;
}`},
		{"value-arm", `function f(): i32 {
    return match (make(2)) {
        Ok(xs) => { let retained = xs; xs = xs.with(0, 77); retained[0] + xs[0] },
        Err(_) => 0
    };
}`},
		{"shadow-owned", `function f(own xs: i32[]): i32 {
    let n = consume(xs);
    if let Ok(xs) = make(2) {
        let retained = xs;
        xs = xs.with(0, 77);
        n = n + retained[0] + xs[0];
    }
    return n;
}`},
		{"shadow-borrowed", `function f(xs: i32[]): i32 {
    if let Ok(xs) = make(2) { return consume(xs); }
    return 0;
}`},
		{"lambda-local", `function run(g: () => i32): i32 { return g(); }
function f(): i32 {
    return run((): i32 => {
        let Ok(xs) = make(2) else { return 0; };
        let retained = xs;
        return retained[0] + xs[0];
    });
}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantOK(t, "fresh-pattern-copy-"+tc.name, freshResultPrelude+tc.body)
		})
	}
}

func TestOwnGuardKeepsLinearPatternSources(t *testing.T) {
	wantOK(t, "owned-handle-free-source-payload-is-a-local", freshResultPrelude+`
function f(own r: Result[i32[], string]): i32 {
    let Ok(xs) = r else { return 0; };
    let retained = xs;
    return retained[0] + xs[0];
}`)
	wantE050(t, "owned-handle-source-payload-stays-linear", `
function f(own r: Result[Writer, string]): i32 {
    let Ok(w) = r else { return 0; };
    let retained = w;
    return retained.fd + w.fd;
}`)
	wantE050(t, "handle-payload-stays-linear", `
function opened(): Result[Writer, string] { return Ok(stdout()); }
function f(): i32 {
    let Ok(w) = opened() else { return 0; };
    let retained = w;
    return retained.fd + w.fd;
}`)
	wantE050(t, "outer-move-survives-a-pattern-shadow", freshResultPrelude+`
function f(own xs: i32[]): i32 {
    let n = consume(xs);
    if let Ok(xs) = make(2) { let retained = xs; n = n + retained[0] + xs[0]; }
    return n + xs[0];
}`)
}

func TestOwnedUseAfterResultPayloadTaken(t *testing.T) {
	wantE050(t, "own-result-matched-then-used", freshResultPrelude+`
function f(own r: Result[i32[], string]): i32 {
    match (r) {
        Ok(xs) => { return consume(xs); },
        Err(_) => { return 0; }
    }
    let again: Result[i32[], string] = r;
    return 0;
}`)
}

func TestOwnedOperandIsBorrowed(t *testing.T) {
	const keepStr = "function keepStr(own s: string): i32 { return s.len(); }\n"
	wantOK(t, "own-compared-then-used", keepStr+`
function f(own s: string, t: string): string {
    if (s != t) {
        return "a" + s;
    }
    return "";
}`)
	wantOK(t, "own-concatenated-twice", keepStr+`
function f(own s: string): string {
    let a: string = s + "x";
    return a + s;
}`)
	wantE050(t, "own-compared-then-moved-twice", keepStr+`
function f(own s: string): i32 {
    if (s == "x") {
        return keepStr(s) + keepStr(s);
    }
    return 0;
}`)
}

func TestOwnedTryMovesItsOperand(t *testing.T) {
	wantE050Move(t, "own-result-tried-twice", `
function f(own r: Result[i32[], string]): Result[i32, string] {
    let a: i32[] = r?;
    let b: i32[] = r?;
    return Ok(a.len() + b.len());
}`)
	wantOK(t, "own-result-tried-once", `
function f(own r: Result[i32[], string]): Result[i32, string] {
    let a: i32[] = r?;
    return Ok(a.len());
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
	wantE051(t, "own-param-field-read-twice", ownHandleConsumer+`
function f(own p: HPair): i32 {
    let h: Handle = p.h;
    let k: i32 = sinkh(h);
    return k + p.h.fd;
}`)
}

func TestOwnGuardRejectsAFieldOfABorrowedParam(t *testing.T) {
	wantE051(t, "borrowed-param-field-unpack", ownHandleConsumer+`
function f(p: HPair): i32 {
    let h: Handle = p.h;
    let k: i32 = sinkh(h);
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

// A lambda is decided as a function of its own, so its local moves at its
// last use, as a local of a nested function does.
func TestOwnGuardAllowsALambdaLocalAtItsLastUse(t *testing.T) {
	wantOK(t, "lambda-local-at-its-last-use", ownConsumer+`
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
	// A payload declared inside a lambda follows the same last-use rule.
	wantOK(t, "payload-at-last-use-in-lambda", ownConsumer+`
enum Bag { Full(i32[]), Empty }
function mk(n: i32): Bag { return Full([n]); }
function run(f: () => i32): i32 { return f(); }
function f(): i32 {
    return run((): i32 => {
        match (mk(1)) {
            Full(xs) => { return consume(xs); },
            Empty => { return 0; }
        }
    });
}`)
	wantE051(t, "payload-read-after-move-in-lambda", ownConsumer+`
enum Bag { Full(i32[]), Empty }
function mk(n: i32): Bag { return Full([n]); }
function run(f: () => i32): i32 { return f(); }
function f(): i32 {
    return run((): i32 => {
        match (mk(1)) {
            Full(xs) => { return consume(xs) + xs.len(); },
            Empty => { return 0; }
        }
    });
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

// The superseded field can move into a call that is an argument of another
// call (#14357).
func TestOwnGuardAllowsSupersededFieldMoveInNestedCall(t *testing.T) {
	wantOK(t, "field-move-nested-call", fieldMovePrelude+`
function again(own s: Cfi): Cfi { return s; }
function step(own a: Asm, v: i32): Asm {
    a = Asm { ...a, cfi: again(record(a.cfi, v)) };
    return Asm { ...a, cfi: again(record(a.cfi, v)) };
}`)
}

// A lambda in the field value can run later, so it does not take the field.
func TestOwnGuardRejectsSupersededFieldMoveInLambda(t *testing.T) {
	wantE051(t, "field-move-lambda", fieldMovePrelude+`
function apply(f: () => Cfi): Cfi { return f(); }
function step(own a: Asm, v: i32): Asm {
    return Asm { ...a, cfi: apply(() => record(a.cfi, v)) };
}`)
}

// A let initializer takes the superseded field as a return does (#14111).
func TestOwnGuardAllowsSupersededFieldMoveOnLet(t *testing.T) {
	wantOK(t, "field-move-let-form", fieldMovePrelude+`
function step(own a: Asm, v: i32): Asm {
    let b: Asm = Asm { ...a, cfi: record(a.cfi, v) };
    return b;
}`)
}

// The else block of a let-else comes before the move in the source, so its
// read of the base is not a later read of the field.
func TestOwnGuardAllowsSupersededFieldMoveAfterLetElse(t *testing.T) {
	wantOK(t, "field-move-after-let-else", fieldMovePrelude+`
function step(own a: Asm, o: Option[i32]): Asm {
    let Some(v) = o else {
        return a;
    };
    let c: Cfi = record(a.cfi, v);
    return Asm { ...a, cfi: c };
}`)
}

// A read of the field after the move still keeps the field from moving, with
// a let-else before it.
func TestOwnGuardRejectsFieldReadAfterMoveAfterLetElse(t *testing.T) {
	wantE051(t, "field-read-after-let-else-move", fieldMovePrelude+`
function step(own a: Asm, o: Option[i32]): Asm {
    let Some(v) = o else {
        return a;
    };
    let c: Cfi = record(a.cfi, v);
    let n: i32 = a.cfi.n;
    return Asm { ...a, cfi: c };
}`)
}

// The base stays owned by the walk, so a read of the moved field after the
// literal is a use after move (#14111).
func TestOwnGuardRejectsReadAfterSupersededFieldMoveOnLet(t *testing.T) {
	wantE050(t, "field-move-let-then-read", fieldMovePrelude+`
function step(own a: Asm, v: i32): Asm {
    let b: Asm = Asm { ...a, cfi: record(a.cfi, v) };
    let n: i32 = a.cfi.n;
    return b;
}`)
}

// A literal inside a call takes the superseded field as a let initializer
// does.
func TestOwnGuardAllowsSupersededFieldMoveInCallArgument(t *testing.T) {
	const helpers = `
function size(a: Asm): i32 { return a.cfi.n; }
function keep(own a: Asm): Asm { return a; }
function drop(own a: Asm): void {}
`
	wantOK(t, "field-move-let-call-argument", fieldMovePrelude+helpers+`
function step(own a: Asm, v: i32): i32 {
    let n: i32 = size(Asm { ...a, cfi: record(a.cfi, v) });
    return n;
}`)
	wantOK(t, "field-move-statement-call-argument", fieldMovePrelude+helpers+`
function step(own a: Asm, v: i32): void {
    drop(Asm { ...a, cfi: record(a.cfi, v) });
}`)
	wantOK(t, "field-move-returned-call-argument", fieldMovePrelude+helpers+`
function step(own a: Asm, v: i32): Asm {
    return keep(Asm { ...a, cfi: record(a.cfi, v) });
}`)
}

func TestOwnGuardRejectsSupersededFieldMoveInCallArgumentMisuse(t *testing.T) {
	const helpers = `
function size(a: Asm): i32 { return a.cfi.n; }
function keep(own a: Asm): Asm { return a; }
`
	wantE050(t, "field-move-call-argument-then-read", fieldMovePrelude+helpers+`
function step(own a: Asm, v: i32): i32 {
    let n: i32 = size(Asm { ...a, cfi: record(a.cfi, v) });
    return n + a.cfi.n;
}`)
	wantE051(t, "field-move-call-argument-borrowed-base", fieldMovePrelude+helpers+`
function step(a: Asm, v: i32): Asm {
    return keep(Asm { ...a, cfi: record(a.cfi, v) });
}`)
}

func TestOwnGuardRejectsSupersededFieldMoveOnBorrowedBase(t *testing.T) {
	wantE051(t, "field-move-borrowed-base-let", fieldMovePrelude+`
function step(a: Asm, v: i32): Asm {
    let b: Asm = Asm { ...a, cfi: record(a.cfi, v) };
    return b;
}`)
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
	wantOK(t, "spread-literal", prelude+`function f(v: W): i32 {
    let w: W = W { ...v, tag: "u" };
    let r: i32 = eat(w);
    return r;
}`)
	wantE051(t, "spread-literal-with-a-handle", ownHandleConsumer+`function f(v: HPair): i32 {
    let w: HPair = HPair { ...v, n: 2 };
    let r: i32 = sinkp(w);
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
	wantOK(t, "branch-between", prelude+`function f(k: i32): i32 {
    let flags: boolean[] = [false];
    let n: i32 = take(flags);
    if (k > 0) { k = k + 1; }
    flags = [true];
    return n + k + flags.len();
}`)
	wantE051(t, "branch-reads-between", prelude+`function f(k: i32): i32 {
    let flags: boolean[] = [false];
    let n: i32 = take(flags);
    if (k > 0) { k = k + flags.len(); }
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
	// The literal takes the owned n and drops n.xs. So grow takes the only
	// reference to n.xs, under any name that holds the literal (#13929).
	wantOK(t, "member-literal-stored-under-another-name", memberUnionPrelude+`
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

const handleRulesPrelude = `struct Handle { fd: i32 }
struct Ends { r: Handle, w: Handle }
function (own h: Handle) close(): i32 { return h.fd; }
function take(own e: Ends): i32 { return e.w.close(); }
`

func TestOwnGuardDeferredCloseIsTheLastUse(t *testing.T) {
	wantOK(t, "defer-close-then-read", handleRulesPrelude+`
function work(): i32 {
    let h: Handle = Handle { fd: 3 };
    defer h.close();
    return h.fd;
}`)
	wantOK(t, "defer-close-own-param-then-read", handleRulesPrelude+`
function work(own p: Handle): i32 {
    defer p.close();
    return p.fd;
}`)
	wantOK(t, "defer-close-field-then-close-sibling", handleRulesPrelude+`
function work(own p: Ends): i32 {
    defer p.r.close();
    let a: i32 = p.w.close();
    return a + p.r.fd;
}`)
	wantE051(t, "defer-close-then-close", handleRulesPrelude+`
function work(): i32 {
    let h: Handle = Handle { fd: 3 };
    defer h.close();
    return h.close();
}`)
	wantE051(t, "defer-close-in-loop", handleRulesPrelude+`
function work(): i32 {
    let h: Handle = Handle { fd: 3 };
    let i: i32 = 0;
    while (i < 2) {
        defer h.close();
        i = i + 1;
    }
    return i;
}`)
	wantE051(t, "defer-close-captured", handleRulesPrelude+`
function work(): i32 {
    let h: Handle = Handle { fd: 3 };
    let get = (): i32 => h.fd;
    defer h.close();
    return get();
}`)
	wantE051(t, "defer-close-field-then-close-field", handleRulesPrelude+`
function work(own p: Ends): i32 {
    defer p.r.close();
    return p.r.close();
}`)
}

func TestOwnGuardOwnParamConsumedTwiceThroughDefer(t *testing.T) {
	err := checkSource(t, handleRulesPrelude+`
function work(own p: Handle): i32 {
    defer p.close();
    return p.close();
}`)
	if err == nil || !strings.Contains(err.Error(), "is consumed twice: a deferred call at line") {
		t.Fatalf("expected the deferred second-consumption E050, got %v", err)
	}
}

func TestOwnGuardFieldMovesOutOfLocalOrOwnParam(t *testing.T) {
	wantOK(t, "field-close-both-ends-local", handleRulesPrelude+`
function work(): i32 {
    let q: Ends = Ends { r: Handle { fd: 3 }, w: Handle { fd: 4 } };
    let a: i32 = q.r.close();
    return a + q.w.close();
}`)
	wantOK(t, "field-close-both-ends-own-param", handleRulesPrelude+`
function work(own e: Ends): i32 {
    let a: i32 = e.r.close();
    return a + e.w.close();
}`)
	wantE051(t, "field-closed-twice", handleRulesPrelude+`
function work(): i32 {
    let q: Ends = Ends { r: Handle { fd: 3 }, w: Handle { fd: 4 } };
    let a: i32 = q.r.close();
    return a + q.r.close();
}`)
	wantE051(t, "whole-used-after-field-close", handleRulesPrelude+`
function work(): i32 {
    let q: Ends = Ends { r: Handle { fd: 3 }, w: Handle { fd: 4 } };
    let a: i32 = q.r.close();
    return a + take(q);
}`)
	wantE051(t, "field-read-after-field-close-own-param", handleRulesPrelude+`
function work(own e: Ends): i32 {
    let a: i32 = e.r.close();
    return a + e.r.fd;
}`)
	wantE051(t, "field-close-of-borrowed-param", handleRulesPrelude+`
function work(e: Ends): i32 {
    return e.r.close();
}`)
	wantE051(t, "field-close-in-loop", handleRulesPrelude+`
function work(): i32 {
    let q: Ends = Ends { r: Handle { fd: 3 }, w: Handle { fd: 4 } };
    let i: i32 = 0;
    while (i < 2) {
        i = i + q.r.close();
    }
    return i;
}`)
}

const readerLoopPrelude = `struct Handle { fd: i32 }
function (own h: Handle) close(): i32 { return h.fd; }
`

func TestOwnGuardLocalDiesOnDivergingLoopPaths(t *testing.T) {
	wantOK(t, "close-then-return-in-loop", readerLoopPrelude+`
function work(): i32 {
    let h: Handle = Handle { fd: 3 };
    let n: i32 = 0;
    while (n < 3) {
        n = n + 1;
        if (n == 2) {
            h.close();
            return n;
        }
    }
    return n + h.fd;
}`)
	wantOK(t, "close-then-break-nothing-after", readerLoopPrelude+`
function work(): i32 {
    let h: Handle = Handle { fd: 3 };
    let n: i32 = 0;
    while (n < 3) {
        n = n + 1;
        if (n == 2) {
            h.close();
            break;
        }
    }
    return n;
}`)
	wantOK(t, "close-then-labelled-break", readerLoopPrelude+`
function work(): i32 {
    let h: Handle = Handle { fd: 3 };
    let n: i32 = 0;
    outer: while (n < 3) {
        let k: i32 = 0;
        while (k < 2) {
            k = k + 1;
            if (n == 1) {
                h.close();
                break outer;
            }
        }
        n = n + 1;
    }
    return n;
}`)
	wantOK(t, "loop-fresh-local-closes-each-round", readerLoopPrelude+`
function work(): i32 {
    let n: i32 = 0;
    while (n < 3) {
        let h: Handle = Handle { fd: n };
        n = n + 1;
        h.close();
    }
    return n;
}`)
	wantE051(t, "close-then-continue", readerLoopPrelude+`
function work(): i32 {
    let h: Handle = Handle { fd: 3 };
    let n: i32 = 0;
    while (n < 3) {
        n = n + 1;
        if (n == 2) {
            h.close();
            continue;
        }
    }
    return n;
}`)
	wantE051(t, "close-then-break-then-close", readerLoopPrelude+`
function work(): i32 {
    let h: Handle = Handle { fd: 3 };
    let n: i32 = 0;
    while (n < 3) {
        n = n + 1;
        if (n == 2) {
            h.close();
            break;
        }
    }
    return h.close() + n;
}`)
	wantE051(t, "close-falls-through-the-loop", readerLoopPrelude+`
function work(): i32 {
    let h: Handle = Handle { fd: 3 };
    let n: i32 = 0;
    while (n < 3) {
        n = n + 1;
        h.close();
    }
    return n;
}`)
	wantE051(t, "close-in-the-loop-head", readerLoopPrelude+`
function work(): i32 {
    let h: Handle = Handle { fd: 3 };
    let n: i32 = 0;
    while (h.close() > n) {
        n = n + 1;
    }
    return n;
}`)
	wantE051(t, "close-in-a-loop-inside-a-value-block", readerLoopPrelude+`
function work(): i32 {
    let h: Handle = Handle { fd: 3 };
    let n: i32 = {
        let k: i32 = 0;
        while (k < 2) {
            k = k + h.close();
        }
        k
    };
    return n;
}`)
	wantE051(t, "close-then-break-then-read", readerLoopPrelude+`
function work(): i32 {
    let h: Handle = Handle { fd: 3 };
    let n: i32 = 0;
    while (n < 3) {
        n = n + 1;
        if (n == 2) {
            h.close();
            break;
        }
    }
    return n + h.fd;
}`)
}

func TestOwnGuardMutuallyExclusiveValueBranches(t *testing.T) {
	wantOK(t, "one-close-on-each-alternative-path", readerLoopPrelude+`
function pick(own h: Handle, flag: boolean): i32 {
    let h2: Handle = Handle { fd: 4 };
    let n: i32 = if (flag) { h2.close() } else { h2.close() + 1 };
    return n;
}`)
	wantE051(t, "read-after-close-in-the-selected-value-branch", readerLoopPrelude+`
function pick(flag: boolean): i32 {
    let h: Handle = Handle { fd: 4 };
    return if (flag) { h.close() + h.fd } else { h.close() };
}`)

}

const payloadPrelude = `struct Handle { fd: i32 }
struct Pack { file: Handle, path: string }
function (own h: Handle) close(): i32 { return h.fd; }
function mkp(): Result[Pack, string] { return Ok(Pack { file: Handle { fd: 4 }, path: "p" }); }
`

func TestOwnGuardMatchOnALocalAtItsLastUse(t *testing.T) {
	wantOK(t, "local-option-matched-once", payloadPrelude+`
function work(): i32 {
    let o: Option[Handle] = Some(Handle { fd: 1 });
    let n: i32 = 0;
    match (o) {
        Some(h) => { n = h.close(); },
        None => {}
    }
    return n;
}`)
	wantOK(t, "field-path-matched-at-its-last-use", payloadPrelude+`
struct Frame { dir: Option[Handle], depth: i32 }
function work(): i32 {
    let top: Frame = Frame { dir: Some(Handle { fd: 2 }), depth: 1 };
    let n: i32 = 0;
    match (top.dir) {
        Some(d) => { n = d.close(); },
        None => {}
    }
    return n + top.depth;
}`)
	wantE051(t, "local-matched-again-after-the-payload-closed", payloadPrelude+`
function work(): i32 {
    let o: Option[Handle] = Some(Handle { fd: 1 });
    let n: i32 = 0;
    match (o) {
        Some(h) => { n = h.close(); },
        None => {}
    }
    match (o) {
        Some(h) => { n = n + h.fd; },
        None => {}
    }
    return n;
}`)
}

func TestOwnGuardPatternBoundPayloadOwnsItsFields(t *testing.T) {
	wantOK(t, "field-of-an-own-parameter-payload", payloadPrelude+`
function work(own o: Option[Pack]): i32 {
    match (o) {
        Some(t) => { return t.file.close(); },
        None => { return 0; }
    }
    return 0;
}`)
	wantOK(t, "field-of-a-fresh-call-payload-through-if-let", payloadPrelude+`
function work(): i32 {
    let n: i32 = 0;
    if let Ok(t) = mkp() {
        n = t.file.close();
    }
    return n;
}`)
	wantE051(t, "field-of-a-borrowed-parameter-payload", payloadPrelude+`
function work(o: Option[Pack]): i32 {
    match (o) {
        Some(t) => { return t.file.close(); },
        None => { return 0; }
    }
    return 0;
}`)
}

func TestOwnGuardForEachOverAnOwnedArray(t *testing.T) {
	wantOK(t, "fresh-local-array-at-its-last-use", payloadPrelude+`
function work(): i32 {
    let hs: Handle[] = [Handle { fd: 1 }, Handle { fd: 2 }];
    let n: i32 = 0;
    for h in hs {
        n = n + h.close();
    }
    return n;
}`)
	wantOK(t, "own-array-parameter", payloadPrelude+`
function work(own hs: Handle[]): i32 {
    let n: i32 = 0;
    for h in hs {
        n = n + h.close();
    }
    return n;
}`)
	wantOK(t, "field-of-each-element", payloadPrelude+`
function work(): i32 {
    let ps: Pack[] = [Pack { file: Handle { fd: 1 }, path: "a" }];
    let n: i32 = 0;
    for p in ps {
        if (p.path == "a") {
            n = n + p.file.close();
        }
    }
    return n;
}`)
	wantE051(t, "array-read-after-the-loop", payloadPrelude+`
function work(): i32 {
    let hs: Handle[] = [Handle { fd: 1 }];
    let n: i32 = 0;
    for h in hs {
        n = n + h.close();
    }
    return n + hs.len();
}`)
	wantE051(t, "borrowed-array-parameter", payloadPrelude+`
function work(hs: Handle[]): i32 {
    let n: i32 = 0;
    for h in hs {
        n = n + h.close();
    }
    return n;
}`)
	wantE051(t, "element-read-after-its-close", payloadPrelude+`
function work(): i32 {
    let hs: Handle[] = [Handle { fd: 1 }];
    let n: i32 = 0;
    for h in hs {
        n = n + h.close();
        n = n + h.fd;
    }
    return n;
}`)
	wantE051(t, "field-closed-in-an-inner-loop", payloadPrelude+`
function work(): i32 {
    let ps: Pack[] = [Pack { file: Handle { fd: 1 }, path: "a" }];
    let n: i32 = 0;
    for p in ps {
        let k: i32 = 0;
        while (k < 2) {
            k = k + 1;
            n = n + p.file.close();
        }
    }
    return n;
}`)
}

func TestOwnGuardTakenPayloadIsAFreshLocal(t *testing.T) {
	wantOK(t, "taken-payload-stored-then-read", `
function round(i: i32): i32 {
    let held: i32[] = [];
    let acc: i32 = 0;
    let o: Option[i32[]] = Some([i, i + 1]);
    if (i >= 0) {
        match (o) { Some(a) => { held = a; acc = a[0]; }, None => {} }
    }
    return acc + held[1];
}`)
	wantOK(t, "taken-payload-closed-at-its-last-use", payloadPrelude+`
function work(): i32 {
    let r: Result[Pack, string] = mkp();
    match (r) {
        Ok(p) => { return p.file.close(); },
        Err(e) => { return 0; }
    }
}`)
	wantE051(t, "lent-payload-not-taken", payloadPrelude+`
function work(o: Option[Handle]): i32 {
    match (o) {
        Some(h) => { return h.close(); },
        None => { return 0; }
    }
}`)
}

func TestOwnGuardAcceptsALocalFromACallWhoseResultHoldsNoHandle(t *testing.T) {
	wantOK(t, "local-from-handle-free-call", ownConsumer+`
function copy_row(xs: i32[]): i32[] {
    if (xs.len() == 0) { return xs; }
    return xs.with(0, xs[0]);
}
function main(): i32 {
    let xs: i32[] = [1];
    let first: i32[] = copy_row(xs);
    return consume(first) + xs.len();
}`)
}

// The element a `for` binds borrows from what the loop walks, so a result
// that returns it borrows too. A struct of numbers cannot hold a handle, and
// a local set from such a call stays fresh.
func TestOwnGuardAcceptsALocalFromACallWhoseStructResultHoldsNoHandle(t *testing.T) {
	wantOK(t, "local-from-handle-free-struct-call", `
struct Mark { i: i32, kind: i32 }
function first_row(rows: Mark[][]): Mark[] {
    for row in rows {
        return row;
    }
    return [];
}
function keep(rows: Mark[][], own ms: Mark[]): i32 { return rows.len() + ms.len(); }
function main(): i32 {
    let rows: Mark[][] = [[Mark { i: 1, kind: 0 }]];
    let got: Mark[] = first_row(rows);
    let n: i32 = keep(rows, got);
    return n + rows.len();
}`)
}

func TestOwnGuardRejectsAForElementReturnedThroughACall(t *testing.T) {
	wantE051(t, "for-element-alias-from-call", `
struct Held { r: Reader }
function first_row(rows: Held[][]): Held[] {
    for row in rows {
        return row;
    }
    return [];
}
function keep(rows: Held[][], own hs: Held[]): i32 { return rows.len() + hs.len(); }
function main(): i32 {
    let rows: Held[][] = [[Held { r: stdin() }]];
    let got: Held[] = first_row(rows);
    let n: i32 = keep(rows, got);
    return n + rows.len();
}`)
}

func TestOwnGuardRejectsAHandleClosedThroughAnAliasFromACall(t *testing.T) {
	wantE051(t, "handle-alias-from-call", `
function pass(r: Reader): Reader { return r; }
function main(): i32 {
    let r: Reader = stdin();
    let alias: Reader = pass(r);
    match (alias.close()) { Ok(_) => {}, Err(_) => {} }
    match (r.close()) { Ok(_) => {}, Err(_) => {} }
    return 0;
}`)
}

// A handle-holding struct, so the old return rule cannot hide an admission.
const admissionPrelude = `struct Acc { items: i32[], w: Writer }
struct Box { a: Acc }
function sink(own a: Acc): i32 { return a.items.len(); }
function push(own a: Acc, v: i32): Acc { return Acc { ...a, items: a.items.append(v) }; }
function mk(n: i32): Acc { return Acc { items: [n], w: stdout() }; }
`

func TestOwnGuardAdmitsEachFreshDeclaration(t *testing.T) {
	wantOK(t, "redeclared-fresh", admissionPrelude+`
function f(k: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < k) {
        let a: Acc = mk(i);
        t = t + a.items.len();
        i = i + 1;
    }
    let a: Acc = mk(7);
    let n: i32 = sink(a);
    return t + n;
}`)
	wantOK(t, "fresh-redeclaration-after-a-borrowed-one", admissionPrelude+`
function f(b: Box, k: i32): i32 {
    if (k > 0) {
        let a: Acc = b.a;
        print(a.items.len().to_string());
    }
    let a: Acc = mk(7);
    let n: i32 = sink(a);
    return n;
}`)
}

func TestOwnGuardAdmitsAnAliasGivenOnlyOwnedValues(t *testing.T) {
	wantOK(t, "alias-of-own-param", admissionPrelude+`
function f(own p: Acc, k: i32): i32 {
    let first: i32 = p.items.len();
    let q: Acc = p;
    q = push(q, k);
    q = push(q, first);
    let n: i32 = sink(q);
    return n;
}`)
	wantE051(t, "alias-given-a-borrowed-value", admissionPrelude+`
function f(own p: Acc, b: Box): i32 {
    let first: i32 = p.items.len();
    let q: Acc = p;
    q = b.a;
    let n: i32 = sink(q);
    return n + first;
}`)
	wantE051(t, "alias-of-a-borrowed-param", admissionPrelude+`
function f(p: Acc): i32 {
    let first: i32 = p.items.len();
    let q: Acc = p;
    let n: i32 = sink(q);
    return n + first;
}`)
	wantE050(t, "source-read-after-the-alias", admissionPrelude+`
function f(own p: Acc): i32 {
    let q: Acc = p;
    let n: i32 = sink(q);
    return n + p.items.len();
}`)
	wantE050(t, "alias-in-a-loop", admissionPrelude+`
function f(own p: Acc, k: i32): i32 {
    let first: i32 = p.items.len();
    let n: i32 = 0;
    while (n < k) {
        let q: Acc = p;
        n = n + sink(q);
    }
    return n + first;
}`)
}

// Issue #13350: a local read out of a borrowed structure gets one answer in
// a let, an assignment and a return.
const borrowedReadPrelude = `struct P { payload: u8[], n: i32 }
struct S { filled: u8[], k: i32 }
struct C { ps: P[], s: S }
struct Row { cells: i32[], name: string }
struct Grid { rows: Row[] }
struct H { w: Writer, n: i32 }
function (s: S) fill(own out: u8[]): S { return S { ...s, filled: out.append(1) }; }
function width(own r: Row): i32 { return r.cells.len(); }
function finish(own w: Writer): i32 { return 0; }
`

// A struct with a method that consumes its receiver is a user-defined handle.
const ownHandleConsumer = `struct Handle { fd: i32 }
function (own h: Handle) close(): i32 { return h.fd; }
struct HPair { h: Handle, n: i32 }
function sinkh(own h: Handle): i32 { return h.close(); }
function sinkp(own p: HPair): i32 { return p.n; }
function hstep(): (Handle, i32) { return (Handle { fd: 1 }, 2); }
`

func TestOwnGuardRejectsHandleAliasOfADestructuredWhole(t *testing.T) {
	wantE051(t, "destructured-whole-named", ownHandleConsumer+`
function f(): i32 {
    let t @ (h, k) = hstep();
    let n: i32 = sinkh(h);
    return n + t.1;
}`)
	wantE051(t, "destructured-local-read-again", ownHandleConsumer+`
function mk(): HPair { return HPair { h: Handle { fd: 1 }, n: 2 }; }
function f(): i32 {
    let p: HPair = mk();
    let HPair { h, n } = p;
    let r: i32 = sinkh(h);
    return r + n + p.n;
}`)
	wantE051(t, "destructured-borrowed-param", ownHandleConsumer+`
function f(p: HPair): i32 {
    let HPair { h, n } = p;
    let r: i32 = sinkh(h);
    return r + n;
}`)
	wantE051(t, "destructured-local-reassigned-from-a-borrow", ownHandleConsumer+`
function mk(): HPair { return HPair { h: Handle { fd: 1 }, n: 2 }; }
function f(q: HPair, c: boolean): i32 {
    let p: HPair = mk();
    if (c) {
        p = q;
    }
    let HPair { h, n } = p;
    let r: i32 = sinkh(h);
    return r + n;
}`)
}

func TestOwnGuardRejectsFreshLocalGivenABorrowedHandle(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"assigned-a-borrowed-param", `
function f(q: Handle): i32 {
    let x: Handle = Handle { fd: 1 };
    x = q;
    let r: i32 = sinkh(x);
    return r;
}`},
		{"assigned-a-borrowed-param-in-return", `
function f(q: Handle): i32 {
    let x: Handle = Handle { fd: 1 };
    x = q;
    return sinkh(x);
}`},
		{"assigned-a-field-of-a-borrowed-param-then-closed", `
function f(q: HPair): i32 {
    let x: Handle = Handle { fd: 1 };
    x = q.h;
    return x.close();
}`},
		{"alias-of-a-borrowed-param", `
function f(q: Handle): i32 {
    let y: Handle = q;
    return sinkh(y);
}`},
		{"field-closed-out-of-a-reassigned-local", `
function mk(): HPair { return HPair { h: Handle { fd: 1 }, n: 2 }; }
function f(q: HPair): i32 {
    let p: HPair = mk();
    p = q;
    return p.h.close() + p.n;
}`},
		{"match-on-a-reassigned-local-takes-its-payload", `
enum Slot { Full(Handle), Empty }
function mk(): Slot { return Full(Handle { fd: 1 }); }
function f(q: Slot): i32 {
    let p: Slot = mk();
    p = q;
    match (p) {
        Full(h) => { return h.close(); },
        Empty => { return 0; }
    }
    return 2;
}`},
	} {
		wantE051(t, tc.name, ownHandleConsumer+tc.body)
	}
	for _, tc := range []struct{ name, body string }{
		{"assigned-a-fresh-value", `
function f(c: boolean): i32 {
    let x: Handle = Handle { fd: 1 };
    if (c) {
        x = Handle { fd: 2 };
    }
    return sinkh(x);
}`},
		{"assigned-a-self-update", `
function mk(): HPair { return HPair { h: Handle { fd: 1 }, n: 2 }; }
function f(c: boolean): i32 {
    let p: HPair = mk();
    if (c) {
        p = HPair { ...p, n: 3 };
    }
    return sinkp(p);
}`},
		{"assigned-an-unpack", `
function f(): i32 {
    let x: Handle = Handle { fd: 1 };
    let t: (Handle, i32) = hstep();
    x = t.0;
    return sinkh(x) + t.1;
}`},
		{"assigned-another-fresh-local", `
function f(): i32 {
    let x: Handle = Handle { fd: 1 };
    let y: Handle = Handle { fd: 2 };
    x = y;
    return sinkh(x);
}`},
		{"alias-of-an-own-param", `
function f(own q: Handle): i32 {
    let y: Handle = q;
    return sinkh(y);
}`},
		{"handle-free-local-assigned-a-borrow", `
function keep(own xs: i32[]): i32 { return xs[0]; }
function f(q: i32[]): i32 {
    let xs: i32[] = [1];
    xs = q;
    return keep(xs);
}`},
		{"assigned-a-pass-through-call", `
function settle(o: Handle, n: i32): Handle { return o; }
function f(): i32 {
    let o: Handle = Handle { fd: 1 };
    o = settle(o, 2);
    return sinkh(o);
}`},
		{"assigned-a-pass-through-call-with-a-handle-free-union", `
struct A { n: i32 }
struct B { s: string }
type U = A | B;
function settle(o: Handle, u: U): Handle { return o; }
function f(u: U): i32 {
    let o: Handle = Handle { fd: 1 };
    o = settle(o, u);
    return sinkh(o);
}`},
		{"assigned-a-payload-of-a-fresh-match", `
enum Slot { Full(Handle), Empty }
function mk(): Slot { return Full(Handle { fd: 1 }); }
function f(): i32 {
    let x: Handle = Handle { fd: 2 };
    match (mk()) {
        Full(h) => { x = h; },
        Empty => {}
    }
    return sinkh(x);
}`},
	} {
		wantOK(t, tc.name, ownHandleConsumer+tc.body)
	}
	for _, tc := range []struct{ name, body string }{
		{"assigned-a-pass-through-call-with-another-lender", `
function pick(o: Handle, p: Handle): Handle { return p; }
function f(q: Handle): i32 {
    let o: Handle = Handle { fd: 1 };
    o = pick(o, q);
    return sinkh(o);
}`},
		{"assigned-a-payload-of-a-borrowed-match", `
enum Slot { Full(Handle), Empty }
function f(q: Slot): i32 {
    let x: Handle = Handle { fd: 2 };
    match (q) {
        Full(h) => { x = h; },
        Empty => {}
    }
    return sinkh(x);
}`},
	} {
		wantE051(t, tc.name, ownHandleConsumer+tc.body)
	}
}

func TestOwnGuardAllowsHandleFreeLocalReadFromBorrowed(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"assign", `
function (c: C) a(i: i32): C {
    let payload: u8[] = c.ps[i].payload;
    let cur: C = C { ...c, ps: c.ps.with(i, P { ...c.ps[i], payload: [] }) };
    cur = C { ...cur, s: cur.s.fill(payload) };
    return cur;
}`},
		{"let", `
function (c: C) a(i: i32): C {
    let payload: u8[] = c.ps[i].payload;
    let cur: C = C { ...c, ps: c.ps.with(i, P { ...c.ps[i], payload: [] }) };
    let next: C = C { ...cur, s: cur.s.fill(payload) };
    return next;
}`},
		{"return", `
function (c: C) a(i: i32): C {
    let payload: u8[] = c.ps[i].payload;
    let cur: C = C { ...c, ps: c.ps.with(i, P { ...c.ps[i], payload: [] }) };
    return C { ...cur, s: cur.s.fill(payload) };
}`},
		{"declared-record", `
function (g: Grid) first(): i32 {
    let r: Row = g.rows[0];
    let w: i32 = width(r);
    return w;
}`},
	} {
		wantOK(t, tc.name, borrowedReadPrelude+tc.body)
	}
	for _, tc := range []struct{ name, body string }{
		{"destructured-whole-named", `
function step(): (i32[], i32) { return ([1], 2); }
function f(): i32 {
    let t @ (xs, k) = step();
    let n: i32 = consume(xs);
    return n + t.1;
}`},
		{"own-param-field-read-twice", `
function f(own p: Pair): i32 {
    let xs: i32[] = p.items;
    let k: i32 = consume(xs);
    return k + p.items.len();
}`},
		{"borrowed-param-field-unpack", `
function f(p: Pair): i32 {
    let xs: i32[] = p.items;
    let k: i32 = consume(xs);
    return k;
}`},
		{"destructured-borrowed-param", `
function f(p: Pair): i32 {
    let Pair { items, n } = p;
    let r: i32 = consume(items);
    return r + n;
}`},
	} {
		wantOK(t, tc.name, ownConsumer+tc.body)
	}
}

func TestOwnGuardRejectsHandleLocalReadFromBorrowed(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"let", `
function (h: H) done(): i32 {
    let w: Writer = h.w;
    let r: i32 = finish(w);
    return r;
}`},
		{"assign", `
function (h: H) done(): i32 {
    let w: Writer = h.w;
    let r: i32 = 0;
    r = finish(w);
    return r;
}`},
		{"return", `
function (h: H) done(): i32 {
    let w: Writer = h.w;
    return finish(w);
}`},
	} {
		wantE051(t, tc.name, borrowedReadPrelude+tc.body)
	}
}

// Issue #13935: a borrowed parameter that cannot hold a handle does not lend
// one to the call's result.
const handleFreeParamPrelude = `struct A { items: i32[], w: Writer }
struct B { w: Writer }
type V = A | B;
function size(own v: V): i32 { return 0; }
`

func TestOwnGuardCallResultIgnoresHandleFreeBorrowedParams(t *testing.T) {
	wantOK(t, "string-param", handleFreeParamPrelude+`
function step(own a: A, name: string): Option[V] {
    if (name == "x") { return None; }
    return Some(A { ...a, items: a.items.append(1) });
}
function f(own a: A): i32 {
    let Some(out) = step(a, "y") else { return 1; };
    return size(out);
}`)
	wantE051(t, "handle-param", handleFreeParamPrelude+`
function pick(own a: A, h: B): Option[V] {
    if (a.items.len() == 0) { return Some(h); }
    return Some(a);
}
function f(own a: A, h: B): i32 {
    let Some(out) = pick(a, h) else { return 1; };
    return size(out);
}`)
}

// A for-each over an owned array borrows it when the array is read again
// after the loop, and takes it at its last use.
func TestOwnedForEachTakesOnlyAtLastUse(t *testing.T) {
	wantOK(t, "read-after-the-loop", `
function count(own xs: string[]): i32 { return xs.len(); }
function g(own names: string[]): i32 {
    for name in names {
        if (name == "bad") { return 0; }
    }
    return count(names);
}`)
	wantE050(t, "consumed-before-the-loop", `
function count(own xs: string[]): i32 { return xs.len(); }
function g(own names: string[]): i32 {
    let n: i32 = count(names);
    for name in names {
        if (name == "bad") { return 0; }
    }
    return n;
}`)
}

const reboundPrelude = `struct G { x: i32[], n: i32 }
function rnd(own g: G): (G, i32) { return (G { x: g.x, n: g.n + 1 }, g.n); }
`

// A local that every path of the next statement rebinds is dead after the
// call that takes it, as it is after a plain assignment (#14124).
func TestOwnGuardLocalReboundOnEveryBranch(t *testing.T) {
	wantOK(t, "if-else-rebinds", reboundPrelude+`
function f(): i32 {
    let g: G = G { x: [], n: 0 };
    let (g1, a) = rnd(g);
    if (a % 2 == 0) { g = g1; } else { g = g1; }
    let (g2, b) = rnd(g);
    return a + b + g2.n;
}`)
	wantOK(t, "if-else-rebinds-in-loop", reboundPrelude+`
function f(): i32 {
    let g: G = G { x: [], n: 0 };
    let t: i32 = 0;
    for k in 0..4 {
        let (g1, a) = rnd(g);
        if (a % 2 == 0) { g = g1; } else { g = g1; t = t + a; }
    }
    return t + g.n;
}`)
	wantOK(t, "match-arms-rebind", reboundPrelude+`
function f(o: Option[i32]): i32 {
    let g: G = G { x: [], n: 0 };
    let (g1, a) = rnd(g);
    match (o) { Some(v) => { g = g1; }, None => { g = G { x: [], n: a }; } }
    let (g2, b) = rnd(g);
    return b + g2.n;
}`)
	wantOK(t, "block-rebinds", reboundPrelude+`
function f(): i32 {
    let g: G = G { x: [], n: 0 };
    let (g1, a) = rnd(g);
    {
        g = g1;
    }
    let (g2, b) = rnd(g);
    return a + b + g2.n;
}`)
}

func TestOwnGuardLocalNotReboundOnEveryBranch(t *testing.T) {
	wantE051(t, "only-then-rebinds", reboundPrelude+`
function f(): i32 {
    let g: G = G { x: [], n: 0 };
    let (g1, a) = rnd(g);
    if (a % 2 == 0) { g = g1; }
    let (g2, b) = rnd(g);
    return a + b + g2.n;
}`)
	wantE051(t, "branch-reads-before-rebind", reboundPrelude+`
function f(): i32 {
    let g: G = G { x: [], n: 0 };
    let (g1, a) = rnd(g);
    if (a % 2 == 0) { let m: i32 = g.n; g = g1; } else { g = g1; }
    let (g2, b) = rnd(g);
    return a + b + g2.n;
}`)
}

// A break or a continue in an arm after a call in a match tag takes the
// local reaches the read where the jump lands (#14125).
func TestOwnGuardJumpAfterMatchTagTake(t *testing.T) {
	const settle = `struct R { items: i32[], n: i32 }
function settle(own r: R): Result[i32, R] { return Err(r); }
`
	wantE051(t, "continue-reaches-the-tag", settle+`
function f(): i32 {
    let r: R = R { items: [], n: 0 };
    let k: i32 = 0;
    while (k < 5) {
        k = k + 1;
        match (settle(r)) { Ok(n) => { continue; }, Err(after) => { r = after; } }
    }
    return 0;
}`)
	wantE051(t, "break-reaches-a-read-after-the-loop", settle+`
function f(): i32 {
    let r: R = R { items: [], n: 0 };
    while (true) {
        match (settle(r)) { Ok(n) => { break; }, Err(after) => { r = after; } }
    }
    return r.n;
}`)
	wantOK(t, "break-with-no-read-after", settle+`
function f(): i32 {
    let r: R = R { items: [], n: 0 };
    let total: i32 = 0;
    while (true) {
        match (settle(r)) { Ok(n) => { total = n; break; }, Err(after) => { r = after; } }
    }
    return total;
}`)
	wantOK(t, "continue-after-a-rebind", settle+`
function f(): i32 {
    let r: R = R { items: [], n: 0 };
    let k: i32 = 0;
    while (k < 5) {
        k = k + 1;
        match (settle(r)) { Ok(n) => { r = R { items: [], n: n }; continue; }, Err(after) => { r = after; } }
    }
    return r.n;
}`)
}

const fieldBranchPrelude = `struct Gen { x: i32[], n: i32 }
struct Fill { g: Gen, xs: i32[] }
function take(own g: Gen, xs: [i32]): (Gen, i32) { return (Gen { x: g.x, n: g.n + 1 }, xs.len()); }
function make(n: i32): Fill { return Fill { g: Gen { x: [], n: n }, xs: [1, 2] }; }
`

// A field move in one branch dies there when a sibling branch mentions the
// field, because no path from the call reaches the sibling (#14126).
func TestOwnGuardFieldMoveBesideSiblingBranch(t *testing.T) {
	wantOK(t, "field-move-in-one-branch", fieldBranchPrelude+`
function f(): i32 {
    let g: Gen = Gen { x: [], n: 0 };
    let total: i32 = 0;
    for k in 0..3 {
        let f: Fill = make(g.n);
        if (k == 0) {
            let (g1, n) = take(f.g, f.xs);
            g = g1;
            total = total + n;
        } else {
            g = f.g;
        }
    }
    return total + g.n;
}`)
	wantE051(t, "field-read-after-the-branches", fieldBranchPrelude+`
function f(c: boolean): i32 {
    let f: Fill = make(1);
    let total: i32 = 0;
    if (c) {
        let (g1, n) = take(f.g, f.xs);
        total = n + g1.n;
    }
    return total + f.g.n;
}`)
	wantE051(t, "base-read-later-in-the-branch", fieldBranchPrelude+`
function keep(own f: Fill): i32 { return f.xs.len(); }
function f(c: boolean): i32 {
    let f: Fill = make(1);
    if (c) {
        let (g1, n) = take(f.g, f.xs);
        return n + keep(f);
    }
    return keep(f);
}`)
}

func TestOwnGuardCallInMatchTagKeepsBindingScope(t *testing.T) {
	const settle = `struct R { items: i32[], n: i32 }
function settle(own r: R): Result[i32, R] { return Err(r); }
`
	wantOK(t, "call-tag-in-sibling-blocks", settle+`
function scoped(c: boolean): i32 {
    let n: i32 = 0;
    if (c) {
        let r: R = R { items: [1], n: 1 };
        match (settle(r)) { Ok(k) => { n = k; }, Err(after) => { n = after.n; } }
    }
    if (!c) {
        let r: R = R { items: [2], n: 2 };
        match (settle(r)) { Ok(k) => { n = k; }, Err(after) => { n = after.n; } }
    }
    return n;
}`)
	wantE051(t, "call-tag-keeps-outer-binding-live", settle+`
function scoped(c: boolean): i32 {
    let r: R = R { items: [1], n: 1 };
    if (c) {
        match (settle(r)) { Ok(k) => {}, Err(after) => {} }
    }
    return r.n;
}`)
}

// A call in a match tag takes its argument when every arm gives the local a
// new value or leaves, as the give-back idiom does in a loop (#14106).
func TestOwnGuardCallInMatchTagRebinds(t *testing.T) {
	const settle = `
struct R { items: i32[], n: i32 }
function settle(own r: R): Result[i32, R] { return Err(R { items: r.items, n: r.n + 1 }); }
`
	if err := checkSource(t, settle+`
function f(): i32 {
    let r: R = R { items: [], n: 0 };
    while (true) {
        if (r.n > 3) {
            return r.n;
        }
        match (settle(r)) {
            Ok(n) => { return n; },
            Err(after) => { r = after; }
        }
    }
    return 0;
}`); err != nil {
		t.Errorf("a call in a match tag whose arms rebind or leave should take its argument, got: %v", err)
	}
	wantE051(t, "call-in-match-tag-arm-reads-the-local", settle+`
function f(): i32 {
    let r: R = R { items: [], n: 0 };
    while (true) {
        match (settle(r)) {
            Ok(n) => { return n; },
            Err(after) => { let m: i32 = r.n; r = after; }
        }
    }
    return 0;
}`)
	wantE051(t, "call-in-match-tag-arm-falls-through", settle+`
function f(): i32 {
    let r: R = R { items: [], n: 0 };
    while (r.n < 3) {
        match (settle(r)) {
            Ok(n) => { return n; },
            Err(after) => {}
        }
    }
    return 0;
}`)
}

// A call whose body returns a fresh handle is an owned scrutinee in a match
// and in a let-else.
func TestOwnGuardScrutineeOfBodyFreshCall(t *testing.T) {
	const opener = `
function accepted(l: Socket): Result[Socket, i32] { return Ok(tcp_accept(l)); }
function take(own s: Socket): i32 { return tcp_close(s); }
`
	if err := checkSource(t, opener+`
function f(l: Socket): i32 {
    match (accepted(l)) {
        Ok(s) => { return take(s); },
        Err(e) => { return e; }
    }
    return 0;
}

function g(l: Socket): i32 {
    let Ok(s) = accepted(l) else { return 1; };
    return take(s);
}`); err != nil {
		t.Errorf("a body-fresh scrutinee should own its payload, got: %v", err)
	}
	wantE051(t, "scrutinee-returns-its-parameter", `
function passed(l: Socket): Result[Socket, i32] { return Ok(l); }
function take(own s: Socket): i32 { return tcp_close(s); }
function f(l: Socket): i32 {
    match (passed(l)) {
        Ok(s) => { return take(s); },
        Err(e) => { return e; }
    }
    return 0;
}`)
}

func TestOwnGuardMatchExpressionOnAFreshCallOwnsItsValue(t *testing.T) {
	const opener = `
function opened(): Result[Socket, i32] {
    let s: Socket = tcp_listen(0);
    if (s.fd < 0) { return Err(s.fd); }
    return Ok(s);
}
`
	wantOK(t, "match-expression-on-a-fresh-call", opener+`
function main(): i32 {
    let s: Socket = match (opened()) {
        Ok(v) => v,
        Err(e) => { return e; },
    };
    return tcp_close(s);
}`)
	wantE051(t, "match-expression-closed-twice", opener+`
function main(): i32 {
    let s: Socket = match (opened()) {
        Ok(v) => v,
        Err(e) => { return e; },
    };
    let a: i32 = tcp_close(s);
    return tcp_close(s) + a;
}`)
	wantE051(t, "match-expression-on-a-parameter", `
function f(r: Result[Socket, i32]): i32 {
    let s: Socket = match (r) {
        Ok(v) => v,
        Err(e) => { return e; },
    };
    return tcp_close(s);
}`)
	wantE051(t, "match-expression-arm-yields-a-parameter", opener+`
function f(l: Socket): i32 {
    let s: Socket = match (opened()) {
        Ok(v) => l,
        Err(e) => { return e; },
    };
    return tcp_close(s);
}`)
}

func TestOwnGuardMatchExpressionPayloadKeepsItsOrigin(t *testing.T) {
	const opener = `
function opened(): Result[Socket, i32] { return Ok(tcp_listen(0)); }
function passed(s: Socket): Socket { return s; }
`
	for _, tc := range []struct {
		name string
		arm  string
		ok   bool
	}{
		{"borrowed-assignment", "v = l; v", false},
		{"borrowed-call-assignment", "v = passed(l); v", false},
		{"conditional-borrowed-assignment", "if (l.fd >= 0) { v = l; } v", false},
		{"borrowed-shadow", "let v: Socket = l; v", false},
		{"fresh-assignment", "v = tcp_listen(0); v", true},
		{"fresh-shadow", "let v: Socket = tcp_listen(0); v", true},
		{"nested-borrowed-shadow", "if (l.fd >= 0) { let v: Socket = l; print(v.fd.to_string()); } v", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := opener + `
function f(l: Socket): i32 {
    let s: Socket = match (opened()) {
        Ok(v) => { ` + tc.arm + ` },
        Err(e) => { return e; },
    };
    return tcp_close(s);
}`
			if tc.ok {
				wantOK(t, tc.name, source)
			} else {
				wantE051(t, tc.name, source)
			}
		})
	}
}

func TestOwnGuardLoopsThatShareABinderOwnTheirElements(t *testing.T) {
	wantOK(t, "owned-loop-element-replaced-by-a-fresh-local", `
function f(): i32 {
    let xs: Socket[] = [tcp_listen(0)];
    for s in xs { let replacement: Socket = tcp_listen(0); s = replacement; tcp_close(s); }
    return 0;
}`)
	wantE051(t, "owned-loop-element-replaced-by-a-borrowed-local", `
function f(l: Socket): i32 {
    let xs: Socket[] = [tcp_listen(0)];
    for s in xs { let replacement: Socket = l; s = replacement; tcp_close(s); }
    return 0;
}`)
	wantE051(t, "owned-loop-element-replaced-by-a-reassigned-local", `
function f(l: Socket): i32 {
    let xs: Socket[] = [tcp_listen(0)];
    for s in xs { let replacement: Socket = tcp_listen(0); replacement = l; s = replacement; tcp_close(s); }
    return 0;
}`)
	wantE051(t, "owned-loop-element-replaced-by-a-borrow", `
function f(l: Socket): i32 {
    let xs: Socket[] = [tcp_listen(0)];
    for s in xs { s = l; tcp_close(s); }
    return 0;
}`)
	wantOK(t, "owned-loop-element-replaced-by-a-fresh-socket", `
function main(): i32 {
    let xs: Socket[] = [tcp_listen(0)];
    for s in xs { s = tcp_listen(0); tcp_close(s); }
    return 0;
}`)
	wantOK(t, "two-loops-one-binder", `
function main(): i32 {
    let xs: Socket[] = [tcp_listen(0)];
    let ys: Socket[] = [tcp_listen(0)];
    for s in xs { tcp_close(s); }
    for s in ys { tcp_close(s); }
    return 0;
}`)
	wantE051(t, "two-loops-one-binder-one-borrowed", `
function f(ys: Socket[]): i32 {
    let xs: Socket[] = [tcp_listen(0)];
    for s in xs { tcp_close(s); }
    for s in ys { tcp_close(s); }
    return 0;
}`)
	wantE051(t, "two-loops-one-binder-closed-twice", `
function main(): i32 {
    let xs: Socket[] = [tcp_listen(0)];
    let ys: Socket[] = [tcp_listen(0)];
    for s in xs { tcp_close(s); }
    for s in ys { tcp_close(s); tcp_close(s); }
    return 0;
}`)
}

func TestOwnGuardBodyFreshValuePayloadRemainsCopyable(t *testing.T) {
	wantOK(t, "copyable-payload-of-body-fresh-call", `
struct Box { items: i32[] }
function make(s: Socket): Result[Box, i32] { return Ok(Box { items: [7] }); }
function f(s: Socket): i32 {
    let Ok(input) = make(s) else { return 1; };
    let alias = input;
    return input.items[0] + alias.items[0];
}`)
}

// A binding of a call result that can hold no handle is a fresh local, as a
// let local is. A copy does not consume it, and it moves into an own
// parameter at its last use.
func TestOwnGuardCallPayloadIsAFreshLocal(t *testing.T) {
	const prelude = `
struct T { aux: string }
function take(t: T, n: i32): i32 { return t.aux.len() + n; }
function keep(own s: string): i32 { return s.len(); }
function pick(xs: string[]): Option[string] {
    for x in xs { return Some(x); }
    return None;
}
`
	for _, tc := range []struct{ name, body string }{
		{"if-let-stored-then-read", `
function f(xs: string[]): i32 {
    if let Some(u) = pick(xs) {
        let t = T { aux: u };
        return take(t, u.len());
    }
    return 0;
}`},
		{"match-stored-then-read", `
function f(xs: string[]): i32 {
    match (pick(xs)) {
        Some(u) => {
            let t = T { aux: u };
            return take(t, u.len());
        },
        None => { return 0; }
    }
    return 0;
}`},
		{"copied-then-moved-at-its-last-use", `
function f(xs: string[]): i32 {
    match (pick(xs)) {
        Some(u) => {
            let kept = u;
            let k: i32 = keep(u);
            return k + kept.len();
        },
        None => { return 0; }
    }
    return 0;
}`},
		// Each arm binds u afresh, so the u of the second match is not a
		// later use of the u of the first.
		{"same-name-in-two-matches", `
function f(xs: string[]): i32 {
    let t: i32 = 0;
    match (pick(xs)) {
        Some(u) => { let a: i32 = keep(u); t = t + a; },
        None => {}
    }
    match (pick(xs)) {
        Some(u) => { let b: i32 = keep(u); t = t + b; },
        None => {}
    }
    return t;
}`},
	} {
		wantOK(t, "call-payload-"+tc.name, prelude+tc.body)
	}
	wantE051(t, "call-payload-moved-before-its-last-use", prelude+`
function f(xs: string[]): i32 {
    if let Some(u) = pick(xs) {
        let k: i32 = keep(u);
        return k + u.len();
    }
    return 0;
}`)
	// Only the arm that binds u starts a new u. The other arm reads the outer
	// local, which the statement after the match reads again.
	wantE051(t, "outer-local-shadowed-in-another-arm", prelude+`
function f(xs: string[]): i32 {
    let u: string = "abc";
    let t: i32 = 0;
    match (pick(xs)) {
        Some(u) => { t = u.len(); },
        None => { t = keep(u); }
    }
    return t + u.len();
}`)
}

// A let-else binding of a call result is a local like any other let: a copy
// does not consume it.
func TestOwnGuardLetElseBindingOfCallIsCopied(t *testing.T) {
	const prelude = `
function make(): Result[u8[], string] { return Ok([1, 2]); }
function keep(own xs: u8[]): i32 { return xs.len() as i32; }
`
	wantOK(t, "let-else-copied-then-updated", prelude+`
function f(): u8 {
    let Ok(first) = make() else { return 0; };
    let retained = first;
    first = first.with(0, 77);
    return retained[0] + first[0];
}`)
	wantOK(t, "let-else-copied-then-moved", prelude+`
function f(): i32 {
    let Ok(first) = make() else { return 0; };
    let retained = first;
    return keep(first) + retained[1] as i32;
}`)
}

// A lambda is decided as a function of its own. A name it declares moves at
// its last use in the lambda's body. A name it captures does not move.
func TestOwnGuardLambdaLocalMovesAtItsLastUse(t *testing.T) {
	const prelude = `
function arr(n: i32): i32[] { return [n]; }
function mk(n: i32): Option[i32[]] { return Some([n]); }
function keep(own ys: i32[]): i32 { return ys[0]; }
function run(f: () => i32): i32 { return f(); }
`
	wantOK(t, "lambda-let-local", prelude+`
function main(): i32 {
    return run((): i32 => {
        let xs = arr(1);
        let k: i32 = keep(xs);
        return k;
    });
}`)
	wantOK(t, "lambda-call-payload", prelude+`
function main(): i32 {
    return run((): i32 => {
        match (mk(1)) {
            Some(xs) => { let k: i32 = keep(xs); return k; },
            None => { return 0; }
        }
    });
}`)
	wantE051(t, "lambda-capture", prelude+`
function main(): i32 {
    let xs = arr(1);
    let g: () => i32 = (): i32 => { return keep(xs); };
    return g() + g();
}`)
}

// A binding of a fresh call that can hold a handle stays owned, so a copy
// moves the handle.
func TestOwnGuardCallPayloadThatHoldsAHandleMovesOnce(t *testing.T) {
	wantE050Move(t, "handle-payload-copied-then-moved", `
function accepted(l: Socket): Result[Socket, i32] { return Ok(tcp_accept(l)); }
function take(own s: Socket): i32 { return tcp_close(s); }
function f(l: Socket): i32 {
    match (accepted(l)) {
        Ok(s) => {
            let kept = s;
            return take(s);
        },
        Err(e) => { return e; }
    }
    return 0;
}`)
}

// The receiver of a method is not an argument for the result-borrow
// analysis. cmd/fern checks the same program after the method rewrite (#14234).
const cellTakeSrc = `
struct Keys { a: Cell[i32[]] }
function take(slot: Cell[i32[]]): i32[] {
    let got = slot.get();
    slot.set([]);
    return got;
}
function consume(own xs: i32[]): i32 { return xs.len(); }
function f(k: Keys): i32 { return consume(take(k.a)); }
function main(): i32 { return 0; }`

func TestOwnGuardIgnoresAMethodReceiver(t *testing.T) {
	wantOK(t, "method-receiver-result", cellTakeSrc)
}

// A tuple payload of a fresh call is owned, a handle in it included, as a
// single payload is (#14237).
const tuplePayloadSrc = `
struct D { buf: u8[], rng: u32 }
function (own d: D) step(): D { return d; }
function start(own d: D): Result[(D, i32[]), string] { return Ok((d, [1])); }
function eat(own d: D, own xs: i32[]): i32 { return d.buf.len() + xs.len(); }
`

func TestOwnGuardAllowsATuplePayloadOfAFreshCall(t *testing.T) {
	wantOK(t, "tuple-payload", tuplePayloadSrc+`
function f(own d: D): i32 {
    match (start(d)) {
        Ok((a, b)) => { return eat(a, b); },
        Err(_) => { return 0; },
    }
}
function main(): i32 { return 0; }`)
	wantOK(t, "destructured-payload", tuplePayloadSrc+`
function f(own d: D): i32 {
    match (start(d)) {
        Ok(p) => {
            let (a, b) = p;
            return eat(a, b);
        },
        Err(_) => { return 0; },
    }
}
function main(): i32 { return 0; }`)
}

func TestOwnGuardRejectsATuplePayloadOfABorrowedValue(t *testing.T) {
	wantE051(t, "borrowed-tuple-payload", tuplePayloadSrc+`
function f(r: Result[(D, i32[]), string]): i32 {
    match (r) {
        Ok((a, b)) => { return eat(a, b); },
        Err(_) => { return 0; },
    }
}

function main(): i32 { return 0; }`)
}

func TestOwnGuardMethodResultPayloads(t *testing.T) {
	const declarations = `
struct State { n: i32 }
enum Failure { Bad }
function make(): Result[State, Failure] { return Ok(State { n: 0 }); }
function (own s: State) step(): Result[(State, i32), Failure] { return Ok((State { n: s.n + 1 }, 1)); }
function (s: State) lend(): Result[State, Failure] { return Ok(s); }
struct Borrower { state: State }
function (b: Borrower) step(): Result[State, Failure] { return Ok(b.state); }
`
	wantOK(t, "consume-method-result", declarations+`
function main(): i32 {
  let Ok(state) = make() else { return 1; };
  let Ok((next, used)) = state.step() else { return 2; };
  let Ok((done, count)) = next.step() else { return 3; };
  return done.n - 2;
}`)
	wantOK(t, "match-method-result", declarations+`
function main(): i32 {
  let Ok(state) = make() else { return 1; };
  match (state.step()) {
    Ok((next, used)) => { let Ok((done, count)) = next.step() else { return 3; }; return done.n; },
    Err(_) => { return 2; },
  }
}`)
	wantE051(t, "borrowed-method-result", declarations+`
function main(): i32 {
  let Ok(state) = make() else { return 1; };
  let Ok(next) = state.lend() else { return 2; };
  let Ok((done, count)) = next.step() else { return 3; };
  return done.n;
}`)
	wantE051(t, "same-name-borrowed-method-result", declarations+`
function main(): i32 {
  let borrower = Borrower { state: State { n: 0 } };
  let Ok(next) = borrower.step() else { return 1; };
  let Ok((done, count)) = next.step() else { return 2; };
  return done.n;
}`)
}

// An argument before the moved one may read a scalar field of it: the read
// gives a copy before the call takes the value (#14238).
const fieldReadSrc = `
struct C { n: i32, xs: i32[] }
function g(n: i32, own c: C): i32 { return n + c.xs.len(); }
function k(xs: [i32], own c: C): i32 { return xs.len() + c.n; }
function make(): C { return C { n: 1, xs: [1] }; }
function made(): Result[C, string] { return Ok(make()); }
`

func TestOwnGuardAllowsAScalarFieldReadBeforeTheMove(t *testing.T) {
	wantOK(t, "local", fieldReadSrc+`
function f(): i32 { let c = make(); return g(c.n, c); }
function main(): i32 { return 0; }`)
	wantOK(t, "payload-in-a-returning-branch", fieldReadSrc+`
function f(): i32 {
    match (made()) {
        Ok(c) => {
            if (c.n > 0) {
                return g(c.n, c);
            }
            return g(0, c);
        },
        Err(_) => { return 0; },
    }
}
function main(): i32 { return 0; }`)
}

func TestOwnGuardRejectsABorrowedFieldReadBeforeTheMove(t *testing.T) {
	wantE051(t, "borrowed-field", fieldReadSrc+`
function f(): i32 { let c = make(); return k(c.xs, c); }
function main(): i32 { return 0; }`)
}

const spreadSrc = `struct A { sock: Option[Socket], d: string }
function take(own a: A): A { return a; }
`

func TestOwnGuardAllowsASpreadOfAnOwnedValue(t *testing.T) {
	wantOK(t, "own-param", spreadSrc+`
function f(own s: A): A { let t: A = A { ...s, d: "x" }; return take(t); }
function main(): i32 { return 0; }`)
	wantOK(t, "fresh-local", spreadSrc+`
function f(): A { let s: A = A { sock: None, d: "" }; let t: A = A { ...s, d: "x" }; return take(t); }
function main(): i32 { return 0; }`)
}

func TestOwnGuardRejectsASpreadThatDoesNotTakeItsBase(t *testing.T) {
	wantE051(t, "borrowed-param", spreadSrc+`
function f(s: A): A { let t: A = A { ...s, d: "x" }; return take(t); }
function main(): i32 { return 0; }`)
	wantE051(t, "base-read-later", spreadSrc+`
function f(own s: A): A { let t: A = A { ...s, d: "x" }; print(s.d); return take(t); }
function main(): i32 { return 0; }`)
	wantE051(t, "field-names-base", spreadSrc+`
function f(own s: A): A { let t: A = A { ...s, d: s.d }; return take(t); }
function main(): i32 { return 0; }`)
}

func TestOwnGuardAllowsSeveralScalarFieldReadsBeforeTheMove(t *testing.T) {
	wantOK(t, "several-scalar-field-reads", fieldReadSrc+`
function many(a: i32, b: i32, own c: C): i32 { return a + b + c.xs.len(); }
function f(): i32 { let c = make(); return many(c.n, c.n, c); }
function main(): i32 { return 0; }`)
}

const scalarCallConsumeSrc = `struct C { n: i32 }
function (own c: C) close(): i32 { return c.n; }
function make(): C { return C { n: 1 }; }
function first(own p: (C, i32)): i32 { let (c, n) = p; return c.close() + n; }
function last(n: i32, own c: C): i32 { return n + c.close(); }
function f(): i32 { let c = make(); return last(first((c, 0)), c); }
function main(): i32 { return 0; }
`

func TestOwnGuardRejectsAConsumeHiddenInAScalarArgument(t *testing.T) {
	wantE051(t, "consume-through-earlier-tuple", scalarCallConsumeSrc)
}

// A field move inside the else block comes before the reads after the
// let-else, so it is not the last use of the base.
func TestOwnGuardRejectsSupersededFieldMoveInLetElseBlock(t *testing.T) {
	wantE051(t, "field-move-in-let-else-block", fieldMovePrelude+`
struct Holder { cfi: Cfi, room: Option[i32] }
function step(own h: Holder): Holder {
    let Some(v) = h.room else {
        let c = record(h.cfi, 0);
        return Holder { ...h, cfi: c };
    };
    let d = record(h.cfi, v);
    return Holder { ...h, cfi: d };
}`)
}
