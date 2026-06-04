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
    return __heap_bump_bytes() + r.a;
}`
}

func genReuseLive2Src() string {
	return `struct Box { a: i32, b: i32, c: i32, d: i32 }
function main(): i32 {
    var p: Box = Box { a: 1, b: 2, c: 3, d: 4 };
    var q: Box = Box { a: 5, b: 6, c: 7, d: 8 };
    var r: Box = Box { a: 9, b: 10, c: 11, d: 12 };
    return __heap_bump_bytes() + p.a + q.a + r.a;
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

var genReuseCases = []struct{ name, src string }{
	{"churn", genReuseChurnSrc},
	{"aliased", genReuseAliasedSrc},
	{"ptr_churn", genReusePtrChurnSrc},
	{"ptr_aliased", genReusePtrAliasedSrc},
	{"crosstype_churn", genReuseCrossTypeChurnSrc},
	{"crosstype_ptr", genReuseCrossTypePtrSrc},
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
