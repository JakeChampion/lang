package ir_test

import (
	"testing"
)

// General FBIP reuse (computeReuseSources): a dead, owned, all-scalar struct
// local D is paired with a LATER same-type construction C (a different local),
// so C reuses D's box in place via __alloc_reuse — the reuse token threaded
// across DIFFERENT locals, beyond the self-overwrite tryStructReuseOverwrite.

// Fires: `a` is dead after `s = a.x`, and `b` constructs the same Point type —
// b reuses a's box. One __alloc_reuse in main.
func TestGeneralReuseFiresForDeadLocal(t *testing.T) {
	ip := lowerForTest(t, `struct Point { x: i32, y: i32 }
function main(): i32 {
    var a: Point = Point { x: 1, y: 2 };
    var s: i32 = a.x + a.y;
    var b: Point = Point { x: s + 1, y: 9 };
    return b.x + b.y;
}`)
	f := funcByName(ip, "main")
	if f == nil {
		t.Fatal("no func main")
	}
	if got := allocReuseCount(f); got != 1 {
		t.Errorf("general reuse should emit one __alloc_reuse (b reuses dead a's box), got %d", got)
	}
}

// Fires inside a LOOP body (the high-value case): each iteration's dead `a`
// is reused for `b`. One __alloc_reuse (the pairing is syntactic).
func TestGeneralReuseFiresInLoopBody(t *testing.T) {
	ip := lowerForTest(t, `struct Point { x: i32, y: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 10) {
        var a: Point = Point { x: i, y: i + 1 };
        var s: i32 = a.x + a.y;
        var b: Point = Point { x: s, y: i };
        acc = acc + b.x + b.y;
        i = i + 1;
    }
    return acc;
}`)
	f := funcByName(ip, "main")
	if got := allocReuseCount(f); got != 1 {
		t.Errorf("general reuse should fire in the loop body (dead a -> b), got %d", got)
	}
}

// Skips: `a` is STILL LIVE at b's construction (read after) — no pairing.
func TestGeneralReuseSkipsLiveSource(t *testing.T) {
	ip := lowerForTest(t, `struct Point { x: i32, y: i32 }
function main(): i32 {
    var a: Point = Point { x: 1, y: 2 };
    var b: Point = Point { x: a.x + 1, y: 9 };
    return a.y + b.x;
}`)
	f := funcByName(ip, "main")
	if got := allocReuseCount(f); got != 0 {
		t.Errorf("a is live at b (read after) — no reuse expected, got %d", got)
	}
}

// Skips: source read INSIDE the construction (b's field reads a) keeps a live
// at C, so no pairing (a is not dead-from-C).
func TestGeneralReuseSkipsSourceReadInConstruction(t *testing.T) {
	ip := lowerForTest(t, `struct Point { x: i32, y: i32 }
function main(): i32 {
    var a: Point = Point { x: 1, y: 2 };
    var b: Point = Point { x: a.x + 1, y: a.y };
    return b.x + b.y;
}`)
	f := funcByName(ip, "main")
	if got := allocReuseCount(f); got != 0 {
		t.Errorf("a read in b's construction — not dead-from-C, no reuse, got %d", got)
	}
}

// Fires for a single-word rc-tracked POINTER field (array): a is dead after
// `s`, so b reuses a's box; a's old items reference is released on the reuse
// branch before b's fresh items overwrites it.
func TestGeneralReuseFiresForPointerField(t *testing.T) {
	ip := lowerForTest(t, `struct Holder { id: i32, items: i32[] }
function main(): i32 {
    var a: Holder = Holder { id: 1, items: [1, 2] };
    var s: i32 = a.id + a.items[0];
    var b: Holder = Holder { id: s, items: [3, 4] };
    return b.id;
}`)
	f := funcByName(ip, "main")
	if got := allocReuseCount(f); got != 1 {
		t.Errorf("pointer-field struct should reuse a dead local's box, got %d", got)
	}
}

// Skips: a STRING field (two-word on wasm / boxed on arm64) is excluded by
// structReuseEligible — same gate as the self-overwrite 5c path.
func TestGeneralReuseSkipsStringField(t *testing.T) {
	ip := lowerForTest(t, `struct Named { id: i32, name: string }
function main(): i32 {
    var a: Named = Named { id: 1, name: "x" };
    var s: i32 = a.id;
    var b: Named = Named { id: s, name: "y" };
    return b.id;
}`)
	f := funcByName(ip, "main")
	if got := allocReuseCount(f); got != 0 {
		t.Errorf("string-field struct is excluded (two-word), got %d", got)
	}
}

// Skips: a WIDE scalar field (i64) is excluded by structReuseEligible.
func TestGeneralReuseSkipsWideScalarField(t *testing.T) {
	ip := lowerForTest(t, `struct Wide { id: i32, big: i64 }
function main(): i32 {
    var a: Wide = Wide { id: 1, big: 2 };
    var s: i32 = a.id;
    var b: Wide = Wide { id: s, big: 3 };
    return b.id;
}`)
	f := funcByName(ip, "main")
	if got := allocReuseCount(f); got != 0 {
		t.Errorf("wide-scalar-field struct is excluded, got %d", got)
	}
}
