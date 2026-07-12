// Differential regression for #4827: an argument/expression-position
// `.append` on an ALIASING operand (a reused ident / field / index) must
// have value semantics — it must NOT mutate the operand's shared backing
// buffer in place. The native grow helper's rc==1 fast path bumps the
// buffer's length header and returns the same pointer; that is only sound
// for the `a = a.append(v)` self-reassign form (whose overwrite-reclaim
// pairs with it). For a reused operand it corrupted later reads of the
// same binding, so interp (correct value semantics) disagreed with every
// compiled backend (x86-64 exited 77 vs interp's 20201 on the headline
// repro). Each case prints its result so the oracle (interp) and all
// three backends are compared by stdout.
//
// The fix (internal/ir emitArrayPush) forces the copy path for a
// non-self-reassign append whose operand aliases an existing binding,
// while leaving the self-reassign in-place optimization (push loops) and
// fresh-temporary appends untouched — the last two are pinned here too so
// a future "optimization" can't quietly reintroduce the aliasing bug.
package e2e

import "testing"

func TestAppendAliasDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping append-alias differential in -short mode")
	}
	cases := []struct {
		name, src string
	}{
		// The headline repro from #4827: `path` (a param) is appended in
		// argument position, then appended again — the second read must see
		// the ORIGINAL path, not the first append's in-place-extended buffer.
		// interp = 20201; compiled was 77 before the fix.
		{"param_arg_position_reused", `import "std/i32";
function walk(path: i32[], depth: i32): i32 {
    if (depth == 0) { return path.len(); }
    var a: i32 = walk(path.append(depth), depth - 1);
    var b: i32 = path.append(depth).len();
    return a * 100 + b;
}
function main(): i32 {
    var p: i32[] = [];
    print(walk(p, 2).to_string());
    return 0;
}`},
		// Deeper recursion (depth 3) — the shape #4810's nested-tuple
		// deep-drop hit in irlower.fern's emit_tuple_child_drops.
		{"param_arg_position_reused_depth3", `import "std/i32";
function walk(path: i32[], depth: i32): i32 {
    if (depth == 0) { return path.len(); }
    var a: i32 = walk(path.append(depth), depth - 1);
    var b: i32 = path.append(depth).len();
    return a * 1000 + b;
}
function main(): i32 {
    var p: i32[] = [];
    print(walk(p, 3).to_string());
    return 0;
}`},
		// Local (not param) ident reused after an expression-position append.
		{"local_ident_reused", `import "std/i32";
function main(): i32 {
    var xs: i32[] = [1, 2, 3];
    var a: i32 = xs.append(9).len();   // 4
    var b: i32 = xs.append(8).len();   // must still be 4, not 5
    var c: i32 = xs.len();             // must still be 3
    print((a * 100 + b * 10 + c).to_string());
    return 0;
}`},
		// A struct field array reused after an append on it.
		{"struct_field_reused", `import "std/i32";
struct Box { items: i32[] }
function main(): i32 {
    var bx: Box = Box { items: [1, 2] };
    var a: i32 = bx.items.append(3).len();   // 3
    var b: i32 = bx.items.append(4).len();   // 3, not 4
    var c: i32 = bx.items.len();             // 2
    print((a * 100 + b * 10 + c).to_string());
    return 0;
}`},
		// An array element (array-of-arrays) reused after an append on it.
		{"index_operand_reused", `import "std/i32";
function main(): i32 {
    var m: i32[][] = [[1, 2], [3]];
    var a: i32 = m[0].append(9).len();   // 3
    var b: i32 = m[0].append(8).len();   // 3, not 4
    var c: i32 = m[0].len();             // 2
    print((a * 100 + b * 10 + c).to_string());
    return 0;
}`},
		// Guard: the self-reassign push loop (`a = a.append(i)`) — the
		// perf-critical in-place fast path — must stay correct. Sum of a
		// growing array's length each iteration; independent of in-place vs
		// copy, but a broken reclaim would diverge or crash.
		{"self_reassign_push_loop", `import "std/i32";
function main(): i32 {
    var a: i32[] = [];
    var i: i32 = 0;
    while (i < 8) { a = a.append(i * i); i = i + 1; }
    var s: i32 = 0;
    var j: i32 = 0;
    while (j < a.len()) { s = s + a[j]; j = j + 1; }
    print((s * 10 + a.len()).to_string());
    return 0;
}`},
		// Guard: a fresh-temporary append (`[..].append(v)`, chained) keeps
		// the in-place fast path (operand is not an alias). Correctness must
		// hold regardless.
		{"fresh_temporary_chain", `import "std/i32";
function main(): i32 {
    var n: i32 = [1, 2, 3].append(4).append(5).append(6).len();  // 6
    print(n.to_string());
    return 0;
}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertNumProgramAgrees(t, tc.src)
		})
	}
}
