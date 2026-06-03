package ir

import "testing"

// Field-store elision fires in tryStructReuseOverwrite for a carried-over
// field (`y: p.y`): its store/retain/release are gated onto the fresh-alloc
// path, which emits the `reused == 0` (OpNot + OpIf) guard. A self-overwrite
// whose fields all CHANGE (a swap) has nothing to elide, so no such guard is
// emitted. OpNot appears nowhere else in these `!`-free programs, so its
// presence is a clean signal that the elision path was taken.

func TestFieldStoreElisionFiresForCarriedField(t *testing.T) {
	p := lowerSource(t, `struct Point { x: i32, y: i32 }
function main(): i32 {
    var p: Point = Point { x: 1, y: 2 };
    p = Point { x: p.x + 1, y: p.y };
    return p.x;
}`)
	if !hasOp(p, "main", OpNot) {
		t.Errorf("expected field-store elision (the fresh-path OpNot guard) for the carried `y: p.y` field; ops:\n%s", p)
	}
}

func TestFieldStoreElisionSkipsAllChangedSwap(t *testing.T) {
	p := lowerSource(t, `struct Point { x: i32, y: i32 }
function main(): i32 {
    var p: Point = Point { x: 1, y: 2 };
    p = Point { x: p.y, y: p.x };
    return p.x;
}`)
	if hasOp(p, "main", OpNot) {
		t.Errorf("a swap changes every field — no carried field to elide, so no fresh-path guard expected; ops:\n%s", p)
	}
}
