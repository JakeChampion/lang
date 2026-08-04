package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// General FBIP reuse (Perceus reuse token, computeReuseSources): a DEAD, owned,
// all-scalar struct local D is paired with a LATER same-type construction C (a
// DIFFERENT local) in the same block, so C reuses D's box in place via
// __alloc_reuse — beyond the self-overwrite tryStructReuseOverwrite (D == C).
// The high-value case is a loop body: each iteration's dead `a` becomes `b`'s
// box, so the loop holds ~1 Point box instead of bump-allocating two per turn.
//
// Soundness rests on the same two gates as self-overwrite reuse: freeEligible
// (D is OWNED, never a borrowed param) + the runtime is_unique check (a shared
// D declines reuse, dec's, and C fresh-allocs — the alias keeps D's box). D's
// slot is zeroed after the hand-off so the exit sweep / a non-C path never
// double-releases. A mispaired class degrades to free+alloc (never unsound).

// genReuseChurnSrc: 300 iterations, each builds a dead `a` then reuses it for
// `b`. The returned value is only correct if every reuse wrote the right box.
const genReuseChurnSrc = `struct Point { x: i32, y: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 300) {
        var a: Point = Point { x: i, y: i + 1 };
        var s: i32 = a.x + a.y;          // a's last use
        var b: Point = Point { x: s + 1, y: i };   // reuses a's box
        acc = acc + b.x + b.y;
        i = i + 1;
    }
    // s = 2i+1, b.x = 2i+2, b.y = i -> b.x+b.y = 3i+2; sum i=0..299 = 3*44850 + 600 = 135150
    if (acc != 135150) { return 999; }
    return __rc_underflow_count();
}`

// genReuseAliasedSrc: D (`a`) is aliased into a live `keep` before its last
// use, so at runtime rc>1 and reuse DECLINES (b fresh-allocs). keep must still
// read a's original values; nothing over-releases.
const genReuseAliasedSrc = `struct Point { x: i32, y: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var a: Point = Point { x: i, y: i + 1 };
        var keep: Point = a;              // alias -> rc 2, reuse declines
        var s: i32 = a.x;
        var b: Point = Point { x: s, y: 7 };
        acc = acc + keep.x + keep.y + b.y;   // keep sees a's [i, i+1]; b.y=7
        i = i + 1;
    }
    // keep.x + keep.y + b.y = i + (i+1) + 7 = 2i+8; sum i=0..199 = 2*19900 + 1600 = 41400
    if (acc != 41400) { return 999; }
    return __rc_underflow_count();
}`

// genReuseDead2Src / genReuseLive2Src: heap-bump win probe. Two sequentially
// dead Points reused vs two simultaneously live — reuse holds ~1 box.
func genReuseDead2Src() string {
	return `struct Box { a: i32, b: i32, c: i32, d: i32 }
function main(): i32 {
    var p: Box = Box { a: 1, b: 2, c: 3, d: 4 };
    var s: i32 = p.a + p.d;
    var q: Box = Box { a: s, b: 0, c: 0, d: 0 };   // reuses p's box
    var t: i32 = q.a;
    var r: Box = Box { a: t, b: 0, c: 0, d: 0 };   // reuses q's box
    return (__heap_bump_bytes() as i32) + r.a;
}`
}

func genReuseLive2Src() string {
	return `struct Box { a: i32, b: i32, c: i32, d: i32 }
function main(): i32 {
    var p: Box = Box { a: 1, b: 2, c: 3, d: 4 };
    var q: Box = Box { a: 5, b: 6, c: 7, d: 8 };
    var r: Box = Box { a: 9, b: 10, c: 11, d: 12 };
    return (__heap_bump_bytes() as i32) + p.a + q.a + r.a;
}`
}

// genReusePtrChurnSrc: pointer-field general reuse. Each iteration builds a
// dead Holder `a` with an array, then reuses a's box for `b` with a FRESH
// array — a's old array is deep-freeing-dropped on the reuse branch before b's
// store overwrites it (no leak), b's array is retained. Value + 0 over-release.
const genReusePtrChurnSrc = `struct Holder { id: i32, items: i32[] }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var a: Holder = Holder { id: i, items: [i, i + 1] };
        var s: i32 = a.id + a.items[0] + a.items[1];   // a's last use
        var b: Holder = Holder { id: s, items: [i + 2, i + 3] };   // reuses a's box
        acc = acc + b.id + b.items[0] + b.items[1];
        i = i + 1;
    }
    // s = i + i + (i+1) = 3i+1; b.id=3i+1, b.items sum = (i+2)+(i+3)=2i+5; total 5i+6
    // sum i=0..199 = 5*19900 + 6*200 = 100700
    if (acc != 100700) { return 999; }
    return __rc_underflow_count();
}`

// genReusePtrAliasedSrc: `a` is aliased into a live `keep` (rc>1), so the
// runtime is_unique check DECLINES reuse — b fresh-allocs and keep keeps a's
// box + array intact. Mirrors the self-overwrite `aliased` contract.
const genReusePtrAliasedSrc = `struct Holder { id: i32, items: i32[] }
function main(): i32 {
    var a: Holder = Holder { id: 1, items: [7, 8] };
    var keep: Holder = a;             // alias -> rc 2, reuse declines
    var s: i32 = a.id;
    var b: Holder = Holder { id: s + 1, items: [3, 4] };
    if (keep.id != 1) { return 1; }
    if (keep.items[0] != 7) { return 2; }
    if (b.id != 2) { return 3; }
    if (b.items[1] != 4) { return 4; }
    return __rc_underflow_count();
}`

// genReuseCrossTypeChurnSrc: cross-type box-class reuse. Each iteration's dead
// Point (2×i32, class 16) is reused for a Pair (2×i32, class 16) — a DIFFERENT
// struct type of the same class. Value-correct only if every reuse wrote the
// right block at C's (Pair's) offsets.
const genReuseCrossTypeChurnSrc = `struct Point { x: i32, y: i32 }
struct Pair { a: i32, b: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 300) {
        var p: Point = Point { x: i, y: i + 1 };
        var s: i32 = p.x + p.y;          // p's last use
        var q: Pair = Pair { a: s, b: i };   // reuses p's box (same class)
        acc = acc + q.a + q.b;
        i = i + 1;
    }
    // s = 2i+1, q.a=2i+1, q.b=i -> q.a+q.b = 3i+1; sum i=0..299 = 3*44850 + 300 = 134850
    if (acc != 134850) { return 999; }
    return __rc_underflow_count();
}`

// genReuseCrossTypePtrSrc: cross-type WITH pointer fields. Dead Holder
// (id, items) reused for Bag (tag, data) — same class. D's old array released
// at D's offset, C's array stored at C's offset. 200-iter, 0 over-release.
const genReuseCrossTypePtrSrc = `struct Holder { id: i32, items: i32[] }
struct Bag { tag: i32, data: i32[] }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var a: Holder = Holder { id: i, items: [i, i + 1] };
        var s: i32 = a.id + a.items[0] + a.items[1];   // a's last use
        var b: Bag = Bag { tag: s, data: [i + 2, i + 3] };   // reuses a's box
        acc = acc + b.tag + b.data[0] + b.data[1];
        i = i + 1;
    }
    // s = i + i + (i+1) = 3i+1; b.tag=3i+1, b.data sum = 2i+5; total 5i+6
    // sum i=0..199 = 5*19900 + 6*200 = 100700
    if (acc != 100700) { return 999; }
    return __rc_underflow_count();
}`

// genReuseTupleChurnSrc: tuple-source reuse. Each iteration's dead tuple `a`
// is reused for tuple `b`. Value-correct only if every reuse wrote the right
// block at the tuple's element offsets.
const genReuseTupleChurnSrc = `function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 300) {
        var a: (i32, i32) = (i, i + 1);
        var s: i32 = a.0 + a.1;          // a's last use
        var b: (i32, i32) = (s + 1, i);   // reuses a's box
        acc = acc + b.0 + b.1;
        i = i + 1;
    }
    // s = 2i+1, b.0=2i+2, b.1=i -> b.0+b.1 = 3i+2; sum i=0..299 = 3*44850 + 600 = 135150
    if (acc != 135150) { return 999; }
    return __rc_underflow_count();
}`

// genReuseTuplePtrSrc: tuple WITH a pointer element. Dead (i32, i32[]) reused
// for a fresh (i32, i32[]) — D's old array released at D's offset each turn.
const genReuseTuplePtrSrc = `function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var a: (i32, i32[]) = (i, [i, i + 1]);
        var s: i32 = a.0 + a.1[0] + a.1[1];   // a's last use
        var b: (i32, i32[]) = (s, [i + 2, i + 3]);   // reuses a's box
        acc = acc + b.0 + b.1[0] + b.1[1];
        i = i + 1;
    }
    // s = i + i + (i+1) = 3i+1; b.0=3i+1, b.1 sum = 2i+5; total 5i+6
    // sum i=0..199 = 5*19900 + 6*200 = 100700
    if (acc != 100700) { return 999; }
    return __rc_underflow_count();
}`

// genReuseEnumChurnSrc: enum-source reuse with a uniform-droppable
// Wrap(i32[]). Each iteration's dead `a` is reused for `b`; a's old array is
// freed at the uniform payload offset on the reuse branch each turn (no leak),
// b's fresh array retained. Value-correct only if every reuse wrote the right
// block.
const genReuseEnumChurnSrc = `enum Wrapper { Wrap(i32[]) }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var a: Wrapper = Wrap([i, i + 1]);
        var s: i32 = match (a) { Wrap(xs) => xs[0] + xs[1] };   // a's last use
        var b: Wrapper = Wrap([s, i]);                          // reuses a's box
        acc = acc + match (b) { Wrap(xs) => xs[0] + xs[1] };
        i = i + 1;
    }
    // s = i + (i+1) = 2i+1; b = [2i+1, i] -> sum 3i+1; total i=0..199 = 3*19900 + 200 = 59900
    if (acc != 59900) { return 999; }
    return __rc_underflow_count();
}`

// genReuseEnumCrossVariantSrc: a uniform two-variant enum, D and C may differ
// in variant (Keep vs Swap, same box class). Exercises the variant-independent
// old-payload free.
const genReuseEnumCrossVariantSrc = `enum Bag { Keep(i32[]), Swap(i32[]) }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var a: Bag = Keep([i, i + 1]);
        var s: i32 = match (a) { Keep(xs) => xs[0], Swap(xs) => xs[1] };   // a dead
        var b: Bag = Swap([s, i + 2]);                                     // reuses a's box
        acc = acc + match (b) { Keep(xs) => xs[0], Swap(xs) => xs[1] };
        i = i + 1;
    }
    // s = i (Keep.xs[0]); b = Swap([i, i+2]) -> match picks xs[1] = i+2; total i=0..199 = sum(i+2) = 19900 + 400 = 20300
    if (acc != 20300) { return 999; }
    return __rc_underflow_count();
}`

// genReuseCrossBlockScalarSrc: cross-block reuse — a function-top-level D is
// reused by a construction NESTED in an if-body. Exercises BOTH paths: go_=true
// reuses D's box for b; go_=false leaves D for the exit sweep. Neither may
// double-free. `go_` is a param so the branch isn't const-folded.
const genReuseCrossBlockScalarSrc = `struct Point { x: i32, y: i32 }
function run(go_: boolean): i32 {
    var a: Point = Point { x: 10, y: 20 };
    var s: i32 = a.x + a.y;          // a's last use, before the if
    var acc: i32 = 0;
    if (go_) {
        var b: Point = Point { x: s + 1, y: 5 };   // reuses a's box on this path
        acc = b.x + b.y;
    }
    return acc;
}
function main(): i32 {
    var t: i32 = run(true);    // s=30; b={31,5} -> 36
    var f: i32 = run(false);   // a exit-swept, acc=0
    if (t != 36) { return 1; }
    if (f != 0) { return 2; }
    return __rc_underflow_count();
}`

// genReuseCrossBlockPtrSrc: the pointer-field cross-block case — the reuse-path
// frees D's old array at C, the not-taken path frees D in the exit sweep. The
// adversarial double-free check for cross-block.
const genReuseCrossBlockPtrSrc = `struct Holder { id: i32, items: i32[] }
function runp(go_: boolean): i32 {
    var a: Holder = Holder { id: 1, items: [7, 8] };
    var s: i32 = a.id + a.items[0] + a.items[1];   // a's last use
    var acc: i32 = 0;
    if (go_) {
        var b: Holder = Holder { id: s, items: [3, 4] };   // reuses a's box; a's [7,8] freed here
        acc = b.id + b.items[0] + b.items[1];
    }
    return acc;
}
function main(): i32 {
    var t: i32 = runp(true);   // s=16; b={16,[3,4]} -> 23
    var f: i32 = runp(false);  // a (with [7,8]) exit-swept
    if (t != 23) { return 1; }
    if (f != 0) { return 2; }
    return __rc_underflow_count();
}`

// genReuseCrossBlockLoopSrc: the dominant cross-block shape — a loop-body D
// reused by a construction nested in an if INSIDE the loop, every iteration.
// The if runs on even iterations only, so odd iterations leave D for the
// next-iteration reinit drop (the adversarial double-free / leak check across
// the taken / not-taken alternation), with a pointer field to free.
const genReuseCrossBlockLoopSrc = `struct Holder { id: i32, items: i32[] }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var a: Holder = Holder { id: i, items: [i, i + 1] };
        var s: i32 = a.id + a.items[0] + a.items[1];   // a's last use in the loop body
        if (i % 2 == 0) {
            var b: Holder = Holder { id: s, items: [i + 2, i + 3] };   // reuses a's box
            acc = acc + b.id + b.items[0] + b.items[1];
        }
        i = i + 1;
    }
    // even i in [0,198]: s = i + i + (i+1) = 3i+1; b.id=3i+1, b.items=(i+2)+(i+3)=2i+5; per = 5i+6
    // i=2j, j=0..99: 5*2j+6 = 10j+6; sum = 10*(99*100/2) + 6*100 = 49500 + 600 = 50100
    if (acc != 50100) { return 999; }
    return __rc_underflow_count();
}`

var genReuseCases = []struct{ name, src string }{
	{"crossblock_scalar", genReuseCrossBlockScalarSrc},
	{"crossblock_ptr", genReuseCrossBlockPtrSrc},
	{"crossblock_loop", genReuseCrossBlockLoopSrc},
	{"churn", genReuseChurnSrc},
	{"aliased", genReuseAliasedSrc},
	{"ptr_churn", genReusePtrChurnSrc},
	{"ptr_aliased", genReusePtrAliasedSrc},
	{"crosstype_churn", genReuseCrossTypeChurnSrc},
	{"crosstype_ptr", genReuseCrossTypePtrSrc},
	{"tuple_churn", genReuseTupleChurnSrc},
	{"tuple_ptr", genReuseTuplePtrSrc},
	{"enum_churn", genReuseEnumChurnSrc},
	{"enum_cross_variant", genReuseEnumCrossVariantSrc},
}

func TestX86_64GeneralReuse(t *testing.T) {
	for _, c := range genReuseCases {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64FreeOn(t, c.src); code != 0 {
				t.Errorf("%s: got %d, want 0", c.name, code)
			}
		})
	}
}

func TestArm64GeneralReuse(t *testing.T) {
	for _, c := range genReuseCases {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64FreeOn(t, c.src); code != 0 {
				t.Errorf("%s: got %d, want 0", c.name, code)
			}
		})
	}
}

func TestWASMGeneralReuse(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	for _, c := range genReuseCases {
		t.Run(c.name, func(t *testing.T) {
			if got := runWasm(t, c.src); got != 0 {
				t.Errorf("%s: got %d, want 0", c.name, got)
			}
		})
	}
	// Heap-bump win: the reused chain holds fewer live boxes than the
	// simultaneously-live control.
	dead := runWasm(t, genReuseDead2Src())
	live := runWasm(t, genReuseLive2Src())
	if dead >= live {
		t.Errorf("general reuse should lower peak boxes: dead-chain %d should be < live %d", dead, live)
	}
}
