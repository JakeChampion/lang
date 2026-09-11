package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The checked-source producer (semsource.fern) is the typed frontend import of
// the self-hosted pre-RC pipeline: checked syntax in, verified semantic values
// out. The print driver pins the produced graphs, types and parameter modes;
// the executable driver runs produced functions through the unit planner and
// physical RC lowering inside an otherwise AST-lowered program on every target.

const semsourcePrintFixture = `
function alias(xs: i32[][], own ys: i32[]): i32[] {
    var a: i32[] = xs[0];
    var b: i32[] = a;
    if (ys[0] > 0) { b = ys; }
    return b;
}
function shadow(n: i32): i32 {
    var n: i32 = n + 1;
    { var n: i32 = n * 2; }
    return n;
}
function loop_phi(limit: i32): i32 {
    var total: i32 = 0;
    var i: i32 = 0;
    while (i < limit) {
        i = i + 1;
        if (i == 3) { continue; }
        if (total > 10) { break; }
        total = total + i;
    }
    return total;
}
function short_circuit(a: boolean, b: i32): boolean {
    return a && b > 0 || !a;
}
function nested(rows: i32[][]): (i32, i32[]) {
    var t: (i32, i32[]) = (rows[0][1], rows[1]);
    var e: i32[] = [];
    if (t.0 == 0) { return (0, e); }
    return t;
}
function view(s: string): string { return s; }
function refused_call(n: i32): i32 { return abs(n); }
function refused_literal(): f64 { return 1.5; }
function refused_width(n: i64): i64 { return n + 1; }
function refused_fallthrough(n: i32): i32 { if (n > 0) { return 1; } }
function refused_destructure(): i32 { var (a, b) = (1, 2); return a + b; }
function halve(n: i32): i32 { return n / 2; }
function refused_global(): i32 { return loop_phi(2); }
function callee(xs: i32[], own ys: i32[]): i32[] { return ys; }
function caller(n: i32): i32[] {
    var a: i32[] = [n];
    var b: i32[] = callee(a, [n, n]);
    callee(b, a);
    return callee(b, b);
}
function noop() { return; }
function refused_void_call(): i32 { noop(); return 1; }
function array_length(xs: i32[]): i32 { return xs.len(); }
// Produced, then refused by the PLAN: a borrowed receiver's unit is not this
// function's to hand over, so the golden records the move gate rather than a
// call-site refusal.
function borrowed_append(xs: i32[]): i32[] { return xs.append(1); }
// The produced for-loop's shape, pinned: the index is a const -1 carried by the
// header phi, the advance is the FIRST thing in the header, and the test comes
// after it. An advance moved below the body would still pass every value test
// and hang on the first continue.
function loop_sum(xs: i32[]): i32 {
    var t: i32 = 0;
    for x in xs { t = t + x; }
    return t;
}
@noinline function owned_append(own xs: i32[]): i32[] { return xs.append(1); }
function refused_transitive(n: i32): i32 { return refused_call(n); }
struct P { n: i32, xs: i32[] }
struct Q { name: string, p: P }
struct G[T] { v: T }
function make(n: i32): P { return P { n: n, xs: [n, n + 1] }; }
function wrap(own p: P, tag: string): Q { return Q { name: tag + "!", p: p }; }
function unwrap(q: Q): i32 {
    var p: P = q.p;
    if (q.name == "x!") { return p.xs[0]; }
    return p.n;
}
function greet(s: string): string { return "hello, " + s; }
function zero_n(p: P): P { return P { ...p, n: 0 }; }
function refused_generic_record(n: i32): i32 { var g: G[i32] = G { v: n }; return g.v; }
function string_length(s: string): i32 { return s.len(); }
function refused_string_method(s: string): string { return s.trim(); }
enum Shape { Dot, Line(i32), Full(i32[]), Pair(i32, i32[]) }
function shape(n: i32): Shape {
    if (n == 0) { return Dot; }
    if (n == 1) { return Line(n); }
    if (n == 2) { return Full([n, n + 1]); }
    return Pair(n, [n]);
}
function measure(s: Shape): i32 {
    match (s) {
        Dot => { return 0; },
        Line(n) => { return n; },
        Pair(_, ys) => { return ys[0]; },
        _ => {}
    }
    var total: i32 = 1;
    if let Full(xs) = s { total = total + xs[0]; }
    return total;
}
function refused_guard(s: Shape): i32 {
    match (s) {
        Line(n) when n > 0 => { return n; },
        _ => { return 0; }
    }
}
function refused_qualified(s: Shape): i32 {
    match (s) {
        Shape.Dot => { return 1; },
        _ => { return 0; }
    }
}
struct Leaf { n: i32 }
struct Twig { xs: i32[] }
type Node = Leaf | Twig;
struct Nest { kid: Tree, n: i32 }
struct Tip { n: i32 }
type Tree = Nest | Tip;
function mk_node(n: i32): Node {
    if (n == 0) { return Leaf { n: 7 }; }
    return Twig { xs: [n, n + 1] };
}
function node_size(nd: Node): i32 {
    match (nd) {
        Leaf(l) => { return l.n; },
        Twig(t) => { return t.xs[1]; }
    }
    return 0 - 1;
}
function recursive_union(t: Tree): i32 {
    match (t) {
        Tip(p) => { return p.n; },
        Nest(nst) => { return nst.n; }
    }
    return 0;
}
function scalar_match(n: i32): i32 {
    match (n) {
        1 => { return 1; },
        _ => { return 0; }
    }
}
`

const semsourcePrintDriver = `import "./semsource"; import "./ssa"; import "./ssaunits"; import "./typeinfo";
import "./parser"; import "./lexer"; import "./util";
function main(): i32 {
    var src: string = "";
    match (read_file(args()[1])) { Ok(text) => { src = text; }, Err(_) => { return 2; } }
    var mod = parser.parse_module(lexer.tokenize(src));
    for p in semsource.build_module(mod) {
        if (!p.ok) { print("refused " + p.why); continue; }
        var out: string = "";
        var i: i32 = 0;
        while (i < p.func.values.len()) {
            if (i > 0) { out = out + " "; }
            out = out + "v" + util.i32_to_string(i) + ":" + typeinfo.spelling(p.func.values[i]);
            i = i + 1;
        }
        var modes: string = "";
        for m in p.modes { modes = modes + " " + util.i32_to_string(m); }
        print("modes" + modes + " result " + typeinfo.spelling(p.func.result));
        print(out);
        var plan = ssaunits.plan(p.func, p.modes);
        if (!plan.ok) { print("plan " + plan.why); }
        print(ssa.print_func(p.func.graph));
    }
    return 0;
}
`

func TestSelfHostSemanticSourcePrint(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	if err := os.WriteFile(filepath.Join(dir, "semsource_print.fern"), []byte(semsourcePrintDriver), 0o644); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(dir, "fixture.fern")
	if err := os.WriteFile(fixture, []byte(semsourcePrintFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := buildSelfHostBin(t, gcc, dir, "semsource_print.fern", "semsource-print")
	got, err := runX86_64Bin(runner, driver, fixture).CombinedOutput()
	if err != nil {
		t.Fatalf("print driver: %v\n%s", err, got)
	}
	want, err := os.ReadFile("testdata/semsource_print.golden")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("produced graphs differ from testdata/semsource_print.golden:\n%s", got)
	}
}

const semsourceRCProgram = `
@noinline function pick(k: i32): i32[] {
    var rows: i32[][] = [[1, 2], [3, 4], [5, 6]];
    var chosen: i32[] = rows[0];
    var i: i32 = 1;
    while (i <= k) {
        if (i == 2) { chosen = rows[i]; break; }
        chosen = rows[1];
        i = i + 1;
    }
    return chosen;
}
@noinline function pair(n: i32, flag: boolean): i32[] {
    var xs: i32[] = [n, 0];
    var count: i32 = 0;
    var t: (i32, i32[]) = (n, xs);
    if (flag && n > 2 || n == 0) {
        var n: i32 = -n;
        t = (n * 2, [n, n + 1]);
        count = t.1[0];
    } else {
        count = t.0 + 1;
    }
    var swapped: (i32, i32[]) = (count, t.1);
    return [swapped.0, swapped.1[1], swapped.1[0]];
}
@noinline function boxed(n: i32): (i32, i32[]) { return (n, [n, n + 1]); }
@noinline function boxed_local(n: i32): (i32, i32[]) {
    var xs: i32[] = [n];
    var t: (i32, i32[]) = (n, xs);
    var c: i32 = t.1[0];
    if (c < 0) { var e: i32[] = []; return (0, e); }
    return t;
}
@noinline function boxed_carry(n: i32): (i32[], boolean) {
    var xs: i32[] = [n, n * 2];
    return (xs, n > 0);
}
@noinline function carry(limit: i32): i32[] {
    var last: i32[] = [7];
    var i: i32 = 0;
    while (i < limit) {
        last = [i];
        if (i == 1) { break; }
        i = i + 1;
    }
    return last;
}
@noinline function count_even(limit: i32): i32 {
    var total: i32 = 0;
    var i: i32 = 0;
    var odd: boolean = false;
    while (i < limit) {
        i = i + 1;
        odd = !odd;
        if (odd) { continue; }
        var j: i32 = 0;
        while (true) {
            if (j >= 2) { break; }
            total = total + i;
            j = j + 1;
        }
    }
    return total;
}
@noinline function fill(n: i32): i32[] {
    var out: i32[] = [n, n + 1, n + 2];
    return out;
}
@noinline function first_of(xs: i32[]): i32 { return xs[0]; }
@noinline function keep(own xs: i32[], k: i32): i32[] {
    if (k > 0) { return xs; }
    return [k];
}
@noinline function chain(n: i32): i32[] {
    var a: i32[] = fill(n);
    var b: i32[] = keep(a, n);
    var c: i32[] = keep(fill(n + 1), 0);
    return [first_of(b) + first_of(c), b[0]];
}
@noinline function twice(n: i32): i32 {
    var a: i32[] = fill(n);
    var x: i32 = first_of(a) + first_of(keep(a, 1)) + first_of(a);
    fill(x);
    return x;
}
@noinline function grow(limit: i32): i32[] {
    var cur: i32[] = [0];
    var i: i32 = 0;
    while (i < limit) {
        cur = keep(fill(i), i);
        i = i + 1;
    }
    return cur;
}
@noinline function count_down(n: i32): i32 {
    if (n <= 0) { return 0; }
    return 1 + count_down(n - 1);
}
struct P { n: i32, xs: i32[] }
struct Q { name: string, p: P }
@noinline function make(n: i32): P { return P { n: n, xs: [n, n + 1] }; }
@noinline function wrap(own p: P, tag: string): Q { return Q { name: tag + "!", p: p }; }
struct S2 { a: i32, b: i32 }
struct W { s: S2 }
struct Counter { n: i32, m: i32 }
@noinline function (c: Counter) total(): i32 { return c.n + c.m; }
@noinline function make_counter(n: i32): Counter { return Counter { n: n, m: n + 1 }; }
// The method CALL is what this exercises: twice_total is itself lowered
// through the boundary, so the call is produced rather than handed to the AST.
@noinline function twice_total(n: i32): i32 {
    var c: Counter = make_counter(n);
    return c.total() + c.total();
}
@noinline function mk_s2(n: i32): S2 { return S2 { a: n, b: n + 1 }; }
@noinline function proj(q: W): S2 { return q.s; }
@noinline function unwrap(q: Q): i32 {
    var p: P = q.p;
    if (q.name == "x!") { return p.xs[0]; }
    return p.n + p.xs[1];
}
@noinline function tally(n: i32): i32 {
    var q: Q = wrap(make(n), "x");
    var r: Q = wrap(make(n + 1), "y");
    var keep: Q = q;
    if (n > 3) { keep = r; }
    return unwrap(q) + unwrap(keep) + unwrap(r);
}
enum Shape { Dot, Line(i32), Full(i32[]), Pair(i32, i32[]) }
struct Holder { s: Shape, n: i32 }
struct Leaf { n: i32 }
struct Twig { xs: i32[] }
type Node = Leaf | Twig;
@noinline function mk_node(n: i32): Node {
    if (n == 0) { return Leaf { n: 7 }; }
    return Twig { xs: [n, n + 1] };
}
@noinline function node_size(nd: Node): i32 {
    match (nd) {
        Leaf(l) => { return l.n; },
        Twig(t) => { return t.xs[1]; }
    }
    return 0 - 1;
}
struct Tip { v: i32 }
struct Fork { l: Tree, r: Tree }
type Tree = Tip | Fork;
enum Chain { End, Link(i32, Chain) }
@noinline function leaf(n: i32): Tree { return Tip { v: n }; }
@noinline function fork(a: Tree, b: Tree): Tree { return Fork { l: a, r: b }; }
@noinline function tree_sum(t: Tree): i32 {
    match (t) {
        Tip(x) => { return x.v; },
        Fork(y) => { return tree_sum(y.l) + tree_sum(y.r); }
    }
    return 0;
}
@noinline function build_sum(n: i32): i32 {
    var a: Tree = leaf(n);
    var b: Tree = leaf(n + 1);
    var t: Tree = fork(a, b);
    var c: Tree = leaf(n + 2);
    var u: Tree = fork(t, c);
    return tree_sum(u) + tree_sum(t);
}
@noinline function chain_len(c: Chain): i32 {
    match (c) { End => { return 0; }, Link(v, rest) => { return v + chain_len(rest); } }
    return 0;
}
@noinline function chain_build(n: i32): i32 {
    var c: Chain = End;
    var d: Chain = Link(n, c);
    var e: Chain = Link(n + 1, d);
    return chain_len(e) + chain_len(d);
}
@noinline function node_sum(limit: i32): i32 {
    var total: i32 = 0;
    var i: i32 = 0;
    var kept: Node = Leaf { n: 0 };
    while (i < limit) {
        var nd: Node = mk_node(i);
        total = total + node_size(nd);
        if (i == 1) { kept = nd; }
        i = i + 1;
    }
    return total + node_size(kept);
}
@noinline function shape(n: i32): Shape {
    if (n == 0) { return Dot; }
    if (n == 1) { return Line(n); }
    if (n == 2) { return Full([n, n + 1]); }
    return Pair(n, [n]);
}
@noinline function measure(s: Shape): i32 {
    match (s) {
        Dot => { return 0; },
        Line(n) => { return n; },
        Full(xs) => { return xs[1]; },
        Pair(a, ys) => { return a + ys[0]; }
    }
    return 0 - 1;
}
@noinline function sum_shapes(limit: i32): i32 {
    var total: i32 = 0;
    var i: i32 = 0;
    var last: Shape = Dot;
    while (i < limit) {
        var s: Shape = shape(i);
        match (s) {
            Line(n) => { total = total + n * 10; },
            _ => { total = total + measure(s); }
        }
        if (i == 2) { last = s; }
        i = i + 1;
    }
    return total + measure(last);
}
@noinline function consume(own s: Shape): i32 {
    if let Full(xs) = s { return xs[0]; }
    return 7;
}
@noinline function boxed_shape(n: i32): i32 { return consume(shape(n)) + consume(Full([n])); }
@noinline function hold(n: i32): i32 {
    var h: Holder = Holder { s: shape(n), n: n };
    return measure(h.s) + h.n;
}
// .len() on a BORROWED receiver reads without consuming; on a counted one the
// read is the last use, so the unit is still this function's to release.
@noinline function size_of(xs: i32[]): i32 { return xs.len(); }
@noinline function eat_size(own xs: i32[]): i32 { return xs.len(); }
@noinline function fresh_size(n: i32): i32 { return fill(n).len() + [n, n, n, n].len(); }
@noinline function text_size(s: string): i32 { return s.len(); }
@noinline function inner_size(p: P): i32 { return p.xs.len(); }
@noinline function sum_all(xs: i32[]): i32 {
    var total: i32 = 0;
    var i: i32 = 0;
    while (i < xs.len()) { total = total + xs[i]; i = i + 1; }
    return total;
}
// .append hands the receiver's unit to the runtime and takes one back: the
// same box when the push fits, a fresh one when it grew. grow_to crosses the
// capacity doublings repeatedly, so the reclaim-on-grow path runs.
@noinline function grow_to(n: i32): i32[] {
    var xs: i32[] = [];
    var i: i32 = 0;
    while (i < n) { xs = xs.append(i); i = i + 1; }
    return xs;
}
@noinline function push_temp(n: i32): i32 { return fill(n).append(99).len(); }
// A reference element: the array takes the value's unit, string or array.
@noinline function words(n: i32): string[] {
    var out: string[] = [];
    var i: i32 = 0;
    while (i < n) { out = out.append("w" + "x"); i = i + 1; }
    return out;
}
@noinline function word_bytes(n: i32): i32 {
    var ws: string[] = words(n);
    var total: i32 = 0;
    var i: i32 = 0;
    while (i < ws.len()) { total = total + ws[i].len(); i = i + 1; }
    return total;
}
@noinline function rows(n: i32): i32[][] {
    var out: i32[][] = [];
    var i: i32 = 0;
    while (i < n) { out = out.append(fill(i)); i = i + 1; }
    return out;
}
@noinline function row_total(n: i32): i32 {
    var rs: i32[][] = rows(n);
    var total: i32 = 0;
    var i: i32 = 0;
    while (i < rs.len()) { total = total + rs[i][0]; i = i + 1; }
    return total;
}
// A produced for over an array. The index advances at the TOP of the loop,
// so skip_two's continue — which branches to the header — re-runs the advance
// instead of skipping it and spinning forever.
@noinline function sum_for(xs: i32[]): i32 {
    var t: i32 = 0;
    for x in xs { t = t + x; }
    return t;
}
@noinline function skip_two(xs: i32[]): i32 {
    var t: i32 = 0;
    for x in xs { if (x == 2) { continue; } t = t + x; }
    return t;
}
@noinline function until_two_for(xs: i32[]): i32 {
    var t: i32 = 0;
    for x in xs { if (x == 2) { break; } t = t + x; }
    return t;
}
@noinline function first_gt(xs: i32[], k: i32): i32 {
    for x in xs { if (x > k) { return x; } }
    return 0 - 1;
}
// The loop binding shadows an outer name, which the exit must restore.
@noinline function shadow_for(xs: i32[]): i32 {
    var x: i32 = 100;
    var t: i32 = 0;
    for x in xs { t = t + x; }
    return t + x;
}
// A counted temporary iterable: produced once, released after the exit.
@noinline function temp_for(n: i32): i32 {
    var t: i32 = 0;
    for x in fill(n) { t = t + x; }
    return t;
}
// Nested loops, with continue and break in the inner one, over a reference
// element borrowed from the outer container.
@noinline function nested_for(n: i32): i32 {
    var t: i32 = 0;
    for r in rows(n) {
        for v in r { if (v == 1) { continue; } if (v == 4) { break; } t = t + v; }
    }
    return t;
}
// A reference element retained into an owned array, consumed inside the
// boundary: an AST caller holding a string[] result would hit the documented
// caller_sigs leak floor, which has nothing to do with this loop.
@noinline function copy_words(n: i32): i32 {
    var out: string[] = [];
    for w in words(n) { out = out.append(w); }
    return out.len() + out[0].len();
}
// slice_unchecked hands back a box of this function's own over the SOURCE's
// bytes on the register backends, and a copy on wasm. One release symbol covers
// both, and the source must outlive the view either way — head slices a
// borrowed parameter, mid keeps a counted local live across the read, and
// temp_slice's source is a temporary whose only use is the slice.
@noinline function head_of(s: string, n: i32): i32 { return slice_unchecked(s, 0, n).len(); }
@noinline function mid_of(n: i32): i32 {
    var s: string = grown(n);
    var v: string = slice_unchecked(s, 1, 4);
    return v.len() + s.len();
}
@noinline function temp_slice(n: i32): i32 { return slice_unchecked(grown(n), 0, 3).len(); }
@noinline function scan_slices(s: string): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < s.len()) { t = t + slice_unchecked(s, i, i + 1).len(); i = i + 1; }
    return t;
}
@noinline function grown(n: i32): string {
    var s: string = "abcdef";
    var i: i32 = 0;
    while (i < n) { s = s + "gh"; i = i + 1; }
    return s;
}
@noinline function grown_size(n: i32): i32 {
    var s: string = "ab";
    var i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    return s.len();
}
@noinline function greet(n: i32): i32 {
    var s: string = "ab";
    var i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    if (s == "abcc") { return 1; }
    if (s != "ab") { return 2; }
    return 0;
}
// A functional update reads the fields it does not replace off the base. Each
// read is a projection anchored to the base, and the construction takes its own
// unit of every reference field, so a copied-through field is retained and the
// base is released after the box is built, not before.
@noinline function bump(p: P): i32 {
    var q: P = P { ...p, n: p.n + 1 };
    return q.n + q.xs.len() + p.xs.len();
}
@noinline function pure_copy(p: P): i32 {
    var q: P = P { ...p };
    return q.n + q.xs.len();
}
// Overrides written out of declaration order, which the checker permits and
// this boundary has to place by name rather than by position.
@noinline function reorder(p: P): i32 {
    var q: P = P { ...p, xs: [9, 9, 9], n: 4 };
    return q.n + q.xs.len();
}
// A base that is a temporary: owned and dead at the construction, so its unit
// is moved and the copied field still needs one of its own.
@noinline function from_temp(n: i32): i32 {
    var q: P = P { ...make(n), n: n + 1 };
    return q.n + q.xs.len();
}
// An owned base carrying a record field, replaced by a string the caller owns.
@noinline function retag(own q: Q, tag: string): i32 {
    var r: Q = Q { ...q, name: tag };
    return r.name.len() + r.p.xs.len();
}
// The base is itself a projection, so the update reads fields off a borrow.
@noinline function nested_up(w: W): i32 {
    var v: W = W { s: S2 { ...w.s, a: 7 } };
    return v.s.a + v.s.b + w.s.a;
}
// One byte of a string. The receiver is read, not consumed, and the result is
// an i32 that owns nothing — so a byte outlives the string it came from, which
// a projection never could.
@noinline function byte_at(s: string, i: i32): i32 { return s[i]; }
@noinline function first_last(s: string): i32 { return s[0] + s[s.len() - 1]; }
@noinline function temp_byte(n: i32): i32 { return grown(n)[1]; }
@noinline function outlives(n: i32): i32 {
    var b: i32 = 0;
    { var s: string = grown(n); b = s[0]; }
    return b;
}
@noinline function checksum(s: string): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < s.len()) { t = t + s[i]; i = i + 1; }
    return t;
}
// The integer operators, including the edges Fern pins rather than traps
// (docs/INTEGER-SEMANTICS.md). A borrowed container feeds the last one, so the
// operands are projections whose anchor has to outlive the operation.
@noinline function div_of(a: i32, b: i32): i32 { return a / b; }
@noinline function rem_of(a: i32, b: i32): i32 { return a % b; }
@noinline function bit_ops(a: i32, b: i32): i32 { return (a & b) + (a | b) + (a ^ b); }
@noinline function shifts(a: i32, n: i32): i32 { return (a << n) + (a >> n); }
@noinline function int_min(): i32 { return 0 - 2147483647 - 1; }
@noinline function ratio_of(xs: i32[]): i32 { return xs[0] / xs[1] + xs[0] % xs[1]; }
// Schema fields the drop walk never reads: each box is built by the AST-lowered
// main and released by a produced function, so the reference field past the wide
// one has to land on the same offset under both layouts.
struct Boxed { d: f64, s: string }
struct Deep { a: i64, xs: i32[], d: f64, s: string }
struct Paired { pt: (i32, f64), s: string }
struct Longs { ns: i64[], s: string }
enum Span { Empty, Wide(f64, string) }
@noinline function boxed_len(own b: Boxed): i32 { return b.s.len(); }
@noinline function deep_len(own d: Deep): i32 { return d.s.len() + d.xs.len(); }
@noinline function paired_len(own p: Paired): i32 { return p.s.len(); }
@noinline function longs_len(own l: Longs): i32 { return l.s.len(); }
@noinline function span_len(own sp: Span): i32 {
    match (sp) {
        Wide(_, s) => { return s.len(); },
        Empty => { return 0; }
    }
    return 0 - 1;
}
function main(): i32 {
    var a: i32[] = pick(0);
    var b: i32[] = pick(1);
    var c: i32[] = pick(5);
    print_int(a[0]); print(""); print_int(b[1]); print(""); print_int(c[0]); print("");
    var p: i32[] = pair(3, true);
    var q: i32[] = pair(1, false);
    var r: i32[] = pair(0, false);
    print_int(p[0]); print(""); print_int(p[1]); print(""); print_int(q[0]); print("");
    print_int(q[1]); print(""); print_int(r[1]); print("");
    var m: (i32, i32[]) = boxed(4);
    print_int(m.1[1]); print("");
    var ml: (i32, i32[]) = boxed_local(5);
    var mc: (i32[], boolean) = boxed_carry(6);
    print_int(ml.1[0]); print(""); print_int(mc.0[1]); print("");
    boxed_local(1);
    boxed_carry(2);
    print_int(carry(2)[0]); print("");
    var d: i32[] = carry(5);
    var e: i32[] = carry(0);
    print_int(d[0] + e[0]); print("");
    print_int(count_even(5)); print(""); print_int(count_even(0)); print("");
    var r: i32[] = chain(3);
    print_int(r[0]); print(""); print_int(r[1]); print("");
    print_int(twice(2)); print(""); print_int(count_down(4)); print("");
    var g: i32[] = grow(3);
    var h: i32[] = grow(0);
    print_int(g[0]); print(""); print_int(h[0]); print("");
    print_int(tally(1)); print(""); print_int(tally(5)); print("");
    print_int(greet(2)); print(""); print_int(greet(0)); print(""); print_int(greet(1)); print("");
    print_int(sum_shapes(4)); print(""); print_int(boxed_shape(3)); print(""); print_int(boxed_shape(0)); print("");
    print_int(hold(1)); print(""); print_int(hold(2)); print("");
    print_int(node_sum(3)); print(""); print_int(node_sum(1)); print("");
    print_int(build_sum(1)); print(""); print_int(build_sum(0)); print("");
    print_int(chain_build(2)); print(""); print_int(chain_build(0)); print("");
    var s2: S2 = mk_s2(4);
    print_int(s2.a); print("");
    // A box-only result that is SHARED: proj hands back a retain over w's own
    // field box, so the row's release must be the box dec alone and the reuse
    // demand below must fork rather than write through to w.
    var w: W = W { s: S2 { a: 1, b: 2 } };
    var d: S2 = proj(w);
    var c: S2 = S2 { ...d, a: 5 };
    print_int(w.s.a + c.a + d.b); print("");
    // A METHOD lowered through this boundary: its receiver is parameter 0 and
    // it borrows, so the box main owns is still main's to release.
    var ct: Counter = make_counter(6);
    print_int(ct.total()); print("");
    print_int(twice_total(3)); print("");
    // .len() over every ownership the receiver can have: a borrow, a counted
    // local, a temporary whose only use is the read, a literal, a field
    // projection, a loop-carried read, and a string built by concatenation.
    var lens: i32[] = fill(2);
    print_int(size_of(lens)); print(""); print_int(sum_all(lens)); print("");
    print_int(fresh_size(1)); print(""); print_int(eat_size(fill(5))); print("");
    print_int(text_size("hello")); print(""); print_int(grown_size(3)); print("");
    var lp: P = P { n: 1, xs: [1, 2] };
    print_int(grown_size(0)); print(""); print_int(inner_size(lp)); print("");
    var g: i32[] = grow_to(9);
    print_int(g.len()); print(""); print_int(sum_all(g)); print("");
    print_int(sum_all(grow_to(0))); print(""); print_int(push_temp(1)); print("");
    print_int(row_total(4)); print(""); print_int(word_bytes(3)); print("");
    print_int(sum_for(lens)); print(""); print_int(skip_two(lens)); print("");
    print_int(until_two_for(lens)); print(""); print_int(first_gt(lens, 2)); print("");
    print_int(shadow_for(lens)); print(""); print_int(temp_for(2)); print("");
    print_int(nested_for(3)); print(""); print_int(copy_words(2)); print("");
    print_int(sum_for([])); print("");
    print_int(head_of("abcdef", 3)); print(""); print_int(mid_of(0)); print("");
    print_int(temp_slice(0)); print(""); print_int(scan_slices("abcd")); print("");
    print_int(boxed_len(Boxed { d: 1.5, s: "abcd" })); print("");
    print_int(deep_len(Deep { a: 7, xs: [1, 2, 3], d: 2.5, s: "xy" })); print("");
    print_int(paired_len(Paired { pt: (9, 0.5), s: "abc" })); print("");
    print_int(longs_len(Longs { ns: [1, 2], s: "hello" })); print("");
    print_int(span_len(Wide(1.5, "abcde"))); print(""); print_int(span_len(Empty)); print("");
    print_int(div_of(7, 2)); print(""); print_int(rem_of(7, 2)); print("");
    print_int(div_of(10, 0)); print(""); print_int(rem_of(10, 0)); print("");
    print_int(div_of(int_min(), 0 - 1)); print(""); print_int(rem_of(int_min(), 0 - 1)); print("");
    print_int(bit_ops(12, 10)); print(""); print_int(shifts(1, 3)); print("");
    print_int(shifts(0 - 8, 33)); print(""); print_int(ratio_of(lens)); print("");
    var up: P = P { n: 1, xs: [1, 2] };
    print_int(bump(up)); print(""); print_int(pure_copy(up)); print("");
    print_int(reorder(up)); print(""); print_int(from_temp(3)); print("");
    print_int(retag(wrap(make(1), "x"), "zz")); print("");
    var uw: W = W { s: S2 { a: 1, b: 2 } };
    print_int(nested_up(uw)); print("");
    print_int(byte_at("abc", 1)); print(""); print_int(first_last("abc")); print("");
    print_int(temp_byte(0)); print(""); print_int(outlives(0)); print("");
    print_int(checksum("ab")); print(""); print_int(checksum("")); print("");
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}
`

// tally(1): unwrap(q) = xs[0] = 1; keep = q → 1; unwrap(r) = n + xs[1] = 2 + 3 = 5 → 7.
// tally(5): 5 + unwrap(r) (6 + 7 = 13) + 13 → 31.
// sum_shapes(4): Dot 0, Line(1) 10, Full([2, 3]) 3, Pair(3, [3]) 6, plus the kept Full: 22.
// boxed_shape(3): Pair takes the wildcard 7, Full([3]) 3: 10; boxed_shape(0): 7 + 0.
// node_sum(3): Leaf 7, Twig([1,2]) 2, Twig([2,3]) 3, plus the kept Twig 2 = 14.
// node_sum(1): the Leaf 7 only, plus the kept Leaf { n: 0 } = 7.
// build_sum(1): u is Fork(Fork(Tip 1, Tip 2), Tip 3) = 6, plus its shared
// subtree t = 3 → 9; build_sum(0): 3 + 1 = 4. A Fork field is typed by the
// union that owns Fork, so Tree reaches itself and only a helper can drop it.
// chain_build(2): Link(3, Link(2, End)) = 5, plus the shared tail d = 2 → 7;
// chain_build(0): 1 + 0 = 1. Chain is the recursive DECLARED enum, the layout
// the compiler's own sources never exercise.
// mk_s2(4).a = 4: a struct with no reference field, so the AST caller releases
// it with the box dec alone.
// The shared projection is 8: w.s.a stays 1 because the reuse demand on a
// shared donor forks to a fresh box rather than writing through, c.a = 5 and
// d.b = 2. This is the path the bare-name row makes reachable, and the guard
// that keeps it safe lives in the reuse emitters, not in the row's gate.
// size_of(fill(2)) = 3 and sum_all = 2 + 3 + 4 = 9; fresh_size(1) = 3 + 4 = 7;
// eat_size(fill(5)) = 3; "hello" is 5 bytes; grown_size(3) is "abccc" = 5 and
// grown_size(0) is "ab" = 2; inner_size reads [1, 2] = 2. The P is a literal
// main owns rather than a make() result, which an AST caller still leaks by
// the documented floor in ssarc.box_only_result.
// grow_to(9) is [0..8]: 9 long, summing to 36; grow_to(0) is empty.
// push_temp(1) pushes onto [1, 2, 3] for 4. row_total(4) reads element 0 of
// fill(0..3) = 0+1+2+3 = 6; word_bytes(3) is three "wx" at 2 bytes = 6.
// The for-loop tail runs over lens = fill(2) = [2, 3, 4]: sum 9; skip_two drops
// the 2 for 7; until_two_for breaks at once for 0; first_gt past 2 is 3;
// shadow_for is 9 plus the outer x of 100; temp_for(2) sums [2, 3, 4] again.
// nested_for(3) walks [0,1,2], [1,2,3], [2,3,4] skipping every 1 and breaking
// at the 4: 2 + (2+3) + (2+3) = 12. copy_words(2) is 2 words of 2 bytes = 4,
// and an empty array iterates zero times.
const semsourceRCWant = "1\n4\n5\n-3\n-2\n2\n0\n1\n5\n5\n12\n1\n8\n12\n0\n3\n3\n6\n4\n2\n0\n7\n31\n1\n0\n2\n22\n10\n7\n2\n5\n14\n7\n9\n4\n7\n1\n4\n8\n13\n14\n3\n9\n7\n3\n5\n5\n2\n2\n9\n36\n0\n4\n6\n6\n9\n7\n0\n3\n109\n9\n12\n4\n0\n3\n9\n3\n4\n4\n5\n3\n5\n5\n0\n3\n1\n0\n10\n-2147483648\n0\n28\n8\n-20\n2\n6\n3\n7\n6\n4\n10\n98\n196\n98\n97\n195\n0\n"

const semsourceRCDriver = `import "./semsource"; import "./ssarc"; import "./ssaunits"; import "./ssa";
import "./parser"; import "./lexer"; import "./irlower"; import "./ir";
import "./ircore"; import "./checker"; import "./asmcore"; import "./asm_ir"; import "./asm_arm64_ir"; import "./wasm_ir";
function main(): i32 {
    var av = args();
    var src: string = "";
    match (read_file(av[2])) { Ok(text) => { src = text; }, Err(_) => { return 2; } }
    var mod = checker.annotate_module(parser.parse_module(lexer.tokenize(src)));
    var tab = irlower.struct_tab(mod.structs);
    var base = ircore.wp_fn_sigs(mod.funcs, tab);
    var produced = semsource.build_module(mod);
    var bodies: irlower.LowerResult[] = [];
    var helpers: irlower.LowerResult[] = [];
    var at: i32 = 0;
    for fd in mod.funcs {
        if (fd.name == "main") { bodies = bodies.append(irlower.LowerResult { ok: false, why: "", ops: [], n_locals: 0, n_params: 0, erased_wide: false, arr_slots: [], i64_slots: [], f64_slots: [], str_slots: [], alias_incs: [], name: "", result_kind: irlower.result_from_decl() }); at = at + 1; continue; }
        var p = produced[at];
        if (!p.ok) { eprint(fd.name + ": " + p.why); return 4; }
        var plan = ssaunits.plan(p.func, p.modes);
        if (!plan.ok) { eprint(fd.name + ": " + plan.why); return 5; }
        var lowered = ssarc.lower(p.func, p.modes, plan);
        if (!lowered.ok) { eprint(fd.name + ": " + lowered.why); return 6; }
        eprint("produced " + fd.name + "\n");
        base = ssarc.caller_sigs(base, fd.name, p.func);
        for h in ssarc.drop_helpers(p.func) { helpers = helpers.append(h); }
        bodies = bodies.append(lowered);
        at = at + 1;
    }
    var g = ircore.lower_gated(mod, tab, base, [], av[1] == "wasm32-wasi");
    if (!g.ok) { eprint("ast lowering failed"); return 3; }
    var cache: irlower.LowerResult[] = [];
    at = 0;
    for fd in mod.funcs {
        if (fd.name == "main") { cache = cache.append(g.cache[at]); } else { cache = cache.append(bodies[at]); }
        at = at + 1;
    }
    // The per-type drop helpers are bodies with no declaration, so they go on
    // the cache tail past mod.funcs, deduped by symbol.
    cache = ssarc.merge_helpers(cache, helpers);
    if (av[1] == "x86-64-linux") {
        print(asm_ir.emit_module_ir_unit_flat(mod, true, false, "", [], mod.funcs, tab, 0, 0 - 1, cache, base));
    } else if (av[1] == "arm64-linux") {
        strbuf_reset();
        var state = asmcore.new_state();
        state = asmcore.EmitState { ...state, struct_decls: tab, funcs: mod.funcs };
        state = asm_arm64_ir.emit_body(mod, state, false, cache, base);
        // The per-type __field_reclaim_<T> / __struct_drop_<T> bodies this unit
        // needs, in the order the real arm64 module emit uses them. Without it a
        // struct with a reference field bound in the AST-lowered main leaves an
        // undefined __fn___struct_drop_<T> at link.
        state = asm_arm64_ir.emit_arm64_reclaim_drop_bodies(state);
        state = asm_arm64_ir.emit_ir_runtime(state, false);
        print(strbuf_take());
    } else { print(wasm_ir.emit_ir_module_mode(mod, cache, 0, base)); }
    return 0;
}
`

func TestSelfHostSemanticSourceRC(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	if err := os.WriteFile(filepath.Join(dir, "semsource_rc.fern"), []byte(semsourceRCDriver), 0o644); err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(dir, "program.fern")
	if err := os.WriteFile(program, []byte(semsourceRCProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := buildSelfHostBin(t, gcc, dir, "semsource_rc.fern", "semsource-rc")
	for _, target := range []string{"arm64-linux", "x86-64-linux", "x86-64-sanitize", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			emitTarget, mode := target, "FERN_LEAKCHECK=1"
			if target == "x86-64-sanitize" {
				emitTarget, mode = "x86-64-linux", "FERN_SANITIZE=1"
			}
			cmd := runX86_64Bin(runner, driver, emitTarget, program)
			cmd.Env = append(os.Environ(), mode)
			var diagnostics bytes.Buffer
			cmd.Stderr = &diagnostics
			output, err := cmd.Output()
			if err != nil {
				t.Fatalf("semantic lowering: %v\n%s", err, diagnostics.String())
			}
			for _, name := range []string{"pick", "pair", "boxed", "carry", "count_even", "fill", "first_of", "keep", "chain", "twice", "count_down", "grow", "make", "wrap", "unwrap", "tally", "greet", "boxed_local", "boxed_carry", "shape", "measure", "sum_shapes", "consume", "boxed_shape", "hold", "mk_node", "node_size", "node_sum", "leaf", "fork", "tree_sum", "build_sum", "chain_len", "chain_build", "mk_s2", "proj", "total", "make_counter", "twice_total", "size_of", "eat_size", "fresh_size", "text_size", "inner_size", "sum_all", "grown_size", "grow_to", "push_temp", "words", "word_bytes", "rows", "row_total", "sum_for", "skip_two", "until_two_for", "first_gt", "shadow_for", "temp_for", "nested_for", "copy_words", "head_of", "mid_of", "temp_slice", "scan_slices", "grown", "boxed_len", "deep_len", "paired_len", "longs_len", "span_len", "div_of", "rem_of", "bit_ops", "shifts", "int_min", "ratio_of", "bump", "pure_copy", "reorder", "from_temp", "retag", "nested_up", "byte_at", "first_last", "temp_byte", "outlives", "checksum"} {
				if !strings.Contains(diagnostics.String(), "produced "+name+"\n") {
					t.Fatalf("%s was not produced:\n%s", name, diagnostics.String())
				}
			}
			run := physicalRCRun(t, gcc, runner, dir, "semsource", target, output)
			got, err := run.CombinedOutput()
			if err != nil {
				if exit, ok := err.(*exec.ExitError); ok {
					t.Fatalf("program exit %d:\n%s", exit.ExitCode(), got)
				}
				t.Fatalf("program: %v\n%s", err, got)
			}
			if !strings.HasPrefix(string(got), semsourceRCWant) {
				t.Fatalf("program output:\n%s", got)
			}
			if target != "wasm32-wasi" {
				var allocs, frees, live int64
				summary := leakSummaryLine(string(got))
				if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
					t.Fatal(err)
				}
				if allocs == 0 || allocs != frees || live != 0 {
					t.Fatalf("unbalanced: %s", summary)
				}
			}
		})
	}
}
