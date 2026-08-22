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
		// Append then a plain read of the same ident — the read must see the
		// original array, not an in-place-extended one.
		{"ident_append_then_read", `import "std/i32";
function main(): i32 {
    var xs: i32[] = [4, 5];
    var a: i32 = xs.append(6).len();   // 3
    var b: i32 = xs.len();             // 2, not 3
    print((a * 10 + b).to_string());
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
		// Guard: the RETURN-position accumulator (the self-host compiler's
		// AST-walker shape) — exempt from the forced copy (inPlacePushes) so
		// the leak-mode self-compile stays O(N); the caller rebinds the
		// threaded accumulator, so in-place vs copy is unobservable and all
		// backends must agree with interp.
		{"return_position_accumulator", `import "std/i32";
function collect(acc: i32[], v: i32): i32[] {
    return acc.append(v * 3);
}
function main(): i32 {
    var acc: i32[] = [];
    var i: i32 = 0;
    while (i < 6) { acc = collect(acc, i); i = i + 1; }
    var s: i32 = 0;
    var j: i32 = 0;
    while (j < acc.len()) { s = s + acc[j]; j = j + 1; }
    print((s * 10 + acc.len()).to_string());
    return 0;
}`},
		// Guard: the borrowed-param self-reassign (`xs = xs.append(i)` where
		// xs is a plain param) — the other inPlacePushes exemption. The
		// rebind means later reads see the appended value on every path.
		{"param_self_reassign_threaded", `import "std/i32";
function extend(xs: i32[], n: i32): i32[] {
    var i: i32 = 0;
    while (i < n) { xs = xs.append(i); i = i + 1; }
    return xs;
}
function main(): i32 {
    var a: i32[] = extend([], 4);
    a = extend(a, 3);
    var s: i32 = 0;
    var j: i32 = 0;
    while (j < a.len()) { s = s + a[j]; j = j + 1; }
    print((s * 10 + a.len()).to_string());
    return 0;
}`},
		// The interpreter grows an append's receiver in place when the
		// slot it extends into is unclaimed (#6395), so a loop-built
		// array carries spare capacity two views can reach. Only one of
		// them may take a given slot; the other must copy, and both must
		// agree with the backends' refcount-driven choice.
		{"grown_buffer_two_appends", `import "std/i32";
function main(): i32 {
    var a: i32[] = [];
    var i: i32 = 0;
    while (i < 5) { a = a.append(i); i = i + 1; }
    var x: i32[] = a.append(100);
    var y: i32[] = a.append(200);
    print((x[5] + y[5] * 10 + a.len() * 100).to_string());
    return 0;
}`},
		// Same split, with one view reached through a struct field.
		{"grown_buffer_append_through_field", `import "std/i32";
struct Box { items: i32[] }
function main(): i32 {
    var a: i32[] = [];
    var i: i32 = 0;
    while (i < 5) { a = a.append(i); i = i + 1; }
    var b: Box = Box { items: a };
    var x: i32[] = b.items.append(100);
    var y: i32[] = a.append(200);
    print((x[5] + y[5] * 10 + b.items.len() * 100).to_string());
    return 0;
}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertNumProgramAgrees(t, tc.src)
		})
	}
}

// appendCopyLeakBoundProgram pins the reclaim half of the #4827 fix: the
// forced copy (#4838) must DIE when consumed by a borrowing call, not
// leak. `take(path.append(i))` with `path` reused after leaked one whole
// buffer per call. The stage-(b) appendCopyTempType recognizer stashes it
// and decs it after the call (scalar-element arrays only; a pointer-element
// copy's elements alias the original's and must not be deep-dropped). 5000
// iterations must stay heap-flat (< 512 B growth), the operand must be
// untouched (value semantics), and no rc underflow may fire.
const appendCopyLeakBoundProgram = `function take(xs: i32[]): i32 { return xs.len(); }

function main(): i32 {
    var path: i32[] = [1, 2];
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 200) { acc = acc + take(path.append(w)); w = w + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < 5000) { acc = acc + take(path.append(i)); i = i + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (path.len() != 2) { return 97; }
    if (acc < 0) { return 96; }
    return 0;
}
`

func TestX86_64AppendCopyLeakBound(t *testing.T) {
	if _, code := compileAndRunX86_64(t, appendCopyLeakBoundProgram); code != 0 {
		t.Errorf("x86-64 append-copy leak bound: exit = %d, want 0 (98 = copy leaked; 99 = over-release; 97 = operand mutated)", code)
	}
}
