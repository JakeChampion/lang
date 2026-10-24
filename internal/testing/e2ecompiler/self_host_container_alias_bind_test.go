package e2ecompiler

import (
	"strings"
	"testing"
)

type containerAliasCase struct {
	name   string
	src    string
	want   int
	allocs int64
	frees  int64
}

func containerAliasCases() []containerAliasCase {
	return []containerAliasCase{
		{
			name: "tuple_alias",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let t: (i32, i32[]) = (i, xs);
    let v: (i32, i32[]) = t;
    return v.1[0] + v.1[1];
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40, allocs: 200, frees: 200,
		},
		{
			name: "tuple_alias_fresh_element",
			src: `function round(i: i32): i32 {
    let t: (i32, i32[]) = (i, [i, i + 1]);
    let v: (i32, i32[]) = t;
    return v.1[0] + v.1[1];
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40, allocs: 200, frees: 200,
		},
		{
			name: "tuple_alias_cancelled",
			src: `function round(i: i32): i32 {
    let t: (i32, i32[]) = (i, [i, i + 1]);
    let v: (i32, i32[]) = t;
    let n: i32 = v.0;
    return n + t.1.len() + i;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 57, allocs: 200, frees: 200,
		},
		{
			name: "tuple_alias_scalar",
			src: `function round(i: i32): i32 {
    let t: (i32, i32) = (i, i + 1);
    let v: (i32, i32) = t;
    return v.0 + v.1;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40, allocs: 0, frees: 0,
		},
		{
			name: "struct_alias",
			src: `struct P { xs: i32[] }
function round(i: i32): i32 {
    let t: P = P { xs: [i, i + 1] };
    let v: P = t;
    return v.xs[0] + v.xs[1];
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40, allocs: 200, frees: 200,
		},
		{
			name: "struct_alias_cancelled",
			src: `struct P { xs: i32[] }
function round(i: i32): i32 {
    let t: P = P { xs: [i, i + 1] };
    let v: P = t;
    let n: i32 = v.xs[0];
    return n + t.xs[1] + i;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 10, allocs: 200, frees: 200,
		},
		{
			name: "struct_alias_fresh_call",
			src: `struct P { xs: i32[] }
function mk(i: i32): P { return P { xs: [i, i + 1] }; }
function round(i: i32): i32 {
    let t: P = mk(i);
    let v: P = t;
    return v.xs[0] + i;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 23, allocs: 200, frees: 200,
		},
		{
			name: "struct_alias_in_a_conditional",
			src: `struct P { xs: i32[] }
function mk(i: i32): P { return P { xs: [i, i + 1] }; }
function round(i: i32): i32 {
    let t: P = mk(i);
    if (i % 2 == 0) { let v: P = t; return v.xs[0] + i; }
    return t.xs[0] + i;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 23, allocs: 200, frees: 200,
		},
		{
			name: "struct_alias_of_a_parameter_refused",
			src: `struct P { xs: i32[] }
function mk(i: i32): P { return P { xs: [i, i + 1] }; }
function take(p: P): i32 { let v: P = p; return v.xs[0]; }
function round(i: i32): i32 { let t: P = mk(i); return take(t) + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 23, allocs: 200, frees: 200,
		},
		{
			name: "struct_alias_of_a_receiver_refused",
			src: `struct P { xs: i32[] }
function mk(i: i32): P { return P { xs: [i, i + 1] }; }
pub function (p: P) first(): i32 { let v: P = p; return v.xs[0]; }
function round(i: i32): i32 { let t: P = mk(i); return t.first() + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 23, allocs: 200, frees: 200,
		},
		{
			name: "struct_alias_reassigned_refused",
			src: `struct P { xs: i32[] }
function mk(i: i32): P { return P { xs: [i, i + 1] }; }
function round(i: i32): i32 { let t: P = mk(i); let v: P = t; v = mk(i + 1); return v.xs[0] + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40, allocs: 400, frees: 400,
		},
		{
			name: "struct_alias_chain",
			src: `struct P { xs: i32[] }
function mk(i: i32): P { return P { xs: [i, i + 1] }; }
function round(i: i32): i32 { let t: P = mk(i); let v: P = t; let u: P = v; return u.xs[0] + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 23, allocs: 200, frees: 200,
		},
		{
			name: "struct_alias_chain_source_read",
			src: `struct P { xs: i32[] }
function mk(i: i32): P { return P { xs: [i, i + 1] }; }
function round(i: i32): i32 { let t: P = mk(i); let v: P = t; let u: P = v; return t.xs[0] + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 23, allocs: 200, frees: 200,
		},
		{
			name: "struct_alias_chain_three_links",
			src: `struct P { xs: i32[] }
function mk(i: i32): P { return P { xs: [i, i + 1] }; }
function round(i: i32): i32 { let t: P = mk(i); let v: P = t; let u: P = v; let z: P = u; return z.xs[0] + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 23, allocs: 200, frees: 200,
		},
		{
			name: "string_alias_chain_conditional",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 { let t: string = w("ab"); let n: i32 = 0; if (i % 2 == 0) { let v: string = t; let u: string = v; n = u.len(); } return n + t.len() + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 5, allocs: 100, frees: 100,
		},
		{
			name: "string_alias_chain_link_returned_refused",
			src: `function w(a: string): string { return a + "!"; }
function esc(i: i32): string { let t: string = w("ab"); let v: string = t; let u: string = v; return u; }
function round(i: i32): i32 { return esc(i).len() + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 21, allocs: 100, frees: 100,
		},
		{
			name: "string_alias_chain_middle_link_held_refused",
			src: `function w(a: string): string { return a + "!"; }
function sink(xs: string[]): i32 { return xs.len(); }
function round(i: i32): i32 { let t: string = w("ab"); let v: string = t; let u: string = v; let held: string[] = [v]; return u.len() + sink(held) + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 38, allocs: 200, frees: 200,
		},
		{
			name: "tuple_alias_chain",
			src: `function round(i: i32): i32 { let t: (i32, i32[]) = (i, [i, i + 1]); let v: (i32, i32[]) = t; let u: (i32, i32[]) = v; return u.1.len() + u.0; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 4, allocs: 200, frees: 200,
		},
		{
			name: "tuple_alias_chain_source_read",
			src: `function round(i: i32): i32 { let t: (i32, i32[]) = (i, [i, i + 1]); let v: (i32, i32[]) = t; let u: (i32, i32[]) = v; return t.1.len() + u.0; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 4, allocs: 200, frees: 200,
		},
		{
			name: "tuple_alias_chain_middle_read",
			src: `function round(i: i32): i32 { let t: (i32, i32[]) = (i, [i, i + 1]); let v: (i32, i32[]) = t; let u: (i32, i32[]) = v; return u.1.len() + v.0; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 4, allocs: 200, frees: 200,
		},
		{
			name: "tuple_alias_chain_three_links",
			src: `function round(i: i32): i32 { let t: (i32, i32[]) = (i, [i, i + 1]); let v: (i32, i32[]) = t; let u: (i32, i32[]) = v; let z: (i32, i32[]) = u; return z.1.len() + z.0; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 4, allocs: 200, frees: 200,
		},
		{
			name: "tuple_alias_chain_conditional",
			src: `function round(i: i32): i32 { let t: (i32, i32[]) = (i, [i, i + 1]); let n: i32 = 0; if (i % 2 == 0) { let v: (i32, i32[]) = t; let u: (i32, i32[]) = v; n = u.1.len(); } return n + t.1.len() + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 21, allocs: 200, frees: 200,
		},
		{
			name: "tuple_alias_chain_link_returned_refused",
			src: `function esc(i: i32): (i32, i32[]) { let t: (i32, i32[]) = (i, [i, i + 1]); let v: (i32, i32[]) = t; let u: (i32, i32[]) = v; return u; }
function round(i: i32): i32 { let r: (i32, i32[]) = esc(i); return r.1.len() + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 4, allocs: 200, frees: 200,
		},
		{
			name: "tuple_alias_chain_middle_link_held_refused",
			src: `function sink(xs: (i32, i32[])[]): i32 { return xs.len(); }
function round(i: i32): i32 { let t: (i32, i32[]) = (i, [i, i + 1]); let v: (i32, i32[]) = t; let u: (i32, i32[]) = v; let held: (i32, i32[])[] = [v]; return u.1.len() + sink(held) + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 21, allocs: 300, frees: 300,
		},
		{
			name: "tuple_alias_chain_middle_link_held_read_back",
			src: `function sink(xs: (i32, i32[])[]): i32 { return xs[0].1.len() * 10 + xs[0].1[1]; }
function round(i: i32): i32 { let t: (i32, i32[]) = (i, [i, i + 1]); let v: (i32, i32[]) = t; let u: (i32, i32[]) = v; let held: (i32, i32[])[] = [v]; return u.1.len() + sink(held) + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 82, allocs: 300, frees: 300,
		},
		{
			name: "tuple_alias_scalar_chain",
			src: `function round(i: i32): i32 { let t: (i32, i32) = (i, i + 1); let v: (i32, i32) = t; let u: (i32, i32) = v; return u.0 + u.1; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40, allocs: 0, frees: 0,
		},
		{
			name: "tuple_alias_scalar_chain_middle_read",
			src: `function round(i: i32): i32 { let t: (i32, i32) = (i, i + 1); let v: (i32, i32) = t; let u: (i32, i32) = v; return u.0 + v.1; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40, allocs: 0, frees: 0,
		},
		{
			name: "tuple_alias_scalar_chain_three_links",
			src: `function round(i: i32): i32 { let t: (i32, i32) = (i, i + 1); let v: (i32, i32) = t; let u: (i32, i32) = v; let z: (i32, i32) = u; return z.0 + z.1; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40, allocs: 0, frees: 0,
		},
		{
			name: "tuple_alias_scalar_chain_conditional",
			src: `function round(i: i32): i32 { let t: (i32, i32) = (i, i + 1); let n: i32 = 0; if (i % 2 == 0) { let v: (i32, i32) = t; let u: (i32, i32) = v; n = u.1; } return n + t.0 + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 33, allocs: 0, frees: 0,
		},
		{
			name: "tuple_alias_scalar_chain_link_returned_refused",
			src: `function esc(i: i32): (i32, i32) { let t: (i32, i32) = (i, i + 1); let v: (i32, i32) = t; let u: (i32, i32) = v; return u; }
function round(i: i32): i32 { let r: (i32, i32) = esc(i); return r.0 + r.1; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40, allocs: 0, frees: 0,
		},
		{
			name: "tuple_alias_scalar_chain_middle_link_held_refused",
			src: `function sink(xs: (i32, i32)[]): i32 { return xs.len(); }
function round(i: i32): i32 { let t: (i32, i32) = (i, i + 1); let v: (i32, i32) = t; let u: (i32, i32) = v; let held: (i32, i32)[] = [v]; return u.0 + sink(held) + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40, allocs: 200, frees: 200,
		},
		{
			name: "enum_alias_reclaimed",
			src: `enum E { A(i32[]), B }
function mke(i: i32): E { if (i % 2 == 0) { return E.A([i, i + 1]); } return E.B; }
function round(i: i32): i32 { let e: E = mke(i); let f: E = e; let n: i32 = 0; match (f) { E.A(k) => { n = k[0]; }, E.B => { n = 1; } } return n; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 10, allocs: 100, frees: 100,
		},
		{
			name: "struct_array_alias_unchanged",
			src: `struct P { xs: i32[] }
function mk(i: i32): P { return P { xs: [i, i + 1] }; }
function round(i: i32): i32 { let ps: P[] = [mk(i)]; let qs: P[] = ps; return qs[0].xs[0] + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 23, allocs: 300, frees: 300,
		},
		{
			name: "struct_if_let_destructure_alias",
			src: `struct P { x: i32, y: i32 }
function round(i: i32): i32 {
    let total: i32 = 0;
    let p: P = P { x: 3 + i, y: 4 + i };
    if let P { x, y } = p { total = total + x + y; }
    return total;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 59, allocs: 100, frees: 100,
		},
		{
			name: "struct_as_pattern_binder",
			src: `struct P { x: i32, y: i32 }
function round(i: i32): i32 {
    let total: i32 = 0;
    let p: P = P { x: 3 + i, y: 4 + i };
    if let w @ P { x, y } = p { total = total + w.x + y; }
    return total;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 59, allocs: 100, frees: 100,
		},
		{
			name: "integer_slot_not_retained",
			src: `import "std/i32";
function round(i: i32): i32 {
    let n: i32 = 0 - 2147483647 - 1 + i;
    let s: string = n.to_string();
    return s.len() + i;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 74, allocs: 200, frees: 200,
		},
		{
			name: "alias_with_post_read",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let t: (i32, i32[]) = (i, xs);
    let v: (i32, i32[]) = t;
    return v.1[0] + t.1[1];
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40, allocs: 200, frees: 200,
		},
		{
			name: "alias_in_a_plain_block",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let t: (i32, i32[]) = (i, xs);
    let acc: i32 = 0;
    { let v: (i32, i32[]) = t; acc = acc + v.1[0]; }
    return acc;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 53, allocs: 200, frees: 200,
		},
		{
			name: "alias_in_a_conditional",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let t: (i32, i32[]) = (i, xs);
    let acc: i32 = 0;
    if (i % 2 == 0) { let v: (i32, i32[]) = t; acc = acc + v.1[0]; }
    return acc;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 43, allocs: 200, frees: 200,
		},
		{
			name: "conditional_alias_with_post_read",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let t: (i32, i32[]) = (i, xs);
    let acc: i32 = 0;
    if (i % 2 == 0) { let v: (i32, i32[]) = t; acc = acc + v.1[0]; }
    return acc + t.1[1];
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 30, allocs: 200, frees: 200,
		},
		{
			name: "array_alias_reference",
			src: `function round(i: i32): i32 {
    let t: i32[] = [i, i + 1];
    let v: i32[] = t;
    return v[0] + v[1];
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40, allocs: 100, frees: 100,
		},
		{
			name: "array_alias_in_a_block",
			src: `function round(i: i32): i32 {
    let t: i32[] = [i, i + 1];
    let acc: i32 = 0;
    { let v: i32[] = t; acc = acc + v[0]; }
    return acc;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 53, allocs: 100, frees: 100,
		},
		{
			name: "array_alias_in_a_conditional",
			src: `function round(i: i32): i32 {
    let t: i32[] = [i, i + 1];
    let acc: i32 = 0;
    if (i % 2 == 0) { let v: i32[] = t; acc = acc + v[0]; }
    return acc;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 43, allocs: 100, frees: 100,
		},
		{
			name: "refused_alias_escapes",
			src: `function sink(q: (i32, i32[])): i32 { return q.1[0]; }
function mk(i: i32): (i32, i32[]) {
    let xs: i32[] = [i, i + 1];
    let t: (i32, i32[]) = (i, xs);
    let v: (i32, i32[]) = t;
    return v;
}
function round(i: i32): i32 { let r: (i32, i32[]) = mk(i); return r.1[0]; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 53, allocs: 200, frees: 200,
		},
		{
			name: "refused_alias_reassigned",
			src: `@noinline function hold(t: (i32, i32[])): i32 { return 0; }
function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let t: (i32, i32[]) = (i, xs);
    let v: (i32, i32[]) = t;
    let w: i32 = hold(v);
    v = (i + 1, xs);
    return v.1[0] + w + hold(v);
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 53, allocs: 200, frees: 200,
		},
		{
			name: "string_alias",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let t: string = w("ab");
    let v: string = t;
    return v.len() + i;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 21, allocs: 100, frees: 100,
		},
		{
			name: "string_alias_cancelled",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let t: string = w("ab");
    let v: string = t;
    let n: i32 = v.len();
    return n + t.len() + i;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 72, allocs: 100, frees: 100,
		},
		{
			name: "string_alias_of_a_parameter_borrowed",
			src: `function w(a: string): string { return a + "!"; }
function plen(p: string): i32 { let v: string = p; return v.len(); }
function round(i: i32): i32 { let t: string = w("ab"); return plen(t) + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 21, allocs: 100, frees: 100,
		},
		{
			name: "string_alias_in_a_conditional",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let t: string = w("ab");
    if (i % 2 == 0) { let v: string = t; return v.len() + i; }
    return t.len() + i;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 21, allocs: 100, frees: 100,
		},
		{
			name: "string_alias_to_string_producer",
			src: `import "std/i32";
function round(i: i32): i32 { let n: i32 = i + 100; let t: string = n.to_string(); let v: string = t; return v.len() + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 21, allocs: 200, frees: 200,
		},
		{
			name: "string_alias_join_producer",
			src: `import "std/array";
@noinline function ids(s: string): string { return s; }
function round(i: i32): i32 { let xs: string[] = [ids("ab"), "cd"]; let t: string = xs.join(","); let v: string = t; return v.len() + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 55, allocs: 200, frees: 200,
		},
		{
			name: "string_alias_replace_producer",
			src: `import "std/string";
function w(a: string): string { return a + "!"; }
function round(i: i32): i32 { let s: string = w("aXb"); let t: string = s.replace("X", "Y"); let v: string = t; return v.len() + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 38, allocs: 200, frees: 200,
		},
		{
			name: "string_alias_trim_closes",
			src: `import "std/string";
function w(a: string): string { return a + "!"; }
function round(i: i32): i32 { let s: string = w("  ab  "); let t: str = s.trim(); let v: str = t; return v.len() + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 55, allocs: 200, frees: 200,
		},
		{
			name: "string_alias_chain",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 { let t: string = w("ab"); let v: string = t; let u: string = v; return u.len() + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 21, allocs: 100, frees: 100,
		},
		{
			name: "string_alias_reassigned_refused",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 { let t: string = w("ab"); let v: string = t; v = w("cd"); return v.len() + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 21, allocs: 200, frees: 200,
		},
		{
			name: "string_accumulator_alias_refused",
			src: `function round(i: i32): i32 { let s: string = ""; let k: i32 = 0; while (k < 3) { s = s + "x"; k = k + 1; } let v: string = s; return v.len() + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 21, allocs: 100, frees: 100,
		},
		{
			name: "string_alias_of_a_for_in_element",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 { let xs: string[] = [w("ab")]; let n: i32 = 0; for e in xs { let v: string = e; n = n + v.len(); } return n + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 21, allocs: 200, frees: 200,
		},
		{
			name: "string_alias_of_a_destructure_binder",
			src: `function w(a: string): string { return a + "!"; }
function mk(): (string, i32) { return (w("ab"), 7); }
function round(i: i32): i32 { let (a, b) = mk(); let v: string = a; return v.len() + b + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 57, allocs: 200, frees: 200,
		},
		{
			name: "strarr_alias",
			src: `function mkstr(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let src: string[] = [mkstr("x"), mkstr("y")];
    let t: i32 = 0;
    let x: string[] = src;
    t = (t + x.len()) % 101;
    t = (t + src.len()) % 101;
    return t;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 68, allocs: 300, frees: 300,
		},
		{
			name: "strarr_alias_in_a_conditional",
			src: `function mkstr(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let src: string[] = [mkstr("x"), mkstr("y")];
    let t: i32 = 0;
    if (i % 2 == 0) { let x: string[] = src; t = (t + x.len()) % 101; }
    t = (t + src.len()) % 101;
    return t;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 51, allocs: 300, frees: 300,
		},
		{
			name: "strarr_alias_element_bytes",
			src: `function mkstr(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let src: string[] = [mkstr("x"), mkstr("y")];
    let x: string[] = src;
    return (x[0][0] as i32 + src[1][0] as i32 + i) % 101;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 32, allocs: 300, frees: 300,
		},
		{
			name: "strarr_alias_chain",
			src: `function mkstr(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let src: string[] = [mkstr("x")];
    let x: string[] = src;
    let y: string[] = x;
    return (x.len() + y.len() + src.len() + i) % 101;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 68, allocs: 200, frees: 200,
		},
		{
			name: "strarr_alias_chain_elem_escape_refused",
			src: `function mkstr(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let src: string[] = [mkstr("x")];
    let x: string[] = src;
    let y: string[] = x;
    let e: string = x[0];
    return (y.len() + e.len() + i) % 101;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 68, allocs: 200, frees: 200,
		},
		{
			name: "enum_alias_chain",
			src: `enum E { A(i32[]), B }
function round(i: i32): i32 {
    let t: E = E.A([i, i + 1]);
    let v: E = t;
    let u: E = v;
    match (u) { E.A(a) => { return a.len() + i; }, E.B => { return i; } }
    return 0;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 4, allocs: 200, frees: 200,
		},
		{
			name: "enum_alias_chain_payload_out_refused",
			src: `enum E { A(i32[]), B }
function round(i: i32): i32 {
    let t: E = E.A([i, i + 1]);
    let v: E = t;
    let u: E = v;
    let out: i32[] = [0];
    match (u) { E.A(a) => { out = a; }, E.B => {} }
    return out.len() + i;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 4, allocs: 200, frees: 200,
		},
		{
			name: "strarr_alias_elem_bind_refused",
			src: `function mkstr(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let src: string[] = [mkstr("x"), mkstr("y")];
    let x: string[] = src;
    let e: string = x[0];
    return (e.len() + src.len() + i) % 101;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 67, allocs: 300, frees: 300,
		},
		{
			name: "strarr_alias_of_a_parameter_borrowed",
			src: `function mkstr(a: string): string { return a + "!"; }
function plen(p: string[]): i32 { let v: string[] = p; return v.len(); }
function round(i: i32): i32 { let src: string[] = [mkstr("x")]; return (plen(src) + i) % 101; }
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 70, allocs: 200, frees: 200,
		}}
}

func TestSelfHostContainerAliasBindX86_64(t *testing.T) {
	boxedProbes(t)
	runContainerAliasCases(t, true)
}

func TestSelfHostContainerAliasScalarX86_64(t *testing.T) {
	runContainerAliasCases(t, false)
}

func runContainerAliasCases(t *testing.T, boxes bool) {
	cli := newStrictCLI(t)
	for _, tc := range containerAliasCases() {
		if (tc.allocs > 0) != boxes {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			asm := cli.emit(t, "x86-64-linux", tc.src, "FERN_LEAKCHECK=1")
			stderr, exit := hevRun(t, cli.runner, buildBin(t, cli.gcc, t.TempDir(), "alias", asm))
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: the alias took a "+
					"DEEP release it did not earn — only the box is retained at the bind)",
					tc.name, exit, tc.want)
			}
			summary := leakSummaryLine(stderr)
			if summary == "" {
				t.Fatalf("%s: no leakcheck summary", tc.name)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("%s: parse %q: %v", tc.name, summary, err)
			}
			if allocs != tc.allocs {
				t.Errorf("%s: %s — want allocs=%d", tc.name, summary, tc.allocs)
			}
			if frees != tc.frees {
				t.Errorf("%s: %s — want frees=%d. FEWER means the alias forgiveness "+
					"stopped reaching this shape (a partial thread shows up as a "+
					"scope-dependent result)", tc.name, summary, tc.frees)
			}

			sanAsm := cli.emit(t, "x86-64-linux", tc.src, "FERN_SANITIZE=1")
			sanErr, sanExit := hevRun(t, cli.runner, buildBin(t, cli.gcc, t.TempDir(), "alias_san", sanAsm))
			if sanExit != tc.want {
				t.Fatalf("%s sanitize leg exited %d, want %d (124 = fatal sanitizer check)", tc.name, sanExit, tc.want)
			}
			if strings.Contains(sanErr, "rc over-release") || strings.Contains(sanErr, "use-after-free") {
				t.Fatalf("%s sanitize leg reported:\n%s", tc.name, sanErr)
			}
		})
	}
}

func TestSelfHostContainerAliasBindWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range containerAliasCases() {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := runWasm(t, cli.emit(t, "wasm32-wasi", tc.src)); got != tc.want {
				t.Errorf("container alias-bind wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

func TestSelfHostContainerAliasBindIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range containerAliasCases() {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
