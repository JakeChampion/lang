package e2eselfhost

import (
	"testing"
)

// iifeFnArmCases exercise a value-position `if` / `match` whose ARMS yield a
// fn value — `let w: (i32) => i32 = (if (c) { <lambda> } else { <lambda> })`,
// `return (match (e) { A => <lambda>, B => <lambda> })` — compiled by the
// self-hosted CLI. The arms may be lambdas (capturing or not), closure locals,
// bare fn names, or arrays of any of these, and the result is bound, returned,
// stored or passed on before it is called.
var iifeFnArmCases = []struct {
	name string
	src  string
	exit int
}{
	// A match-EXPRESSION arm whose value is a NESTED match-expression with
	// lambda arms. The outer arm is walked; before the fix nothing descended
	// into the inner IIFE's arms. Reduced from fernsmith seed 322.
	{"nested-match-expr-fn-arm", "enum Status { Active, Inactive } function gen_f0(p1: Status): (i32) => i32 { return (match (p1) { Active => (match (p1) { Active => ((a: i32) => a), Inactive => ((b: i32) => 95) }), Inactive => ((c: i32) => 72) }); } function main(): i32 { let f: (i32) => i32 = gen_f0(Status.Active); return f(7); }", 7},
	// An if-EXPRESSION with lambda arms, bound to a local and then CALLED.
	// The capture (`n`) is what kept the IIFE off the hoist path.
	{"ifexpr-lambda-arms-var-called", "function main(): i32 { let n: i32 = 1; let w: (i32) => i32 = (if (n > 0) { ((a: i32) => 89) } else { ((b: i32) => b) }); return w(3); }", 89},
	// A match-EXPRESSION with lambda arms RETURNED from a function, the result
	// bound and called by the caller.
	{"matchexpr-lambda-arms-returned", "enum Status { Active, Inactive } function gen(p1: Status): (i32) => i32 { return (match (p1) { Active => ((a: i32) => 5), Inactive => ((c: i32) => 72) }); } function main(): i32 { let f: (i32) => i32 = gen(Status.Inactive); return f(7); }", 72},
	// A NO-CAPTURE IIFE (constant condition, no free vars), bound and called
	// straight away.
	{"nocapture-iife-called-directly", "function main(): i32 { let w: (i32) => i32 = (if (true) { ((a: i32) => 89) } else { ((b: i32) => b) }); return w(3); }", 89},

	// #7438 — the no-capture half of the same fork. Such an IIFE used to be
	// hoisted to a bare `__lam_N` FuncDecl instead, and the worklist drain
	// withholds the `return <lambda>` desugar from lifted bodies (#5281) — the
	// very rewrite that turns an arm lambda into a boxed closure local. So the
	// arm stayed a raw fn POINTER. The case above survived it because main read
	// that pointer and called it; the crash needed the value to reach a SECOND
	// closure, which dispatches its captured fn value env-first and so loaded a
	// "box slot 0" out of the arm function's first code bytes and jumped through
	// it. Reduced from fernsmith seed 73.
	{"nocapture-iife-arms-captured-by-closure", "function main(): i32 { let v1: (i32) => i32 = (if (true) { ((a: i32) => 5i32) } else { ((b: i32) => 26i32) }); let v4: (i32) => i32 = ((y: i32) => v1(y)); return v4(3i32) & 63i32; }", 5},
	{"nocapture-iife-arms-else-branch-captured", "function main(): i32 { let v1: (i32) => i32 = (if (false) { ((a: i32) => 5i32) } else { ((b: i32) => 26i32) }); let v4: (i32) => i32 = ((y: i32) => v1(y)); return v4(3i32) & 63i32; }", 26},
	// The match-expression spelling, which already boxed; here so the two
	// spellings stay pinned together.
	{"nocapture-matchexpr-arms-captured-by-closure", "enum S { A, B } function main(): i32 { let v1: (i32) => i32 = (match (S.A) { A => ((x: i32) => 7i32), B => ((y: i32) => 26i32) }); let v4: (i32) => i32 = ((z: i32) => v1(z)); return v4(3i32) & 63i32; }", 7},
	// The result reaching its binding through an erased-generic PASSTHROUGH.
	// Hoisting the IIFE makes the init `id(<closure-returning call>)`, and the
	// closure-local marking reads the ARGUMENT, so the box has to be recognised
	// there too — otherwise `v1(3)` bare-calls the box pointer as code.
	{"nocapture-iife-arms-through-passthrough", "function id[T](x: T): T { return x; } function main(): i32 { let v1: (i32) => i32 = id((if (true) { ((a: i32) => 5i32) } else { ((b: i32) => 26i32) })); return v1(3i32) & 63i32; }", 5},
	{"nocapture-iife-arms-through-passthrough-captured", "function id[T](x: T): T { return x; } function main(): i32 { let v1: (i32) => i32 = id((if (true) { ((a: i32) => a) } else { ((b: i32) => 26i32) })); let v4: (i32) => i32 = ((y: i32) => v1(v1(y))); return v4(9i32) & 63i32; }", 9},

	// #6323 — the DESTINATION half. The arms above are lambdas, which the
	// #6256 hoist turns into a call whose result a binding already dispatches
	// env-first. When the arms are instead closure LOCALS, no hoist is
	// involved: `let w = (if (c) { v0 } else { v0 })` bound the result to a
	// plain scalar slot because clo_init's match on the init had no case for an
	// IIFE callee, so `w(3)` bare-called the box pointer as code. That compiled
	// clean and SIGSEGV'd — no bail, so no bail-counting gate could see it.
	{"ifexpr-closure-local-arms-called", "function main(): i32 { let v0: (i32) => i32 = ((a: i32) => 89); let n: i32 = 1; let w: (i32) => i32 = (if (n > 0) { v0 } else { v0 }); return w(3); }", 89},
	{"matchexpr-closure-local-arms-called", "enum S { A, B } function main(): i32 { let v0: (i32) => i32 = ((a: i32) => 89); let v1: (i32) => i32 = ((b: i32) => 7); let e: S = S.A; let w: (i32) => i32 = (match (e) { A => v0, B => v1 }); return w(3); }", 89},
	// The counterpart guard, and the reason the marking is gated on every arm
	// being provably a box: BARE fn-name arms are plain fn pointers, so the
	// slot must NOT be marked a closure local — env-first dispatch on a bare
	// pointer is the same crash in the other direction.
	{"ifexpr-bare-fnname-arms-stay-plain", "function inc(x: i32): i32 { return x + 1; } function dbl(x: i32): i32 { return x * 2; } function main(): i32 { let n: i32 = 1; let w: (i32) => i32 = (if (n > 0) { inc } else { dbl }); return w(41); }", 42},

	// #6324 — MIXED arms: one closure-local, one inline lambda. These bailed
	// until the hoist could carry a fn-TYPED capture as a parameter, which needs
	// the signature `cap_type`'s flat "fn" tag throws away (fn_ret /
	// fn_param_types). The capture is copied from the ParamDecl
	// when it is a param and reconstructed from the lambda when it is a local
	// bound from one; anything else still declines.
	//
	// The first of these is #6256's Repro D, which had been open since the issue
	// was filed. All three exercise a different consumption of the result —
	// array element, called local binding, and returned-then-called — because
	// only the last two need the hoisted function to register as
	// closure-returning (form (b') in closure_ret_fns_of), and the first passes
	// without it.
	{"mixed-arms-array-element", "function main(): i32 { let v0: (i32) => i32 = ((a: i32) => 89); let xs: ((i32) => i32)[] = [v0, (if (true) { v0 } else { ((b: i32) => b) }), v0]; return xs[1i32](3i32); }", 89},
	{"mixed-arms-var-called", "function main(): i32 { let v0: (i32) => i32 = ((a: i32) => 89); let n: i32 = 1; let w: (i32) => i32 = (if (n > 0) { v0 } else { ((b: i32) => b) }); return w(3); }", 89},
	{"mixed-arms-returned-then-called", "function gen(n: i32): (i32) => i32 { let v0: (i32) => i32 = ((a: i32) => 89); return (if (n > 0) { v0 } else { ((b: i32) => b) }); } function main(): i32 { let f: (i32) => i32 = gen(1); return f(3); }", 89},

	// The arm lambda itself CAPTURES. The cases above hoist the IIFE and the
	// arm lambdas are capture-free, so each lifts to its own `__lam_N`; a
	// capturing one cannot lift, and inside the hoisted `<fd>$iifeN` it reached
	// lower_expr bare and asked for a `<fd>$iifeN$clo` — one name, built by
	// nobody, since the escaping-closure hoist of the time only claimed a body
	// whose LAST statement is the return and an arm's return is one level down.
	//
	// The arms now get the same `return <lambda>` desugar the worklist gives a
	// source function, so each lifts through its local binding to a uniquely
	// named `<fd>$iifeN$cloM` box. distinct-captures is the case that needs the
	// names to differ; string-capture occupies the env box's pointer slot.
	{"capturing-arm-lambda", "function main(): i32 { let n: i32 = 7i32; let v2: (i32) => i32 = (if (true) { ((x: i32) => (x + n)) } else { ((x: i32) => 41i32) }); return v2(1i32) & 63i32; }", 8},
	{"capturing-arms-distinct-captures", "function main(): i32 { let n: i32 = 7i32; let m: i32 = 20i32; let c: boolean = false; let v2: (i32) => i32 = (if (c) { ((x: i32) => (x + n)) } else { ((x: i32) => (x + m)) }); return v2(1i32) & 63i32; }", 21},
	{"matchexpr-capturing-arms", "enum S { A, B } function main(): i32 { let n: i32 = 9i32; let e: S = S.B; let f: (i32) => i32 = (match (e) { A => ((x: i32) => (x + n)), B => ((y: i32) => (y * n)) }); return f(3i32) & 63i32; }", 27},
	{"capturing-arm-string-capture", "function main(): i32 { let s: string = \"abcd\"; let f: (i32) => i32 = (if (true) { ((x: i32) => (x + s.len())) } else { ((y: i32) => y) }); return f(3i32) & 63i32; }", 7},
	// Consumed through a factory: the hoisted `<fd>$iifeN` must still register
	// as closure-returning (its arms now return a local, form (b) rather than
	// form (a) of closure_ret_fns_of) or the caller bare-dispatches the box.
	{"capturing-arm-bound-then-returned", "function gen(n: i32): (i32) => i32 { let w: (i32) => i32 = (if (n > 0) { ((x: i32) => (x + n)) } else { ((y: i32) => (y - n)) }); return w; } function main(): i32 { let f: (i32) => i32 = gen(5i32); return f(4i32) & 63i32; }", 9},

	// The same arms with the IIFE directly in RETURN position, which the case
	// above reaches only via a local. unwrap_sole_iife_return beta-reduces a sole
	// `return (…)()` body into the enclosing function, so the arm returns appear
	// AFTER the pre-worklist `return <lambda>` desugar has run and nothing had
	// walked them. The desugar now also runs per worklist entry, on every
	// return including the tail one (#10025).
	{"sole-return-iife-capturing-arms", "function gen(n: i32): (i32) => i32 { return (if (n > 0) { ((x: i32) => (x + n)) } else { ((y: i32) => (y - n)) }); } function main(): i32 { let f: (i32) => i32 = gen(5i32); return f(4i32) & 63i32; }", 9},
	{"sole-return-matchexpr-capturing-arms", "enum S { A, B } function gen(n: i32, e: S): (i32) => i32 { return (match (e) { A => ((x: i32) => (x + n)), B => ((y: i32) => (y * n)) }); } function main(): i32 { let f: (i32) => i32 = gen(5i32, S.B); return f(4i32) & 63i32; }", 20},

	// #10333 — the tuple, struct-pattern and nested-pattern match desugars
	// yield each arm's value by storing it into the expression's value local
	// (`__tm…_r = <lambda>`), not by `return`, so the hoist gate never saw a
	// lambda arm and a capturing one reached lower_expr raw.
	{"tuplematch-capturing-arms-in-loop", "function main(): i32 { let base: i32 = 5; let t: i32 = 0; let i: i32 = 0; while (i < 6) { let k: i32 = i; let g: (i32) => i32 = (match ((i % 3, 0)) { (0, _) => ((x: i32) => x - base), (1, _) => ((x: i32) => k), _ => ((x: i32) => x + 1) }); t = t + g(3) % 7; i = i + 1; } return t % 101; }", 9},
	{"tuplematch-arm-captures-its-binder", "function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 4) { let h: (i32) => i32 = (match ((i % 2, i)) { (0, k) => ((x: i32) => x * k), _ => ((x: i32) => x) }); t = t + h(2); i = i + 1; } return t; }", 8},
	{"structmatch-capturing-arms-returned", "struct P { a: i32, b: i32 } function pick(p: P, base: i32): (i32) => i32 { return match (p) { P { a: 0, b } => ((x: i32) => x - base + b), P { a: 1, b } => ((x: i32) => b), _ => ((x: i32) => x + 1) }; } function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 6) { let g: (i32) => i32 = pick(P { a: i % 3, b: i }, 5); t = t + g(3); i = i + 1; } return t; }", 12},

	// The arms hold an ARRAY of fn values rather than one. Same root cause: a
	// capturing lambda in an un-hoisted arm is unreachable, so the arm literals
	// are boxed in place. Every function array is on the env-box ABI, so the
	// binding has one dispatch ABI whatever each arm yields.
	//
	// else-branch-taken runs the OTHER arm, whose lambda does not capture and is
	// wrapped in a `$wrap` trampoline box purely to match; mixed-cap-and-name has
	// a bare fn name beside a capturing lambda in the same literal.
	{"arm-array-capturing-lambda", "function main(): i32 { let v1: i32 = 3i32; let xs: ((i32) => i32)[] = (if (true) { [((x: i32) => (x + v1))] } else { [((y: i32) => y)] }); return xs[0i32](1i32) & 63i32; }", 4},
	{"arm-array-else-branch-taken", "function main(): i32 { let v1: i32 = 3i32; let c: boolean = false; let xs: ((i32) => i32)[] = (if (c) { [((x: i32) => (x + v1))] } else { [((y: i32) => (y + 10i32))] }); return xs[0i32](1i32) & 63i32; }", 11},
	{"matchexpr-arm-array-capturing", "enum S { A, B } function main(): i32 { let v1: i32 = 3i32; let e: S = S.B; let xs: ((i32) => i32)[] = (match (e) { A => [((x: i32) => (x + v1))], B => [((y: i32) => (y * v1))] }); return xs[0i32](2i32) & 63i32; }", 6},
	{"arm-array-mixed-cap-and-fnname", "function inc(x: i32): i32 { return x + 1i32; } function main(): i32 { let v1: i32 = 3i32; let xs: ((i32) => i32)[] = (if (true) { [((x: i32) => (x + v1)), inc] } else { [inc, inc] }); return (xs[0i32](1i32) + xs[1i32](1i32)) & 63i32; }", 6},
	// An arm spelled as a NAME rather than an array literal (#8163): the named
	// array boxed where it was built, as every function array literal does.
	// Before that, `xs[0](1)` called the box pointer as code: accepted, linked,
	// SIGSEGV, where native runs the same program to 4.
	//
	// named-local-then-arm has the name in the TAKEN arm, and
	// named-local-both-arrays-used calls the named array itself, which proves
	// `ys` still dispatches through its own `$wrap` box.
	// nested-iife-arm-array-named-local puts the name one IIFE deeper.
	// arm-array-param-name and arm-array-call-result name an array this function
	// did not build: a parameter, and a call's result (#10715).
	{"arm-array-named-local-sibling", "function main(): i32 { let v1: i32 = 3i32; let ys: ((i32) => i32)[] = [((z: i32) => z)]; let xs: ((i32) => i32)[] = (if (true) { [((x: i32) => (x + v1))] } else { ys }); return xs[0i32](1i32) & 63i32; }", 4},
	{"arm-array-named-local-else-taken", "function main(): i32 { let v1: i32 = 3i32; let c: boolean = false; let ys: ((i32) => i32)[] = [((z: i32) => (z + 10i32))]; let xs: ((i32) => i32)[] = (if (c) { [((x: i32) => (x + v1))] } else { ys }); return xs[0i32](1i32) & 63i32; }", 11},
	{"arm-array-named-local-then-arm", "function main(): i32 { let v1: i32 = 3i32; let c: boolean = true; let ys: ((i32) => i32)[] = [((z: i32) => (z + 10i32))]; let xs: ((i32) => i32)[] = (if (c) { ys } else { [((x: i32) => (x + v1))] }); return xs[0i32](1i32) & 63i32; }", 11},
	{"arm-array-named-local-both-arrays-used", "function main(): i32 { let v1: i32 = 3i32; let ys: ((i32) => i32)[] = [((z: i32) => z)]; let xs: ((i32) => i32)[] = (if (true) { [((x: i32) => (x + v1))] } else { ys }); return ((xs[0i32](1i32) * 10i32) + ys[0i32](2i32)) & 63i32; }", 42},
	{"matchexpr-arm-array-named-local", "enum S { A, B } function main(): i32 { let v1: i32 = 3i32; let e: S = S.A; let ys: ((i32) => i32)[] = [((z: i32) => (z + 10i32))]; let xs: ((i32) => i32)[] = (match (e) { A => [((x: i32) => (x + v1))], B => ys }); return xs[0i32](1i32) & 63i32; }", 4},
	{"nested-iife-arm-array-named-local", "function main(): i32 { let v1: i32 = 3i32; let ys: ((i32) => i32)[] = [((z: i32) => (z + 10i32))]; let zs: ((i32) => i32)[] = [((w: i32) => (w * 2i32))]; let c: boolean = true; let xs: ((i32) => i32)[] = (if (c) { [((x: i32) => (x + v1))] } else { (if (c) { ys } else { zs }) }); return xs[0i32](1i32) & 63i32; }", 4},
	{"arm-array-param-name", "function pick(ys: ((i32) => i32)[], c: boolean): i32 { let v1: i32 = 3i32; let xs: ((i32) => i32)[] = (if (c) { [((x: i32) => (x + v1))] } else { ys }); return xs[0i32](1i32) & 63i32; } function main(): i32 { let zs: ((i32) => i32)[] = [((z: i32) => z)]; return pick(zs, true); }", 4},
	{"arm-array-param-name-else-taken", "function pick(ys: ((i32) => i32)[], c: boolean): i32 { let v1: i32 = 3i32; let xs: ((i32) => i32)[] = (if (c) { [((x: i32) => (x + v1))] } else { ys }); return xs[0i32](1i32) & 63i32; } function main(): i32 { let zs: ((i32) => i32)[] = [((z: i32) => (z + 10i32))]; return pick(zs, false); }", 11},
	{"arm-array-call-result", "function mk(): ((i32) => i32)[] { return [((z: i32) => z)]; } function main(): i32 { let v1: i32 = 3i32; let xs: ((i32) => i32)[] = (if (true) { [((x: i32) => (x + v1))] } else { mk() }); return xs[0i32](1i32) & 63i32; }", 4},
	{"arm-array-call-result-taken", "function mk(): ((i32) => i32)[] { return [((z: i32) => (z + 20i32))]; } function main(): i32 { let v1: i32 = 3i32; let c: boolean = false; let xs: ((i32) => i32)[] = (if (c) { [((x: i32) => (x + v1))] } else { mk() }); return xs[0i32](1i32) & 63i32; }", 21},
	// `ps`, a named-function array the IIFE never touches, dispatches through
	// its `$wrap` boxes alongside the arm arrays.
	{"arm-array-named-local-unrelated-fnptr-array", "function inc(x: i32): i32 { return x + 1i32; } function main(): i32 { let v1: i32 = 3i32; let ps: ((i32) => i32)[] = [inc]; let ys: ((i32) => i32)[] = [((z: i32) => z)]; let xs: ((i32) => i32)[] = (if (true) { [((x: i32) => (x + v1))] } else { ys }); return ((xs[0i32](1i32) * 10i32) + ps[0i32](1i32)) & 63i32; }", 42},
	{"arm-array-named-local-foreach-over-name", "function main(): i32 { let v1: i32 = 3i32; let ys: ((i32) => i32)[] = [((z: i32) => (z + 1i32))]; let xs: ((i32) => i32)[] = (if (true) { [((x: i32) => (x + v1))] } else { ys }); let t: i32 = 0i32; for f in ys { t = t + f(1i32); } return (xs[0i32](1i32) + t) & 63i32; }", 6},
	{"arm-array-named-local-append-to-name", "function main(): i32 { let v1: i32 = 3i32; let ys: ((i32) => i32)[] = [((z: i32) => (z + 1i32))]; let ws: ((i32) => i32)[] = ys.append(((q: i32) => (q + 5i32))); let xs: ((i32) => i32)[] = (if (true) { [((x: i32) => (x + v1))] } else { ys }); return (xs[0i32](1i32) + ws[1i32](1i32)) & 63i32; }", 10},

	// An all-no-capture arm array and an all-named-function arm array box
	// their elements like any other function array, so the binding they yield
	// dispatches env-first.
	{"arm-array-nocapture", "function main(): i32 { let xs: ((i32) => i32)[] = (if (true) { [((x: i32) => (x + 3i32))] } else { [((y: i32) => y)] }); return xs[0i32](1i32) & 63i32; }", 4},
	{"arm-array-bare-fnnames", "function inc(x: i32): i32 { return x + 1i32; } function dbl(x: i32): i32 { return x * 2i32; } function main(): i32 { let xs: ((i32) => i32)[] = (if (true) { [inc] } else { [dbl] }); return xs[0i32](41i32) & 63i32; }", 42},
	// The IIFE is not the whole value but sits INSIDE one — an array element, a
	// struct field, a call argument. try_fn_field_value owns every such position
	// and had no case for an IIFE, so its arm lambdas were never boxed: the two
	// array cases compiled and SIGSEGV'd (the plain elements stayed bare fn
	// pointers while the hoisted IIFE element yielded a box, #5071's mixed ABI
	// one container out), and the field / argument cases bailed on a `<fn>$clo`
	// nothing built. All four are reduced from the shapes fernsmith seeds 74 and
	// 491 actually contain.
	//
	// payload-capture is the one that needs the IIFE's OWN bindings to count as
	// captures: the arm lambda reads the match arm's payload, which is invisible
	// to lambda_captures against the enclosing function, so the lambda read as
	// capture-free — the array then looked uniform when it was not.
	{"arm-array-iife-element-capturing", "function gen(p1: i32, c: boolean): i32 { let xs: ((i32) => i32)[] = [((a: i32) => a), (if (c) { ((x: i32) => (x + p1)) } else { ((y: i32) => p1) })]; return xs[1i32](3i32); } function main(): i32 { return gen(4i32, true) & 63i32; }", 7},
	{"arm-array-iife-element-payload-capture", "function main(): i32 { let v2: Option[i32] = Some(5i32); let xs: ((i32) => i32)[] = [((z: i32) => z), (match (v2) { Some(p) => ((x: i32) => (x + p)), None => ((y: i32) => y) })]; return xs[1i32](2i32) & 63i32; }", 7},
	{"struct-field-iife-arms", "struct H { f: (i32) => i32 } function main(): i32 { let n: i32 = 4i32; let h: H = H { f: (if (true) { ((x: i32) => (x + n)) } else { ((y: i32) => y) }) }; return h.f(3i32) & 63i32; }", 7},
	{"call-arg-iife-arms", "function apply(f: (i32) => i32, v: i32): i32 { return f(v); } function main(): i32 { let n: i32 = 6i32; return apply((if (true) { ((x: i32) => (x + n)) } else { ((y: i32) => y) }), 3i32) & 63i32; }", 9},
	// An IIFE element whose arms capture NOTHING. The IIFE still yields a box
	// (the hoist is unconditional), so the array must move to the env-first
	// representation WITH it — the sibling bare `__lam_N` element beside a boxed
	// one is #5071 in the other direction, and it exits -1.
	{"arm-array-iife-element-nocapture", "function main(): i32 { let xs: ((i32) => i32)[] = [((a: i32) => (a + 1i32)), (if (true) { ((x: i32) => (x * 2i32)) } else { ((y: i32) => y) })]; return (xs[0i32](1i32) + xs[1i32](3i32)) & 63i32; }", 8},

	// The lambda-returning-lambda the tail-return hoist owns: the per-entry
	// desugar must leave it alone (#5281 regressed exactly this shape).
	{"curry-tail-return-unchanged", "function curry(a: i32): (i32) => ((i32) => i32) { return ((b: i32) => ((c: i32) => (a + b + c))); } function main(): i32 { let g: (i32) => ((i32) => i32) = curry(1i32); let h: (i32) => i32 = g(2i32); return h(3i32) & 63i32; }", 6},

	// An IIFE arm that yields a passthrough call holding a raw lambda. The
	// hoist claims a capturing IIFE only when an arm yields a LAMBDA, and this
	// arm yields a CALL, so nothing walked the arms and the lambda one level in
	// bailed the module. A passthrough hands its argument straight back, so a
	// lambda there is an arm lambda. Reduced from fernsmith seed 393.
	{"arm-passthrough-holds-lambda", "function pick[T](cond: boolean, a: T, b: T): T { return if (cond) { a } else { b }; } function main(): i32 { let v0: (i32) => i32 = ((a: i32) => 40i32); let n: i32 = 2i32; let t: boolean = false; let xs: ((i32) => i32)[] = [(if (t) { v0 } else { pick(t, v0, ((x: i32) => (x + n))) })]; return xs[0i32](3i32) & 63i32; }", 5},

	// EVERY arm of the capturing IIFE is itself a value-position if/match, so
	// the lambda that needs hoisting sits one level in. Both of the hoist's
	// gates read only what an arm RETURNS, and a nested if/match returns a
	// CALL — so an outer IIFE with no directly-lambda arm declined the hoist
	// and the lambda inside reached lower_expr bare, asking for a `<fd>$clo`
	// nothing built. They now descend through a nested IIFE, as the arm-array
	// gate already did.
	//
	// The nesting is the whole trigger, not the IIFE count: giving the outer
	// level a single directly-lambda arm makes the identical program compile,
	// which is why nested-match-expr-fn-arm above passes without this. Reduced
	// from fernsmith seed 199.
	{"nested-iife-arms-capturing", "function main(): i32 { let n: i32 = 5i32; let f: (i32) => i32 = (if (true) { (if (false) { ((a: i32) => 20i32) } else { ((b: i32) => (b + n)) }) } else { (if (true) { ((c: i32) => 40i32) } else { ((d: i32) => 3i32) }) }); return f(2i32) & 63i32; }", 7},
	{"nested-iife-arms-else-branch-taken", "function main(): i32 { let n: i32 = 5i32; let c: boolean = false; let f: (i32) => i32 = (if (c) { (if (false) { ((a: i32) => 20i32) } else { ((b: i32) => (b + n)) }) } else { (if (true) { ((cc: i32) => (n * 4i32)) } else { ((d: i32) => 3i32) }) }); return f(2i32) & 63i32; }", 20},
	{"nested-matchexpr-iife-arms-capturing", "enum S { A, B } function main(): i32 { let n: i32 = 9i32; let e: S = S.A; let f: (i32) => i32 = (match (e) { A => (match (e) { A => ((x: i32) => (x * n)), B => ((y: i32) => y) }), B => (match (e) { A => ((z: i32) => 1i32), B => ((w: i32) => 2i32) }) }); return f(3i32) & 63i32; }", 27},
	{"nested-iife-arms-three-deep", "function main(): i32 { let n: i32 = 6i32; let f: (i32) => i32 = (if (true) { (if (true) { (if (false) { ((a: i32) => 20i32) } else { ((b: i32) => (b + n)) }) } else { (if (true) { ((c: i32) => 1i32) } else { ((d: i32) => 2i32) }) }) } else { (if (true) { ((e: i32) => 3i32) } else { ((g: i32) => 4i32) }) }); return f(1i32) & 63i32; }", 7},
	// Through a factory: the hoisted `<fd>$iifeN` has to register as
	// closure-returning or the caller bare-dispatches the box it hands back.
	{"nested-iife-arms-bound-then-returned", "function gen(n: i32, c: boolean): (i32) => i32 { let w: (i32) => i32 = (if (c) { (if (false) { ((a: i32) => 20i32) } else { ((b: i32) => (b + n)) }) } else { (if (true) { ((cc: i32) => n) } else { ((d: i32) => 3i32) }) }); return w; } function main(): i32 { let f: (i32) => i32 = gen(5i32, true); return f(2i32) & 63i32; }", 7},
	// One nested arm holds an already-boxed closure local beside the lambda:
	// the lambda is still the binding constraint, so the outer IIFE hoists.
	{"nested-iife-arms-mixed-closure-local", "function main(): i32 { let v0: (i32) => i32 = ((a: i32) => 41i32); let n: i32 = 5i32; let c: boolean = true; let f: (i32) => i32 = (if (c) { (if (false) { v0 } else { ((b: i32) => (b + n)) }) } else { (if (c) { v0 } else { v0 }) }); return f(2i32) & 63i32; }", 7},
	// The controls for the two representations the descent must leave alone:
	// nested arms that are all already-boxed closure locals stay on #6323's
	// clo_init marking, and nested bare fn-name arms stay plain fn pointers.
	{"nested-iife-closure-local-arms-unchanged", "function main(): i32 { let v0: (i32) => i32 = ((a: i32) => 41i32); let c: boolean = true; let f: (i32) => i32 = (if (c) { (if (c) { v0 } else { v0 }) } else { (if (c) { v0 } else { v0 }) }); return f(3i32) & 63i32; }", 41},
	{"nested-iife-bare-fnname-arms-unchanged", "function inc(x: i32): i32 { return x + 1i32; } function dbl(x: i32): i32 { return x * 2i32; } function main(): i32 { let c: boolean = true; let f: (i32) => i32 = (if (c) { (if (c) { inc } else { dbl }) } else { (if (c) { dbl } else { inc }) }); return f(40i32) & 63i32; }", 41},

	// An arm literal of bare function names is a function array like any
	// other, so it boxes beside a boxed sibling or a call's result; left raw,
	// the binding dispatched a code address env-first and SIGSEGVed (#8795).
	{"arm-array-fnname-beside-call-result", "function dbl(x: i32): i32 { return x * 2i32; } function mk(): ((i32) => i32)[] { return [((z: i32) => (z + 7i32))]; } function run(xs: ((i32) => i32)[]): i32 { return xs[0i32](4i32); } function pick(c: boolean): i32 { let xs: ((i32) => i32)[] = (if (c) { [dbl] } else { mk() }); return (xs[0i32](1i32) + run(xs)) & 63i32; } function main(): i32 { return pick(true) + pick(false) * 2i32; }", 48},
	{"arm-array-fnnames-only", "function dbl(x: i32): i32 { return x * 2i32; } function inc(x: i32): i32 { return x + 1i32; } function run(xs: ((i32) => i32)[]): i32 { return xs[0i32](4i32); } function pick(c: boolean): i32 { let xs: ((i32) => i32)[] = (if (c) { [dbl] } else { [inc] }); return (xs[0i32](1i32) + run(xs)) & 63i32; } function main(): i32 { return pick(true) + pick(false) * 10i32; }", 80},
	{"arm-array-nocapture-lambda-beside-param", "function run(xs: ((i32) => i32)[]): i32 { return xs[0i32](4i32); } function pick(ys: ((i32) => i32)[], c: boolean): i32 { let xs: ((i32) => i32)[] = (if (c) { [((x: i32) => (x + 1i32))] } else { ys }); return (xs[0i32](1i32) + run(xs)) & 63i32; } function main(): i32 { let zs: ((i32) => i32)[] = [((z: i32) => z)]; return pick(zs, true) + pick(zs, false) * 10i32; }", 57},
}

func TestSelfHostIIFEFnArmIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range iifeFnArmCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

func TestSelfHostIIFEFnArmIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range iifeFnArmCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

func TestSelfHostIIFEFnArmWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range iifeFnArmCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runWasm(t, cli.emit(t, "wasm32-wasi", tc.src)); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
