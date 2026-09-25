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
// The checked slice is a decision tree with two sinks: every test that
// fails branches to the one None block, and the result is a phi of the Some
// of the view and that None. The length is read once and each bound
// evaluated once, ahead of every test.
function window(s: string, lo: i32, hi: i32): Option[str] { return s[lo:hi]; }
// e? is control flow rather than an operator: the failure edge RETURNS the
// same failure rebuilt at this body's own result type, so nothing joins and
// the value is the success payload on the edge left open.
function unwrapped(o: Option[i32]): Option[i32] { var v: i32 = o?; return Some(v + 1); }
function refused_call(n: i32): i32 { return abs(n); }
function float_literal(): f64 { return 1.5; }
// The unsigned widths: the u64 occupies the i64's slot and the u32 the i32's, and
// each takes the operator forms that read no sign bit. A u32 literal past 2^31
// has no signed immediate to use, so it carries its source text the way a
// 64-bit one does; a cast names the mask its destination applies. Negation is
// refused at both, as it is at the byte: its result leaves the range the type
// names.
function wide_unsigned(n: u64): u64 { return n + 1u64; }
function half_unsigned(d: u32): u32 { return (2147483648u32 / d) >> 1; }
function unsigned_widths(n: i32): i32 { return ((n as u32) as u64) as i32; }
function unsigned_float(n: u32): f64 { return n as f64; }
function refused_unsigned_negation(n: u32): u32 { return -n; }
// String ordering is the runtime's byte compare against zero, and it reads a
// view's bytes as readily as an owned string's.
function sorted(a: string, b: string): boolean { return a < b; }
function ordered(a: string, b: str): i32 {
    if (a <= b) { return 1; }
    if (a > b) { return 2; }
    if (a >= b) { return 3; }
    return 0;
}
// The checker's E052 refuses this body; the print driver annotates without
// checking it, and the live end is the unreachable terminator a checked
// body's desugared total match leaves behind.
function ends_unreachable(n: i32): i32 { if (n > 0) { return 1; } }
function destructure(): i32 { var (a, b) = (1, 2); return a + b; }
function split_pair(p: (i32, i32[])): i32 { var (_, xs) = p; var (n, ys) = p; return n + xs.len() + ys.len(); }
function nested_destructure(): i32 { var (a, (b, c)) = (1, (2, 3)); return a + b + c; }
function struct_destructure(b: Bag): i32 { var whole @ Bag { tag, .. } = b; return tag.len() + whole.items.len(); }
function for_pair(xs: (i32, i32)[]): i32 { var t: i32 = 0; for (a, b) in xs { t = t + a * b; } return t; }
function halve(n: i32): i32 { return n / 2; }
function refused_global(): i32 { return loop_phi(2); }
const LIMIT: i32 = 7;
function limit_ref(): i32 { return LIMIT + 1; }
function callee(xs: i32[], own ys: i32[]): i32[] { return ys; }
function caller(n: i32): i32[] {
    var a: i32[] = [n];
    var b: i32[] = callee(a, [n, n]);
    callee(b, a);
    return callee(b, b);
}
function noop() { return; }
function void_call(): i32 { noop(); return 1; }
// The runtime builtins with a contract: the writers and the builder's append
// read a string, reset and take own nothing, the byte search reads its
// string; a void call stands only as a statement.
function shout(s: string): i32 { print(s); eprint(s); return s.len(); }
function built(s: string): i32 { strbuf_reset(); strbuf_append("ab"); strbuf_append(s); var t: string = strbuf_take(); return t.len(); }
function find_byte(s: string, b: i32): i32 { return __memchr(s, b, 1); }
function refused_void_value(): i32 { var n: i32 = noop(); return n; }
function array_length(xs: i32[]): i32 { return xs.len(); }
// A borrowed receiver's box is not this function's to grow: the push takes a
// unit the plan retains at the call and hands back a copy, where the counted
// receiver below moves its own.
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
// A literal written out of declaration order, which the checker permits and a
// positional construction has to PLACE rather than refuse. The golden pins the
// placement: the array is built first because it is written first, and the
// record_new still takes the i32 before it.
function out_of_order(n: i32): P { return P { xs: [n, n + 1], n: n }; }
function refused_generic_record(n: i32): i32 { var g: G[i32] = G { v: n }; return g.v; }
// A value-position block is a zero-argument call of a zero-parameter lambda,
// which every backend INLINES. The if-EXPRESSION desugar puts one if there
// whose arms each RETURN the block's value, so the arms are produced as a
// branch's are and join at a phi — the golden pins that join, and a reference
// value makes it a unit of this function's own. An else-if nests one arm
// inside another, and a scalar match-expression desugars to the same chain. A
// block with leading statements — a general block body, a match expression
// the tuple desugar routes through a value local — runs them in the enclosing
// block and takes its trailing return's value.
function if_expr(n: i32): i32 { var k: i32 = if (n > 0) { 1 } else { 0 }; return k + n; }
function if_expr_chain(n: i32): i32 { var k: i32 = if (n > 2) { 2 } else if (n > 0) { 1 } else { 0 }; return k; }
function if_expr_ref(n: i32): i32[] { var xs: i32[] = if (n > 0) { [n] } else { [n, n] }; return xs; }
function if_expr_local(n: i32): i32 { var k: i32 = if (n > 0) { var d: i32 = n * 2; d + 1 } else { 0 }; return k; }
function match_expr(n: i32): i32 { var k: i32 = match (n) { 1 => 5, _ => 0 }; return k; }
function block_expr(n: i32): i32 { var k: i32 = { var m: i32 = n + 1; m * 2 }; return k; }
struct Bag { items: i32[], tag: string }
function mapped(xs: i32[], f: (i32) => i32): i32[] { var out: i32[] = []; for x in xs { out = out.append(f(x)); } return out; }
// The lift replaces a function-value argument with the closure box it builds,
// and the checker reads that box's type off the hoisted body: without it the
// box types to unknown and the literal holding the call collapses, so the
// golden's refusal for this one names the call rather than the record.
function lifted_field(b: Bag, xs: i32[]): Bag { return Bag { ...b, items: mapped(xs, (v: i32): i32 => v + 1) }; }
function string_length(s: string): i32 { return s.len(); }
// An address takes the operators whose lowering never reads the operand width
// — the backends run these on the whole register — and the golden pins which
// ones those are. The two it refuses are in the RC fixture's sibling: a shift
// masks its count to the narrow width and a divide picks its register pair
// from it, so both would truncate a real address.
function offset(buf: usize): usize { return buf + 8; }
function delta(a: usize, b: usize): usize { return a - b; }
function masked(p: usize): usize { return p & 15; }
function scaled(p: usize): usize { return p * 2; }
function above(a: usize, b: usize): boolean { return a > b; }
function same(a: usize, b: usize): boolean { return a == b; }
// Refused, and the golden says so: no binary at the pointer width exists for
// the backends to resolve, so these two stay out until one does.
function halved(p: usize): usize { return p / 2; }
function shifted(p: usize): usize { return p >> 3; }
// The checker widens the narrower side of a pointer-width operator instead of
// running the operator at the narrow width, and the tree read here does not
// carry that conversion — so the producer inserts it, on whichever side is
// narrow and from whichever width. A comparison is why the width is asked of
// the operands rather than read off the whole expression, whose type is the
// boolean. The nested form is the one core/map writes: the inner multiply stays at
// the i32, since the checker widens only at the operator the address is in.
function at(buf: usize, off: i32): usize { return buf + off; }
function at_rev(off: i32, buf: usize): usize { return off + buf; }
function at_wide(buf: usize, off: i64): usize { return buf - off; }
function at_byte(buf: usize, off: u8): usize { return buf + off; }
function before(buf: usize, n: i32): boolean { return buf < n; }
function before_rev(n: i32, buf: usize): boolean { return n < buf; }
function span(buf: usize, hdr: i32, cap: i32): usize { return buf + hdr + cap * 4; }
// A codepoint converts where an i32 does and compares only for equality. The
// golden pins which conversion each direction is: into and out of the 32-bit
// widths a mask of the destination, out of the byte none at all since it
// already fits, and across the 64-bit boundary a real extend or wrap. char
// reaches no float on either side, which is native's E033 and is why no
// fixture here spells one.
function codepoint(n: i32): char { return n as char; }
function ordinal(c: char): i32 { return c as i32; }
function wide_point(c: char): i64 { return c as i64; }
function narrow_point(w: i64): char { return w as char; }
function byte_point(c: char): u8 { return c as u8; }
function point_byte(b: u8): char { return b as char; }
function same_point(a: char, b: char): boolean { return a == b; }
// A method whose receiver carries the type variable reaches the free generic
// the registration passes fold it into, and the call instantiates that
// template at the receiver's element. A method on a PRIMITIVE receiver needs
// no fold at all — it is keyed by the receiver's spelling, i32.doubled, the
// way a record's is keyed by its declaration.
//
// The third fold, a method with its own type variables on a generic-struct
// receiver, is refused AT the call, not before it: the parameter is concrete
// and admitted, and the boundary keys no __smm_ prefix, so via_smm reaches
// the lookup and refuses on the contract its receiver's spelling does not
// name — which is the unsupported call target the golden pins.
struct Holder[T] { item: T }
function (xs: T[]) second_or(d: T): T { if (xs.len() < 2) { return d; } return xs[1]; }
function (h: Holder[T]) tagged[U](u: U): i32 { return h.item.len(); }
// A method on a generic receiver is a template whose variables the receiver
// spells; a call instantiates it through the receiver's type.
function (o: Option[T]) has_it(): boolean { match (o) { Some(_) => { return true; }, None => { return false; } } }
function (o: Option[T]) or_val(fallback: T): T { match (o) { Some(x) => { return x; }, None => { return fallback; } } }
function opt_calls(n: i32): i32 { var o: Option[i32] = Some(n); if (o.has_it()) { return o.or_val(0); } return 0 - 1; }
function (n: i32) doubled(): i32 { return n * 2; }
function via_arrm(a: i32[]): i32 { return a.second_or(0); }
function via_smm(h: Holder[string]): i32 { return h.tagged(true); }
function via_prim(k: i32): i32 { return k.doubled(); }
// The eleven string methods the AST lowering emits an OP for rather than a
// call. The receiver is lent to every one; the three that answer text and the
// two that answer an array hand back a fresh box of the caller's own, and the
// predicates answer a scalar the string goes on owning. contains has no op
// of its own on any backend and is index_of at or past zero, as the AST path
// spells it.
function trimmed(s: string): string { return s.trim(); }
function shouted(s: string): i32 { return s.to_ascii_upper().len() + s.to_ascii_lower().len(); }
function split_up(s: string, sep: string): i32 { return s.split(sep).len() + s.lines().len(); }
function scanned_text(s: string, p: string): i32 {
    return s.repeat(2).len() + s.replace(p, "x").len();
}
// The builtins whose result is an instantiated builtin union, and a literal of
// one: the golden pins the contract's INSTANTIATION as the value type, the
// exhausted match's last arm with no test of its own, and the refusal of a
// payload too wide for the box's one word.
// ---- generic declarations (docs/SEMANTIC-GENERICS.md) ---------------------
// A generic declaration is a TEMPLATE: a call site binds its variables and the
// body is produced once per instantiation, named by the types bound, so the
// accumulator is a plain i32 in one instance and a plain string[] in another.
// The accumulator is threaded by REPLACEMENT, the astwalk fold shape.
function fold_two[T](a: T, visit: (i32, T) => T): T {
    var acc: T = visit(1, a);
    acc = visit(2, acc);
    return acc;
}
function add_at(n: i32, a: i32): i32 { return a + n; }
function folded(): i32 { return fold_two(10, add_at); }
// READING the accumulator after passing it: through a LENDING visitor the
// instance borrows it twice and retains nothing, at a scalar or a reference;
// through a CONSUMING one an OWNED accumulator handed over and read again is
// a retain in the string[] instance and nothing in the i32 one, each instance
// planning its own.
function reread[T](a: T, visit: (i32, T) => T, join: (T, T) => T): T {
    return join(visit(1, a), a);
}
function join_at(a: i32, b: i32): i32 { return a - b; }
function reread_int(): i32 { return reread(5, add_at, join_at); }
function reread_own[T](own a: T, visit: (i32, own T) => T, join: (own T, own T) => T): T {
    return join(visit(1, a), a);
}
function join_words(own a: string[], own b: string[]): string[] { return a.append(b[0]); }
function reread_words(): i32 { return reread_own(["q"], own_word, join_words).len(); }
function reread_ints(): i32 { return reread_own(5, add_at, join_at); }
// An OWNED parameter abandoned UNCONSUMED is dropped by the instance, which
// knows its type; a template never had one to drop.
function abandon[T](own a: T, own b: T): T { return b; }
function abandon_words(): i32 {
    var x: string[] = ["x"];
    var y: string[] = ["y", "z"];
    return abandon(x, y).len();
}
// The variable bound to a REFERENCE through a LENDING visitor: the instance
// borrows its accumulator as the declaration says, and each step's result is
// a unit of its own, released when the next step supersedes it.
function add_word(n: i32, a: string[]): string[] { return a.append("x"); }
function ref_acc(): i32 { var w: string[] = []; return fold_two(w, add_word).len(); }
// And through a CONSUMING one, own in the function type: the caller hands
// its unit over at each step and takes back the one the call returns.
function fold_own[T](own a: T, visit: (i32, own T) => T): T {
    var acc: T = visit(1, a);
    acc = visit(2, acc);
    return acc;
}
function own_word(n: i32, own a: string[]): string[] { return a.append("x"); }
function owned_ref_acc(): i32 { return fold_own(["seed"], own_word).len(); }
function opt_len(name: string): i32 {
    var n: i32 = 0;
    match (env(name)) {
        Some(v) => { n = v.len(); },
        None => { n = 0; }
    }
    return n;
}
function wrap_opt(s: string): Option[string] { return Some(s + "!"); }
function refused_wide_opt(n: i64): Option[i64] { return Some(n); }
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
// A body that ends in a total match needs no return of its own, and the arm
// that closes the chain needs no test: nothing else is left for the value to
// be.
function shape_tag(s: Shape): i32 {
    match (s) {
        Dot => { return 0; },
        Line(n) => { return n; },
        Full(xs) => { return xs[0]; },
        Pair(a, ys) => { return a + ys[0]; }
    }
}
function guarded_line(s: Shape): i32 {
    match (s) {
        Line(n) when n > 0 => { return n; },
        _ => { return 0; }
    }
}
function qualified_arm(s: Shape): i32 {
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
// A byte is its own semantic type: the suffix names it, a string's element
// carries it, and an operator over two of them stays in it. The golden pins
// the value types, which is where a u8 that silently became an i32 would show.
// An unsuffixed literal takes the width of what it sits beside, on EITHER side
// of the operator — v3 and v5 below are bytes, not i32s.
function byte_ops(s: string, i: i32): i32 {
    var b: u8 = s[i];
    var next: u8 = b + 1;
    if (b == b'a' || 122 > next) { return next as i32; }
    return b as i32;
}
function narrowed(n: i32): u8 { return n as u8; }
// A hexadecimal literal reaches the constant through the same value the
// production lowering reads, and the checker (E047) has already proved it fits
// the type it is written at.
function hex_mask(n: i32): i32 { return (n & 0x00ff00ff) | 0x2A; }
// The 64-bit integer, with its own slot: a literal carries its source text
// rather than an immediate that cannot hold it, the operators run at width 64,
// and the conversions across the boundary are an extend and a wrap.
function wide_ops(n: i32): i32 {
    var big: i64 = (n as i64) * 1000000007i64;
    if (big > 0i64) { return (big >> 32) as i32; }
    return (0i64 - big) as i32;
}

function float_cast(n: i32): f64 { return n as f64; }
function refused_mixed_width(b: u8, n: i32): i32 { return b + n; }

// The f64 is a value: literals carry their text, the float operators are the
// stack IR's own and never wrap, a comparison is a boolean, and a conversion
// to or from a signed integer is a real instruction. The remainder has no
// float form. The narrower float occupies the same slot rounded to single
// precision, so its operator is the f64's with the rounding after it.
function float_ops(x: f64, n: i32): i32 {
    var y: f64 = x * 2.5 + (n as f64);
    if (y > 10.0 || -y == x) { return (y / 2.0) as i32; }
    var w: i64 = (y - x) as i64;
    return (w as f64 + 0.5) as i32;
}
function refused_float_rem(x: f64): f64 { return x % 2.0; }
function narrow_float(x: f32): f32 { return x + 1.0; }

// A string view: a slice is one; an owned string bound or passed where a view
// is declared is lent (a retag that borrows the box); a view passed to a
// borrowed 'string' parameter is the retag the other way. A view result reads
// the one parameter it is anchored to; a plain view result that reads a
// local, or either of two parameters, returns a copy instead, and any other
// result holding such views escapes its source and is refused. An array of
// views is anchored to its source the same way.
function view_len(v: str): i32 { return v.len(); }
function view_of(s: string): i32 {
    var v: str = slice_unchecked(s, 1, 3);
    var w: str = s;
    var inner: str = slice_unchecked(w, 0, 1);
    if (v == w || inner != "a") { return v[0] as i32; }
    return v.len() + view_len(s) + string_length(v) + inner.len();
}
function copy_view(v: str): string { return v + ""; }
function view_result(s: string): str { return slice_unchecked(s, 0, 1); }
function copied_view_of_a_local(t: string): str {
    var s: string = t + "x";
    return slice_unchecked(s, 0, 1);
}
function copied_view_of_either(a: string, b: string, c: boolean): str {
    if (c) { return a; }
    return b;
}
function refused_option_of_either(a: string, b: string, c: boolean): Option[str] {
    if (c) { return a[0:1]; }
    return b[0:1];
}
function view_element(s: string): i32 {
    var xs: str[] = [];
    xs = xs.append(slice_unchecked(s, 0, 1));
    return xs.len();
}

// One element replaced: the receiver's unit is handed over as an append's
// is, and the array handed back holds the value. A view of s into an array
// holding views of vs's own source reads two sources, and is refused.
function replace_at(own xs: i32[], i: i32, v: i32): i32[] { return xs.with(i, v); }
function refused_with_view(own vs: str[], s: string): str[] { return vs.with(0, slice_unchecked(s, 0, 1)); }

// An integer literal tree is the width of its destination: a subtraction
// from zero binds an i64 with no conversion, and a literal beside a wide
// operand is that operand's width.
function wide_literal_tree(n: i64): i64 {
    var x: i64 = 0 - 1;
    var y: i64 = n + 1;
    if (y < 0 - 2) { return x * 2; }
    return y;
}

// A byte loop over a string: each step reads one u8 of the borrowed text.
function count_byte(s: string, b: u8): i32 {
    var n: i32 = 0;
    for c in s { if (c == b) { n = n + 1; } }
    return n;
}

// A literal takes the destination's type, so a member widens to the union
// the tuple or array declares rather than the literal being typed by its
// elements and refused at the binding.
function leaf_pair(n: i32): (Node, i32) {
    var p: (Node, i32) = (Leaf { n: n }, n);
    return p;
}
function leaves(n: i32): Node[] {
    var xs: Node[] = [Leaf { n: n }, Twig { xs: [n] }];
    return xs;
}

// A shift count of another integer width is converted to the value's width
// before the operator, the way the runtime masks it.
function shift_wide(n: i64, k: i32): i64 { return (n << k) + (n >> 3); }

// Five more runtime builtins with a contract: a write to stdout and the
// process exit are void calls, a byte array packs into a fresh string, and
// a double crosses to and from its bit pattern.
function bail(code: i32): i32 {
    if (code > 0) { write("bail"); exit(code); }
    return code;
}
function bytes_text(bs: u8[]): string { return string_from_bytes_unchecked(bs); }
function bit_round(x: f64): f64 { return f64_from_bits(f64_bits(x)); }

// A for-loop element is a checker BINDING inside the loop body, so an
// expression reading it has a type. A record or variant literal over the
// element takes that type, and an unannotated binding is inferred from it.
function bump_each(ps: P[]): i32 {
    var t: i32 = 0;
    for p in ps {
        var q: P = P { ...p, n: p.n + 1 };
        var d = q.n + p.xs.len();
        t = t + d + q.xs.len();
    }
    return t;
}
function line_each(ns: i32[]): i32 {
    var t: i32 = 0;
    for k in ns { var s: Shape = Line(k + 1); t = t + measure(s); }
    return t;
}

// A function VALUE is the environment box the lambda lift builds: the hoisted
// body's address in slot 0, then the captures. A call through one is
// environment-first, so the box is lent to the body as the environment it
// reads its captures out of, and the result is a unit of the caller's own. A
// capture that is a reference is one unit the box owns, walked by the
// environment record the body in slot 0 names when the box dies. Refused: a
// signature with a slot wider than the word the untagged indirect call
// describes, a function value as a result, and one as an element.
function twice_it(x: i32): i32 { return x * 2; }
function apply_int(f: (i32) => i32, x: i32): i32 { return f(x); }
function use_apply(n: i32): i32 { return apply_int(twice_it, n); }
function shift_by(k: i32, n: i32): i32 { return apply_int((x: i32): i32 => { return x + k; }, n); }
// A lambda spelling no result type has the type of the first value it
// returns, read in the scope the statements before it built.
function inferred_plain(n: i32): i32 { return apply_int((x: i32) => x + 1, n); }
// A float binding with no annotation settles at the f64 an unsuffixed
// literal is.
function float_binding(n: i32): i32 { var f = 2.5; var g = f + 1.5; if (g > 3.0) { return n; } return 0; }
function inferred_ret(k: i32, n: i32): i32 { return apply_int((x: i32) => { var m: i32 = x * k; return m + 1; }, n); }
// A function value is lent, never handed over: a parameter that would take
// its box and release it here has no contract, since only the frame that
// built the box knows the captures its release must walk.
function refused_own_fn(own f: (i32) => i32, n: i32): i32 { return f(n); }
// A closure TAKES a captured function value, like every other reference it
// holds, so the box's release walks that field. The capture is a parameter
// here and a local closure below, and neither is a special case.
function via_capture(f: (i32) => i32, n: i32): i32 { return apply_int((x: i32): i32 => { return f(x) + 1; }, n); }
function capture_local(n: i32): i32 {
    var g: (i32) => i32 = (x: i32): i32 => { return x + n; };
    return apply_int((x: i32): i32 => { return g(x) + 1; }, n);
}
function bound_fn(n: i32): i32 { var g: (i32) => i32 = twice_it; return g(n) + g(1); }
function head_of_arr(xs: i32[]): i32 { return xs[0]; }
function apply_arr(f: (i32[]) => i32, xs: i32[]): i32 { return f(xs) + f([9, 8]); }
function lend_array(n: i32): i32 { var a: i32[] = [n, n + 1]; return apply_arr(head_of_arr, a); }
function pick_shift(k: i32, n: i32): i32 {
    var g: (i32) => i32 = (x: i32): i32 => { return x + k; };
    if (n > 2) { g = (x: i32): i32 => { return x - k; }; }
    return g(n) + g(0);
}
function shift_loop(k: i32, n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) { t = t + apply_int((x: i32): i32 => { return x * k; }, i); i = i + 1; }
    return t;
}
function eat_text(own w: string): i32 { return w.len(); }
function apply_text(f: (string) => i32, s: string): i32 { return f(s); }
function refused_own_value(s: string): i32 { return apply_text(eat_text, s); }
function text_capture(w: string, n: i32): i32 { return apply_int((x: i32): i32 => { return x + w.len(); }, n); }
function words_capture(n: i32): i32 {
    var ws: string[] = ["ab"];
    var f: (i32) => i32 = (x: i32): i32 => { return x + ws.len(); };
    return f(n) + f(1);
}
function pick_capture(k: i32, n: i32): i32 {
    var ws: string[] = ["ab"];
    var w: string = "xyz";
    var g: (i32) => i32 = (x: i32): i32 => { return x + ws.len(); };
    if (k > 0) { g = (x: i32): i32 => { return x + w.len(); }; }
    return g(n);
}
// A nested function with an owning parameter is a lambda bound to a name,
// and the name's type spells the own the way the box's does.
struct Tally { n: i32 }
function use_of(n: i32, f: string, a: Tally): Tally { return Tally { n: a.n + f.len() + n }; }
function scan(x: i32, f: string): Tally {
    function ve(n: i32, own a: Tally): Tally { return use_of(n, f, a); }
    return fold_own(Tally { n: x }, ve);
}
// The same with an owning ARRAY parameter, whose own sits on the spelling's
// base name under the array suffix.
function scan_words(x: i32, f: string): i32 {
    function ve(n: i32, own a: string[]): string[] { return a.append(f); }
    return fold_own(["a"], ve).len();
}
function wide_sig(f: (i64) => i64, n: i64): i64 { return f(n); }
function fn_result_named(): (i32) => i32 { return twice_it; }
function fn_element_array(n: i32): i32 { var fs: ((i32) => i32)[] = [twice_it]; return fs.len(); }

// A free builtin whose result the checker types is a value like any other, so
// the record literal, array or operator written around the call keeps its own
// type instead of collapsing to unknown. The byte array such a call is handed
// carries the element its parameter declares: there is no implicit numeric
// conversion, so an i32 written into a u8[] literal is refused.
struct Frag { text: string, n: i32 }
function frag_of(bs: u8[], n: i32): Frag { return Frag { text: string_from_bytes_unchecked(bs), n: n }; }
function byte_text(b: u8): i32 { return string_from_bytes_unchecked([b]).len(); }
function rounded(x: f64, n: i32): i32 { return n + wide_low(f64_bits(x)); }
function wide_low(n: i64): i32 { return (n & 255i64) as i32; }
function refused_byte_width(b: i32): i32 { return string_from_bytes_unchecked([b]).len(); }

// Cell[T] is a nominal name over a one-element box rather than a declared
// record, so a cell-typed declared field resolves through its element and the
// enum or record carrying one has a schema. The cell's own vocabulary is the
// construction, which takes the element's unit, a read that hands out a unit
// of the element, and a write that takes the new element's unit and stands
// only as a statement.
struct Cellar { c: Cell[i32], n: i32 }
enum Crate { Plain(i32), Celled(Cell[i32], i32) }
function cell_field(k: Cellar): i32 { return k.n; }
function cell_payload(own b: Crate): i32 {
    match (b) {
        Plain(k) => { return k; },
        Celled(_, k) => { return k; }
    }
    return 0 - 1;
}
function cell_round(n: i32): i32 { var c: Cell[i32] = cell_new(n); c.set(c.get() + 1); return c.get(); }
function cell_text(s: string): i32 {
    var c: Cell[string] = cell_new(s);
    var first: string = c.get();
    c.set(first + "!");
    return first.len() + c.get().len();
}
function refused_cell_value(n: i32): i32 { var c: Cell[i32] = cell_new(n); var k: i32 = c.set(n); return k; }
// The 32-bit float occupies the f64's slot at single precision: a conversion
// into it, a literal of it and an operator's result at it are each rounded
// where they are made, and the bit pair reads and writes that rounded value.
function narrow_bits(x: f64): i32 { var y: f32 = x as f32; return f32_bits(y); }
function widened(b: i32): f64 { return (f32_from_bits(b)) as f64; }
function narrow_sum(a: f32, b: f32): f32 { return a + b * 0.5; }
// The pointer-width integer is an address: it converts to and from every
// integer width, and the string builder's handle is one. The narrowing
// direction keeps only the low half of a real address, which is the source's
// to mean — native admits it, and E069 is the checker's warning about it.
function handle_out(h: usize): i64 { return h as i64; }
function handle_in(n: i64): usize { return n as usize; }
function built(n: i32): string {
    var b: usize = buf_new(n);
    buf_push(b, "ab");
    buf_push_byte(b, 99);
    var s: string = buf_take(b);
    buf_free(b);
    return s;
}
// An address and the box it names share a slot, so both directions of the
// reinterpretation are casts the lowering emits nothing for: the one core/map's
// key column reads back, and the one that hands the address out. The second
// ANCHORS its source, so the box outlives every read through the address. What
// an anchor cannot cover is an address that leaves the frame, which is why
// core/map writes an explicit rc-inc on the one it returns.
function addr_as_text(p: usize): string { return p as string; }
function addr_as_bytes(p: usize): u8[] { return p as u8[]; }
function text_as_addr(s: string): usize { return s as usize; }
// The ADDRESS is what cast_admits keeps out of the float domain — it reaches
// one through the 64-bit integer the source writes — so this is what a refused
// cast looks like now that both directions of the address reinterpretation are
// admitted, and now that the byte converts like every other integer width.
function refused_address_as_float(h: usize): f64 { return h as f64; }
function handle_sum(h: usize, k: usize): usize { return h + k; }
function handle_narrow(h: usize): i32 { return h as i32; }
function handle_byte(h: usize): u8 { return h as u8; }
function handle_lit(): usize { var p: usize = 16; return p; }
// The outcome of a write is a Result whose Ok carries void: a payload that is
// no payload, so the arm's binding names nothing and the box is a tag with a
// zero word behind it.
function saved(path: string, text: string): i32 {
    match (write_file(path, text)) {
        Ok(u) => { return 1; },
        Err(e) => { return 0; }
    }
    return 0 - 1;
}
function made(path: string): i32 {
    match (create_dir_all(path)) {
        Ok(_) => { return 1; },
        Err(e) => { return 0; }
    }
    return 0 - 1;
}
function saved_exec(path: string, text: string): i32 {
    match (write_file_exec(path, text)) {
        Ok(_) => { return 1; },
        Err(e) => { return 0; }
    }
    return 0 - 1;
}
// A map is admitted at one shape: a string KEY column over a NARROW SCALAR
// value column, which is what __fern_map_free_ks releases. Its box carries no
// reference count on the register backends, so a unit of one is LINEAR: a plan
// that would share it is refused. An insert is handed the receiver's unit and
// the key's, which the key column owns until the map is released; has and
// get_or borrow both and answer a scalar.
function seen_twice(a: string, b: string): i32 {
    var m: Map[string, i32] = map_new(4);
    m = m.insert(a, 1);
    m = m.insert(b, m.get_or(a, 0) + 1);
    if (m.has(b)) { return m.get_or(b, 0); }
    return 0;
}
function flagged(ws: string[]): i32 {
    var m: Map[string, boolean] = map_new(ws.len() + 1);
    for w in ws { m = m.insert(w, true); }
    var n: i32 = 0;
    for w in ws { if (m.has(w)) { n = n + 1; } }
    return n;
}
// A column of records: the map owns a unit of every value, released through
// the record's own drop when the map is released and on the entry an insert
// supersedes; a get retains the payload of the Option it answers.
function keyed_recs(a: string, n: i32): i32 {
    var m: Map[string, Q] = map_new(2);
    m = m.insert(a, Q { name: a, p: P { n: n, xs: [n] } });
    m = m.insert(a, Q { name: a + a, p: P { n: n + 1, xs: [] } });
    var t: i32 = 0;
    if let Some(q) = m.get(a) { t = q.p.n; }
    return t + m.len();
}
// A map whose value column is COUNTED: the map owns a unit of every value, the
// release walks the column, and a read retains what it answers because the
// value may be the column's or the caller's default. A key that is not a
// string is still refused, its column being one the release does not walk.
function map_value(m: Map[string, string], k: string): i32 { return m.get_or(k, k).len(); }
// The runtime intrinsics, typed as native's FuncSigs types them and contracted
// here: the f64 primitives std/float dispatches to, the bit counts, the raw
// memory escape hatches, the byte scans over a LENT string, and the C-ABI
// trampolines. Only __alloc_u8 hands back a reference — a fresh zeroed
// buffer the caller owns — so it is the one whose unit this frame drops.
function measured(x: f64, k: u32): i32 {
    var root: f64 = __sqrt_f64(x);
    var raised: f64 = __pow_f64(root, 2.0);
    return (__floor_f64(raised) as i32) + __popcount32(k) + __clz64(1u64);
}
function scanned(text: string, byte: i32): i32 {
    return __count_byte(text, byte) + __memchr(text, byte, 0) + __ascii_run(text, 0) + __sum_bytes(text);
}
function buffered(n: i32): i32 {
    var buf: u8[] = __alloc_u8(n);
    return buf.len() + __ptr_width();
}
function poked(): i32 {
    var block: usize = __alloc(16);
    __store_i32(block, 7);
    var read: i32 = __load_i32(block);
    __free(block, 16);
    return read;
}
// A map names no element in its construction, so the destination is the only
// place its shape is written; a construction reaching a slot that spells none
// has no shape to take.
function refused_map_bare(k: string): i32 { return map_new(2).insert(k, 1).get_or(k, 0); }
// A guard is read after the payload bindings and a false one leaves for the
// next arm's test; the end of a value-returning body the checker proved
// unreachable aborts.
function guarded_and(o: Option[i32], k: i32): i32 { match (o) { Some(n) when k > 0 && n > 2 => { return n; }, _ => { return 0; } } }
function guarded(o: Option[i32]): i32 {
    match (o) {
        Some(k) when k > 5 => { return k * 2; },
        Some(k) => { return k; },
        None => { return 0; },
    }
}
// An integer key column holds no unit, so the map is freed whole and its
// reads carry the integer key kind.
function int_map_key(m: Map[i32, i32], n: i32): i32 { return m.get_or(n, 0); }
function map_get_line(m: Map[i32, i32], k: i32): i32 { match (m.get(k)) { Some(v) => { return v; }, None => { return 0; } } }
// A literal's map_new(n).insert(k, v) chain takes the destination's shape
// through the chain, since an insert hands its receiver back.
function map_lit(n: i32): i32 { var m: Map[i32, i32] = Map { 1: n, 2: n + 1 }; return m.get_or(2, 0); }
// A generic array method with a variable of its own: the parser's instance
// at the element keeps U for the lambda argument to bind.
function map_to[T, U](xs: T[], f: (T) => U): U[] { var out: U[] = []; for x in xs { out = out.append(f(x)); } return out; }
function (xs: T[]) map_to[U](f: (T) => U): U[] { return map_to(xs, f); }
function mapped_to(xs: i32[]): i32 { var ws: string[] = xs.map_to((x: i32): string => "s"); return ws.len(); }
// A void method in statement position is a call like a void function's.
function (b: Bag) log(): void { print(b.tag); }
function (o: Option[T]) note(): void { print("n"); }
function log_bag(b: Bag, o: Option[i32]): i32 { b.log(); o.note(); return b.items.len(); }
// A labelled exit leaves the loop the label names and every loop inside it;
// a parameter with a default is an ordinary parameter, the parser having
// filled the call sites.
function labelled(n: i32): i32 { var t: i32 = 0; var i: i32 = 0; outer: while (i < n) { i = i + 1; var j: i32 = 0; while (j < 3) { j = j + 1; if (j == i) { continue outer; } if (j == 2) { break outer; } t = t + 1; } } return t; }
function with_default(n: i32, by: i32 = 1): i32 { return n + by; }
// A value block is typed by its destination: the checker reads the type the
// desugar guessed from the arms' syntax, which calls an empty array arm an
// i32. A match on a Some the destination does not name is typed from its
// payload.
function vb_empty(k: i32): i32[] { var o: Option[i32] = Some(k); return (match (o) { Some(v) => [v, v], None => [] }); }
function vb_bare(k: i32): i32 { return (match (Some(k)) { Some(v) => v + 1, None => 0 }); }
// A match expression the tuple desugar routes through a value local: the
// block's leading statements run in the enclosing block and its trailing
// return reads the local.
function tm_line(k: i32): i32 { var t = (k, 2); return match (t) { (1, b) => b * 10, (a, _) => a }; }
// An address has no saturating or checked operator: the clamp is at a width
// the type names, which is the target's, and the native checker refuses it.
function refused_usize_sat(a: usize, b: usize): usize { return a +| b; }
function refused_usize_chk(a: usize, b: usize): i32 { match (a *? b) { Some(_) => { return 1; }, None => { return 0; } } }
`

const semsourcePrintDriver = `import "./semsource"; import "./ssa"; import "./ssaunits"; import "./typeinfo";
import "./parser"; import "./lexer"; import "./util"; import "./irlower";
function show(p: semsource.Produced): void {
    if (!p.ok) { print("refused " + p.why); return; }
    if (p.template) { print("template instantiated"); return; }
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
function main(): i32 {
    var src: string = "";
    match (read_file(args()[1])) { Ok(text) => { src = text; }, Err(_) => { return 2; } }
    var parsed = parser.parse_module(lexer.tokenize(src));
    // The production pipeline injects the front end's own enum variants
    // (IoError, JsonValue) as declarations before the lambda lift runs;
    // without them a Result's error arm names a union nothing declares.
    var mod = irlower.lift_lambdas(parser.register_struct_method_generics(parser.register_map_method_generics(parser.register_array_method_generics(parser.Module { ...parsed, structs: parser.inject_builtin_enums(parsed.structs) }))));
    // Every declaration in order, then every instance the templates were
    // produced at.
    var built = semsource.build_module(mod);
    for p in built.decls { show(p); }
    for p in built.instances { show(p); }
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
	// CI-DARK: FERN_UPDATE_GOLDEN — a regeneration tool, not coverage: it
	// rewrites the golden from the driver's output before the compare, so a
	// lane setting it would disable this gate. The compare below is the CI
	// behaviour.
	if os.Getenv("FERN_UPDATE_GOLDEN") != "" {
		if err := os.WriteFile("testdata/semsource_print.golden", got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile("testdata/semsource_print.golden")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("produced graphs differ from testdata/semsource_print.golden:\n%s", goldenDiff(string(want), string(got)))
	}
}

// goldenDiff reports the first differing line with a window either side.
// Printing the whole output instead buries the one changed line in several
// thousand, and the reader's next move is to extract it from the CI log and
// diff it by hand.
func goldenDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	at := 0
	for at < len(w) && at < len(g) && w[at] == g[at] {
		at++
	}
	window := func(lines []string) string {
		lo, hi := at-3, at+8
		if lo < 0 {
			lo = 0
		}
		if hi > len(lines) {
			hi = len(lines)
		}
		var b strings.Builder
		for i := lo; i < hi; i++ {
			mark := "  "
			if i >= at {
				mark = "> "
			}
			fmt.Fprintf(&b, "%s%4d | %s\n", mark, i+1, lines[i])
		}
		return b.String()
	}
	return fmt.Sprintf("%d golden lines, %d produced; first differ at line %d\n--- want ---\n%s--- got ---\n%s",
		len(w), len(g), at+1, window(w), window(g))
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
struct Words { ws: string[], k: i32 }
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
@noinline function shape_code(s: Shape): i32 {
    match (s) {
        Dot => { return 0; },
        Line(n) => { return n; },
        Full(xs) => { return xs.len(); },
        Pair(a, ys) => { return a + ys.len(); }
    }
}
@noinline function eat_shape(own s: Shape): i32 {
    match (s) {
        Dot => { return 1; },
        Line(n) => { return n + 1; },
        Full(xs) => { return xs[0] + 2; },
        Pair(a, ys) => { return a + ys[0] + 3; }
    }
}
@noinline function node_tag(nd: Node): i32 {
    match (nd) {
        Leaf(l) => { return l.n; },
        Twig(t) => { return t.xs[0]; }
    }
}
@noinline function tag_probe(n: i32): i32 {
    return shape_code(shape(3)) + eat_shape(shape(2)) + node_tag(mk_node(n));
}
@noinline function shape_codes(limit: i32): i32 {
    var total: i32 = 0;
    var i: i32 = 0;
    while (i < limit) {
        total = total + shape_code(shape(i)) + eat_shape(shape(i));
        i = i + 1;
    }
    return total + node_tag(mk_node(limit));
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
// The receiver the loop header carries is the caller's box on the first step:
// the header retained it, so the push has a unit but not the only one. The
// runtime's push leaves a shared receiver's count alone and hands back an
// un-share copy, so the release that makes the push take exactly one unit is
// the lowering's. grow_to(9) leaves spare capacity, which is where an in-place
// grow would also show up in the caller's own length.
@noinline function borrow_acc(xs: i32[], n: i32): i32 {
    var i: i32 = 0;
    while (i < n) { xs = xs.append(100 + i); i = i + 1; }
    return xs.len();
}
// A receiver the plan does not MOVE: its unit is retained at the push, so the
// count test takes the copy and the box handed back is one nobody else names
// -- the source keeps its length and its elements. A borrowed parameter's box,
// a field's, an element of a borrowed array of arrays, an own parameter's read
// again after the push, and the field receiver in a loop, which is the
// immutable-update threading shape and copies once per step.
@noinline function push_borrowed(xs: i32[], v: i32): i32[] { return xs.append(v); }
@noinline function set_borrowed(xs: i32[], i: i32, v: i32): i32[] { return xs.with(i, v); }
@noinline function push_field_len(p: P, v: i32): i32 {
    var q: P = P { ...p, xs: p.xs.append(v) };
    return q.xs.len() * 10 + p.xs.len();
}
@noinline function set_field_at(p: P, i: i32, v: i32): i32 {
    var q: P = P { ...p, xs: p.xs.with(i, v) };
    return q.xs[i] * 10 + p.xs[i];
}
@noinline function push_elem_len(rs: i32[][], v: i32): i32 {
    var ys: i32[] = rs[0].append(v);
    return ys.len() * 10 + rs[0].len();
}
@noinline function elem_push(v: i32): i32 {
    var rs: i32[][] = [[1, 2], [3, 4]];
    return push_elem_len(rs, v);
}
@noinline function push_kept(own xs: i32[], v: i32): i32 {
    var ys: i32[] = xs.append(v);
    return ys.len() * 10 + xs.len();
}
// A field append whose record this frame reads no further through that field
// grows the buffer IN PLACE (#9365, the second half of #8785): the functional
// update through a borrowed record, an owned one, and a loop-carried one. A
// caller that KEEPS its record (acc_kept) holds a count on the field across
// the call, so the push copies and its own length is unchanged; the AST
// lowering brackets the same way through the regrown registry, since this
// fixture's main is AST-lowered. Every element of tags_total is a counted
// box, so a buffer released twice would read back as a wrong name.
struct Acc { xs: i32[], tag: string }
struct Tag { name: string }
struct Tags { list: Tag[], n: i32 }
@noinline function acc_push(a: Acc, v: i32): Acc { return Acc { ...a, xs: a.xs.append(v) }; }
@noinline function acc_push_own(own a: Acc, v: i32): Acc {
    a = Acc { ...a, xs: a.xs.append(v) };
    return Acc { ...a, xs: a.xs.append(v + 1) };
}
@noinline function acc_fill(n: i32): i32 {
    var a: Acc = Acc { xs: [], tag: "t" };
    var i: i32 = 0;
    while (i < n) { a = acc_push(a, i); i = i + 1; }
    return a.xs.len() * 10 + a.tag.len();
}
@noinline function acc_kept(v: i32): i32 {
    var a: Acc = Acc { xs: [1, 2], tag: "kept" };
    var b: Acc = acc_push(a, v);
    var c: Acc = acc_push_own(Acc { ...a, tag: "own" }, v);
    return a.xs.len() * 100 + b.xs.len() * 10 + c.xs.len();
}
@noinline function acc_loop(n: i32): i32 {
    var a: Acc = Acc { xs: [], tag: "" };
    var i: i32 = 0;
    while (i < n) { a = Acc { ...a, xs: a.xs.append(i * i) }; i = i + 1; }
    return a.xs[n - 1];
}
// The field HANDED to a callee that appends to it — the x86 assembler's
// x86_osz(a.code, size) — where the caller reads the record no further
// through that field: the caller hands the buffer on without a bracket, the
// callee grows it in place, and the caller of THAT frame brackets by the row
// the closure gave it (acc_via_kept keeps its record, so its length holds).
@noinline function acc_osz(xs: i32[], size: i32): i32[] {
    if (size == 16) { return xs.append(102); }
    return xs;
}
@noinline function acc_via(a: Acc, size: i32): Acc { return Acc { ...a, xs: acc_osz(a.xs, size) }; }
@noinline function acc_via_fill(n: i32): i32 {
    var a: Acc = Acc { xs: [], tag: "v" };
    var i: i32 = 0;
    while (i < n) { a = acc_via(a, 16); i = i + 1; }
    return a.xs.len() * 10 + a.tag.len();
}
@noinline function acc_via_kept(v: i32): i32 {
    var a: Acc = Acc { xs: [v], tag: "kept" };
    var b: Acc = acc_via(a, 16);
    var c: Acc = acc_via(a, 32);
    return a.xs.len() * 100 + b.xs.len() * 10 + c.xs.len();
}
// A record with a second holder this frame cannot see: the handed field is
// gated on the record's count, so the callee copies and held[0] keeps its
// length.
@noinline function acc_via_shared(v: i32): i32 {
    var a: Acc = Acc { xs: [v], tag: "s" };
    var held: Acc[] = [a];
    var b: Acc = acc_via(a, 16);
    return held[0].xs.len() * 10 + b.xs.len();
}
// A record field handed into a COUNTED slot from a record this frame owns:
// the callee's contract takes the unit, and retaining the projection would
// leave the record holding a second count on the buffer the callee then
// appends to. The supply steals the field instead when the record has no
// other holder (ssaunits.steal_unit), so thread_run's chain copies nothing;
// thread_shared keeps a second holder of the field's box and the callee's
// append pays the copy that holder needs.
struct Threaded { acc: Acc, labels: i32[], why: string }
@noinline function thread_step(own c: Threaded, v: i32): Threaded {
    return Threaded { ...c, acc: acc_push_own(c.acc, v), labels: c.labels.append(v) };
}
@noinline function thread_run(n: i32): i32 {
    var before: i32 = __arr_push_shared_count();
    var c: Threaded = Threaded { acc: Acc { xs: [], tag: "c" }, labels: [], why: "" };
    var i: i32 = 0;
    while (i < n) { c = thread_step(c, i); i = i + 1; }
    return (c.acc.xs.len() + c.labels.len()) * 10 + (__arr_push_shared_count() - before);
}
@noinline function thread_shared(v: i32): i32 {
    var c: Threaded = Threaded { acc: Acc { xs: [v], tag: "c" }, labels: [], why: "" };
    var held: Threaded[] = [c];
    var d: Threaded = thread_step(Threaded { ...c, why: "s" }, 5);
    return held[0].acc.xs.len() * 10 + d.acc.xs.len();
}
// Match-expressions and if-expressions in value position: every arm carries
// its value to the join's phi, a string, a record and an array among them,
// so the units the arms build reach the binding through the phi.
enum Shade { Dark, Light }
struct Tagged { n: i32, tag: string }
@noinline function vb_words(k: i32): i32 {
    var o: Option[i32] = Some(k);
    var words: string[] = (match (o) { Some(v) => ["a" + "b", "c"], None => [] });
    var sh: Shade = Dark;
    var tag: string = (match (sh) { Dark => "d" + "k", Light => "l" });
    var ot: Option[string] = Some(tag);
    var cell: Tagged = (match (ot) { Some(t) => Tagged { n: k, tag: t }, None => Tagged { n: 0, tag: "" } });
    var pair: (i32, string) = (if (k > 1) { (k, cell.tag) } else { (0, "z") });
    return words.len() * 100 + tag.len() * 10 + cell.tag.len() + pair.1.len();
}
@noinline function vb_rows(k: i32): i32 {
    var rows: i32[] = (if (k > 0) { [k, k] } else { [k] });
    var orows: Option[i32[]] = Some(rows);
    var picked: i32[] = (match (orows) { Some(r) => r, None => [0 - 1] });
    return rows.len() * 10 + picked.len();
}
// The saturating and checked operators: the checked ones answer an Option
// box, a fresh unit this frame owns and releases, at the narrow width, the
// wide one and unsigned.
@noinline function sat_mix(a: i32, b: i32): i32 { return (a +| b) + (a -| b) + ((a *| b) >> 16) + (a <<| b); }
@noinline function chk_count(a: i32, b: i32): i32 {
    var n: i32 = 0;
    match (a +? b) { Some(v) => { n = n + 1; }, None => { n = n + 0; } }
    match (a *? b) { Some(v) => { n = n + 2; }, None => { n = n + 0; } }
    match (a /? b) { Some(v) => { n = n + 4; }, None => { n = n + 0; } }
    match (a <<? b) { Some(v) => { n = n + 8; }, None => { n = n + 0; } }
    return n;
}
@noinline function chk_wide(a: i64, b: i64): i64 { match (a *? b) { Some(v) => { return v; }, None => { return 0i64 - 1i64; } } }
@noinline function sat_byte(a: u8, b: u8): i32 { return ((a +| b) as i32) * 1000 + ((a -| b) as i32); }
@noinline function chk_unsigned(a: u32, b: u32): i32 { match (a -? b) { Some(v) => { return (v as i32); }, None => { return 0 - 7; } } }
// A view sliced from a string this frame owns, lent to a callee that stores
// it: the callee is handed a copy, so the record reads its text after the
// local is released and its block reused (#9407). The negative case lends an
// owned string, which needs no copy.
struct Lit { text: string, neg: boolean }
@noinline function lit_of(text: string, neg: boolean): Lit { return Lit { text: text, neg: neg }; }
@noinline function lit_int(n: i32): Lit {
    var s: string = "-" + grown(n);
    if (n < 0) { return lit_of(s, false); }
    return lit_of(slice_unchecked(s, 1, s.len()), true);
}
@noinline function churn(n: i32): string[] {
    var out: string[] = [];
    var i: i32 = 0;
    while (i < n) { out = out.append("zz" + grown(i % 5)); i = i + 1; }
    return out;
}
@noinline function lit_bytes(n: i32): i32 {
    var a: Lit = lit_int(n);
    var junk: string[] = churn(64);
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < a.text.len()) { t = t + (a.text[i] as i32); i = i + 1; }
    if (a.neg) { t = t + 1000; }
    return t + junk.len() - 64;
}
@noinline function tags_add(ts: Tags, s: string): Tags { return Tags { ...ts, list: ts.list.append(Tag { name: s + "!" }), n: ts.n + 1 }; }
@noinline function tags_total(k: i32): i32 {
    var words: string[] = ["alpha-long-word", "beta-long-word", "gamma-long-word", "delta-long-word"];
    var ts: Tags = Tags { list: [], n: 0 };
    var i: i32 = 0;
    while (i < k) { ts = tags_add(ts, words[i % 4]); i = i + 1; }
    var held: Tags = tags_add(ts, "extra-word");
    var total: i32 = 0;
    for t in ts.list { total = total + t.name.len(); }
    return total * 100 + held.list.len() * 10 + ts.n;
}
@noinline function build_rows(n: i32): i32 {
    var p: P = P { n: 0, xs: [] };
    var i: i32 = 0;
    while (i < n) { p = P { ...p, xs: p.xs.append(i) }; i = i + 1; }
    return p.xs.len() * 10 + p.xs[n - 1];
}
// A counted element type through the same copy: it duplicates every element
// pointer, so both buffers hold a count of each, and the element the store
// replaces gives back the one it held.
@noinline function push_word(ws: string[], w: string): string[] { return ws.append(w + ""); }
@noinline function word_lens(n: i32): i32 {
    var ws: string[] = ["ab", "cd"];
    var vs: string[] = push_word(ws, "xyz");
    return vs.len() * 100 + vs[2].len() * 10 + ws.len() + n;
}
@noinline function set_word_borrowed(ws: string[], w: string): string[] { return ws.with(0, w + ""); }
@noinline function word_set(n: i32): i32 {
    var ws: string[] = ["ab", "cd"];
    var vs: string[] = set_word_borrowed(ws, "wxyz");
    return vs[0].len() * 10 + ws[0].len() + n;
}
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
    var v: str = slice_unchecked(s, 1, 4);
    return v.len() + s.len();
}
@noinline function temp_slice(n: i32): i32 { return slice_unchecked(grown(n), 0, 3).len(); }
@noinline function scan_slices(s: string): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < s.len()) { t = t + slice_unchecked(s, i, i + 1).len(); i = i + 1; }
    return t;
}

// The CHECKED slice, s[a:b], answers Some of a view of the source's bytes
// when the window is in bounds and both ends sit on a codepoint boundary, and
// None when it is not. The Some box holds the view, so the source must outlive
// the box the same way slice_unchecked's does — checked_head slices a borrowed
// parameter, checked_mid keeps a counted local live across the read, and
// checked_temp's source is a temporary whose only use is the slice. The two
// refusals take the None edge, where no view is built at all: checked_miss on
// a bound past the end, checked_split on one that lands inside the é.
@noinline function checked_head(s: string, n: i32): i32 {
    match (s[0:n]) {
        Some(v) => { return v.len(); },
        None => { return 0; }
    }
}
@noinline function checked_mid(n: i32): i32 {
    var s: string = grown(n);
    match (s[1:4]) {
        Some(v) => { return v.len() + s.len(); },
        None => { return 0; }
    }
}
@noinline function checked_temp(n: i32): i32 {
    match (grown(n)[0:3]) {
        Some(v) => { return v.len(); },
        None => { return 0; }
    }
}
@noinline function checked_miss(n: i32): i32 {
    var s: string = grown(n);
    match (s[2:99]) {
        Some(v) => { return v.len(); },
        None => { return s.len(); }
    }
}
@noinline function checked_split(n: i32): i32 {
    var s: string = "héllo";
    var i: i32 = 0;
    while (i < n) { s = s + "!"; i = i + 1; }
    match (s[1:2]) {
        Some(v) => { return v.len(); },
        None => { return s.len(); }
    }
}
// The open forms name no bound of their own: s[lo:] takes the length and s[:hi]
// takes 0, and the base is evaluated ONCE for either. Rewriting the base into
// base.len() to get that bound put two copies of it in the tree and ran a
// side-effecting base twice, so open_base prints and the want holds exactly
// three lines: a doubled base is a failed run, not a silently equal number.
@noinline function open_base(): string {
    print("ob");
    return "abcdef";
}
@noinline function open_window(): i32 {
    var t: i32 = 0;
    match (open_base()[2:]) {
        Some(v) => { t = t + v.len(); },
        None => { t = t + 70; }
    }
    match (open_base()[:3]) {
        Some(v) => { t = t + v.len(); },
        None => { t = t + 70; }
    }
    match (open_base()[:]) {
        Some(v) => { t = t + v.len(); },
        None => { t = t + 70; }
    }
    return t;
}
@try enum Tr { Fine(i32), Bad(string) }

// e? unwraps a two-variant enum: the success payload continues the expression,
// and the failure leaves the function with the same failure rebuilt at its own
// result type. try_quarter takes both edges over an Option of a scalar;
// try_head carries a VIEW off the success edge, so its source has to outlive
// it; try_parse's failure carries a counted string, which the failure edge
// MOVES into the variant it builds rather than copying; and try_loop runs both
// edges five times over, so a leak on either has somewhere to show.
@noinline function try_even(n: i32): Option[i32] {
    if (n % 2 == 0) { return Some(n / 2); }
    return None;
}
@noinline function try_quarter(n: i32): Option[i32] {
    var h: i32 = try_even(n)?;
    return try_even(h);
}
@noinline function try_opt(n: i32): i32 {
    match (try_quarter(n)) {
        Some(v) => { return v; },
        None => { return 60; }
    }
}
@noinline function try_head(s: string): Option[i32] {
    var v: str = s[0:3]?;
    return Some(v.len() + s.len());
}
@noinline function try_view(s: string): i32 {
    match (try_head(s)) {
        Some(v) => { return v; },
        None => { return 50; }
    }
}
@noinline function try_parse(n: i32): Tr {
    if (n > 0) { return Tr.Fine(n); }
    return Tr.Bad(grown(n + 3));
}
@noinline function try_msg(n: i32): Tr {
    var v: i32 = try_parse(n)?;
    return Tr.Fine(v * 2);
}
@noinline function try_loop(k: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0 - 2;
    while (i < k) {
        match (try_msg(i)) {
            Fine(v) => { t = t + v; },
            Bad(m) => { t = t + m.len(); }
        }
        i = i + 1;
    }
    return t;
}
@noinline function checked_scan(s: string): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < s.len()) {
        match (s[i:i + 1]) {
            Some(v) => { t = t + v.len(); },
            None => { t = t + 7; }
        }
        i = i + 1;
    }
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
// A literal whose fields are written out of declaration order. Evaluation stays
// in WRITTEN order — the array is constructed before the string — and the
// construction is still positional, so each value has to reach the slot its
// NAME selects rather than the one its position would.
@noinline function out_of_order(n: i32): i32 {
    var q: Q = Q { p: P { xs: [n, n + 1], n: n }, name: "ab" };
    return q.p.n + q.p.xs[1] + q.name.len();
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
// One byte of a string, typed u8 the way the checker types it and widened to
// an i32 by a written cast, because Fern has no implicit numeric conversion.
// The receiver is read, not consumed, and the byte owns nothing — so it
// outlives the string it came from, which a projection never could. The
// temporary and the dying local are each read at n = 0 and n = 1: a literal's
// box is immortal and its release a no-op, so only the grown one proves a
// COUNTED box is freed after the read.
@noinline function byte_at(s: string, i: i32): i32 { return s[i] as i32; }
@noinline function first_last(s: string): i32 { return (s[0] as i32) + (s[s.len() - 1] as i32); }
@noinline function temp_byte(n: i32): i32 { return grown(n)[1] as i32; }
@noinline function outlives(n: i32): i32 {
    var b: i32 = 0;
    { var s: string = grown(n); b = s[0] as i32; }
    return b;
}
@noinline function checksum(s: string): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < s.len()) { t = t + (s[i] as i32); i = i + 1; }
    return t;
}
// Arithmetic runs in a register wider than the type it is written at, so the
// operators whose result can leave that width mask back to it: 255u8 + 1 is
// 0 and 2147483647 + 1 is INT_MIN, not the wide value the register holds.
// An unsuffixed literal takes the width of what it sits beside, so the 1 and
// the 0x0f below are bytes.
@noinline function byte_wrap(n: u8): i32 { return (n + 1) as i32; }
@noinline function byte_shift(n: u8): i32 { return (n << 1) as i32; }
@noinline function byte_mask(s: string, i: i32): i32 { return (s[i] & 0x0f) as i32; }
// The i32 masks are observed through a COMPARISON, not through a printed
// value: a printed result is truncated to 32 bits on its way out, so it reads
// the same whether the register held the wrapped value or the wide one. A
// signed test does not — 2147483648 is positive where INT_MIN is negative, and
// 4294967296 is not zero where the wrapped product is.
@noinline function wide_wrap(n: i32): i32 {
    if (n + 1 < 0) { return 1; }
    return 0;
}
@noinline function wide_mul(n: i32): i32 {
    if (n * n == 0) { return 1; }
    return 0;
}
// A narrowing masks; the widening back does nothing, because the byte's own
// producing sites already keep it in range.
@noinline function narrow(n: i32): i32 { return (n as u8) as i32; }
// A 64-bit scalar gets a slot of its own, which only wasm spells out — in the
// function's type for a parameter and a result, and in its locals otherwise.
// Every value below needs more than 32 bits, so a body that lost the upper
// half answers differently rather than identically: 1i64 << 40 is 0 in the low
// word where a 32-bit shift would mask the count to 8 and give 256.
@noinline function wide_shift(k: i32): i32 {
    var big: i64 = 1i64 << 40;
    return (big >> (k as i64)) as i32;
}
@noinline function wide_product(n: i32): i32 {
    var b: i64 = (n as i64) * 1000000007i64;
    return (b >> 32) as i32;
}
@noinline function wide_low(n: i32): i32 {
    var b: i64 = (n as i64) * 1000000007i64;
    return (b & 255i64) as i32;
}
// The two narrowing casts out of the wide domain: to the i32 it wraps to, and
// through that to a byte.
@noinline function wide_byte(n: i32): i32 {
    var b: i64 = (n as i64) * 1000000007i64;
    return (b as u8) as i32;
}
@noinline function wide_narrow(n: i32): i32 {
    var b: i64 = (n as i64) * 1000000007i64;
    return b as i32;
}
// Negation is a zero-minus, and the zero has to be pushed at the operand's own
// width or the subtraction's two sides disagree.
@noinline function wide_neg(n: i32): i32 {
    var b: i64 = (n as i64) * 1000000007i64;
    var a: i64 = -b;
    if (a < 0i64) { return (0i64 - a >> 32) as i32; }
    return 0 - 1;
}
// A wide value carried across a loop as a phi, and one written in hexadecimal.
@noinline function wide_count(limit: i32): i32 {
    var t: i64 = 0;
    var i: i32 = 0;
    while (i < limit) { t = t + 4294967296i64; i = i + 1; }
    return (t >> 32) as i32;
}
@noinline function wide_hex(): i32 {
    var m: i64 = 0x100000000i64;
    return (m >> 32) as i32;
}
@noinline function wide_cmp(n: i32): i32 {
    var a: i64 = (n as i64) * 1000000007i64;
    var b: i64 = 5000000035i64;
    if (a < b) { return 1; }
    if (a == b) { return 2; }
    return 3;
}
@noinline function wide_div(n: i32): i32 {
    var b: i64 = (n as i64) * 1000000007i64;
    return (b / 1000000000i64) as i32 + (b % 1000000000i64) as i32;
}
// A wide value crossing a produced-to-produced call, as a result and then as a
// parameter: the one place a lost slot class is a type error rather than a
// wrong number, because wasm spells both in the callee's declared type.
@noinline function wide_of(n: i32): i64 { return (n as i64) * 1000000007i64; }
@noinline function wide_hi(v: i64): i32 { return (v >> 32) as i32; }
@noinline function wide_call(n: i32): i32 { return wide_hi(wide_of(n)); }
// The unsigned widths. Every value below has the slot's sign bit set, so a
// signed opcode answers differently rather than identically: the ordering,
// the division, the remainder and the right shift all read the slot without a
// sign bit, and a u32 arithmetic result that leaves the range wraps back to
// it. A u32 literal past 2^31 has no signed immediate to use and reaches the
// backends as its source text.
@noinline function u32_cmp(n: u32): i32 {
    if (n > 1u32) { return 1; }
    return 0;
}
@noinline function u32_div(n: u32): i32 { return (n / 3u32) as i32; }
@noinline function u32_rem(n: u32): i32 { return (n % 7u32) as i32; }
@noinline function u32_shift(n: u32): i32 { return (n >> 28) as i32; }
@noinline function u32_wrap(n: u32): i32 { return ((n * n) >> 16) as i32; }
// The casts a u32 sits at either end of: an i32 widens into it by taking the
// low word unsigned, and it narrows to the i32 whose sign bit is its top bit,
// to a byte, and — through a real conversion, not a reinterpretation — to and
// from the f64, whose signed truncation would clamp at the signed maximum.
@noinline function u32_widen(n: i32): i32 { return ((n as u32) >> 24) as i32; }
@noinline function u32_signed(n: u32): i32 { return n as i32; }
@noinline function u32_byte(n: u32): i32 { return (n as u8) as i32; }
@noinline function u32_float(n: u32): i32 { return ((n as f64) / 1000000.0) as i32; }
@noinline function u32_of_f64(x: f64): i32 { return ((x as u32) >> 8) as i32; }
// The same at 64 bits, where the value also needs a slot of its own.
@noinline function u64_cmp(): i32 {
    var m: u64 = 1u64 << 63;
    if (m > 1u64) { return 1; }
    return 0;
}
@noinline function u64_div(): i32 {
    var m: u64 = 1u64 << 63;
    return (m / 1000000000000000u64) as i32;
}
@noinline function u64_rem(): i32 {
    var m: u64 = 1u64 << 63;
    return (m % 1000000000u64) as i32;
}
@noinline function u64_shift(): i32 {
    var m: u64 = 1u64 << 63;
    return (m >> 60) as i32;
}
// A widening extends by the SOURCE's signedness: an i32 fills the high half
// from its sign bit, a u32 with zeros.
@noinline function u64_from_i32(n: i32): i32 { return ((n as u64) >> 60) as i32; }
@noinline function u64_from_u32(n: u32): i32 { return ((n as u64) >> 24) as i32; }
@noinline function u64_narrow(): i32 {
    var m: u64 = (1u64 << 63) + 12345u64;
    return ((m as u32) >> 4) as i32;
}
@noinline function u64_float(): i32 {
    var m: u64 = 1u64 << 63;
    return ((m as f64) / 1000000000000000.0) as i32;
}
@noinline function u64_of_f64(x: f64): i32 { return ((x as u64) >> 32) as i32; }
// The string comparisons. Ordering is the runtime's byte compare against
// zero, blind to which box holds the bytes: an owned string, a view of one,
// and a temporary this function owns and has to release all order the same.
@noinline function ord_bits(a: string, b: string): i32 {
    var n: i32 = 0;
    if (a < b) { n = n + 1; }
    if (a <= b) { n = n + 2; }
    if (a > b) { n = n + 4; }
    if (a >= b) { n = n + 8; }
    return n;
}
@noinline function ord_view(s: string): i32 {
    var head: str = slice_unchecked(s, 0, 1);
    var tail: str = slice_unchecked(s, 1, 2);
    if (head < tail) { return 1; }
    return 0;
}
@noinline function ord_temp(a: string): i32 {
    if (a + "!" < "b") { return 1; }
    return 0;
}
// A string view. A slice is one; an owned string bound or passed where a view
// is declared is lent (the box borrowed, never released here); a view passed
// to a borrowed 'string' parameter, a method's receiver included, is lent the
// other way. Every read is blind to which box holds the bytes.
@noinline function view_len(v: str): i32 { return v.len(); }
@noinline function (s: string) copied(): string { return s + ""; }
// The f64: literals, the four operators and the comparisons, negation, the
// conversions from and to both signed widths, a loop-carried float and a float
// crossing produced-to-produced calls as a parameter and a result. Every
// answer leaves as an i32 through a truncation.
@noinline function scale(x: f64, n: i32): f64 { return x * (n as f64) + 0.5; }
@noinline function ratio(a: i32, b: i32): i32 {
    var q: f64 = (a as f64) / (b as f64);
    if (q < 0.0) { q = -q; }
    return (q * 100.0) as i32;
}
@noinline function float_cmp(x: f64, y: f64): i32 {
    if (x == y) { return 0; }
    if (x < y) { return 0 - 1; }
    return 1;
}
@noinline function float_loop(n: i32): i32 {
    var acc: f64 = 0.0;
    var i: i32 = 0;
    while (i < n) { acc = acc + 0.25; i = i + 1; }
    return (acc * 4.0) as i32;
}
@noinline function float_call(n: i32): i32 { return (scale(1.5, n) * 2.0) as i32; }
@noinline function wide_float(n: i64): i32 {
    var d: f64 = n as f64;
    if ((d as i64) != n) { return 0 - 1; }
    return (d / 1000000.0) as i32;
}
// A wide FIELD is read at its own width: the box is built by the AST-lowered
// main and every slot is 8 bytes, so only wasm's typed load can tell an f64
// or i64 field from a pointer in the low half of its slot.
struct WideRec { d: f64, n: i64, s: string }
@noinline function wide_fields(own w: WideRec): i32 {
    var d: f64 = w.d;
    return (d * 2.0) as i32 + (w.n >> 32) as i32 + w.s.len();
}
@noinline function span_wide(own sp: Span): i32 {
    match (sp) {
        Wide(d, s) => { return (d * 4.0) as i32 + s.len(); },
        Empty => { return 0; }
    }
    return 0 - 1;
}
// A wide FIELD is stored at its own width too: the construction names its
// declaration, which is where wasm's typed store reads the width, so an f64
// or i64 written here is read back whole by the AST-lowered and the produced
// readers alike.
@noinline function mk_wide(d: f64, n: i64, s: string): i32 {
    var w: WideRec = WideRec { d: d, n: n, s: s };
    return wide_fields(w);
}
@noinline function mk_span(d: f64): i32 {
    var sp: Span = Wide(d, "abc");
    return span_wide(sp);
}
// A 64-bit ARRAY element. Each element op carries its own slot width, so what
// a literal, a push or a replacement writes is what a read hands back: on wasm
// an i32[] packs four-byte slots and these do not, so a width lost anywhere on
// that path reads back half a value or its neighbour's. The i64 and the f64
// share the eight-byte stride and differ in the load and the store.
@noinline function wide_lit(n: i64): i64[] { return [n, n * 3i64, n + 1i64]; }
@noinline function wide_sum(own xs: i64[]): i64 {
    var total: i64 = 0i64;
    var i: i32 = 0;
    while (i < xs.len()) { total = total + xs[i]; i = i + 1; }
    return total;
}
@noinline function wide_lit_sum(n: i64): i32 { return (wide_sum(wide_lit(n)) >> 32) as i32; }
// The pushes cross the capacity doublings, so the un-share copy and the
// reclaim-on-grow both run at the stride these ops choose. The element written
// first is read back after the last grow: a buffer released early reads back
// as something other than what went into it, which a balanced allocation count
// alone would not report.
@noinline function wide_grow(n: i64, k: i32): i32 {
    var xs: i64[] = [];
    var i: i32 = 0;
    while (i < k) { xs = xs.append(n + (i as i64)); i = i + 1; }
    if (xs.len() != k) { return 0 - 1; }
    if (k > 0 && xs[0] != n) { return 0 - 2; }
    return (wide_sum(xs) >> 32) as i32;
}
@noinline function wide_set(own xs: i64[], i: i32, v: i64): i64[] { return xs.with(i, v); }
// The donor is shared, so the replacement forks a copy at the same stride and
// the donor keeps the element it had.
@noinline function wide_copy_set(xs: i64[], v: i64): i32 {
    var ys: i64[] = wide_set(xs, 1, v);
    if (ys[1] != v) { return 0 - 1; }
    if (xs[1] == v) { return 0 - 2; }
    return ((ys[1] + xs[1]) >> 32) as i32;
}
// The eight-byte element ops name the SLOT, not the sign, so a u64 element
// takes the same ones an i64 does. Every value here lives above 2^32,
// so a 32-bit slot loses the whole high word and the readback reports it.
@noinline function uwide_lit(n: u64): u64[] { return [n, n * 3u64, n + 1u64]; }
@noinline function uwide_sum(own xs: u64[]): u64 {
    var total: u64 = 0u64;
    var i: i32 = 0;
    while (i < xs.len()) { total = total + xs[i]; i = i + 1; }
    return total;
}
@noinline function uwide_lit_sum(n: u64): i32 { return (uwide_sum(uwide_lit(n)) >> 32u64) as i32; }
@noinline function uwide_grow(n: u64, k: i32): i32 {
    var xs: u64[] = [];
    var i: i32 = 0;
    while (i < k) { xs = xs.append(n + (i as u64)); i = i + 1; }
    if (xs.len() != k) { return 0 - 1; }
    if (k > 0 && xs[0] != n) { return 0 - 2; }
    return (uwide_sum(xs) >> 32u64) as i32;
}
@noinline function uwide_set(own xs: u64[], i: i32, v: u64): u64[] { return xs.with(i, v); }
@noinline function uwide_copy_set(xs: u64[], v: u64): i32 {
    var ys: u64[] = uwide_set(xs, 1, v);
    if (ys[1] != v) { return 0 - 1; }
    if (xs[1] == v) { return 0 - 2; }
    return ((ys[1] + xs[1]) >> 32u64) as i32;
}
// A 64-bit and an f64 TUPLE element, the one container whose construction had
// no width to write at. A tuple's slots are eight bytes on every backend and
// the READ has always named its own (op_tuple_get_w); the construction now
// spells the per-element kinds (op_tuple_make_k), so a width lost there stores
// four bytes into an eight-byte slot and the read hands back half a value. The
// string element beside the wide one puts the drop walk over the same box.
@noinline function wide_pair(n: i64): (i64, i32) { return (n * 3i64, 7); }
@noinline function wide_pair_sum(n: i64): i32 {
    var p: (i64, i32) = wide_pair(n);
    return ((p.0 >> 32) as i32) + p.1;
}
@noinline function float_pair(x: f64): (f64, string) { return (x * 2.0, "ab"); }
@noinline function float_pair_sum(x: f64): i32 {
    var p: (f64, string) = float_pair(x);
    return (p.0 as i32) + p.1.len();
}
@noinline function float_arr(x: f64): f64[] { return [x, x * 2.0, x + 1.0]; }
@noinline function float_sum(own ds: f64[]): f64 {
    var total: f64 = 0.0;
    var i: i32 = 0;
    while (i < ds.len()) { total = total + ds[i]; i = i + 1; }
    return total;
}
@noinline function float_lit_sum(x: f64): i32 { return (float_sum(float_arr(x)) * 10.0) as i32; }
@noinline function float_grow(x: f64, k: i32): i32 {
    var ds: f64[] = [];
    var i: i32 = 0;
    while (i < k) { ds = ds.append(x + (i as f64)); i = i + 1; }
    if (ds.len() != k) { return 0 - 1; }
    if (ds[0] != x) { return 0 - 2; }
    ds = ds.with(0, x * 4.0);
    return (float_sum(ds) * 2.0) as i32;
}
// One element replaced: in place when the received unit is the box's only
// one and in a copy otherwise, chosen by the count at run time. A counted
// element type retains the copy's elements and releases the one replaced.
@noinline function set_at(own xs: i32[], i: i32, v: i32): i32[] { return xs.with(i, v); }
@noinline function fill_squares(n: i32): i32 {
    var xs: i32[] = [];
    var i: i32 = 0;
    while (i < n) { xs = xs.append(0); i = i + 1; }
    i = 0;
    while (i < n) { xs = xs.with(i, i * i); i = i + 1; }
    return xs[n - 1] + xs.len();
}
@noinline function copy_set(xs: i32[]): i32 {
    var ys: i32[] = set_at(xs, 0, 7);
    return ys[0] + xs[0];
}
// A with on a record FIELD the frame reads no further through — the
// functional update P { ...p, xs: p.xs.with(i, v) } — is admitted for the
// in-place write as the field append is (field_grow_root): the element is
// written into the record's own buffer when the record and the buffer are
// each sole-held, and into a copy otherwise, so a second holder of the buffer
// keeps its value. A string element replaced in place is released.
@noinline function set_kept(own p: P, i: i32, v: i32): P { return P { ...p, xs: p.xs.with(i, v) }; }
@noinline function fill_field(n: i32): i32 {
    var p: P = P { n: n, xs: [] };
    var i: i32 = 0;
    while (i < n) { p = P { ...p, xs: p.xs.append(0) }; i = i + 1; }
    i = 0;
    while (i < n) { p = set_kept(p, i, i * 3); i = i + 1; }
    return p.xs[n - 1] * 10 + p.xs.len();
}
@noinline function set_shared_field(n: i32): i32 {
    var p: P = P { n: n, xs: [1, 2, 3] };
    var ys: i32[] = p.xs;
    p = set_kept(p, 0, 9);
    return p.xs[0] * 100 + ys[0] * 10 + p.n;
}
@noinline function set_word_field(own w: Words, s: string): Words { return Words { ...w, ws: w.ws.with(1, s) }; }
@noinline function word_field_set(n: i32): i32 {
    var w: Words = Words { ws: ["ab", "cde"], k: n };
    w = set_word_field(w, "fghij" + "");
    var held: string[] = w.ws;
    w = set_word_field(w, "z" + "");
    return w.ws[1].len() * 100 + held[1].len() * 10 + w.k;
}
@noinline function set_word(own ws: string[], w: string): string[] { return ws.with(1, w); }
@noinline function word_swap(n: i32): i32 {
    var ws: string[] = ["ab", "cde"];
    ws = set_word(ws, "fghi");
    return ws[1].len() + n;
}
@noinline function shared_word(n: i32): i32 {
    var ws: string[] = ["ab", "c"];
    var vs: string[] = set_word(ws, "z");
    return vs[1].len() * 10 + ws[1].len() + n;
}
@noinline function set_p(own ps: P[], p: P): i32 {
    var qs: P[] = ps.with(0, p);
    return qs[0].n + qs[0].xs.len() + qs.len();
}
// A tuple destructure: each name is a projection of the one initializer
// value, which stays live under those borrows; a discard is a binding no
// read follows.
@noinline function halves(n: i32): (i32, i32[]) { return (n, [n, n]); }
@noinline function unpack(n: i32): i32 {
    var (k, xs) = halves(n);
    return k + xs.len();
}
@noinline function unpack_discard(n: i32): i32 {
    var (_, xs) = halves(n);
    var (m, _) = halves(n + 1);
    return xs[0] + m;
}
@noinline function unpack_words(s: string): i32 {
    var (w, count) = (s, s.len());
    return w.len() + count;
}
// A nested tuple position is projected and destructured again, a struct
// pattern's names are field projections, and the at-binder names the whole.
struct Dp { k: i32, name: string, tail: i32[] }
@noinline function mk_dp(n: i32): Dp { return Dp { k: n, name: "dp" + "!", tail: [n, n, n] }; }
@noinline function struct_unpack(n: i32): i32 {
    var Dp { k, name: nm, .. } = mk_dp(n);
    return k + nm.len();
}
@noinline function at_unpack(n: i32): i32 {
    var whole @ Dp { tail, .. } = mk_dp(n);
    return tail.len() + whole.name.len() + whole.k;
}
@noinline function nested_unpack(n: i32): i32 {
    var (a, (w, xs)) = (n, ("ab" + "c", [n, n]));
    return a + w.len() + xs.len();
}
// A for header takes the same pattern a declaration does, destructuring
// each element.
@noinline function for_pairs(n: i32): i32 {
    var xs: (i32, string)[] = [(n, "a" + "b"), (n + 1, "c")];
    var total: i32 = 0;
    for (k, w) in xs { total = total + k + w.len(); }
    var deep: ((i32, i32), string)[] = [((n, 2), "x" + "y")];
    for ((a, b), s) in deep { total = total + a * b + s.len(); }
    return total;
}
// A module-level constant is a zero-parameter function to the parser, and a
// bare reference to one is a call; a string constant hands back a unit the
// reader owns.
const BASE: i32 = 40;
const TAG: string = "ab";
@noinline function based(n: i32): i32 { return BASE + n; }
@noinline function tagged(n: i32): i32 { return TAG.len() + n; }
// A void call stands as a statement, of a produced void function and of the
// runtime builtins with a contract: the writer copies the bytes it is lent,
// the builder round-trips its appends into an owned string, and the byte
// search reads its string.
@noinline function tick(n: i32) { if (n > 0) { print("tick"); } }
@noinline function ticked(n: i32): i32 { tick(n); return n + 1; }
@noinline function built(s: string): i32 {
    strbuf_reset();
    strbuf_append("ab");
    strbuf_append(s);
    var t: string = strbuf_take();
    return t.len();
}
@noinline function find_byte(s: string, b: i32): i32 { return __memchr(s, b, 1); }
@noinline function scan_views(s: string): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i + 2 <= s.len()) {
        var w: str = slice_unchecked(s, i, i + 2);
        if (w == "cd") { t = t + 100; }
        t = t + w.len() + view_len(w) + text_size(w) + (w[0] as i32);
        i = i + 1;
    }
    return t;
}
@noinline function lent_views(n: i32): i32 {
    var s: string = grown(n);
    var v: str = s;
    var u: str = v;
    var inner: str = slice_unchecked(v, 1, 3);
    var o: string = inner.copied();
    var p: string = u.copied() + o;
    return v.len() + u.len() + inner.len() + o.len() + p.len() + view_len(s);
}
// The source is a temporary whose only use is the slice; the view of a view
// keeps both alive across its reads.
@noinline function view_of_temp(n: i32): i32 {
    var v: str = slice_unchecked(grown(n), 0, 2);
    var w: str = slice_unchecked(v, 1, 2);
    if (w != "b") { return 0 - 1; }
    return v.copied().len() + w.len();
}
@noinline function upper(s: string, i: i32): i32 {
    var c: u8 = s[i];
    if (c >= b'a' && c <= b'z') { c = c - 32; }
    return c as i32;
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
function wide_literal_tree(n: i64): i32 {
    var x: i64 = 0 - 1;
    var y: i64 = n + 1;
    if (y < 0 - 2) { return (x * 2) as i32; }
    return (y - 4999999990i64) as i32;
}
function count_byte(s: string, b: u8): i32 {
    var n: i32 = 0;
    for c in s { if (c == b) { n = n + 1; } }
    return n;
}
function leaf_pair(n: i32): (Node, i32) {
    var p: (Node, i32) = (Leaf { n: n }, n);
    return p;
}
function leaves(n: i32): Node[] {
    var xs: Node[] = [Leaf { n: n }, Twig { xs: [n, n + 1] }];
    return xs;
}
function leaf_pair_size(n: i32): i32 {
    var p: (Node, i32) = leaf_pair(n);
    return node_size(p.0) + p.1;
}
function leaves_size(n: i32): i32 {
    var xs: Node[] = leaves(n);
    return xs.len() + node_size(xs[1]);
}
function shift_wide(n: i64, k: i32): i32 { return ((n << k) + (n >> 3)) as i32; }
function bail(code: i32): i32 {
    if (code > 0) { write("bail"); exit(code); }
    return code;
}
function bytes_text(bs: u8[]): string { return string_from_bytes_unchecked(bs); }
function bit_round(x: f64): i32 { return f64_from_bits(f64_bits(x)) as i32; }
// A for-loop element is a checker binding inside the loop body, so a record
// or variant literal over the element has a type instead of collapsing to the
// unknown this boundary refuses. Each construction takes its own unit of the
// reference field it copies through, and the element itself stays borrowed
// from the container for the step.
@noinline function bump_each(n: i32): i32 {
    var ps: P[] = [make(n), make(n + 1)];
    var t: i32 = 0;
    for p in ps {
        var q: P = P { ...p, n: p.n + 1 };
        var d = q.n + p.xs.len();
        t = t + d + q.xs.len();
    }
    return t;
}
@noinline function line_each(n: i32): i32 {
    var t: i32 = 0;
    for k in fill(n) { var s: Shape = Line(k + 1); t = t + measure(s); }
    return t;
}
@noinline function word_recs(n: i32): i32 {
    var t: i32 = 0;
    for w in words(n) {
        var q: Q = Q { name: w, p: P { n: w.len(), xs: [n] } };
        var m = q.p.n + w.len();
        t = t + q.name.len() + m + q.p.xs[0];
    }
    return t;
}
// A Cell[T] is the language's one mutable slot and IS a one-element array box,
// so a cell-typed field is a counted reference: the container retains it at a
// construction and releases it at a drop, walked at the cell's element. main
// holds the only cell_new and hands every holder straight to an own parameter,
// because the AST lowering does not reclaim a cell-typed field at all
// (docs/SELFHOST-SEMANTIC-SOURCE.md records the reproduction).
struct Slot { c: Cell[i32], n: i32 }
struct Note { w: Cell[string], n: i32 }
enum Held { Bare(i32), Celled(Cell[i32], i32) }
// A nested arm pattern desugars at parse time into a done-flag chain of
// flat matches that falls through by construction; the checker reads the
// chain as diverging when every arm returns, so the body's end is
// unreachable and aborts rather than being refused. A guard is read after
// the payload bindings and a false one falls to the next arm.
enum In2 { Ok2(i32), Er2(i32) }
enum Out2 { Pr(In2, In2), Qn(i32) }
@noinline function nested_arms(v: Out2): i32 {
    match (v) {
        Pr(Ok2(a), Ok2(b)) => { return a + b; },
        Pr(Ok2(a), _) => { return 100 + a; },
        Pr(_, Ok2(b)) => { return 200 + b; },
        Qn(n) => { return 1000 + n; },
        _ => { return 0; },
    }
}
@noinline function mk_out(k: i32): Out2 {
    if (k == 0) { return Pr(Ok2(1), Ok2(2)); }
    if (k == 1) { return Pr(Ok2(1), Er2(9)); }
    if (k == 2) { return Pr(Er2(1), Ok2(5)); }
    if (k == 3) { return Pr(Er2(1), Er2(2)); }
    return Qn(4);
}
@noinline function nested_case(k: i32): i32 {
    var v: Out2 = mk_out(k);
    return nested_arms(v);
}
@noinline function guarded_pick(n: i32): i32 {
    var o: Option[i32] = Some(n);
    match (o) {
        Some(k) when k > 5 => { return k * 2; },
        Some(k) => { return k; },
        None => { return 0; },
    }
}
@noinline function guarded_words(short: boolean, min: i32): i32 {
    var ws: string[] = ["ab", "cdef", "g"];
    if (short) { ws = ["ab"]; }
    var total: i32 = 0;
    for w in ws {
        var o: Option[string] = Some(w + "");
        match (o) {
            Some(s) when s.len() >= min => { total = total + s.len(); },
            _ => { total = total + 1; },
        }
    }
    return total;
}
// A guard that short-circuits ends in a block of its own, and the next arm's
// test lists that block, not the arm's, among its predecessors.
@noinline function guarded_and(k: i32, w: string): i32 {
    var o: Option[string] = Some(w + "");
    match (o) {
        Some(s) when k > 0 && s.len() > 2 => { return s.len() * 10; },
        Some(s) => { return s.len(); },
        None => { return 0; },
    }
}
// A path headed by the union's own name constructs the variant, or names it
// in a pattern; a path headed by a struct's name calls the associated
// function its impl declared.
enum Qe { Qa(string), Qb }
trait Qmk { function make(n: i32): Self; }
struct Qp { v: i32, tag: string }
impl Qmk for Qp { function make(n: i32): Self { return Qp { v: n * 2, tag: "q" }; } }
@noinline function qualified_pick(k: i32): i32 {
    var e: Qe = Qe.Qb;
    if (k > 0) { e = Qe.Qa("abc" + "d"); }
    var o: Option[i32] = Option.Some(k);
    var n: i32 = 0;
    match (o) { Some(m) => { n = m; }, None => { n = 0 - 1; } }
    match (e) {
        Qe.Qa(s) => { return s.len() + n; },
        Qe.Qb => { return n; },
    }
}
@noinline function assoc_make(k: i32): i32 {
    var p: Qp = Qp.make(k);
    return p.v + p.tag.len();
}
// A match expression the tuple or struct desugar routes through a value
// local: the block declares the local, runs the done-flag chain in the
// enclosing block, and its trailing return reads the local.
@noinline function tm_word(k: i32): string {
    var t: (string, i32) = ("elem", k);
    return match (t) {
        (q, 4) => q + "!",
        (q, n) => q
    };
}
@noinline function tm_sum(n: i32): i32 {
    var total: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var p = (i, n - i);
        total = total + match (p) { (0, b) => b * 10, (a, b) when a == b => a + b, (a, _) => a };
        i = i + 1;
    }
    return total;
}
@noinline function tm_words(n: i32): i32 {
    var ws: string[] = ["ab", "", "cde"];
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var pair = (ws[i % 3], ws[i % 3].len());
        var s: string = match (pair) { (t, 0) => "empty", (t, _) => t + "." };
        acc = acc + s.len();
        i = i + 1;
    }
    return acc;
}
@noinline function tm_show(k: i32): i32 { var s: string = tm_word(k); print(s); return s.len(); }
struct Pt2 { x: i32, y: i32, tag: string }
@noinline function sm_pick(k: i32): string {
    var p: Pt2 = Pt2 { x: k, y: k * 2, tag: "pt" };
    return match (p) {
        Pt2 { x: 1, y, tag } => tag + ":" + tag,
        Pt2 { x, y, tag } => tag
    };
}
@noinline function sm_show(k: i32): i32 { var s: string = sm_pick(k); print(s); return s.len(); }
@noinline function slot_n(own s: Slot): i32 { return s.n; }
@noinline function note_n(own t: Note): i32 { return t.n; }
@noinline function held_n(own h: Held): i32 {
    match (h) {
        Bare(k) => { return k; },
        Celled(_, k) => { return k + 1; }
    }
    return 0 - 1;
}
@noinline function slot_share(s: Slot): i32 { return slot_n(Slot { c: s.c, n: s.n + 1 }); }
@noinline function note_share(t: Note): i32 { return note_n(Note { w: t.w, n: t.n + 2 }); }
@noinline function slot_pair(own s: Slot): i32 { var k: i32 = slot_share(s); return k + slot_n(s); }
@noinline function note_pair(own t: Note): i32 { var k: i32 = note_share(t); return k + note_n(t); }
@noinline function slot_held(own s: Slot): i32 { var cc: Cell[i32] = s.c; return held_n(Celled(cc, s.n)); }
// The cell's own vocabulary: a write is seen by every holder of the box, a
// read of a reference element is a unit of its own that outlives the write
// that replaces it, and a wide element uses the slot's own width.
@noinline function cell_count(n: i32): i32 {
    var c: Cell[i32] = cell_new(n);
    var i: i32 = 0;
    while (i < 3) { c.set(c.get() + 2); i = i + 1; }
    return c.get();
}
@noinline function cell_share(n: i32): i32 {
    var c: Cell[i32] = cell_new(n);
    var s: Slot = Slot { c: c, n: 1 };
    c.set(n + 5);
    return s.c.get() + s.n;
}
@noinline function cell_words(w: string): i32 {
    var c: Cell[string] = cell_new(w + "a");
    var first: string = c.get();
    c.set(first + "b");
    var churn: string[] = [];
    var i: i32 = 0;
    while (i < 12) { churn = churn.append("junk"); i = i + 1; }
    return first.len() + c.get().len() + churn.len() - 12;
}
@noinline function cell_wide(n: i64): i32 {
    var c: Cell[i64] = cell_new(n);
    c.set(c.get() + 1);
    return c.get() as i32;
}
@noinline function cell_float(x: f64): i32 {
    var c: Cell[f64] = cell_new(x);
    c.set(c.get() * 2.0);
    return c.get() as i32;
}
@noinline function cell_closure(n: i32): i32 {
    var c: Cell[i32] = cell_new(n);
    var bump: () => i32 = (): i32 => { c.set(c.get() + 1); return c.get(); };
    bump();
    bump();
    return c.get();
}
// The 32-bit float: a conversion into it rounds to single precision, so the
// odd integer past 2^24 rounds back; a literal of it, an operator at it and a
// declared field of it carry the same rounding, which the bit pattern shows.
struct Half { v: f32, n: i32 }
@noinline function f32_round_int(x: f64): i32 { return ((f32_from_bits(f32_bits(x as f32))) as f64) as i32; }
@noinline function f32_lit_bits(): i32 { var y: f32 = 0.1f32; return f32_bits(y); }
@noinline function f32_sum_bits(a: f32, b: f32): i32 { return f32_bits(a + b); }
@noinline function f32_field(x: f64): i32 { var h: Half = Half { v: x as f32, n: 1 }; return f32_bits(h.v) + h.n; }
@noinline function f32_cmp(a: f32, b: f32): i32 { if (a < b) { return 1; } return 0; }
@noinline function f32_from_int(n: i32): i32 { return f32_bits(n as f32); }
// The string builder through its pointer-width handle: the handle round-trips
// through an i64 and back, the whole address on a register backend, and the
// bytes come back as a string of this function's own.
@noinline function buf_text(k: i32): i32 {
    var b: usize = buf_new(8);
    var i: i32 = 0;
    while (i < k) { buf_push(b, "ab"); i = i + 1; }
    buf_push_range(b, "xyz", 1, 3);
    buf_push_byte(b, 33);
    var n: i32 = buf_len(b);
    var s: string = buf_take(b);
    buf_free(b);
    return n + s.len();
}
@noinline function buf_handle_round(k: i32): i32 {
    var b: usize = buf_new(4);
    var w: i64 = b as i64;
    var back: usize = w as usize;
    buf_push(b, "q");
    var n: i32 = buf_len(back);
    buf_free(back);
    return n + k;
}
// Mixed-width address arithmetic against a real address, which is what the
// widening is for: the offsets are the differences from the base, so a
// widening placed on the wrong side or one that dropped a high half moves the
// answer. Which extension it used is NOT visible here — a difference is taken
// at the low 32 bits, where the signed and the unsigned widening of the same
// offset agree; addr_order's compare is what reads the sign. The last term is
// the byte destination, whose mask a truncation alone would not apply: 300
// leaves the address as 44.
// Reading an address back as the box it names reinterprets rather than
// converts, so it moves no value and takes no unit of its own: the address
// borrows the box, which stays its owner's to release. This is core/map's key
// column — __map_hash_str and __map_eq_str take a usize out of a slot and read
// it as the string it points at — and main holds the boxes, so a frame here
// that released one would be reading freed memory.
@noinline function addr_text(p: usize): i32 { return (p as string).len(); }
@noinline function addr_eq(p: usize, q: usize): i32 {
    if ((p as string) == (q as string)) { return 1; }
    return 0;
}
@noinline function addr_walk(n: i32): i32 {
    var base: usize = buf_new(64);
    var fwd: usize = base + n;
    var neg: i32 = 0 - 3;
    var back: usize = base + neg;
    var wide: usize = base - 5i64;
    var byte: u8 = 7;
    var up: usize = base + byte;
    var nested: usize = base + n + n * 2;
    var d: i32 = (fwd as i32) - (base as i32);
    d = d + ((back as i32) - (base as i32));
    d = d + ((wide as i32) - (base as i32));
    d = d + ((up as i32) - (base as i32));
    d = d + ((nested as i32) - (base as i32));
    var lit: usize = 300;
    d = d + ((lit as u8) as i32);
    buf_free(base);
    return d;
}
// The width comes off an operand here, since the whole expression is the
// boolean — and off whichever operand carries it, which is why both orders
// are written.
//
// The last compare is the one that reads the SIGN of the widening, which no
// difference can: an address is compared unsigned over its whole width, so a
// sign-extended -3 lands below the base and a zero-extended one lands 4 GiB
// above it. On wasm both answer true and must, since the address is the i32
// there and the two widenings are the same value.
@noinline function addr_order(n: i32): i32 {
    var base: usize = buf_new(16);
    var k: i32 = 0;
    if (base > n) { k = k + 1; }
    if (n < base) { k = k + 2; }
    var neg: i32 = 0 - 3;
    if (base + neg < base) { k = k + 4; }
    buf_free(base);
    return k;
}
// The if-EXPRESSION value block. It is inlined, not called, so its arms join
// at a phi: a reference value is a unit of this function's own on whichever
// arm the branch took, and an arm handing over the function's own counted
// parameter leaves the other arm to release it — from both sides, since the
// arms supply the phi in written order. pick_word's string goes to a produced
// caller, as every reference result here does: an AST-lowered main releases
// none of them.
@noinline function pick_len(n: i32): i32 { var xs: i32[] = if (n > 1) { [n, n + 1] } else { [n] }; return xs.len() + xs[0]; }
@noinline function pick_word(n: i32): string { var w: string = if (n > 0) { "ab" + "c" } else { "d" }; return w; }
@noinline function pick_word_len(n: i32): i32 { return pick_word(n).len(); }
@noinline function pick_kept(n: i32, own ys: i32[]): i32 { var zs: i32[] = if (n > 0) { ys } else { [0, 0, 0] }; return zs.len(); }
@noinline function pick_flip(n: i32, own ys: i32[]): i32 { var zs: i32[] = if (n <= 0) { [0, 0, 0] } else { ys }; return zs.len(); }
@noinline function pick_nested(n: i32): i32 { var k: i32 = if (n > 2) { 2 } else if (n > 0) { var d: i32 = n + 4; d } else { 0 }; return k; }
// Function values and the calls through them. A value is the environment box
// the lambda lift builds: a bare name reaches its callee through a trampoline
// that ignores the box, a capturing lambda through one carrying its captures
// in the slots after the address. The box is a unit of the constructing
// function's own, so these cover where it dies — after the call that lent it,
// once per step when a loop builds one, at the rebinding that replaces it, and
// on the arm a branch join did not take. Also a scalar and a reference
// argument lent across an indirect call, one whose counted result the caller
// owns and one whose result it discards.
@noinline function dbl(x: i32): i32 { return x * 2; }
@noinline function negate(x: i32): i32 { return 0 - x; }
@noinline function apply_int(f: (i32) => i32, x: i32): i32 { return f(x); }
@noinline function call_twice(n: i32): i32 { return apply_int(dbl, n) + apply_int(dbl, 1); }
// A box that captures BOTH a borrowed function value and an owned array: it
// takes the array's unit and borrows the function's, so the array is released
// with the box and the function value with the caller's own.
@noinline function via_cap(f: (i32) => i32, n: i32): i32 {
    var ws: string[] = ["alpha", "beta"];
    var r: i32 = apply_int((x: i32): i32 => { return f(x) + ws.len(); }, n);
    return r + ws[1].len();
}
// The bare function names reach the borrowed slot from PRODUCED code, so both
// boxes are this boundary's to build and release.
@noinline function cap_fn(n: i32): i32 { return via_cap(dbl, n) + via_cap(negate, n); }
// A map owns one unit of every key in its string column, released with the map
// by its own helper; its box carries no count, so every unit of the map itself
// is moved. The keys here are a mix of fresh temporaries, borrowed parameters
// and array elements, and one of them is inserted twice so an overwrite
// releases the key it supersedes.
@noinline function map_tally(a: string, b: string): i32 {
    var m: Map[string, i32] = map_new(4);
    m = m.insert(a, 1);
    m = m.insert(a + b, 2);
    m = m.insert(a, m.get_or(a, 0) + 10);
    var n: i32 = m.get_or(a, 0) + m.get_or(a + b, 0);
    if (m.has(b)) { n = n + 100; }
    return n;
}
@noinline function map_words(a: string, b: string): i32 {
    var ws: string[] = [a, b, a + b, a];
    var m: Map[string, boolean] = map_new(ws.len() + 1);
    for w in ws { m = m.insert(w, true); }
    var n: i32 = 0;
    for w in ws { if (m.has(w + "")) { n = n + 1; } }
    return n;
}
// A map built in one frame and handed to another: the callee takes the unit
// and releases it, so nothing is left for the caller to drop.
@noinline function map_eat(own m: Map[string, i32], k: string): i32 { return m.get_or(k, 0); }
// The one intrinsic whose result the caller owns: __alloc_u8 hands back a
// fresh zeroed buffer, counted like any other array, so the frame that takes
// it drops it. The byte scans beside it are LENT their string and keep
// nothing of it, so a fresh one handed to a scan dies here rather than in the
// map's column.
@noinline function alloc_bytes(n: i32): i32 {
    var buf: u8[] = __alloc_u8(n);
    var second: u8[] = __alloc_u8(n + 1);
    return buf.len() + second.len();
}
@noinline function scan_temp(a: string, b: string): i32 {
    var joined: string = a + b;
    return __count_byte(joined, 97) + __sum_bytes(joined) + __ascii_run(joined, 0);
}
@noinline function float_bits(x: f64): i32 {
    return (__floor_f64(__sqrt_f64(x)) as i32) + __popcount32(255u32) + __ctz32(8u32);
}
// A string method that answers TEXT hands back a fresh box this frame owns and
// drops. The receiver here is a fresh concatenation, so a method that retained
// it would strand a box and one that consumed it would leave the next call
// reading freed bytes.
@noinline function text_methods(a: string, b: string): i32 {
    var joined: string = a + b;
    var up: string = joined.to_ascii_upper();
    var parts: string[] = joined.split(a);
    return up.len() + parts.len() + joined.trim().len() + joined.repeat(2).len();
}
@noinline function map_hand(k: string): i32 {
    var m: Map[string, i32] = map_new(2);
    return map_eat(m.insert(k, 7), k);
}
// A method on a generic receiver is a template the receiver's type
// instantiates: once at i32, once at string, whose payload the instance
// hands back retained.
function (o: Option[T]) has_it(): boolean { match (o) { Some(_) => { return true; }, None => { return false; } } }
function (o: Option[T]) or_val(fallback: T): T { match (o) { Some(x) => { return x; }, None => { return fallback; } } }
@noinline function opt_has(n: i32): i32 {
    var o: Option[i32] = Some(n);
    var none: Option[i32] = None;
    var t: i32 = 0;
    if (o.has_it()) { t = t + o.or_val(0); }
    if (!none.has_it()) { t = t + none.or_val(100); }
    return t;
}
@noinline function opt_words(w: string): i32 {
    var o: Option[string] = Some(w + "!");
    var none: Option[string] = None;
    var a: string = o.or_val("none");
    var b: string = none.or_val("no" + "ne");
    return a.len() * 10 + b.len();
}
// A get answers an Option in a box of the frame's own, released as any
// Option is; the value column is narrow, so the payload is a copy.
@noinline function map_get_hit(n: i32): i32 {
    var m: Map[string, i32] = map_new(4);
    m = m.insert("k" + "1", n);
    var t: i32 = 0;
    match (m.get("k" + "1")) {
        Some(v) => { t = v; },
        None => { t = 0 - 1; },
    }
    if let Some(w) = m.get("zz") { t = t + w; }
    return t;
}
@noinline function labelled_sum(n: i32): i32 {
    var total: i32 = 0;
    var i: i32 = 0;
    outer: while (i < n) {
        i = i + 1;
        var j: i32 = 0;
        while (j < 5) {
            var w: string = "a" + "b";
            j = j + 1;
            if (j == i) { total = total + w.len(); continue outer; }
            if (j == 4) { break outer; }
            total = total + 1;
        }
    }
    return total;
}
@noinline function inc_by(n: i32, by: i32 = 1): i32 { return n + by; }
@noinline function inc_calls(n: i32): i32 { return inc_by(n, 1) + inc_by(n, 5); }
function (p: Pt2) show(): void { print(p.tag + "!"); }
@noinline function show_pt(k: i32): i32 { var p: Pt2 = Pt2 { x: k, y: 1, tag: "p" + "t" }; p.show(); return p.x; }
@noinline function map_lit_words(n: i32): i32 {
    var m: Map[string, i32] = Map { "a" + "b": n, "c": 2 };
    return m.get_or("ab", 0) * 10 + m.get_or("c", 0);
}
@noinline function map_get_int(n: i32): i32 {
    var m: Map[i32, i32] = map_new(4);
    m = m.insert(n, n * 3);
    var t: i32 = 0;
    if let Some(v) = m.get(n) { t = t + v; }
    if let Some(v) = m.get(n + 1) { t = t + 1000; }
    return t;
}
// A COUNTED value column: the map owns a unit of every value it holds, so the
// overwrite has to release what it supersedes and the read has to hand back a
// unit of its own rather than the column's.
@noinline function map_vstr(a: string, b: string): i32 {
    var m: Map[string, string] = map_new(4);
    m = m.insert(a + "", a + b);
    m = m.insert(a + "", b + a + b);
    m = m.insert(b + "", a + "");
    var got: string = m.get_or(a + "", "");
    return got.len() + m.get_or(b + "", "").len() + m.get_or("absent", "xy").len();
}
// The same over a column of string ARRAYS, whose release walks each value's
// elements as well as its buffer.
@noinline function map_vwords(a: string, b: string): i32 {
    var m: Map[string, string[]] = map_new(4);
    m = m.insert(a + "", [a + b, b + ""]);
    m = m.insert(a + "", [b + a]);
    return m.get_or(a + "", []).len() + m.get_or("absent", [a + ""]).len();
}
// An integer key column holds no unit per key, so the map is freed whole with
// __fern_map_free; a string value column beside it is still counted, released
// on overwrite and with the map.
@noinline function map_ints(n: i32): i32 {
    var m: Map[i32, i32] = map_new(4);
    var i: i32 = 0;
    while (i < n) { m = m.insert(i, i * i); i = i + 1; }
    m = m.insert(2, m.get_or(2, 0) + 100);
    var t: i32 = 0;
    i = 0;
    while (i < n + 2) { t = t + m.get_or(i, 0 - 1); i = i + 1; }
    if (m.has(n)) { t = t + 1000; }
    return t + m.len() * 10000;
}
@noinline function map_int_words(a: string, b: string): i32 {
    var m: Map[i32, string] = map_new(4);
    m = m.insert(1, a + b);
    m = m.insert(1, b + a + b);
    m = m.insert(2, a + "");
    return m.get_or(1, "").len() * 10 + m.get_or(2, "").len() + m.get_or(3, "xyz").len();
}
@noinline function head_of_arr(xs: i32[]): i32 { return xs[0]; }
@noinline function apply_arr(f: (i32[]) => i32, xs: i32[]): i32 { return f(xs) + f([9, 8]); }
@noinline function lend_array(n: i32): i32 { var a: i32[] = [n, n + 1]; return apply_arr(head_of_arr, a); }
@noinline function text_len(s: string): i32 { return s.len(); }
@noinline function apply_text(f: (string) => i32, s: string): i32 { return f(s); }
@noinline function lam_inferred(n: i32): i32 { return apply_int((x: i32) => x * 3, n); }
@noinline function float_bound(n: i32): i32 { var f = 2.5; var g = f + 1.5; var h = g * 2.0; if (h > 7.5) { return n; } return 0; }
@noinline function lam_text(w: string): i32 { return apply_text((s: string) => (s + "!").len(), w); }
@noinline function lend_text(n: i32): i32 { var t: string = "ab" + "cd"; return apply_text(text_len, t) + n; }
@noinline function boxed_of(n: i32): i32[] { return [n, n + 1]; }
@noinline function apply_box(f: (i32) => i32[], n: i32): i32 { var xs: i32[] = f(n); return xs[1]; }
@noinline function drop_box(f: (i32) => i32[], n: i32): i32 { f(n); return n; }
@noinline function box_via(n: i32): i32 { return apply_box(boxed_of, n) + drop_box(boxed_of, n); }
@noinline function pick_fn(n: i32): i32 {
    var g: (i32) => i32 = dbl;
    if (n > 2) { g = negate; }
    return g(n);
}
@noinline function shift_by(k: i32, n: i32): i32 { return apply_int((x: i32): i32 => { return x + k; }, n); }
@noinline function shift_loop(k: i32, n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) { t = t + apply_int((x: i32): i32 => { return x * k; }, i); i = i + 1; }
    return t;
}
@noinline function pick_shift(k: i32, n: i32): i32 {
    var g: (i32) => i32 = (x: i32): i32 => { return x + k; };
    if (n > 2) { g = (x: i32): i32 => { return x - k; }; }
    return g(n) + g(0);
}
// The builtins whose result is one of the front end's generic unions. The box
// the caller receives owns every part of itself, so an arm binding a payload
// only borrows it and the box's own release walks whatever no arm took.
@noinline function env_len(name: string): i32 {
    var n: i32 = 0 - 1;
    match (env(name)) {
        Some(v) => { n = v.len(); },
        None => { n = 0; }
    }
    return n;
}
@noinline function touch_env(name: string): i32 { env(name); return name.len(); }
@noinline function line_len(): i32 {
    var n: i32 = 0 - 1;
    match (read_line()) {
        Some(l) => { n = l.len(); },
        None => { n = 0; }
    }
    return n;
}
@noinline function read_len(path: string): i32 {
    var n: i32 = 0 - 1;
    match (read_file(path)) {
        Ok(text) => { n = text.len(); },
        Err(e) => { n = 0; }
    }
    return n;
}
@noinline function dir_count(path: string): i32 {
    var n: i32 = 0 - 1;
    match (read_dir(path)) {
        Ok(names) => { n = names.len(); },
        Err(e) => { n = 0; }
    }
    return n;
}
// An Option built here rather than received: the payload is a string this
// function owns, so the box takes that unit and gives it back when it dies.
// The second one is never read at all, so only the box's release frees it.
@noinline function wrapped_len(s: string): i32 {
    var o: Option[string] = Some(s + "!");
    var n: i32 = 0;
    match (o) {
        Some(v) => { n = v.len(); },
        None => { n = 0; }
    }
    return n;
}
@noinline function drop_opt(s: string): i32 {
    var o: Option[string] = Some(s + "!");
    return s.len();
}
// Replaced once per step, so every superseded box is released before the
// header phi takes the next one.
@noinline function pick_opt(n: i32): i32 {
    var o: Option[string] = None;
    var i: i32 = 0;
    while (i < n) { o = Some("ab"); i = i + 1; }
    var len: i32 = 0 - 1;
    match (o) {
        Some(v) => { len = v.len(); },
        None => { len = 0; }
    }
    return len;
}
// A Result whose error arm carries an enum the FRONT END injects: the box owns
// the IoError, which owns a string of its own, and the branch that replaces
// the Ok box releases it.
@noinline function mk_result(n: i32, path: string): i32 {
    var r: Result[string, IoError] = Ok(path + "!");
    if (n == 0) { r = Err(NotFound(path + "?")); }
    var len: i32 = 0;
    match (r) {
        Ok(t) => { len = t.len(); },
        Err(e) => { len = 0 - 1; }
    }
    return len;
}
// The builtins whose result owns nothing but the argument array.
@noinline function has_args(): i32 {
    var av: string[] = args();
    if (av.len() > 0) { return 1; }
    return 0;
}
@noinline function emit_byte(c: i32): i32 { putchar(c); return c + 1; }
@noinline function bits_to_int(b: i32): i32 { return (f32_from_bits(b) * 2.0) as i32; }
@noinline function underflow_now(): i32 { return __rc_underflow_count(); }
@noinline function bytes_len(path: string): i32 {
    var n: i32 = 0 - 1;
    match (read_file_bytes(path)) {
        Ok(bytes) => { n = bytes.len(); },
        Err(e) => { n = 0; }
    }
    return n;
}
@noinline function stat_seen(path: string): i32 {
    var n: i32 = 0 - 1;
    match (stat(path)) {
        Ok(st) => { n = 1; },
        Err(e) => { n = 0; }
    }
    return n;
}
@noinline function lstat_seen(path: string): i32 {
    var n: i32 = 0 - 1;
    match (lstat(path)) {
        Ok(st) => { n = 1; },
        Err(e) => { n = 0; }
    }
    return n;
}
// The two shared-append totals are a measurement of the run, so the value is
// not the same on every target; that they are readable and non-negative is.
@noinline function shared_pushes(): i32 {
    var seen: i32 = __arr_push_shared_count() + (__arr_push_shared_bytes() as i32);
    if (seen < 0) { return 1; }
    return 0;
}
// ---- generic declarations (docs/SEMANTIC-GENERICS.md) ---------------------
// A generic declaration is a TEMPLATE, produced once per instantiation its
// callers bind and named by the types bound, so the accumulator here is a
// plain i32 or boolean in one instance and a string[] further down; the AST
// lowering keeps the one erased body for its own callers, main among them.
@noinline function add_at(n: i32, a: i32): i32 { return a + n; }
@noinline function or_over(n: i32, a: boolean): boolean { return a || n > 1; }
@noinline function fold_acc[T](a: T, visit: (i32, T) => T): T {
    var acc: T = visit(1, a);
    acc = visit(2, acc);
    return acc;
}
// A generic calling a SECOND generic: the instance requests the instance it
// needs, to a fixpoint over the requests.
@noinline function fold_twice[T](a: T, visit: (i32, T) => T): T {
    return fold_acc(fold_acc(a, visit), visit);
}
// The accumulator carried through a loop PHI, replaced once per step.
@noinline function fold_loop[T](a: T, n: i32, visit: (i32, T) => T): T {
    var acc: T = a;
    var i: i32 = 0;
    while (i < n) { acc = visit(i, acc); i = i + 1; }
    return acc;
}
@noinline function folded_sum(): i32 { return fold_acc(10, add_at); }
@noinline function folded_twice(): i32 { return fold_twice(0, add_at); }
@noinline function folded_loop(n: i32): i32 { return fold_loop(0, n, add_at); }
@noinline function folded_flag(): i32 { if (fold_acc(false, or_over)) { return 1; } return 0; }
// A heap value HELD ACROSS a scalar fold and read back after churn has had
// every chance to reuse a box freed too early. An over-release reads a short
// array and answers the sentinel rather than the length, so a wrong ANSWER —
// not a balanced allocation count — is what a mistake here shows as.
@noinline function held_across(n: i32): i32 {
    var xs: string[] = ["alpha", "beta", "gamma"];
    var t: i32 = fold_loop(n, 4, add_at);
    var churn: string[] = [];
    var i: i32 = 0;
    while (i < 12) { churn = churn.append("junk"); i = i + 1; }
    if (xs.len() != 3) { return 0 - 1; }
    if (xs[2].len() != 5) { return 0 - 2; }
    return t + churn.len();
}
// The variable bound to a REFERENCE. The function type spells the accumulator
// slot consuming, so the caller hands its unit over at each step and takes back
// the one the call returns — the consuming convention, end to end. Both
// visitor shapes are here, because they are the two it has to get right: the
// IDENTITY, whose returned box IS the argument, and the FRESH one, which drops
// the acc it consumed. Each is reached through a bare NAME, which the lift
// wraps in a trampoline, and through a lambda.
@noinline function keep_words(n: i32, own a: string[]): string[] { return a; }
@noinline function add_word(n: i32, own a: string[]): string[] { return a.append("w"); }
@noinline function fold_words[T](own a: T, n: i32, visit: (i32, own T) => T): T {
    var acc: T = a;
    var i: i32 = 0;
    while (i < n) { acc = visit(i, acc); i = i + 1; }
    return acc;
}
@noinline function words_kept(n: i32): i32 {
    var out: string[] = fold_words(["a", "b"], n, keep_words);
    return out.len();
}
@noinline function words_grown(n: i32): i32 {
    var out: string[] = fold_words(["a"], n, add_word);
    return out.len();
}
@noinline function words_lambda(n: i32): i32 {
    var out: string[] = fold_words(["a"], n, (i: i32, own a: string[]): string[] => { return a.append("x"); });
    return out.len();
}
// A heap value held across the reference fold and read back after churn that
// has had every chance to hand out a box freed too early: an over-release
// reads a short array and answers the sentinel, so the mistake shows as a
// wrong ANSWER rather than as a balanced allocation count.
@noinline function words_held(n: i32): i32 {
    var held: string[] = ["alpha", "beta", "gamma"];
    var out: string[] = fold_words(["a"], n, add_word);
    var churn: string[] = [];
    var i: i32 = 0;
    while (i < 12) { churn = churn.append("junk"); i = i + 1; }
    if (held.len() != 3) { return 0 - 1; }
    if (held[2].len() != 5) { return 0 - 2; }
    return out.len() + churn.len();
}
// Reference captures. The box owns one unit of each capture that is one: the
// constructing function retains what it still reads after and moves what it
// does not, and the box's release walks them by the environment record the
// body in slot 0 names — after the calls that lent a box held in a local,
// once per loop step, and on the arm a join did not take. The captured array
// is read back after churn so an over-release answers the sentinel.
@noinline function cap_text(w: string, n: i32): i32 { return apply_int((x: i32): i32 => { return x + w.len(); }, n); }
@noinline function cap_words(n: i32): i32 {
    var ws: string[] = ["ab", "cde"];
    var f: (i32) => i32 = (x: i32): i32 => { return x + ws.len() + ws[1].len(); };
    return f(n) + f(1);
}
@noinline function cap_pick(k: i32, n: i32): i32 {
    var ws: string[] = ["ab"];
    var w: string = "xyz";
    var g: (i32) => i32 = (x: i32): i32 => { return x + ws.len(); };
    if (k > 0) { g = (x: i32): i32 => { return x + w.len(); }; }
    return g(n);
}
@noinline function cap_loop(n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var ws: string[] = ["a"];
        ws = ws.append("bc");
        t = t + apply_int((x: i32): i32 => { return x * ws[1].len(); }, i);
        i = i + 1;
    }
    return t;
}
@noinline function cap_held(n: i32): i32 {
    var ws: string[] = ["alpha", "beta"];
    var r: i32 = apply_int((x: i32): i32 => { return x + ws.len(); }, n);
    var churn: string[] = [];
    var i: i32 = 0;
    while (i < 12) { churn = churn.append("junk"); i = i + 1; }
    if (ws[1].len() != 4) { return 0 - 1; }
    return r + churn.len();
}
@noinline function cap_rec(n: i32): i32 {
    var q: Q = Q { name: "abcd", p: P { n: n, xs: [1, 2, 3] } };
    var f: (i32) => i32 = (x: i32): i32 => { return x + q.name.len() + q.p.xs.len(); };
    return f(1);
}
// ---- the outcome of a write ----------------------------------------------
// Each writer hands back Result[void, IoError]: the Ok arm carries nothing, so
// its binding names nothing and the box is released with no payload walked.
// main runs them in a directory of the test's own, mapped in for wasm; the
// executable-bit writer is not among them because wasm grants no fsmode,
// so a program naming it never reaches the wasm emitter.
@noinline function made_dir(path: string): i32 {
    match (create_dir_all(path)) { Ok(_) => { return 1; }, Err(e) => { return 0; } }
    return 0 - 1;
}
@noinline function wrote(path: string, text: string): i32 {
    match (write_file(path, text)) { Ok(u) => { return 1; }, Err(e) => { return 0; } }
    return 0 - 1;
}
@noinline function unlinked(path: string): i32 {
    match (remove_file(path)) { Ok(_) => { return 1; }, Err(e) => { return 0; } }
    return 0 - 1;
}
@noinline function removed(path: string): i32 {
    match (remove_dir_all(path)) { Ok(_) => { return 1; }, Err(e) => { return 0; } }
    return 0 - 1;
}
@noinline function points(n: i32): i32 {
    var out: char[] = [];
    out = out.append(n as char);
    out = out.append((n + 1) as char);
    var total: i32 = 0;
    for c in out { total = total + (c as i32); }
    return total;
}
@noinline function point_eq(n: i32): i32 {
    var a: char = n as char;
    var b: char = (n + 1) as char;
    if (a == b) { return 1; }
    if (a != b) { return 2; }
    return 0;
}
@noinline function wide_some(n: i64): i32 {
    match (wide_maybe(n)) { Some(v) => { return (v / 1000000000i64) as i32; }, None => { return 0; } }
    return 0;
}
@noinline function wide_maybe(n: i64): Option[i64] {
    if (n > 0i64) { return Some(n * 2i64); }
    return None;
}
@noinline function float_some(x: f64): i32 {
    match (float_maybe(x)) { Some(v) => { return (v * 4.0) as i32; }, None => { return 0; } }
    return 0;
}
@noinline function float_maybe(x: f64): Option[f64] {
    if (x > 0.0) { return Some(x * 2.0); }
    return None;
}
// A MIXED union: the Ok is wide and the Err is a pointer, so both arms ride
// the slot the wide form names. Written and read at one width or wasm refuses
// the module.
@noinline function mixed_res(n: i64): i32 {
    match (wide_or_text(n)) { Ok(v) => { return (v / 1000000i64) as i32; }, Err(e) => { return e.len(); } }
    return 0;
}
@noinline function wide_or_text(n: i64): Result[i64, string] {
    if (n > 0i64) { return Ok(n * 3i64); }
    return Err("negative");
}
// A column of BOXES: the map owns a unit of every record, union or array it
// holds, released through the value's own drop when the map is released and
// on the entry an insert supersedes. A get of one retains the payload for
// the Option it answers, which the frame releases as any Option.
struct MV { name: string, n: i32 }
enum MS { One(MV), Two(MV, MV), Zero }
@noinline function map_vrec(a: string, n: i32): i32 {
    var m: Map[string, MV] = map_new(4);
    m = m.insert(a + "", MV { name: a + "x", n: n });
    m = m.insert(a + "", MV { name: a + "yy", n: n + 1 });
    m = m.insert("z", MV { name: "", n: 7 });
    var d: MV = MV { name: "", n: 0 };
    var got: MV = m.get_or(a + "", d);
    var t: i32 = got.name.len() * 10 + got.n + m.get_or("q", d).n;
    if let Some(v) = m.get(a + "") { t = t + v.n; }
    if let Some(v) = m.get("nope") { t = t + 1000; }
    return t + m.len();
}
@noinline function map_venum(a: string): i32 {
    var m: Map[i32, MS] = map_new(2);
    m = m.insert(1, MS.One(MV { name: a + "", n: 1 }));
    m = m.insert(2, MS.Two(MV { name: a + a, n: 2 }, MV { name: "", n: 3 }));
    m = m.insert(1, MS.Zero);
    var t: i32 = 0;
    match (m.get_or(2, MS.Zero)) {
        MS.One(p) => { t = p.n; },
        MS.Two(p, q) => { t = p.name.len() + q.n; },
        MS.Zero => { t = 0 - 1; },
    }
    if let Some(s) = m.get(1) {
        match (s) {
            MS.Zero => { t = t + 100; },
            _ => { t = t + 200; },
        }
    }
    return t * 10 + m.len();
}
@noinline function map_varr(n: i32): i32 {
    var m: Map[string, i32[]] = map_new(2);
    m = m.insert("a", [n, n + 1]);
    m = m.insert("a", [n * 2]);
    m = m.insert("b", [1, 2, 3]);
    var e: i32[] = [];
    return m.get_or("a", e)[0] + m.get_or("b", e).len() + m.get_or("c", e).len();
}
// napped is the fixture's sleep caller. One microsecond, so the fixture pays
// nothing for it, and the count is positive so it takes the arm that fills the
// subscription — the arm that allocated an 88-byte buffer per call and never
// freed it (#9480). It is here BECAUSE this fixture demands allocs == frees on
// the register legs and refuses growth past the floor on wasm, which is the
// assertion that leak could not pass.
@noinline function napped(n: i32): i32 {
    sleep_ns(1000 as i64);
    return n;
}
// tick_ns is the fixture's only HOST-builtin caller: a produced callee reaching
// a wasi clock, called from the AST-lowered main. It answers its argument
// whenever the monotonic clock is past zero, so the value is 1 rather than a
// timestamp. It is here because the clock's 8-byte scratch write is what made
// #9481's unguarded wasm struct-drop fault: nothing else in this program
// leaves a large word in the low scratch a null box could read as a pointer.
@noinline function tick_ns(n: i32): i32 {
    var t: i64 = monotonic_ns();
    if (t > 0 as i64) { return n; }
    return 0;
}
struct Reused { tag: string, cells: i32[], n: i32 }
@noinline function reused_step(seed: i32): Reused {
    var a: Reused = Reused { tag: "aa", cells: [seed, seed + 1], n: seed };
    var s: i32 = a.n + a.cells[0] + a.cells[1] + a.tag.len();
    return Reused { tag: "bbb", cells: [s, s + 2], n: s };
}
@noinline function reuse_loop(n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var r: Reused = reused_step(i);
        t = t + r.n + r.cells[0] + r.tag.len();
        i = i + 1;
    }
    return t;
}
@noinline function reuse_shared(n: i32): i32 {
    var keep: Reused[] = [];
    var a: Reused = Reused { tag: "dd", cells: [n], n: n };
    keep = keep.append(a);
    var s: i32 = a.n + a.cells[0];
    var b: Reused = Reused { tag: "cc", cells: [s], n: s };
    return keep[0].n + b.n + b.tag.len();
}
// The donor and the recipient of a pairing need not share a TYPE: a box is
// slots, and the runtime's alloc_reuse compares the count itself. Mote and Glyph are
// members of a struct-union, so the shape word a construction writes is READ by
// the match below — a recipient inheriting the donor's would take the wrong arm
// rather than leak, which is what makes these witness the word and not just the
// storage. Trio is one field wider than either, so the pairing declines it: the
// donor's box has three slots and the construction claims four, and the
// runtime would return the block to its freelist and allocate anyway.
struct Mote { text: string, k: i32 }
struct Glyph { xs: i32[], k: i32 }
type Sigil = Mote | Glyph;
struct Trio { a: string, b: i32[], c: i32 }
@noinline function sigil_code(g: Sigil): i32 {
    match (g) {
        Mote(m) => { return m.k; },
        Glyph(y) => { return y.xs[1] + 100; }
    }
    return 0 - 1;
}
@noinline function cross_step(seed: i32): Sigil {
    var a: Mote = Mote { text: "nn", k: seed };
    var s: i32 = a.k + a.text.len();
    return Glyph { xs: [s, s + 1], k: s };
}
@noinline function cross_back(seed: i32): Sigil {
    var a: Glyph = Glyph { xs: [seed], k: seed };
    var s: i32 = a.k + a.xs[0];
    return Mote { text: "mm", k: s };
}
// An AST-lowered caller handing a produced callee's union result straight to
// another produced callee leaks both boxes — the documented gap this file's
// enum shapes already route around — so the hand-off happens in produced code.
@noinline function cross_back_code(seed: i32): i32 { return sigil_code(cross_back(seed)); }
@noinline function cross_loop(n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) { t = t + sigil_code(cross_step(i)); i = i + 1; }
    return t;
}
@noinline function cross_wide(n: i32): i32 {
    var p: Mote = Mote { text: "pp", k: n };
    var s: i32 = p.k + p.text.len();
    var w: Trio = Trio { a: "qq", b: [s], c: s };
    return w.c + w.b[0] + w.a.len();
}
// A tuple box is one word per element and no shape word, so it is storage a
// donor of any of the three forms can be, and storage any of them can take:
// tuple_step pairs a tuple with a tuple, tuple_from_rec a record's box with a
// tuple, and rec_from_tuple a tuple's with a record's. The two cross-form ones
// are a two-field record against a three-element tuple because the pairing
// matches SLOTS: a record box carries a shape word the tuple's does not, so
// the two agree at three slots and not at two.
struct Parcel { a: string, b: i32 }
@noinline function tuple_step(seed: i32): (i32, string) {
    var a: (string, i32[]) = ("aa", [seed, seed + 1]);
    var s: i32 = a.1[0] + a.1[1] + a.0.len();
    return (s, "bb");
}
@noinline function tuple_loop(n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var p: (i32, string) = tuple_step(i);
        t = t + p.0 + p.1.len();
        i = i + 1;
    }
    return t;
}
@noinline function tuple_from_rec(n: i32): i32 {
    var d: Parcel = Parcel { a: "cc", b: n };
    var s: i32 = d.b + d.a.len();
    var q: (i32, string, i32) = (s, "dd", s + 1);
    return q.0 + q.1.len() + q.2;
}
@noinline function rec_from_tuple(n: i32): i32 {
    var d: (string, i32, i32) = ("ee", n, n + 1);
    var s: i32 = d.1 + d.2 + d.0.len();
    var q: Parcel = Parcel { a: "ff", b: s };
    return q.b + q.a.len();
}
function print_int(n: i32): i32 {
    if (n < 0) {
        putchar(45);
        if (n < 0 - 9) { print_int(0 - n / 10); }
        putchar(48 - n % 10);
        return 0;
    }
    if (n > 9) { print_int(n / 10); }
    putchar(48 + n % 10);
    return 0;
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
    print_int(borrow_acc(g, 3)); print(""); print_int(g.len()); print("");
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
    print_int(nested_up(uw)); print(""); print_int(out_of_order(3)); print("");
    print_int(byte_at("abc", 1)); print(""); print_int(first_last("abc")); print("");
    print_int(temp_byte(0)); print(""); print_int(temp_byte(1)); print("");
    print_int(outlives(0)); print(""); print_int(outlives(1)); print("");
    print_int(checksum("ab")); print(""); print_int(checksum("")); print("");
    print_int(byte_wrap(255)); print(""); print_int(byte_wrap(1)); print("");
    print_int(byte_shift(200)); print(""); print_int(byte_mask("abc", 0)); print("");
    print_int(wide_wrap(2147483647)); print(""); print_int(wide_mul(65536)); print("");
    print_int(narrow(300)); print(""); print_int(upper("abc", 0)); print("");
    print_int(upper("A", 0)); print(""); print_int(upper("z", 0)); print("");
    print_int(wide_shift(33)); print(""); print_int(wide_shift(0)); print("");
    print_int(wide_product(5)); print(""); print_int(wide_low(5)); print("");
    print_int(wide_byte(5)); print(""); print_int(wide_narrow(5)); print("");
    print_int(wide_neg(5)); print(""); print_int(wide_count(3)); print("");
    print_int(wide_hex()); print(""); print_int(wide_cmp(5)); print("");
    print_int(wide_cmp(1)); print(""); print_int(wide_div(5)); print("");
    print_int(wide_call(5)); print(""); print_int(wide_call(0)); print("");
    print_int(scan_views("abcdef")); print(""); print_int(lent_views(1)); print(""); print_int(lent_views(0)); print("");
    print_int(view_of_temp(2)); print(""); print_int(view_of_temp(0)); print("");
    print_int(ratio(1, 4)); print(""); print_int(ratio(0 - 3, 2)); print("");
    print_int(float_cmp(1.5, 1.5)); print(""); print_int(float_cmp(1.0, 2.0)); print(""); print_int(float_cmp(3.0, 2.0)); print("");
    print_int(float_loop(10)); print(""); print_int(float_call(3)); print(""); print_int(wide_float(5000000000i64)); print("");
    print_int(wide_fields(WideRec { d: 1.25, n: 8589934592i64, s: "ab" })); print(""); print_int(span_wide(Wide(1.5, "abc"))); print("");
    print_int(mk_wide(2.5, 4294967296i64, "abcd")); print(""); print_int(mk_span(0.25)); print("");
    print_int(wide_lit_sum(5000000000i64)); print(""); print_int(wide_grow(4294967296i64, 6)); print("");
    print_int(wide_grow(1i64, 0)); print(""); print_int(wide_copy_set([1i64, 4294967296i64, 3i64], 8589934592i64)); print("");
    print_int(uwide_lit_sum(5000000000u64)); print(""); print_int(uwide_grow(4294967296u64, 6)); print("");
    print_int(uwide_copy_set([1u64, 4294967296u64, 3u64], 8589934592u64)); print("");
    print_int(wide_pair_sum(5000000000i64)); print(""); print_int(float_pair_sum(2.5)); print("");
    print_int(float_lit_sum(1.5)); print(""); print_int(float_grow(0.5, 5)); print(""); print_int(float_grow(2.0, 1)); print("");
    print_int(fill_squares(5)); print(""); print_int(copy_set([1, 2])); print(""); print_int(word_swap(1)); print("");
    print_int(shared_word(1)); print("");
    print_int(set_p([P { n: 1, xs: [] }], P { n: 4, xs: [1, 2] })); print("");
    print_int(unpack(3)); print(""); print_int(unpack_discard(2)); print(""); print_int(unpack_words("abc")); print("");
    print_int(based(2)); print(""); print_int(tagged(1)); print("");
    print_int(ticked(1)); print(""); print_int(ticked(0)); print(""); print_int(built("cde")); print(""); print_int(find_byte("abcabc", 99)); print("");
    print_int(wide_literal_tree(5000000000i64)); print(""); print_int(wide_literal_tree(0 - 9)); print("");
    print_int(count_byte("banana", 97 as u8)); print(""); print_int(count_byte("", 97 as u8)); print("");
    print_int(leaf_pair_size(3)); print(""); print_int(leaves_size(6)); print("");
    print_int(shift_wide(3 as i64, 4)); print(""); print_int(shift_wide(1024 as i64, 2)); print("");
    print_int(bail(0)); print(""); print_int(bytes_text([104 as u8, 105 as u8, 33 as u8]).len()); print(""); print_int(bit_round(2.75)); print("");
    print_int(bump_each(1)); print(""); print_int(line_each(2)); print("");
    print_int(word_recs(2)); print(""); print_int(word_recs(0)); print("");
    print_int(call_twice(3)); print(""); print_int(lend_array(2)); print("");
    print_int(lend_text(1)); print(""); print_int(box_via(4)); print("");
    print_int(pick_fn(3)); print(""); print_int(pick_fn(1)); print("");
    print_int(shift_by(4, 5)); print(""); print_int(shift_loop(3, 4)); print("");
    print_int(pick_shift(2, 3)); print(""); print_int(pick_shift(2, 1)); print("");
    var bxs: i32[] = fill(2);
    var bp: P = P { n: 1, xs: [1, 2] };
    print_int(sum_all(push_borrowed(bxs, 9))); print(""); print_int(bxs.len()); print("");
    print_int(sum_all(set_borrowed(bxs, 0, 9))); print(""); print_int(bxs[0]); print("");
    print_int(push_field_len(bp, 5)); print(""); print_int(set_field_at(bp, 0, 7)); print("");
    print_int(elem_push(9)); print(""); print_int(push_kept(fill(1), 9)); print("");
    print_int(build_rows(4)); print(""); print_int(word_lens(0)); print(""); print_int(word_set(0)); print("");
    print_int(tag_probe(0)); print(""); print_int(shape_codes(4)); print("");
    print_int(env_len("FERN_ABSENT_VAR")); print(""); print_int(touch_env("FERN_ABSENT_VAR")); print("");
    print_int(line_len()); print(""); print_int(read_len("/nonexistent/fern/semsource")); print("");
    print_int(dir_count("/nonexistent/fern/semsource")); print(""); print_int(wrapped_len("abc")); print("");
    print_int(drop_opt("abcd")); print(""); print_int(pick_opt(3)); print(""); print_int(pick_opt(0)); print("");
    print_int(mk_result(1, "ab")); print(""); print_int(mk_result(0, "ab")); print("");
    print_int(has_args()); print(""); print_int(emit_byte(65)); print("");
    print_int(bits_to_int(1065353216)); print(""); print_int(underflow_now()); print("");
    print_int(bytes_len("/nonexistent/fern/semsource")); print(""); print_int(stat_seen("/nonexistent/fern/semsource")); print("");
    print_int(lstat_seen("/nonexistent/fern/semsource")); print(""); print_int(shared_pushes()); print("");
    print_int(slot_pair(Slot { c: cell_new(4), n: 3 })); print("");
    print_int(note_pair(Note { w: cell_new("ab"), n: 5 })); print("");
    print_int(held_n(Bare(6))); print(""); print_int(slot_held(Slot { c: cell_new(1), n: 8 })); print("");
    print_int(u32_cmp(4294967295u32)); print(""); print_int(u32_div(4294967295u32)); print("");
    print_int(u32_rem(4294967295u32)); print(""); print_int(u32_shift(4294967295u32)); print("");
    print_int(u32_wrap(65536u32)); print(""); print_int(u32_widen(0 - 1)); print("");
    print_int(u32_signed(4294967295u32)); print(""); print_int(u32_byte(4294967295u32)); print("");
    print_int(u32_float(4294967295u32)); print(""); print_int(u32_of_f64(3000000000.0)); print("");
    print_int(u64_cmp()); print(""); print_int(u64_div()); print("");
    print_int(u64_rem()); print(""); print_int(u64_shift()); print("");
    print_int(u64_from_i32(0 - 1)); print(""); print_int(u64_from_u32(4294967295u32)); print("");
    print_int(u64_narrow()); print(""); print_int(u64_float()); print("");
    print_int(u64_of_f64(5000000000.0 * 2000000000.0)); print("");
    print_int(ord_bits("ab", "b")); print(""); print_int(ord_bits("b", "ab")); print("");
    print_int(ord_bits("ab", "ab")); print(""); print_int(ord_view("ab")); print("");
    print_int(ord_view("ba")); print(""); print_int(ord_temp("ab")); print("");
    print_int(folded_sum()); print(""); print_int(folded_twice()); print("");
    print_int(folded_loop(4)); print(""); print_int(folded_flag()); print("");
    print_int(held_across(5)); print("");
    print_int(pick_len(3)); print(""); print_int(pick_len(0)); print("");
    print_int(pick_word_len(1)); print(""); print_int(pick_word_len(0)); print("");
    print_int(pick_kept(1, [7, 8])); print(""); print_int(pick_kept(0, [7, 8])); print("");
    print_int(pick_flip(1, [7, 8])); print(""); print_int(pick_flip(0, [7, 8])); print("");
    print_int(pick_nested(4)); print(""); print_int(pick_nested(1)); print(""); print_int(pick_nested(0)); print("");
    print_int(words_kept(3)); print(""); print_int(words_grown(3)); print("");
    print_int(words_lambda(2)); print(""); print_int(words_held(4)); print("");
    print_int(cap_text("abc", 4)); print(""); print_int(cap_words(2)); print(""); print_int(cap_pick(1, 5)); print("");
    print_int(cap_pick(0, 5)); print(""); print_int(cap_loop(3)); print(""); print_int(cap_held(3)); print(""); print_int(cap_rec(2)); print("");
    print_int(made_dir("semsource_io/sub")); print(""); print_int(wrote("semsource_io/sub/out.txt", "written")); print("");
    print_int(read_len("semsource_io/sub/out.txt")); print(""); print_int(unlinked("semsource_io/sub/out.txt")); print("");
    print_int(unlinked("semsource_io/sub/out.txt")); print(""); print_int(removed("semsource_io")); print("");
    print_int(wrote("semsource_io/sub/out.txt", "x")); print("");
    print_int(cell_count(1)); print(""); print_int(cell_share(2)); print(""); print_int(cell_words("x")); print("");
    print_int(cell_wide(5i64)); print(""); print_int(cell_float(1.5)); print(""); print_int(cell_closure(4)); print("");
    print_int(f32_round_int(16777217.0)); print(""); print_int(f32_lit_bits()); print("");
    print_int(f32_sum_bits(16777216.0 as f32, 1.0 as f32)); print(""); print_int(f32_field(0.5)); print("");
    print_int(f32_cmp(1.0 as f32, 2.0 as f32)); print(""); print_int(f32_from_int(3)); print("");
    print_int(buf_text(2)); print(""); print_int(buf_handle_round(5)); print("");
    print_int(cap_fn(3)); print("");
    print_int(map_tally("ab", "cd")); print(""); print_int(map_words("ab", "cd")); print("");
    print_int(map_hand("k")); print("");
    print_int(alloc_bytes(4)); print(""); print_int(scan_temp("ab", "ca")); print("");
    print_int(float_bits(16.0)); print("");
    print_int(wide_some(5000000000i64)); print(""); print_int(wide_some(0 - 1i64)); print("");
    print_int(float_some(2.5)); print(""); print_int(float_some(0.0 - 1.0)); print("");
    print_int(mixed_res(7000000i64)); print(""); print_int(mixed_res(0 - 1i64)); print("");
    print_int(text_methods("ab", "cd")); print("");
    print_int(points(65)); print(""); print_int(point_eq(65)); print("");
    print_int(addr_walk(6)); print(""); print_int(addr_order(1)); print("");
    print_int(addr_text("abcde" as usize)); print("");
    print_int(addr_eq("abcde" as usize, "abcde" as usize)); print("");
    print_int(addr_eq("abcde" as usize, "xyz" as usize)); print("");
    print_int(map_vstr("ab", "cd")); print(""); print_int(map_vwords("ab", "cd")); print("");
    print_int(acc_fill(5)); print(""); print_int(acc_kept(9)); print("");
    print_int(acc_loop(4)); print(""); print_int(tags_total(3)); print("");
    print_int(acc_via_fill(6)); print(""); print_int(acc_via_kept(7)); print("");
    print_int(acc_via_shared(8)); print("");
    print_int(thread_run(7)); print(""); print_int(thread_shared(3)); print("");
    print_int(sat_mix(2147483000, 1000)); print(""); print_int(chk_count(7, 0)); print(""); print_int(chk_count(0 - 2147483647 - 1, 0 - 1)); print("");
    print_int(chk_count(3, 4)); print(""); print_int((chk_wide(3000000000i64, 3i64) >> 30i64) as i32); print(""); print_int(chk_wide(9223372036854775807i64, 2i64) as i32); print("");
    print_int(sat_byte(200u8, 100u8)); print(""); print_int(chk_unsigned(5u32, 9u32)); print(""); print_int(chk_unsigned(9u32, 5u32)); print("");
    print_int(vb_words(3)); print(""); print_int(vb_words(0)); print(""); print_int(vb_rows(2)); print(""); print_int(vb_rows(0)); print("");
    print_int(lit_bytes(1)); print(""); print_int(lit_bytes(0 - 1)); print("");
    print_int(fill_field(4)); print(""); print_int(set_shared_field(5)); print(""); print_int(word_field_set(2)); print("");
    print_int(map_ints(5)); print(""); print_int(map_int_words("ab", "cde")); print("");
    print_int(nested_case(0)); print(""); print_int(nested_case(1)); print(""); print_int(nested_case(2)); print("");
    print_int(nested_case(3)); print(""); print_int(nested_case(4)); print("");
    print_int(guarded_pick(7)); print(""); print_int(guarded_pick(3)); print(""); print_int(guarded_words(false, 2)); print(""); print_int(guarded_words(true, 200)); print("");
    print_int(for_pairs(3)); print(""); print_int(struct_unpack(4)); print(""); print_int(at_unpack(2)); print(""); print_int(nested_unpack(5)); print("");
    print_int(guarded_and(1, "abcd")); print(""); print_int(guarded_and(0, "abcd")); print(""); print_int(guarded_and(1, "ab")); print("");
    print_int(qualified_pick(3)); print(""); print_int(qualified_pick(0)); print(""); print_int(assoc_make(5)); print("");
    print_int(tm_show(4)); print(""); print_int(tm_show(2)); print(""); print_int(tm_sum(4)); print(""); print_int(tm_words(3)); print(""); print_int(sm_show(1)); print(""); print_int(sm_show(5)); print("");
    print_int(map_get_hit(5)); print(""); print_int(map_get_int(4)); print("");
    print_int(opt_has(7)); print(""); print_int(opt_words("ab")); print("");
    print_int(lam_inferred(4)); print(""); print_int(lam_text("ab")); print("");
    print_int(float_bound(9)); print("");
    print_int(map_lit_words(4)); print("");
    print_int(map_vrec("ab", 3)); print(""); print_int(map_venum("ab")); print(""); print_int(map_varr(5)); print("");
    print_int(show_pt(6)); print("");
    print_int(labelled_sum(3)); print(""); print_int(labelled_sum(6)); print(""); print_int(inc_calls(4)); print("");
    print_int(checked_head("abcdef", 3)); print(""); print_int(checked_mid(0)); print("");
    print_int(checked_temp(0)); print(""); print_int(checked_miss(0)); print("");
    print_int(checked_split(0)); print(""); print_int(checked_scan("abcd")); print("");
    print_int(open_window()); print("");
    print_int(try_opt(8)); print(""); print_int(try_opt(3)); print("");
    print_int(try_view("abcdef")); print(""); print_int(try_view("ab")); print("");
    print_int(try_loop(3)); print("");
    print_int(tick_ns(1)); print("");
    print_int(napped(2)); print("");
    print_int(reuse_loop(4)); print(""); print_int(reuse_shared(5)); print("");
    print_int(cross_loop(4)); print(""); print_int(cross_back_code(5)); print("");
    print_int(cross_wide(3)); print("");
    print_int(tuple_loop(4)); print(""); print_int(tuple_from_rec(3)); print("");
    print_int(rec_from_tuple(5)); print("");
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}
`

// tally(1): unwrap(q) = xs[0] = 1; keep = q → 1; unwrap(r) = n + xs[1] = 2 + 3 = 5 → 7.
// tally(5): 5 + unwrap(r) (6 + 7 = 13) + 13 → 31.
// sum_shapes(4): Dot 0, Line(1) 10, Full([2, 3]) 3, Pair(3, [3]) 6, plus the kept Full: 22.
// A total match closes a value-returning body with no trailing return, and its
// last arm carries no test: shape_code(Pair(3, [3])) = 3 + 1 = 4,
// eat_shape(Full([2, 3])) consumes its own parameter for 2 + 2 = 4, and
// node_tag over the struct-union narrows a Leaf for 7 — tag_probe(0) is their
// 15. shape_codes(4) runs the first two over a fresh Shape per step —
// 1 + 3 + 6 + 13 = 23 — and closes with node_tag(Twig([4, 5])) = 4, for 27.
// Both callers are produced: an AST-lowered main handing a produced callee's
// ENUM result to another produced callee leaks it (80 bytes for a
// Pair(3, [3]) measured), which is the open union-result position below.
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
// borrow_acc(g, 3) is 12 and g is still 9 long afterwards.
// The not-moved receivers all copy: push_borrowed over fill(2) sums 18 and
// leaves bxs 3 long, set_borrowed sums 16 and leaves bxs[0] at 2,
// push_field_len is 3 and 2, set_field_at 7 and 1, elem_push 3 and 2,
// push_kept 4 and 3, build_rows [0..3] and word_lens 3 words of which the
// pushed one is 3 bytes, word_set a 4-byte replacement over a 2-byte element.
// push_temp(1) pushes onto [1, 2, 3] for 4. row_total(4) reads element 0 of
// fill(0..3) = 0+1+2+3 = 6; word_bytes(3) is three "wx" at 2 bytes = 6.
// The for-loop tail runs over lens = fill(2) = [2, 3, 4]: sum 9; skip_two drops
// the 2 for 7; until_two_for breaks at once for 0; first_gt past 2 is 3;
// shadow_for is 9 plus the outer x of 100; temp_for(2) sums [2, 3, 4] again.
// The cell shapes: slot_pair lends its own Slot to slot_share, which retains
// the cell into a fresh Slot it consumes for 3 + 1 = 4, then consumes its own
// for 3 — 7, with the cell released twice and freed once. note_pair is the
// same over a string cell: 5 + 2 = 7 then 5, for 12. held_n(Bare(6)) takes the
// payload-free arm for 6, and slot_held binds its Slot's cell, retains it into
// a Celled payload, drops the Slot and consumes the payload for 8 + 1 = 9.
// nested_for(3) walks [0,1,2], [1,2,3], [2,3,4] skipping every 1 and breaking
// at the 4: 2 + (2+3) + (2+3) = 12. copy_words(2) is 2 words of 2 bytes = 4,
// and an empty array iterates zero times.
// shift_by(4, 5) adds the captured 4 to 5 for 9; shift_loop(3, 4) builds one
// box per step and sums 3 * (0+1+2+3) = 18; pick_shift(2, 3) takes the second
// arm for (3-2) + (0-2) = -1 and pick_shift(2, 1) the first for 3 + 2 = 5.
// The wide elements answer in units of 2^32: wide_lit_sum(5e9) sums 25000000001
// for 5, wide_grow(2^32, 6) sums six steps from 2^32 for 6 and wide_grow(_, 0)
// is 0, and wide_copy_set replaces element 1 of a shared [1, 2^32, 3] with 2^33
// for (2^33 + 2^32) >> 32 = 3. The float elements: float_lit_sum(1.5) sums
// [1.5, 3.0, 2.5] for 70, float_grow(0.5, 5) replaces the 0.5 with 2.0 and
// doubles 14.0 for 28, and float_grow(2.0, 1) doubles the replaced 8.0 for 16.
// The u64 elements answer the same way at the same slot: uwide_lit_sum(5e9)
// is 5, uwide_grow(2^32, 6) is 6, and uwide_copy_set is 3.
// The wide TUPLE elements: wide_pair_sum(5e9) takes 15e9 >> 32 for 3 and adds
// the narrow 7 beside it for 10, and float_pair_sum(2.5) takes the 5.0 and the
// two bytes of the string element beside it for 7.
// The unsigned tail, every value chosen so a signed opcode gives a DIFFERENT
// number: 4294967295u32 is above 1 (1), divides by 3 to 1431655765 and leaves
// 3 mod 7, shifts right by 28 to 15 rather than to -1, masks to the byte 255
// and reinterprets as the i32 -1; 65536 * 65536 wraps to 0 at 2^32; -1 widens
// into the u32 whose top byte is 255; the u32 converts to the f64 4294967295,
// a millionth of which truncates to 4294, and 3e9 truncates back into the u32
// 3000000000, an eighth of a kibi of which is 11718750. At 64 bits 2^63 is
// above 1 (1), divides by 10^15 to 9223 and leaves 854775808 mod 10^9, shifts
// right by 60 to 8; -1 sign-extends into a u64 whose top nibble is 15 where a
// u32's 4294967295 zero-extends to a value 255 after a 24-bit shift; 2^63 +
// 12345 keeps only the 12345 in its low word, 771 after a 4-bit shift; the u64
// converts to the f64 2^63, a quadrillionth of which is 9223; and 10^19
// truncates back into the u64 whose high word is 2328306436 — the i32 print
// of which is -1966660860, where a signed truncation would have clamped at
// INT64_MAX and printed 2147483647.
// map_vstr overwrites "ab" with "cdabcd" and adds "cd" -> "ab", so the reads
// are 6 + 2 and the miss answers its 2-byte default: 10. map_vwords overwrites
// the two-element value at "ab" with a one-element one, and its miss answers a
// one-element default: 1 + 1 = 2. Both columns are counted, so every value the
// overwrite supersedes is released with the map rather than left behind.
// points(65) fills a char[] of its own with 65 and 66 and sums them for 131,
// with the box freed on the way out; point_eq(65) takes the inequality arm
// for 2, char having no ordering to take.
// The string comparisons: "ab" is under "b" (1 + 2 = 3), "b" over it
// (4 + 8 = 12), and a string is under-or-equal and over-or-equal itself
// (2 + 8 = 10). Two views of one string order by the bytes they point at, and
// a concatenation this function owns orders before it is released.
// The cross-type pairings: cross_loop(4) reads each Glyph's second element
// plus 100 for 103 + 104 + 105 + 106 = 418, cross_back(5) rebuilds a Mote in
// a Glyph's box for 10, and cross_wide(3) offers a two-field donor to a
// three-field construction, which the runtime declines, for 5 + 5 + 2 = 12.
// The tuple forms: tuple_loop(4) sums 2i + 5 over i = 0..3 for 32,
// tuple_from_rec(3) is 5 + 2 + 6 = 13 and rec_from_tuple(5) is 13 + 2 = 15.
const semsourceRCWant = "1\n4\n5\n-3\n-2\n2\n0\n1\n5\n5\n12\n1\n8\n12\n0\n3\n3\n6\n4\n2\n0\n7\n31\n1\n0\n2\n22\n10\n7\n2\n5\n14\n7\n9\n4\n7\n1\n4\n8\n13\n14\n3\n9\n7\n3\n5\n5\n2\n2\n9\n36\n12\n9\n0\n4\n6\n6\n9\n7\n0\n3\n109\n9\n12\n4\n0\n3\n9\n3\n4\n4\n5\n3\n5\n5\n0\n3\n1\n0\n10\n-2147483648\n0\n28\n8\n-20\n2\n6\n3\n7\n6\n4\n10\n9\n98\n196\n98\n98\n97\n97\n195\n0\n0\n2\n144\n1\n1\n1\n44\n65\n65\n90\n128\n0\n1\n35\n35\n705032739\n1\n3\n1\n2\n1\n40\n1\n0\n625\n38\n30\n3\n3\n25\n150\n0\n-1\n1\n10\n10\n5000\n6\n9\n10\n4\n5\n6\n0\n3\n5\n6\n3\n10\n7\n70\n28\n16\n21\n8\n5\n12\n7\n5\n5\n6\n42\n3\ntick\n2\n1\n5\n2\n11\n-2\n3\n0\n6\n9\n48\n4224\n0\n3\n2\n13\n12\n16\n0\n8\n11\n5\n9\n-3\n2\n9\n18\n-1\n5\n18\n3\n16\n2\n32\n71\n32\n43\n43\n332\n42\n15\n27\n0\n15\n0\n0\n0\n4\n4\n2\n0\n3\n-1\n1\nA66\n2\n0\n0\n0\n0\n0\n7\n12\n6\n9\n1\n1431655765\n3\n15\n0\n255\n-1\n255\n4294\n11718750\n1\n9223\n854775808\n8\n15\n255\n771\n9223\n-1966660860\n3\n12\n10\n1\n0\n1\n13\n6\n6\n1\n23\n5\n1\n3\n1\n2\n3\n2\n3\n2\n5\n0\n2\n4\n3\n17\n7\n13\n8\n6\n6\n17\n8\n1\n1\n7\n1\n0\n1\n0\n7\n8\n5\n6\n3\n6\n16777216\n1036831949\n1266679808\n1056964609\n1\n1077936128\n14\n6\n15\n13\n4\n7\n9\n397\n15\n10\n0\n20\n0\n21\n8\n18\n131\n2\n67\n7\n5\n1\n0\n10\n2\n51\n234\n9\n4743\n61\n121\n12\n210\n13\n-2147452531\n11\n0\n15\n8\n-1\n255100\n-7\n4\n224\n223\n22\n11\n1804\n642\n94\n915\n152\n50128\n85\n3\n101\n205\n0\n1004\n14\n3\n7\n1\n18\n7\n8\n10\n40\n4\n2\n7\n0\n11\nelem!\n5\nelem\n4\n48\n12\npt:pt\n5\npt\n2\n5\n12\n107\n34\n12\n3\n9\n42\n50\n1072\n13\npt!\n6\n9\n17\n14\n3\n9\n3\n6\n6\n4\nob\nob\nob\n13\n2\n60\n9\n50\n36\n1\n2\n72\n17\n418\n10\n12\n32\n13\n15\n"

const semsourceRCDriver = `import "./semsource"; import "./ssarc"; import "./ssaunits"; import "./ssa"; import "./ssasem";
import "./parser"; import "./lexer"; import "./irlower"; import "./ir";
import "./ircore"; import "./checker"; import "./asmcore"; import "./asm_ir"; import "./asm_arm64_ir"; import "./wasm_ir";
function main(): i32 {
    var av = args();
    var src: string = "";
    match (read_file(av[2])) { Ok(text) => { src = text; }, Err(_) => { return 2; } }
    var parsed = parser.parse_module(lexer.tokenize(src));
    var mod = irlower.lift_lambdas(checker.annotate_module(parser.register_struct_method_generics(parser.register_map_method_generics(parser.register_array_method_generics(parser.Module { ...parsed, structs: parser.inject_builtin_enums(parsed.structs) })))));
    var tab = irlower.struct_tab(mod.structs);
    var base = ircore.wp_fn_sigs(mod.funcs, tab);
    var built = semsource.build_module(mod);
    var bodies: irlower.LowerResult[] = [];
    var helpers: irlower.LowerResult[] = [];
    var skipped: irlower.LowerResult = irlower.LowerResult { ok: false, why: "", ops: [], n_locals: 0, n_params: 0, erased_wide: false, superseded: false, arr_slots: [], i64_slots: [], f64_slots: [], str_slots: [], alias_incs: [], name: "", result_kind: irlower.result_from_decl() };
    // Every body is planned before any is lowered, as semlower does: a
    // caller's bracket reads the fields its callees' plans may grow (grows),
    // and the AST-lowered main reads the same through the regrown registries.
    var plans: ssaunits.Plan[] = [];
    var keys: string[] = [];
    var funcs: ssasem.Func[] = [];
    var seeds: string[] = [];
    var consumed: string[] = [];
    var at: i32 = 0;
    for fd in mod.funcs {
        var p = built.decls[at];
        var plan = ssaunits.refused("");
        // main is AST-lowered, and so is a template's own erased body, which
        // main's calls name; the template's instances are bodies of their own.
        if (fd.name == "main" || p.template) {
            if (!p.ok && fd.name != "main") { eprint(fd.name + ": " + p.why); return 4; }
        } else {
            if (!p.ok) { eprint(fd.name + ": " + p.why); return 4; }
            plan = ssaunits.plan(p.func, p.modes);
            if (!plan.ok) { eprint(fd.name + ": " + plan.why); return 5; }
        }
        plans = plans.append(plan);
        keys = keys.append(fd.name);
        funcs = funcs.append(p.func);
        at = at + 1;
    }
    for p in built.instances {
        if (!p.ok) { eprint(p.key + ": " + p.why); return 4; }
        var plan = ssaunits.plan(p.func, p.modes);
        if (!plan.ok) { eprint(p.func.graph.name + ": " + plan.why); return 5; }
        plans = plans.append(plan);
        keys = keys.append(p.func.graph.name);
        funcs = funcs.append(p.func);
    }
    var grows: ssaunits.GrowRow[] = ssaunits.grow_table(keys, funcs, plans);
    at = 0;
    for fd in mod.funcs {
        var p = built.decls[at];
        if (fd.name == "main" || p.template) {
            if (p.template) { eprint("produced " + fd.name + "\n"); }
            bodies = bodies.append(skipped);
            at = at + 1;
            continue;
        }
        var lowered = ssarc.lower(p.func, p.modes, plans[at], tab, grows);
        if (!lowered.ok) { eprint(fd.name + ": " + lowered.why); return 6; }
        eprint("produced " + fd.name + "\n");
        base = ssarc.caller_sigs(base, fd.name, p.func, p.modes, plans[at]);
        seeds = seeds.append(fd.name + "|" + ssarc.grow_mask(fd.name, p.func, grows, false));
        for row in ssarc.consumed_array_rows(fd.name, p.func, p.modes) { consumed = consumed.append(row); }
        for h in ssarc.drop_helpers(p.func) { helpers = helpers.append(h); }
        bodies = bodies.append(lowered);
        at = at + 1;
    }
    var instances: irlower.LowerResult[] = [];
    var ai: i32 = 0;
    for p in built.instances {
        var lowered = ssarc.lower(p.func, p.modes, plans[mod.funcs.len() + ai], tab, grows);
        if (!lowered.ok) { eprint(p.func.graph.name + ": " + lowered.why); return 6; }
        eprint("instance " + p.func.graph.name + "\n");
        for h in ssarc.drop_helpers(p.func) { helpers = helpers.append(h); }
        instances = instances.append(lowered);
        ai = ai + 1;
    }
    base = irlower.regrow_sigs(base, mod.funcs, tab, seeds);
    base = irlower.consume_sigs(base, mod.funcs, consumed);
    var g = ircore.lower_gated(mod, tab, base, [], av[1] == "wasm32-wasi", ircore.no_sub());
    if (!g.ok) { eprint("ast lowering failed"); return 3; }
    var cache: irlower.LowerResult[] = [];
    at = 0;
    for fd in mod.funcs {
        if (bodies[at].ok) { cache = cache.append(bodies[at]); } else { cache = cache.append(g.cache[at]); }
        at = at + 1;
    }
    // The instances and the per-type drop helpers are bodies with no
    // declaration, so they go on the cache tail past mod.funcs, deduped by
    // symbol.
    cache = ssarc.merge_helpers(cache, instances);
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

// inScratchDir runs the program in a fresh directory of its own, so the
// writers it exercises touch nothing else: the working directory for a native
// run, and the one preopened directory for wasm, where a relative path
// resolves against the first preopen.
func inScratchDir(t *testing.T, run *exec.Cmd, target string) *exec.Cmd {
	t.Helper()
	scratch := t.TempDir()
	run.Dir = scratch
	if target == "wasm32-wasi" {
		run.Args = append(run.Args[:2:2], append([]string{"--dir", scratch}, run.Args[2:]...)...)
	}
	return run
}

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
			for _, name := range []string{"pick", "pair", "boxed", "carry", "count_even", "fill", "first_of", "keep", "chain", "twice", "count_down", "grow", "make", "wrap", "unwrap", "tally", "greet", "boxed_local", "boxed_carry", "shape", "measure", "sum_shapes", "consume", "boxed_shape", "hold", "mk_node", "node_size", "node_sum", "leaf", "fork", "tree_sum", "build_sum", "chain_len", "chain_build", "mk_s2", "proj", "total", "make_counter", "twice_total", "size_of", "eat_size", "fresh_size", "text_size", "inner_size", "sum_all", "grown_size", "grow_to", "push_temp", "borrow_acc", "push_borrowed", "set_borrowed", "push_field_len", "set_field_at", "push_elem_len", "elem_push", "push_kept", "build_rows", "push_word", "word_lens", "set_word_borrowed", "word_set", "words", "word_bytes", "rows", "row_total", "sum_for", "skip_two", "until_two_for", "first_gt", "shadow_for", "temp_for", "nested_for", "copy_words", "head_of", "mid_of", "temp_slice", "scan_slices", "grown", "boxed_len", "deep_len", "paired_len", "longs_len", "span_len", "div_of", "rem_of", "bit_ops", "shifts", "int_min", "ratio_of", "bump", "pure_copy", "reorder", "from_temp", "retag", "nested_up", "out_of_order", "byte_at", "first_last", "temp_byte", "outlives", "checksum", "byte_wrap", "byte_shift", "byte_mask", "wide_wrap", "wide_mul", "narrow", "upper", "wide_shift", "wide_product", "wide_low", "wide_byte", "wide_narrow", "wide_neg", "wide_count", "wide_hex", "wide_cmp", "wide_div", "wide_of", "wide_hi", "wide_call", "view_len", "copied", "scan_views", "lent_views", "view_of_temp", "scale", "ratio", "float_cmp", "float_loop", "float_call", "wide_float", "wide_fields", "span_wide", "mk_wide", "mk_span", "wide_lit", "wide_sum", "wide_lit_sum", "wide_grow", "wide_set", "wide_copy_set", "uwide_lit", "uwide_sum", "uwide_lit_sum", "uwide_grow", "uwide_set", "uwide_copy_set", "wide_pair", "wide_pair_sum", "float_pair", "float_pair_sum", "float_arr", "float_sum", "float_lit_sum", "float_grow", "set_at", "fill_squares", "copy_set", "set_word", "word_swap", "shared_word", "set_p", "halves", "unpack", "unpack_discard", "unpack_words", "based", "tagged", "tick", "ticked", "built", "find_byte", "bump_each", "line_each", "word_recs", "dbl", "negate", "apply_int", "call_twice", "head_of_arr", "apply_arr", "lend_array", "text_len", "apply_text", "lend_text", "boxed_of", "apply_box", "drop_box", "box_via", "pick_fn", "shift_by", "shift_loop", "pick_shift", "shape_code", "eat_shape", "node_tag", "tag_probe", "shape_codes", "env_len", "touch_env", "line_len", "read_len", "dir_count", "wrapped_len", "drop_opt", "pick_opt", "mk_result", "has_args", "emit_byte", "bits_to_int", "underflow_now", "bytes_len", "stat_seen", "lstat_seen", "shared_pushes", "slot_n", "note_n", "held_n", "slot_share", "note_share", "slot_pair", "note_pair", "slot_held", "u32_cmp", "u32_div", "u32_rem", "u32_shift", "u32_wrap", "u32_widen", "u32_signed", "u32_byte", "u32_float", "u32_of_f64", "u64_cmp", "u64_div", "u64_rem", "u64_shift", "u64_from_i32", "u64_from_u32", "u64_narrow", "u64_float", "u64_of_f64", "ord_bits", "ord_view", "ord_temp", "add_at", "or_over", "fold_acc", "fold_twice", "fold_loop", "folded_sum", "folded_twice", "folded_loop", "folded_flag", "held_across", "keep_words", "add_word", "fold_words", "words_kept", "words_grown", "words_lambda", "words_held", "pick_len", "pick_word", "pick_word_len", "pick_kept", "pick_flip", "pick_nested", "cap_text", "cap_words", "cap_pick", "cap_loop", "cap_held", "cap_rec", "made_dir", "wrote", "unlinked", "removed", "cell_count", "cell_share", "cell_words", "cell_wide", "cell_float", "cell_closure", "f32_round_int", "f32_lit_bits", "f32_sum_bits", "f32_field", "f32_cmp", "f32_from_int", "buf_text", "buf_handle_round", "via_cap", "cap_fn", "map_tally", "map_words", "map_eat", "map_hand", "alloc_bytes", "scan_temp", "addr_walk", "addr_order", "addr_text", "addr_eq", "float_bits", "wide_some", "wide_maybe", "float_some", "float_maybe", "mixed_res", "wide_or_text", "text_methods", "points", "point_eq", "map_vstr", "map_vwords", "acc_push", "acc_push_own", "acc_fill", "acc_kept", "acc_loop", "tags_add", "tags_total", "acc_osz", "acc_via", "acc_via_fill", "acc_via_kept", "acc_via_shared", "thread_step", "thread_run", "thread_shared", "sat_mix", "chk_count", "chk_wide", "sat_byte", "chk_unsigned", "vb_words", "vb_rows", "lit_of", "lit_int", "churn", "lit_bytes", "set_kept", "fill_field", "set_shared_field", "set_word_field", "word_field_set", "map_ints", "map_int_words", "map_get_hit", "map_get_int", "opt_has", "opt_words", "lam_inferred", "lam_text", "float_bound", "map_lit_words", "show", "show_pt", "labelled_sum", "inc_by", "inc_calls", "nested_arms", "mk_out", "nested_case", "guarded_pick", "guarded_words", "for_pairs", "mk_dp", "struct_unpack", "at_unpack", "nested_unpack", "guarded_and", "qualified_pick", "assoc_make", "tm_word", "tm_show", "tm_sum", "tm_words", "sm_pick", "sm_show", "map_vrec", "map_venum", "map_varr", "checked_head", "checked_mid", "checked_temp", "checked_miss", "checked_split", "checked_scan", "open_base", "open_window", "try_even", "try_quarter", "try_opt", "try_head", "try_view", "try_parse", "try_msg", "try_loop", "tick_ns", "napped", "reused_step", "reuse_loop", "reuse_shared", "sigil_code", "cross_step", "cross_back", "cross_back_code", "cross_loop", "cross_wide", "tuple_step", "tuple_loop", "tuple_from_rec", "rec_from_tuple", "print_int"} {
				if !strings.Contains(diagnostics.String(), "produced "+name+"\n") {
					t.Fatalf("%s was not produced:\n%s", name, diagnostics.String())
				}
			}
			run := inScratchDir(t, physicalRCRun(t, gcc, runner, dir, "semsource", target, output), target)
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
			// Everything the program prints has to be in the want above. The
			// prefix is what lets the leak-check summary follow it, and it also
			// means a print appended to main without a value appended here is
			// asserted by nothing: three fixture sets landed that way before
			// this check existed, and each read as green.
			for _, line := range strings.Split(strings.TrimPrefix(string(got), semsourceRCWant), "\n") {
				if line == "" || strings.HasPrefix(line, "leakcheck:") {
					continue
				}
				t.Fatalf("output past the end of semsourceRCWant: %q\nAppend its value to that "+
					"constant — until you do, the fixture printing it proves nothing.", line)
			}
			var allocs, frees, live int64
			summary := leakSummaryLine(string(got))
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatal(err)
			}
			if allocs == 0 {
				t.Fatalf("no allocations recorded: %s", summary)
			}
			if allocs != frees || live != 0 {
				t.Fatalf("unbalanced: %s", summary)
			}
		})
	}
}
