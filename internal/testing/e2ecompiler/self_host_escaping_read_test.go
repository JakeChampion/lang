package e2ecompiler

import "testing"

// A string or string[] read out of a field that leaves the frame — returned,
// stored by a callee, reassigned over a local, bound and outliving a rebind of
// its source, iterated, lent, captured, indexed — is a counted reference the
// typed lowering retains at the read and releases at its consumer (#5338). Each
// row lets the source die, churns the freelist with blocks of the same sizes
// and other bytes, then reads the value back, so an over-release reads
// recycled memory (exit 100) as well as the census seeing a leak.

const escapingReadDecl = `struct S { name: string, n: i32 }
struct Q { f: string[] }
struct H { s: string }
function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
function mk(i: i32): S { return S { name: w("s"), n: i }; }
function mkq(i: i32): Q { let o: string[] = []; o = o.append(w("a")); o = o.append(w("bb")); return Q { f: o }; }
function churn(i: i32): i32 {
    let a: string[] = [];
    a = a.append(w("q"));
    a = a.append(w("rr"));
    let b: S = S { name: w("t"), n: i };
    return a[0].len() + b.name.len();
}
`

const escapingReadMain = `
function main(): i32 {
    let t: i32 = 0; let i: i32 = 0; let bad: i32 = 0;
    while (i < 200) { let r: i32 = round(i); if (r < 0) { bad = bad + 1; } t = t + r; i = i + 1; }
    if (bad > 0) { return 100; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 89;
}
`

func escapingReadRow(name, body string) leakRow {
	return leakRow{name, escapingReadDecl + body + escapingReadMain, true}
}

var escapingReadRows = []leakRow{
	escapingReadRow("returned_field", `@noinline function name_of(x: S): string { return x.name; }
function held(i: i32): string { let s: S = mk(i); return name_of(s); }
function round(i: i32): i32 {
    let t: string = held(i);
    let junk: i32 = churn(i);
    if (t != w("s")) { return 0 - 1; }
    return (t.len() + junk) % 7;
}`),
	escapingReadRow("stored_by_callee", `@noinline function keep(s: string): H { return H { s: s }; }
function held(i: i32): H { let s: S = mk(i); return keep(s.name); }
function round(i: i32): i32 {
    let h: H = held(i);
    let junk: i32 = churn(i);
    if (h.s != w("s")) { return 0 - 1; }
    return (h.s.len() + junk) % 7;
}`),
	escapingReadRow("reassigned_alias", `function held(i: i32): string {
    let s: S = mk(i);
    let t: string = w("x");
    if (i % 3 != 0) { t = s.name; }
    return t;
}
function round(i: i32): i32 {
    let t: string = held(i);
    let junk: i32 = churn(i);
    if (i % 3 != 0 && t != w("s")) { return 0 - 1; }
    if (i % 3 == 0 && t != w("x")) { return 0 - 2; }
    return (t.len() + junk) % 7;
}`),
	escapingReadRow("source_rebound", `function round(i: i32): i32 {
    let s: S = mk(i);
    let t: string = s.name;
    s = S { name: w("r"), n: i };
    let junk: i32 = churn(i);
    if (t != w("s") || s.name != w("r")) { return 0 - 1; }
    return (t.len() + s.name.len() + junk) % 7;
}`),
	escapingReadRow("iterated_field", `function last_of(i: i32): string {
    let q: Q = mkq(i);
    let last: string = "";
    for s in q.f { last = s; }
    return last;
}
function round(i: i32): i32 {
    let t: string = last_of(i);
    let junk: i32 = churn(i);
    if (t != w("bb")) { return 0 - 1; }
    return (t.len() + junk) % 7;
}`),
	escapingReadRow("lent_field", `@noinline function peek(xs: string[]): i32 { return xs.len() + xs[1].len(); }
function round(i: i32): i32 {
    let q: Q = mkq(i);
    let n: i32 = peek(q.f);
    let junk: i32 = churn(i);
    if (q.f[0] != w("a")) { return 0 - 1; }
    return (n + junk) % 7;
}`),
	escapingReadRow("captured_field", `function cap(i: i32): (i32) => i32 {
    let s: S = mk(i);
    return (k: i32): i32 => s.name.len() + k;
}
function round(i: i32): i32 {
    let f: (i32) => i32 = cap(i);
    let junk: i32 = churn(i);
    let n: i32 = f(1);
    if (n != w("s").len() + 1) { return 0 - 1; }
    return (n + junk) % 7;
}`),
	escapingReadRow("indexed_field", `function first_of(i: i32): string {
    let q: Q = mkq(i);
    let e: string = q.f[0];
    return e;
}
function round(i: i32): i32 {
    let t: string = first_of(i);
    let junk: i32 = churn(i);
    if (t != w("a")) { return 0 - 1; }
    return (t.len() + junk) % 7;
}`),
}

func TestSelfHostEscapingReadX86_64(t *testing.T) {
	runLeakRowsX86_64(t, escapingReadRows)
}

func TestSelfHostEscapingReadArm64(t *testing.T) {
	runLeakRowsArm64(t, escapingReadRows)
}

func TestSelfHostEscapingReadWasm(t *testing.T) {
	runLeakRowsWasm(t, escapingReadRows)
}
