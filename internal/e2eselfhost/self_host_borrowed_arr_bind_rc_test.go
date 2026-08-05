package e2eselfhost

import "testing"

// Self-host RC (#6049): an array bound out of a container the binding does NOT
// own — a match-arm enum / Option / Result payload, or a tuple-destructure
// element — must be alias-inc'd at the bind.
//
// The self-host exit sweep (emit_dec_sweep_except_list) decs EVERY is_arr slot,
// so a borrowed array binding released the container's buffer once per bind
// while the container still pointed at it. One match was survivable (the
// construction-side inc of #2649 paid for exactly one), but re-matching the SAME
// value released it again: `std/regex`'s `RSeq(RNode[])` arm ran once per
// __rx_match, so `(ab)+` over "ababab" — which re-matches the same subtree four
// times — read recycled memory on the third traversal and segfaulted. Six regex
// fixtures failed on the self-host x86-64 leg this way.
//
// The fix is lower_var's #3457 alias_inc extended to the three borrowed-bind
// sites (emit_borrowed_arr_dup in irlower.fern): retain at the bind so the sweep
// dec is balanced. Each program below re-reads the same borrowed array four
// times and reports __rc_underflow_count() rather than relying on a crash — the
// over-release is deterministic where the SIGSEGV it causes is not.

// borrowedEnumArrayPayload: the regex shape, distilled. `Seq(N[])` is matched
// four times over one long-lived enum value.
const borrowedEnumArrayPayload = `enum N { Ch(i32), Seq(N[]) }
function m(n: N, ti: i32): i32 {
    match (n) {
        Ch(c) => { return c + ti; },
        Seq(xs) => {
            var acc: i32 = 0;
            var k: i32 = 0;
            while (k < xs.len()) { acc = acc + m(xs[k], ti); k = k + 1; }
            return acc;
        }
    }
}
function main(): i32 {
    var root: N = Seq([Ch(1), Ch(2)]);
    var r: i32 = m(root, 0) + m(root, 1) + m(root, 2) + m(root, 3);
    var u: i32 = __rc_underflow_count();
    if (u != 0) { return 90 + u; }
    return r;
}`

// borrowedOptionArrayPayload: the same shape through an Option payload.
const borrowedOptionArrayPayload = `function get(o: Option[i32[]], ti: i32): i32 {
    match (o) {
        Some(xs) => { return xs.len() + ti; },
        None => { return ti; }
    }
}
function main(): i32 {
    var a: i32[] = [1, 2];
    var o: Option[i32[]] = Some(a);
    var r: i32 = get(o, 0) + get(o, 1) + get(o, 2) + get(o, 3);
    var u: i32 = __rc_underflow_count();
    if (u != 0) { return 90 + u; }
    return r;
}`

// borrowedTupleArrayElement: the same shape through a tuple destructure. The
// single-element read `var xs = t.0` was already covered by #3457's alias_inc;
// the N-element destructure was not.
const borrowedTupleArrayElement = `function get(t: (i32[], i32), ti: i32): i32 {
    var (xs, n) = t;
    return xs.len() + n + ti;
}
function main(): i32 {
    var a: i32[] = [1, 2];
    var t: (i32[], i32) = (a, 5);
    var r: i32 = get(t, 0) + get(t, 1) + get(t, 2) + get(t, 3);
    var u: i32 = __rc_underflow_count();
    if (u != 0) { return 90 + u; }
    return r;
}`

// The second half of #6049: an rc-BOX read out of a struct field and stored as
// an array ELEMENT must be retained by the container.
//
// A pure field read does not mark its object as escaping (reclaimable_names_of
// treats `p.x` as copying a scalar out and leaving p local), so a fresh
// non-escaping struct local is still deep-dropped at scope exit — releasing the
// enum box the array had just taken. std/regex's parser threads its tree through
// `RParse { node, pos, g }` and collects children with `items =
// items.append(q.node)` / `[first.node]`, so every child node was freed as its
// RParse died and the next allocation recycled the box: `(ab|cd)` parsed to a
// tree whose alternation branch pointed back at its own enclosing RGroup, and
// __rx_match recursed until the stack overflowed.
//
// Both programs read the element back AFTER allocating a fresh box that lands on
// the freed one, so the failure is the deterministic wrong value 9, not a crash.

// borrowedEnumFieldAppend: the `items.append(q.node)` shape.
const borrowedEnumFieldAppend = `enum N { Ch(i32), Grp(G) }
struct G { idx: i32, node: N }
struct P { node: N, pos: i32 }
function kind(n: N): i32 {
    match (n) {
        Ch(c) => { return c; },
        Grp(gd) => { return 200 + gd.idx; }
    }
}
function seq(v: i32): P { return P { node: Ch(v), pos: v }; }
function build(v: i32): N[] {
    var xs: N[] = [];
    var first: P = seq(v);
    xs = xs.append(first.node);
    return xs;
}
function main(): i32 {
    var xs: N[] = build(3);
    var b: N = Grp(G { idx: 7, node: Ch(9) });
    if (kind(b) != 207) { return 91; }
    return kind(xs[0]);
}`

// borrowedEnumFieldLiteral: the `[first.node]` shape.
const borrowedEnumFieldLiteral = `enum N { Ch(i32), Grp(G) }
struct G { idx: i32, node: N }
struct P { node: N, pos: i32 }
function kind(n: N): i32 {
    match (n) {
        Ch(c) => { return c; },
        Grp(gd) => { return 200 + gd.idx; }
    }
}
function seq(v: i32): P { return P { node: Ch(v), pos: v }; }
function build(v: i32): N[] {
    var first: P = seq(v);
    var xs: N[] = [first.node];
    return xs;
}
function main(): i32 {
    var xs: N[] = build(3);
    var b: N = Grp(G { idx: 7, node: Ch(9) });
    if (kind(b) != 207) { return 91; }
    return kind(xs[0]);
}`

func TestSelfHostBorrowedArrayBindRC(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	cases := []struct {
		name string
		src  string
		want int
	}{
		// m(root, ti) = (1+ti) + (2+ti) for ti = 0..3 -> 3+5+7+9 = 24.
		{"enum_array_payload", borrowedEnumArrayPayload, 24},
		// xs.len()+ti for ti = 0..3 -> 2+3+4+5 = 14.
		{"option_array_payload", borrowedOptionArrayPayload, 14},
		// xs.len()+n+ti for ti = 0..3 -> 7+8+9+10 = 34.
		{"tuple_destructure_element", borrowedTupleArrayElement, 34},
		// The element is Ch(3); 9 means the freed box was recycled by Ch(9).
		{"enum_field_append", borrowedEnumFieldAppend, 3},
		{"enum_field_literal", borrowedEnumFieldLiteral, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code := compileAndRunSelfHostIR(t, gcc, runner, dir, driverBin, tc.name, tc.src)
			if code != tc.want {
				if code > 90 {
					t.Errorf("%s: %d rc under-releases on a borrowed array bind (exit %d, want %d)", tc.name, code-90, code, tc.want)
					return
				}
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
