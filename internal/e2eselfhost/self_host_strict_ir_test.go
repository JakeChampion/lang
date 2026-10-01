package e2eselfhost

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The self-hosted CLI refuses a module the typed lowering does not produce
// whole: exit 3, after a `FERN_SEM_IR: <function>: <reason>` line for each
// refused declaration.
//
// Two halves, and both are essential:
//
//   - strictIRCorpus asserts NO refusal across constructs the typed lowering is
//     supposed to cover. A newly-unlowerable construct fails here, naming the
//     function, instead of silently taking the AST lowering.
//   - TestSelfHostStrictIRRefusesBail asserts a real refusal DOES exit 3, so a
//     green corpus means the tripwire is armed rather than inert.
//
// Every `want` must be in [0, 126): the wasm leg exits through WASI, which
// rejects anything above that with `exit with invalid exit status`, whereas an
// ELF exit code is simply taken mod 256. A case that returns 160 therefore
// passes on x86-64 and traps on wasm, which reads like a backend miscompile.
var strictIRCorpus = []struct {
	name string
	src  string
	want int
}{
	// An array holding views of two parameters is anchored to both (#10687).
	{"views-of-two-sources", `function g(x: string, y: string): str[] { var o: str[] = []; o = o.append(slice_unchecked(x, 0, 1)); o = o.append(slice_unchecked(y, 0, 1)); return o; }
function main(): i32 { var xs: str[] = g("ab", "cd"); return xs.len(); }
`, 2},
	// Every read of a view map value takes a fresh box (#10701).
	{"view-map-value-reads", `import "core/map";
function main(): i32 {
    var b: string = "abcdefgh";
    var m: Map[i32, str] = map_new(4);
    m = m.insert(1, slice_unchecked(b, 2, 6));
    var n: i32 = m.get_or(1, "").len() + m.get_or(2, "x").len();
    match (m.get(1)) { Some(v) => { n = n + v.len(); }, None => { n = n + 100; } }
    for v in m.values() { n = n + v.len(); }
    for (k, v) in m { n = n + k + v.len(); }
    return n;
}
`, 18},
	// A dyn holding a view merged past its source is copied at the merge, so
	// the size is read from the copy after the source is released.
	{"dyn-view-merged-past-its-source", `trait Size { function size(self: Self): i32; }
struct P { a: str }
impl Size for P { function size(self: P): i32 { return self.a.len() * 10 + (self.a[0] as i32) - 97; } }
function mk(n: i32): string {
    var s: string = "ab";
    var i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    return s;
}
function wrap(s: string): dyn Size { var p: P = P { a: slice_unchecked(s, 1, 4) }; return p; }
function g(n: i32): i32 {
    var d: dyn Size = P { a: "q" };
    if (n != 0) {
        var s: string = mk(n);
        d = wrap(s);
    }
    var junk: string[] = [];
    var i: i32 = 0;
    while (i < 50) { junk = junk.append("zz"); i = i + 1; }
    return d.size();
}
function main(): i32 { return g(3) + g(0); }
`, 57},
	// One local view held twice by an array: each element takes a fresh view.
	{"local-view-held-twice", `function g(s: string): str[] { var w: str = slice_unchecked(s, 0, 2); var o: str[] = [w, w]; return o; }
function main(): i32 { return g("abcd").len(); }
`, 2},
	// A builtin's Result bound from a match-expression whose Err arm returns
	// early (#9326), beside its user-wrapper twin so a fix that widened only
	// one of them is visible. The early return leaves the enclosing function;
	// the arm hands the block no value. The paths do not exist, so only the Err
	// arm runs; TestSelfHostValueBlockEarlyReturn runs both arms.
	{"builtin-result-value-block-early-return", `function probe(p: string): i32 {
    var si: FileStat = match (stat(p)) { Ok(v) => v, Err(_) => { return 1; } };
    var held: FileStat = si;
    return 4;
}
function main(): i32 { return probe("/nonexistent-9326/a") + probe("/nonexistent-9326/b"); }
`, 2},
	{"wrapped-result-value-block-early-return", `function mine(p: string): Result[FileStat, string] {
    match (stat(p)) { Ok(v) => { return Ok(v); }, Err(_) => { return Err("no"); } }
}
function probe(p: string): i32 {
    var si: FileStat = match (mine(p)) { Ok(v) => v, Err(_) => { return 1; } };
    var held: FileStat = si;
    return 4;
}
function main(): i32 { return probe("/nonexistent-9326/a") + probe("/nonexistent-9326/b"); }
`, 2},
	// Typed captures now resolve pointer elements of tuple destructures. This
	// used to be a refusal fixture; require successful lowering and execution.
	{"destructured-array-closure", `function main(): i32 {
    var t: (i32[], i32) = ([3i32], 4i32);
    var (a, b) = t;
    var fs: ((i32) => i32)[] = [((x: i32) => (x + a[0i32]))];
    return (fs[0i32](1i32) + b) & 63i32;
}
`, 8},
	// The #5642 shape itself: checked operators in a match scrutinee, the
	// construct whose missing recovery case motivated the issue. Both arms are
	// exercised — f(100, 3) fits u8, f(250, 10) overflows.
	{"checked-operators", `
function f(a: u8, b: u8): i32 {
    match (a +? b) { Some(v) => { return v as i32; }, None => { return 99; } }
}
function g(a: i32, b: i32): i32 {
    match (a *? b) { Some(v) => { return v; }, None => { return 7; } }
}
function main(): i32 { return f(100, 3) + g(2, 3) - f(250, 10); }
`, 10},
	// Closures with captures, held in an array and dispatched through a
	// fn-typed param.
	{"closures", `
function apply(f: (i32) => i32, x: i32): i32 { return f(x); }
function main(): i32 {
    var n: i32 = 5;
    var add: (i32) => i32 = (x: i32): i32 => { return x + n; };
    var dbl: (i32) => i32 = (x: i32): i32 => { return x * 2; };
    var fs: ((i32) => i32)[] = [add, dbl];
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < fs.len()) { t = t + apply(fs[i], 3); i = i + 1; }
    return t;
}
`, 14},
	// Enum payloads, a guarded arm, and an exhaustive match.
	{"enum-match-guard", `
enum Shape { Circle(i32), Rect(i32, i32), Empty }
function area(s: Shape): i32 {
    match (s) {
        Circle(r) when r > 10 => { return 999; },
        Circle(r) => { return 3 * r * r; },
        Rect(w, h) => { return w * h; },
        Empty => { return 0; }
    }
}
function main(): i32 { return area(Circle(2)) + area(Rect(3, 4)) + area(Empty); }
`, 24},
	// Heap traffic: a struct array grown by append, with string fields read
	// back after construction.
	{"struct-array-strings", `
struct P { name: string, n: i32 }
function label(i: i32): string {
    if (i % 2 == 0) { return "ab"; }
    return "xyz";
}
function build(n: i32): P[] {
    var out: P[] = [];
    var i: i32 = 0;
    while (i < n) { out = out.append(P { name: label(i) + "!", n: i }); i = i + 1; }
    return out;
}
function main(): i32 {
    var ps: P[] = build(5);
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < ps.len()) { if (ps[i].name.len() == 3) { t = t + ps[i].n + 1; } i = i + 1; }
    return t;
}
`, 9},
	// Generics, tuples, and the `?` operator — the other consuming position
	// whose scrutinee-type recovery #5642 had to fix alongside lower_match's.
	{"generics-tuples-try", `
function pair[K, V](k: K, v: V): (K, V) { return (k, v); }
function first(t: (i32, string)): i32 { return t.0; }
function parse(s: string): Result[i32, string] {
    if (s == "ok") { return Ok(1); }
    return Err("bad");
}
function chain(s: string): Result[i32, string] {
    var v: i32 = parse(s)?;
    return Ok(v + 41);
}
function main(): i32 {
    var t: (i32, string) = pair(1, "x");
    match (chain("ok")) { Ok(v) => { return v + first(t); }, Err(_) => { return 0; } }
}
`, 43},
	// A match whose scrutinee is a call through a capture-free / capturing
	// closure LOCAL returning Option: the lambda must lift to a hoisted __lam_N
	// so the call resolves and the scrutinee's Option type recovers. Before the
	// StmtMatch arm in irlower's subst_fcall_stmts, the leftover `f` reference in
	// `match (f())` blocked the binding lift, so the lambda fell to the inline
	// escaping-closure path (const_func(<fn>$clo)) and bailed the module to AST
	// (#3457 slice 3). Under the flag these must route IR (no exit-3 bail).
	{"match-closure-local-opt", `
function main(): i32 {
    var f: () => Option[i32] = () => Some(7);
    match (f()) { Some(v) => { return v; }, None => { return 0; } }
}
`, 7},
	{"match-capturing-closure-local-opt", `
function main(): i32 {
    var n: i32 = 7;
    var f: () => Option[i32] = () => Some(n);
    match (f()) { Some(v) => { return v; }, None => { return 0; } }
}
`, 7},
	// A match whose scrutinee calls an ANNOTATED fn-typed local bound to a named
	// Option/Result-returning fn (`var f: () => Option[i32] = g; match (f())`):
	// the binding seeds its return type (mark_closure_opt_ret, gated on the
	// fn-type annotation) so the payload recovers and the module routes IR. The
	// unannotated `var f = g` form is deliberately NOT covered — its `f()` call
	// miscompiles on the IR path, so the lowering leaves it unseeded and bails.
	{"match-fnlocal-named-opt", `
function g(): Option[i32] { return Some(7); }
function main(): i32 {
    var f: () => Option[i32] = g;
    match (f()) { Some(v) => { return v; }, None => { return 0; } }
}
`, 7},
	{"match-fnlocal-named-result", `
function g(): Result[i32, i32] { return Ok(5); }
function main(): i32 {
    var f: () => Result[i32, i32] = g;
    match (f()) { Ok(v) => { return v; }, Err(_) => { return 9; } }
}
`, 5},
	// `?` whose success payload is itself a bracketed generic
	// (`Result[Option[i32], E]`) — the last per-function shape lower_try
	// declined (#3457 endgame). The payload box is pointer-shaped, read
	// through the same op_opt_payload as a struct/enum, and the `var x:
	// Option[i32] = f(n)?` binding types the slot from its annotation, so
	// the following `match (x)` recovers both arms.
	{"try-generic-payload", `
function f(n: i32): Result[Option[i32], i32] { return Ok(Some(n)); }
function g(n: i32): Result[i32, i32] {
    var x: Option[i32] = f(n)?;
    match (x) { Some(v) => { return Ok(v); }, None => { return Ok(0); } }
}
function main(): i32 { match (g(5)) { Ok(v) => { return v; }, Err(_) => { return 9; } } }
`, 5},
	// The same shape on a bare Option (`Option[Option[i32]]`), plus a
	// None-payload leg so the inner enum's other variant is exercised too.
	{"try-generic-payload-option", `
function f(n: i32): Option[Option[i32]] { if (n > 3) { return Some(Some(n)); } return Some(None); }
function g(n: i32): Option[i32] {
    var x: Option[i32] = f(n)?;
    match (x) { Some(v) => { return Some(v + 1); }, None => { return Some(50); } }
}
function main(): i32 {
    var a: i32 = 0;
    match (g(7)) { Some(v) => { a = v; }, None => { a = 99; } }
    match (g(1)) { Some(v) => { a = a + v; }, None => { a = a + 99; } }
    return a;
}
`, 58},
	// A `?`-chain whose bound generic payload is itself unwrapped by a second
	// `?`: the payload slot must survive being fed back into the try path.
	{"try-generic-payload-chain", `
function inner(n: i32): Result[Result[i32, i32], i32] { if (n > 0) { return Ok(Ok(n)); } return Ok(Err(3)); }
function outer(n: i32): Result[i32, i32] {
    var o: Result[i32, i32] = inner(n)?;
    var v: i32 = o?;
    return Ok(v * 2);
}
function main(): i32 { match (outer(9)) { Ok(v) => { return v; }, Err(_) => { return 88; } } }
`, 18},
	// std/i32's min/max/clamp on a scalar receiver. Asymmetric operands catch an
	// operand-order swap; the two clamp calls exercise the hi and lo saturating
	// edges.
	{"i32-min-max-clamp", `import "std/i32";
function main(): i32 {
    var a: i32 = 8;
    var b: i32 = 3;
    return a.min(b) + a.max(b) + (99).clamp(0, 10) + (0 - 5).clamp(0, 10);
}
`, 21},
	// std/array's xs.first() / xs.last() over every element kind, each result
	// CONSUMED so the call's result type has to be recovered too: a string
	// element through `.len()`, a struct element through `.n`, an
	// array-of-arrays element through a second `[i]`, and a string[][] element
	// through a chained `.first()`. A missing recovery mis-dispatches (arr_len on
	// a string box reads a different field) rather than refusing, so the exit
	// code is what catches it.
	{"arr-first-last", `import "std/array";
import "std/option";
struct P { name: string, n: i32 }
function build(): i32[] {
    var out: i32[] = [];
    out = out.append(4);
    out = out.append(6);
    return out;
}
function main(): i32 {
    var xs: i32[] = [10, 20, 30];
    var ss: string[] = ["ab", "cde"];
    var ps: P[] = [P { name: "x", n: 3 }, P { name: "yy", n: 4 }];
    var m: i32[][] = [[1, 2], [3, 4]];
    var mm: string[][] = [["a", "bb"], ["ccc"]];
    var none: P = P { name: "", n: 0 };
    var t: i32 = xs.first().unwrap_or(0) + xs.last().unwrap_or(0);            // 40
    t = t + ss.first().unwrap_or("").len() + ss.last().unwrap_or("").len();   // +5
    t = t + ps.first().unwrap_or(none).n + ps.last().unwrap_or(none).name.len(); // +5
    t = t + m.first().unwrap_or([])[1] + m.last().unwrap_or([])[0];           // +5
    t = t + mm.first().unwrap_or([])[1].len() + mm.last().unwrap_or([]).first().unwrap_or("").len(); // +5
    return t + build().last().unwrap_or(0) + build().first().unwrap_or(0);    // +10
}
`, 70},
	// std/i32's ASCII classifier / case family on a byte receiver. The receivers
	// are the point: a u8 LOCAL, an `as u8` cast, and a string INDEX, which is a
	// byte since #5629.
	{"ascii-byte-methods", `import "std/i32";
function main(): i32 {
    var t: i32 = 0;
    var A: u8 = 65;
    var z: u8 = 122;
    var d: u8 = 53;
    var f: u8 = 70;
    var g: u8 = 71;
    var sp: u8 = 32;
    if (A.to_ascii_lower() as i32 == 97) { t = t + 1; }
    if (z.to_ascii_upper() as i32 == 90) { t = t + 2; }
    if (A.to_ascii_upper() as i32 == 65) { t = t + 4; }   // already upper: unchanged
    if (z.to_ascii_lower() as i32 == 122) { t = t + 8; }  // already lower: unchanged
    if (d.is_ascii_digit() && !A.is_ascii_digit()) { t = t + 16; }
    if (z.is_ascii_lower() && A.is_ascii_upper()) { t = t + 32; }
    if (A.is_ascii_alpha() && A.is_ascii_letter() && d.is_ascii_alnum()) { t = t + 1; }
    if (!sp.is_ascii_alnum() && !sp.is_ascii_alpha()) { t = t + 2; }
    if (f.is_ascii_hex_digit() && d.is_ascii_hex_digit() && !g.is_ascii_hex_digit()) { t = t + 4; }
    if ((66 as u8).to_ascii_lower() as i32 == 98) { t = t + 8; }
    var s: string = "Q";
    if (s[0].is_ascii_upper() && s[0].to_ascii_lower() as i32 == 113) { t = t + 16; }
    return t;
}
`, 94},
	// b.to_ascii_string() — a fresh 1-char string from a byte, including on the
	// byte a chained to_ascii_lower() returns.
	{"ascii-to-string", `import "std/i32";
function main(): i32 {
    var c: u8 = 65;
    var s: string = c.to_ascii_string();
    var t: i32 = 0;
    if (s[0] as i32 == 65) { t = t + 1; }
    if (s.len() == 1) { t = t + 2; }
    if ((66 as u8).to_ascii_string()[0] as i32 == 66) { t = t + 4; }
    if (c.to_ascii_lower().to_ascii_string()[0] as i32 == 97) { t = t + 8; }
    return t;
}
`, 15},
	// A Cell[T] PARAMETER. The local-annotation path marks a `var c: Cell[i32]`
	// slot is_cell, but the param columns hard-coded it false, so `c.get()` on a
	// parameter keyed "i32.get" — an unknown symbol — and bailed the module.
	// Cell[string] is included because the element kind drives the read: an
	// untracked element loads as an i32 and `.len()` on it is meaningless.
	{"cell-param", `
function bump(c: Cell[i32]): void { c.set(c.get() + 1); }
function slen(c: Cell[string]): i32 { return c.get().len(); }
function main(): i32 {
    var c: Cell[i32] = cell_new(10);
    bump(c);
    bump(c);
    var s: Cell[string] = cell_new("abc");
    s.set("de");
    return c.get() + slen(s) + s.get().len();
}
`, 16},
	// A nested RESULT payload bound in a match-EXPRESSION (`Some(r)` over an
	// Option[Result[…]]). iife_payload_bindable admitted a nested `Option[` payload
	// into an i32 temp from an ident scrutinee and omitted `Result[` from the same
	// spelling test, so this bailed while the identical STATEMENT-form match
	// lowered. The argument for admitting it is the Option half's: arms share a
	// result type, so an i32 temp means the bound box is only ever consumed to
	// compute an i32, never stored as the result.
	{"iife-match-nested-result-payload", `
function g(o: Option[Result[i32, i32]]): i32 {
    return match (o) { Some(r) => 7, None => 0 - 1 };
}
function h(o: Option[Result[i32, i32]]): i32 {
    match (o) { Some(r) => { match (r) { Ok(n) => { return n + 100; }, Err(e) => { return e; } } }, None => { return 0; } }
}
function main(): i32 { return g(Some(Ok(5))) + h(Some(Ok(5))); }
`, 112},
	// A NESTED PATTERN in a match-EXPRESSION (`Some(Ok(n)) => n + 100`). The
	// parser desugars a nested arm into a flat outer arm whose body re-matches the
	// payload on an inner `match` STATEMENT, so the arm's terminal is not a
	// `return E` and lower_iife_match bailed the whole module — while the identical
	// match in STATEMENT form lowered. iife_rewrite_arm_body now rewrites that
	// inner match recursively, storing into the same value temp, and carries the
	// f64 / string / i64 width guards down with it so an ill-fitting tail bails
	// wherever it sits rather than only at the top level.
	{"iife-match-nested-pattern", `
function g(o: Option[Result[i32, i32]]): i32 {
    return match (o) {
        Some(Ok(n)) => n + 100,
        Some(Err(e)) => e,
        None => 0 - 1,
    };
}
function main(): i32 { return g(Some(Ok(5))) + g(Some(Err(2))) + g(None); }
`, 106},
	// An UNANNOTATED binding of an erased-generic `T[]`-returning call
	// (`var s = sort_by_key(ps, …)`). array_ret_fns_of already registered the
	// function — is_array_type only tests the `[]` suffix, so `T[]` counts and the
	// slot is is_arr — but struct_ret_fns_of recorded no ELEMENT type, because
	// stripping `[]` from `T[]` leaves the typevar. So `s[i].k` had no struct type
	// and the CALLER bailed while the generic function itself
	// lowered fine. A positional "name|$arg<i>" argref now records "the element
	// type is argument i's element type", resolved at the call site — the same
	// convention the erased string / array returns use.
	//
	// The annotated form (`var s: P[] = …`) always worked, which is what made this
	// the third second-mechanism gap of the set. Struct AND enum elements are both
	// covered; `qs` is a separate array from `ps` on purpose, because reading a
	// source array after a generic mutated it through `.with` measures leak-mode
	// aliasing (an in-place store on the register/wasm backends, a copy in the
	// interpreter) rather than anything about this fix.
	{"generic-array-return-unannotated", `
struct P { k: i32 }
enum C { Red, Blue }
function idf[T](arr: T[]): T[] { return arr; }
function sort_by_key[T](arr: T[], key: (T) => i32): T[] {
    var out: T[] = arr;
    var i: i32 = 1;
    while (i < out.len()) {
        var j: i32 = i;
        while (j > 0 && key(out[j]) < key(out[j - 1])) {
            var tv: T = out[j];
            out = out.with(j, out[j - 1]);
            out = out.with(j - 1, tv);
            j = j - 1;
        }
        i = i + 1;
    }
    return out;
}
function main(): i32 {
    var ps: P[] = [P { k: 3 }, P { k: 1 }, P { k: 2 }];
    var s = sort_by_key(ps, (p: P): i32 => { return p.k; });
    var t: i32 = s[0].k * 10 + s[2].k;          // 1*10 + 3
    var qs: P[] = [P { k: 7 }];
    var d = idf(qs);
    t = t + d[0].k;                              // + 7
    var cs: C[] = [Blue, Red];
    var e = idf(cs);
    match (e[0]) { Red => { t = t + 1; }, Blue => { t = t + 2; } }
    return t;
}
`, 22},
	// A DIRECT, hand-written IIFE — `((): i32 => { return 7; })()` — in
	// `return` position, and with string, struct and nested-IIFE results flowing
	// through the value. The capturing forms lower as direct calls that take
	// their captures as arguments.
	{"direct-iife", `
struct P { n: i32 }
function g(n: i32): i32 { return n * 2; }
function ret(): i32 { return ((): i32 => { return 7; })(); }
function main(): i32 {
    var t: i32 = ret();                                              // 7
    var n: i32 = 5;
    t = t + ((): i32 => { return n + 2; })();                   // +7
    t = t + ((): string => { return "ab" + "cd"; })().len();    // +4
    t = t + ((): P => { return P { n: 6 }; })().n;              // +6
    t = t + ((): i32 => { return ((): i32 => { return 3; })() + 4; })(); // +7
    var i: i32 = 0;
    while (i < 3) { t = t + ((): i32 => { return g(i); })(); i = i + 1; }      // +6
    return t;
}
`, 37},
	// The if/match-EXPRESSION desugars share the IIFE shape, so they are the
	// regression side of the case above: a fix that mishandled a StmtIf /
	// StmtMatch body would change these, not the direct form.
	{"iife-if-match-expression", `
function main(): i32 {
    var a: i32 = if (3 > 2) { 5 } else { 1 };
    var b: i32 = match (a) { 5 => { 20 }, _ => { 0 } };
    return a + b;
}
`, 25},
	// The receiver guard on the case above: a STRUCT with user methods named
	// `first` / `last` keeps its own return types. Classifying those calls as
	// element reads (the bug an unguarded `field == "first"` test introduces)
	// types `b.last()` as "" instead of string, so `.len()` mis-dispatches —
	// silently, since the module still lowers. Exit 7 on both legs is the pin.
	{"arr-first-last-user-method", `
struct Box { n: i32 }
function (b: Box) first(): i32 { return b.n + 1; }
function (b: Box) last(): string { return "zz"; }
function main(): i32 {
    var b: Box = Box { n: 4 };
    return b.first() + b.last().len();
}
`, 7},
	// A USER array method returning Option[T], matched INLINE. The call itself
	// always lowered; what was missing was the scrutinee's result TYPE — the
	// match-scrutinee (and try-operator) resolvers only knew the BUILTIN array
	// methods (min / max), so `match (xs.pick())` had no payload type and bailed
	// the whole module, while binding it first (`var o: Option[i32] =
	// xs.pick(); match (o)`) lowered. std/array's `gcd_all` / `lcm_all` are the
	// real consumers (TestSelfHostArray, TestSelfHostStdTestE2E). Both the
	// inline and the bound form are pinned, since it is the pair that identifies
	// the gap as a missing type recovery rather than missing lowering.
	{"arr-user-method-option-inline", `
function __method_Array_pick(arr: i32[]): Option[i32] {
    if (arr.len() == 0) { return None; }
    return Some(arr[0]);
}
function __method_Array_tail_str(arr: string[]): Option[string] {
    if (arr.len() < 2) { return None; }
    return Some(arr[arr.len() - 1]);
}
function main(): i32 {
    var xs: i32[] = [12, 18];
    var t: i32 = 0;
    match (xs.pick()) { Some(g) => { t = g; }, None => { t = 99; } }
    var bound: Option[i32] = xs.pick();
    match (bound) { Some(g) => { t = t + g; }, None => { t = t + 99; } }
    var empty: i32[] = [];
    match (empty.pick()) { Some(g) => { t = t + g; }, None => { t = t + 1; } }
    var ss: string[] = ["a", "bcd"];
    match (ss.tail_str()) { Some(v) => { t = t + v.len(); }, None => { t = t + 50; } }
    return t;                                    // 12 + 12 + 1 + 3
}
`, 28},
	// An UNANNOTATED cell binding — `var c = cell_new(0)` with no `: Cell[i32]`.
	// The annotated spelling records is_cell from the type name; without it the
	// slot stayed plain and `c.get()` dispatched as a method on the ELEMENT type
	// ("call to unknown symbol i32.get"), bailing the module — the shape
	// TestSelfHostImmutabilityGate's cell-scalar-ok case feeds. The element KIND
	// matters too, not just the cell-ness: a string cell whose slot misses
	// is_strarr loads its element as an i32, so `.len()` reads a non-pointer.
	// i64 cells are absent, but NOT because of a codegen divergence — an earlier
	// `var c: Cell[i64] = cell_new(5000000000)` is REJECTED by
	// the native checker (E003: cannot assign Cell[i32] to Cell[i64]): cell_new
	// types its argument in isolation, so a bare literal settles to i32 and the
	// annotation never reaches it. The interpreter was not computing a different
	// answer, it was refusing to compile. Spelled `cell_new(5000000000 as i64)`
	// both paths agree (42). The inference gap is real but is native-checker
	// business, not this shape's.
	{"cell-unannotated", `
function main(): i32 {
    var ci = cell_new(7);
    ci.set(ci.get() + 3);
    var cs = cell_new("ab");
    cs.set(cs.get() + "cd");
    var cf = cell_new(1.5);
    cf.set(cf.get() + 0.5);
    var acc: i32 = ci.get() + cs.get().len();
    if (cf.get() == 2.0) { acc = acc + 10; }
    return acc;                                  // 10 + 4 + 10
}
`, 24},
	// `.to_string()` on an INLINE wide cast — `(n as i64).to_string()` — beside
	// the bound form. Both widths: u64 renders 2^64-1 as the full decimal only if
	// it keeps the UNSIGNED formatter, so losing the width shows up as 20 vs 2.
	// Real consumers: examples/tests/{i64,u64}_test.fern's test_to_string_wide.
	{"wide-cast-to-string", `import "std/i64";
import "std/u64";
function main(): i32 {
    var a: i32 = (1234567890123 as i64).to_string().len();      // 13
    var b: i32 = (42 as i64).to_string().len();                 //  2
    var c: i32 = (18446744073709551615 as u64).to_string().len();// 20
    var v: i64 = 1234567890123 as i64;
    var d: i32 = v.to_string().len();                           // 13
    return a + b + c + d;
}
`, 48},
	// An annotated TUPLE binding whose initialiser is a method call the tuple-tag
	// inference does not key. Each StmtVar arm recovers element tags from the
	// INITIALISER — the method arm keys `tuple_ret_type("<Struct>.<m>")` — so a
	// method on an Option/Result receiver (std/option's `some.unzip()`, the real
	// consumer in examples/tests/option_combinators_test.fern) recorded nothing
	// and `sa.0.unwrap_or(0)` dispatched as `i32.unwrap_or`, an unknown symbol.
	// The annotation names every element, so it now fills the hole — only when
	// nothing else did, which is what keeps every self-typing binding's tags
	// (and therefore its asm) identical.
	//
	// Both elements are CONSUMED at their own types: an i32 payload and a string
	// payload whose `.len()` would read a non-pointer if the tag were lost, so a
	// half-recovered tag shows up as a wrong answer rather than as a bail.
	{"tuple-annotation-from-call", `
function unwrap_or_i(o: Option[i32], d: i32): i32 { match (o) { Some(v) => { return v; }, None => { return d; } } }
function unwrap_or_s(o: Option[string], d: string): string { match (o) { Some(v) => { return v; }, None => { return d; } } }
function split_pair(t: (i32, string)): (Option[i32], Option[string]) { return (Some(t.0), Some(t.1)); }
function main(): i32 {
    var sa: (Option[i32], Option[string]) = split_pair((7, "hi"));
    return unwrap_or_i(sa.0, 0) + unwrap_or_s(sa.1, "").len();   // 7 + 2
}
`, 9},
	// A loop over the innermost level of a 4-deep nested array.
	{"nested-for", `function main(): i32 {
    var hyper: i32[][][][] = [[[[1]], [[2, 3]]]];
    var sum = 0;
    for cube in hyper { for plane in cube { for row in plane { for v in row { sum = sum + v; } } } }
    return sum;
}
`, 6},
	// A match in VALUE position whose payload is an 8-byte-element array.
	{"iife-value-block", `enum W { Wide(i64[]), Empty }
function main(): i32 {
    var w: W = Wide([5i64, 6i64]);
    var u: i64 = (match (w) { Wide(xs) => xs[0], Empty => 9i64 });
    return (u as i32) & 255i32;
}
`, 5},
}

// runDriver runs a self-host driver over `src`, optionally with FERN_STRICT_IR
// set, and returns stdout, stderr and the exit code.
func runDriver(t *testing.T, runner []string, bin string, src []byte, strict bool, args ...string) ([]byte, string, int) {
	t.Helper()
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin, args...)
	} else {
		a := append([]string{}, runner[1:]...)
		a = append(a, bin)
		a = append(a, args...)
		cmd = exec.Command(runner[0], a...)
	}
	cmd.Stdin = bytes.NewReader(src)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// Strip any ambient FERN_STRICT_IR first: the whole package can be run under
	// it as a probe (see docs/SELFHOST-AST-RETIREMENT.md), and inheriting it would
	// make the "unset" leg strict too, silently voiding the inertness assertion.
	cmd.Env = []string{}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "FERN_STRICT_IR=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	if strict {
		cmd.Env = append(cmd.Env, "FERN_STRICT_IR=1")
	}
	_ = cmd.Run()
	return stdout.Bytes(), stderr.String(), cmd.ProcessState.ExitCode()
}

// strictIRDriver builds the asm_run driver, which compiles one self-contained
// module from stdin.
func strictIRDriver(t *testing.T) (string, []string, string) {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	return gcc, runner, buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
}

// strictExit is the CLI's exit code from a tryEmit error, or 0 without one.
func strictExit(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("CLI did not run: %v", err)
	}
	return exit.ExitCode()
}

// TestSelfHostStrictIRX86_64 asserts the corpus is produced whole and that
// each program runs to its expected exit code.
func TestSelfHostStrictIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range strictIRCorpus {
		t.Run(tc.name, func(t *testing.T) {
			on := cli.emit(t, "x86-64-linux", tc.src)
			if code, _ := cli.runX86(t, on); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostStrictIRRefusesBail is the gate: a program the typed lowering
// refuses fails the compile with exit 3, naming the function, with no FERN_
// variable set. Without this, a green corpus is consistent with a refusal
// passing silently.
func TestSelfHostStrictIRRefusesBail(t *testing.T) {
	cli := newStrictCLI(t)
	tc := strictIRBailReasons[0]
	_, diags, err := cli.tryEmit(t, "x86-64-linux", tc.src)
	if code := strictExit(t, err); code != 3 {
		t.Fatalf("exited %d, want a refusal (3)\n%s", code, diags)
	}
	if !strings.Contains(diags, "FERN_SEM_IR: "+tc.fn+": ") || !strings.Contains(diags, "FERN_SEM_IR: the typed lowering refused") {
		t.Errorf("refusal did not name the refused function:\n%s", diags)
	}
}

// strictIRBailReasons pins that a refusal names the REASON, not merely the
// function it was in, so two refusals can be told apart without bisecting a
// body by hand.
//
// Each program here is VALID — checked against the native compiler, not merely
// observed to refuse — because an invalid program is refused for reasons that
// say nothing about the typed lowering.
//
// A row breaking because its gap CLOSED is the intended failure: the fixture no
// longer demonstrates a reason. Move it into strictIRCorpus, which requires
// successful compilation and execution, and retain valid refusal examples for
// the remaining gaps. Do not weaken an assertion to accept both outcomes.
var strictIRBailReasons = []struct {
	name   string
	src    string
	fn     string
	reason string
}{
	// A closure capturing a bare view is refused where it is built: returned,
	// it would outlive the string it views.
	{"closure-captures-a-view", `function mk(n: i32): string {
    var s: string = "ab";
    var i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    return s;
}
function viewer(n: i32): () => i32 {
    var s: string = mk(n);
    var v: str = slice_unchecked(s, 1, 4);
    return () => v.len() * 10 + (v[0] as i32) - 97;
}
function main(): i32 { var f: () => i32 = viewer(3); return f(); }
`, "viewer", "closure capture type"},
	// An instance bound to a view would hand out a view it was lent.
	{"template-bound-to-a-view", `pub function first[T](f: () => T): T {
    var xs: T[] = [f()];
    return xs[0];
}

function main(): i32 {
    var b: string = "abcdefgh";
    print(first((): str => slice_unchecked(b, 2, 5)));
    return 0;
}
`, "first$str", "a view is lent, never retained"},
}

// TestSelfHostStrictIRNamesBailReason asserts each fixture's refusal names its
// own function and reason, and that no two of them collapse to the same
// message.
func TestSelfHostStrictIRNamesBailReason(t *testing.T) {
	cli := newStrictCLI(t)
	langBin := buildLangBinForInterp(t)

	seen := map[string]string{}
	for _, tc := range strictIRBailReasons {
		t.Run(tc.name, func(t *testing.T) {
			if out, err := nativeCheck(t, langBin, tc.src); err != nil {
				t.Fatalf("the native compiler rejects this fixture, so its refusal says nothing about the typed lowering: %v\n%s", err, out)
			}
			_, diags, err := cli.tryEmit(t, "x86-64-linux", tc.src)
			if code := strictExit(t, err); code != 3 {
				t.Fatalf("exited %d, want a refusal (3)\n%s", code, diags)
			}
			if !strings.Contains(diags, "FERN_SEM_IR: "+tc.fn+": ") {
				t.Errorf("refusal did not name %q as the refused function:\n%s", tc.fn, diags)
			}
			if !strings.Contains(diags, tc.reason) {
				t.Errorf("refusal did not carry the reason %q:\n%s", tc.reason, diags)
			}
		})
		if prev, dup := seen[tc.reason]; dup {
			t.Errorf("%s and %s expect the same reason %q — a reason that does not "+
				"distinguish two different refusals is no better than the bare function name",
				prev, tc.name, tc.reason)
		}
		seen[tc.reason] = tc.name
	}
}

// nativeCheck runs `fern -check` over `src` and returns the compiler's output
// alongside any rejection.
func nativeCheck(t *testing.T, langBin, src string) ([]byte, error) {
	t.Helper()
	f := filepath.Join(t.TempDir(), "fixture.fern")
	if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return exec.Command(langBin, "-check", f).CombinedOutput()
}

// TestSelfHostStrictIRWasm runs the corpus through the CLI's wasm32-wasi target.
func TestSelfHostStrictIRWasm(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range strictIRCorpus {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := runWasm(t, cli.emit(t, "wasm32-wasi", tc.src)); got != tc.want {
				t.Errorf("strict wasm %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostStrictIRNamesUnresolvedFunctionValue pins the OTHER half of the
// per-function bail: the body lowers fine and the refusal is a function VALUE
// that does not resolve. That is a different debugging task from a refused body
// — you look at the symbol, not the code — and the bare function name never
// distinguished them.
//
// It needs its own driver. The CLI's checker rejects an undefined name before
// anything lowers, so there every function value names a function the program
// declares or lifts. `asm_ir_run` takes a
// program-wide known-symbol set
// (`-ir-extern`), which is what makes a fn value naming a SIBLING unit's
// function — the missing import, the monomorphised clone absent from this view,
// the unregistered helper — reachable as an input.
//
// The `defined` case is the control, and it is what stops this passing for the
// wrong reason: the same program with `bfoo` present emits, so the refusal is
// caused by the missing definition rather than by the shape of the call.
func TestSelfHostStrictIRNamesUnresolvedFunctionValue(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostFiles(t, dir, "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "airun")

	const apply = "function apply(f: (i32) => i32, x: i32): i32 { return f(x); }\n" +
		"function main(): i32 { return apply(bfoo, 6); }\n"

	t.Run("missing", func(t *testing.T) {
		out, stderr, code := runDriver(t, runner, driverBin, []byte(apply), true)
		if code != 3 {
			t.Fatalf("driver exited %d with %d bytes, want a refusal (3)\n%s", code, len(out), stderr)
		}
		if !strings.Contains(stderr, "FERN_SEM_IR: main: ") {
			t.Errorf("refusal did not name main as the bailing function:\n%s", stderr)
		}
		if !strings.Contains(stderr, "unbound name is not a semantic value: bfoo") {
			t.Errorf("refusal did not name the offending function value:\n%s", stderr)
		}
	})

	t.Run("defined", func(t *testing.T) {
		src := "function bfoo(x: i32): i32 { return x * 7; }\n" + apply
		out, stderr, code := runDriver(t, runner, driverBin, []byte(src), true)
		if code != 0 || len(out) == 0 {
			t.Fatalf("the same program with bfoo defined must emit, else the refusal above "+
				"is about the call shape rather than the missing definition: exited %d with %d bytes\n%s",
				code, len(out), stderr)
		}
	})
}
