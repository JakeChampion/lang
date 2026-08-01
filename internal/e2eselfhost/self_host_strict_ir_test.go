package e2eselfhost

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// FERN_STRICT_IR (#5646) turns the self-host compiler's IR-to-AST bail into a
// hard error instead of a silent fall-through.
//
// The fallback is only SAFE when the AST emitter can express what the IR path
// declined. When it can't, the fallback emits wrong code and nothing notices
// until a differential test disagrees at a runtime exit code, far from the
// cause. #5642 is the worked example: `match (a +? b)` had no `ExprBinary` case
// in `lower_match`'s scrutinee-type recovery, so the enclosing function bailed
// to an AST emitter with no checked-operator lowering at all. That surfaced as
// 46 failing subtests whose symptoms read like several unrelated bugs — wrong
// match arm taken, payload read as zero, SIGABRT — none of which were
// checked-arithmetic bugs.
//
// These tests are the tripwire that would have caught it at the bail. Two
// halves, and both are load-bearing:
//
//   - strictIRCorpus asserts NO refusal across constructs the IR path is
//     supposed to cover. A newly-unlowerable construct fails here, naming the
//     function, instead of miscompiling.
//   - TestSelfHostStrictIRRefusesBail asserts a real bail DOES refuse, so a
//     green corpus means the tripwire is armed rather than inert.
//
// The corpus also self-certifies its own routing: a program that fell back
// would exit 3 under the flag, so "strict run succeeded" IS "lowered on the IR
// path" — no separate path-probe assertion needed.
//
// The flag is checked in asm_ir.fern, which both backends' eligibility runs
// through (wasm_ir's `wasm_eligible` calls `asm_ir.eligible_core`), so the
// x86-64 and wasm legs cover the same per-function gate.
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
    var add: (i32) => i32 = function (x: i32): i32 { return x + n; };
    var dbl: (i32) => i32 = function (x: i32): i32 { return x * 2; };
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
	// miscompiles on the IR path, so it stays on the AST fallback.
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
	// Branchless i32 min/max/clamp lowered directly on the IR path
	// (emit_i32_minmax_slots) — no runtime helper, no need/globls plumbing.
	// Until this existed a scalar `n.min(m)` / `n.max(m)` / `n.clamp(lo, hi)`
	// bailed the module to the AST emitter (#3457 slice 5). Asymmetric operands
	// catch an operand-order swap; the two clamp calls exercise the hi and lo
	// saturating edges.
	{"i32-min-max-clamp", `
function main(): i32 {
    var a: i32 = 8;
    var b: i32 = 3;
    return a.min(b) + a.max(b) + (99).clamp(0, 10) + (0 - 5).clamp(0, 10);
}
`, 21},
	// xs.first() / xs.last() lowered as the equivalent index read. Until this
	// existed either one bailed the whole module to the AST emitter (#3457
	// slice 5). The receivers cover every element kind the intercept admits, and
	// each result is CONSUMED so the call's result type has to be recovered too:
	// a string element through `.len()`, a struct element through `.n`, an
	// array-of-arrays element through a second `[i]`, and a string[][] element
	// through a chained `.first()`. A missing recovery mis-dispatches (arr_len on
	// a string box reads a different field) rather than bailing, so the exit code
	// is what catches it.
	{"arr-first-last", `
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
    var t: i32 = xs.first() + xs.last();            // 40
    t = t + ss.first().len() + ss.last().len();     // +5
    t = t + ps.first().n + ps.last().name.len();    // +5
    t = t + m.first()[1] + m.last()[0];             // +5
    t = t + mm.first()[1].len() + mm.last().first().len(); // +5
    return t + build().last() + build().first();    // +10
}
`, 70},
	// xs.index_of(t) / xs.contains(t) on a string[], backed by the
	// __fern_arr_str_index_of Fern helper (x86 + arm64) and its WAT twin
	// $__fern_arr_str_index_of (wasm). Until this existed either one bailed the
	// module to the AST emitter (#3457 slice 5).
	//
	// `find` takes both operands as PARAMS, so its body carries no string
	// literal — the shape that proves the wasm gate pulls $__fern_streq in for
	// the call itself rather than relying on some other string op being present.
	// The `"c" + "c"` argument is the reason this cannot share the i32 helper's
	// pointer compare: a freshly concatenated block must still match the
	// element's .rodata slot by CONTENT.
	{"arr-string-index-of", `
function find(xs: string[], t: string): i32 { return xs.index_of(t); }
function main(): i32 {
    var xs: string[] = ["a", "bb", "cc"];
    var i: i32 = xs.index_of("bb");        // 1
    var j: i32 = xs.index_of("zz");        // -1
    var k: i32 = find(xs, "c" + "c");      // 2
    if (xs.contains("cc") && !xs.contains("qq")) { return i + (0 - j) + k + 10; }
    return 1;
}
`, 14},
	// The ASCII classifier / case family on a byte receiver, lowered from the one
	// unsigned-range primitive `(b - lo) <=u span` plus the mask idiom for the two
	// case conversions. Until this existed every one of them bailed the module to
	// the AST emitter, where they are hand-written `setbe` / `cmovbe` sequences
	// (#3457 slice 5).
	//
	// The receivers are the point: a u8-tracked LOCAL (the commonest shape, and
	// the one an expr_recv_prim_type gate silently misses — a `var c: u8` slot
	// records "u8" as its declared struct type, so the receiver reads as a
	// struct), an `as u8` cast, and a string INDEX, which is a byte since #5629.
	// The total is kept under 126: a WASI exit status above that is rejected
	// outright, which reads exactly like a miscompile.
	{"ascii-byte-methods", `
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
	// b.to_ascii_string() — a fresh 1-char string from a byte. It desugars to
	// chr(b), which already lowers to the __fern_chr runtime helper, rather than
	// hand-emitting the two allocations the AST emitter open-codes. The CHAINED
	// receiver is the case that needed more than the desugar: to_ascii_lower /
	// _upper return a byte, and expr_subword_kind cannot see a call RESULT, so
	// `b.to_ascii_lower().to_ascii_string()` declined while each half lowered.
	{"ascii-to-string", `
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
	// and the CALLER bailed to the AST emitter while the generic function itself
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
    var s = sort_by_key(ps, function (p: P): i32 { return p.k; });
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
	// xs.reverse() / xs.concat(ys) on arrays, lowered to the same
	// __fern_arr_reverse / __fern_arr_concat runtime helpers the AST emitters
	// call (op_arr_reverse / op_arr_concat, modelled on op_arr_slice). Until this
	// existed either one bailed the module to the AST emitter (#3457 slice 5).
	//
	// Every result is CONSUMED, so the result-TYPE recovery is under test too: a
	// missing one mis-dispatches `.len()` on a string[] rather than bailing. The
	// empty-array pair pins len 0 rather than a trap, and `reverse().reverse()`
	// pins the chained case, where the receiver of the outer call is itself a
	// builtin result.
	{"arr-reverse-concat", `
function main(): i32 {
    var xs: i32[] = [1, 2, 3];
    var r: i32[] = xs.reverse();
    var ys: i32[] = [4, 5];
    var c: i32[] = xs.concat(ys);
    var ss: string[] = ["ab", "c"];
    var sr: string[] = ss.reverse();
    var sc: string[] = ss.concat(["de"]);
    var e: i32[] = [];
    var t: i32 = r[0] * 10 + r.len() + c.len() + c[4];        // 30 + 3 + 5 + 5
    t = t + sr.first().len() + sc.len() + sc.last().len();     // + 1 + 3 + 2
    return t + e.reverse().len() + e.concat(e).len() + xs.reverse().reverse()[0]; // + 0 + 0 + 1
}
`, 50},
	// The UNANNOTATED binding plus `for … in`. Two separate mechanisms have to
	// agree: the expression classifiers (which type `xs.reverse()` itself) and
	// lower_stmt_var's is_arr list (which types the SLOT). Only the second is
	// what `for v in r` consults — it requires is_arr_slot and bails outright
	// otherwise — so with the classifiers alone this shape still routed AST while
	// `r.len()` worked, which is how it survived. The annotated form goes through
	// is_array_type_name(v.type_name) instead and was never affected.
	{"arr-reverse-concat-unannotated-foreach", `
function main(): i32 {
    var xs: string[] = ["a", "b", "c"];
    var ys = xs.reverse();
    var t: i32 = 0;
    for s in ys { t = t + s.len(); }
    var ns = xs.concat(["dd"]);
    for s in ns { t = t + s.len(); }
    return t;
}
`, 8},
	// reverse() copies, so the source stays independent: appending to `a`
	// afterwards must not be visible through `r`. A helper that returned the
	// receiver instead of a fresh array passes every length assertion above and
	// fails this one.
	{"arr-reverse-is-a-copy", `
function main(): i32 {
    var a: i32[] = [1, 2];
    var r: i32[] = a.reverse();
    a = a.append(9);
    return r[0] * 10 + r.len() + a.len();
}
`, 25},
	// A DIRECT, hand-written IIFE — `(function (): i32 { return 7; })()`.
	// lower_iife handled only the if/match-EXPRESSION desugars (a StmtIf or
	// StmtMatch body); a single-`return` body fell through its catch-all and
	// bailed the module to the AST emitter (#3457 slice 5). Only the shapes the
	// lift leaves inline ever reached it, which is why the gap was narrow: a
	// bound `var a = (…)()` hoists to __lam_N and lowers, while `return (…)();`
	// does not — hence `ret()` here, the originally-reported form.
	//
	// The result types are the point of the rest: string, struct and a nested
	// IIFE all flow through the inlined value, and the loop body's capture (`i`)
	// must read the enclosing local rather than a copy — inlining is only correct
	// because the lambda is invoked immediately in this scope.
	{"direct-iife", `
struct P { n: i32 }
function g(n: i32): i32 { return n * 2; }
function ret(): i32 { return (function (): i32 { return 7; })(); }
function main(): i32 {
    var t: i32 = ret();                                              // 7
    var n: i32 = 5;
    t = t + (function (): i32 { return n + 2; })();                   // +7
    t = t + (function (): string { return "ab" + "cd"; })().len();    // +4
    t = t + (function (): P { return P { n: 6 }; })().n;              // +6
    t = t + (function (): i32 { return (function (): i32 { return 3; })() + 4; })(); // +7
    var i: i32 = 0;
    while (i < 3) { t = t + (function (): i32 { return g(i); })(); i = i + 1; }      // +6
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
	// note here claimed the IR path and the interpreter "disagree" on Cell[i64],
	// which was wrong. `var c: Cell[i64] = cell_new(5000000000)` is REJECTED by
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
	// `.to_string()` on an INLINE wide cast — `(n as i64).to_string()`. The wide
	// to_string intercept lowered its receiver with lower_expr, which has no
	// `as_i64` / `as_u64` arm (the same hole the i64[]-literal path documents),
	// so the whole module bailed; the bound form `var v: i64 = n as i64;
	// v.to_string()` lowered, which is the tell. Both forms are pinned, and both
	// widths: u64 renders 2^64-1 as the full decimal only if it keeps the
	// UNSIGNED formatter, so a receiver-lowering change that lost the width would
	// show up as 20 vs 2 rather than as a bail. Real consumers:
	// examples/tests/{i64,u64}_test.fern's test_to_string_wide.
	{"wide-cast-to-string", `
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

// overBudgetProgram is a module past the 512-function merged-bundle budget
// (#3425) — the one bail site that is deterministically reachable from a valid
// program, and so the only way to prove the tripwire fires.
func overBudgetProgram() []byte {
	var b strings.Builder
	for i := 0; i < 513; i++ {
		fmt.Fprintf(&b, "function zf%d(): i32 { return %d; }\n", i, i%7)
	}
	b.WriteString("function main(): i32 { return zf0() + zf1(); }\n")
	return []byte(b.String())
}

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

// TestSelfHostStrictIRX86_64 asserts the corpus lowers with no bail under
// FERN_STRICT_IR, that the flag is otherwise inert (byte-identical asm), and
// that each program still runs to its expected exit code.
func TestSelfHostStrictIRX86_64(t *testing.T) {
	gcc, runner, driverBin := strictIRDriver(t)
	dir := filepath.Dir(driverBin)

	for _, tc := range strictIRCorpus {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src)
			off, _, offCode := runDriver(t, runner, driverBin, src, false)
			if offCode != 0 || len(off) == 0 {
				t.Fatalf("driver (unset) exited %d with %d bytes", offCode, len(off))
			}
			on, stderr, onCode := runDriver(t, runner, driverBin, src, true)
			if strings.Contains(stderr, "FERN_STRICT_IR:") {
				t.Fatalf("%s bailed to the AST emitter under FERN_STRICT_IR:\n%s", tc.name, stderr)
			}
			if onCode != 0 {
				t.Fatalf("driver (FERN_STRICT_IR=1) exited %d\n%s", onCode, stderr)
			}
			if !bytes.Equal(off, on) {
				t.Fatalf("%s: FERN_STRICT_IR changed the emitted asm (%d vs %d bytes); the flag must only affect the bail path", tc.name, len(off), len(on))
			}
			progBin := buildBin(t, gcc, dir, "strict_"+tc.name, string(on))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostStrictIRRefusesBail is the teeth: a program that genuinely bails
// must be REFUSED, and the flag must name the bail site. Without this, a green
// corpus is consistent with the flag doing nothing at all.
//
// The unset-flag leg no longer asserts a silent AST fallback, because there is
// none: asm_run.fern routes through asm_ir.emit_module_or_error (#3457 slice 5),
// so an ineligible module is an error with or without the flag. What the flag
// still changes — and what this pins — is WHICH error: without it the driver says
// only that the module is ineligible, with it the gate exits 3 naming the bail
// site. That difference is the whole diagnostic value of the flag now.
func TestSelfHostStrictIRRefusesBail(t *testing.T) {
	_, runner, driverBin := strictIRDriver(t)
	src := overBudgetProgram()

	off, offErr, offCode := runDriver(t, runner, driverBin, src, false)
	if offCode == 0 || len(off) != 0 {
		t.Fatalf("unset: driver exited %d with %d bytes, want a refusal (the AST emitter is unreachable)", offCode, len(off))
	}
	if !strings.Contains(offErr, "not IR-eligible") {
		t.Errorf("unset: refusal did not say the module is ineligible:\n%s", offErr)
	}
	on, stderr, onCode := runDriver(t, runner, driverBin, src, true)
	if onCode != 3 {
		t.Fatalf("FERN_STRICT_IR=1: driver exited %d with %d bytes, want a refusal (3)\n%s", onCode, len(on), stderr)
	}
	if !strings.Contains(stderr, "FERN_STRICT_IR:") || !strings.Contains(stderr, "512-function") {
		t.Errorf("refusal did not name the bail:\n%s", stderr)
	}
}

// TestSelfHostStrictIRWasm runs the corpus through the wasm IR driver. The
// eligibility gate is shared (wasm_eligible calls asm_ir.eligible_core), so the
// same per-function bail is covered on both backends.
func TestSelfHostStrictIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host strict-IR wasm e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range strictIRCorpus {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src)
			wat, stderr, code := runDriver(t, runner, driverBin, src, true, "-ir")
			if strings.Contains(stderr, "FERN_STRICT_IR:") {
				t.Fatalf("%s bailed to the AST emitter under FERN_STRICT_IR:\n%s", tc.name, stderr)
			}
			if code != 0 || len(wat) == 0 {
				t.Fatalf("driver (FERN_STRICT_IR=1) exited %d with %d bytes\n%s", code, len(wat), stderr)
			}
			watFile := filepath.Join(dir, "strict_ir_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got := run.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("strict-IR wasm %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}
