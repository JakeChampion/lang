// The struct-update spread `p = T{ ...p, f: v }` is Fern's record-update
// idiom — E048 forbids field assignment, so every mutation of a struct is
// written this way, so tryStructReuseOverwrite must not refuse it — that
// path places only the
// explicitly listed fields and a fresh box's un-listed ones would read back
// as 0. Filling the un-listed fields in as the `p.<name>` the spread means
// (structUpdateFieldInits) makes them ordinary CARRIED fields, which the
// path already knew how to place on both branches — so the idiom reuses its
// box instead of allocating a new one per update.
package ir_test

import "testing"

// Fires: a self-spread on an owned local reuses the box. Without the
// normalisation this lowered through the general StructLit path — one fresh
// box per update, i.e. one allocation per loop iteration.
func TestStructUpdateSpreadSelfOverwriteReuses(t *testing.T) {
	ip := lowerForTest(t, `struct S { xs: i32[], n: i32, m: i32 }
function main(): i32 {
    var s: S = S { xs: [1, 2, 3], n: 0, m: 7 };
    var i: i32 = 0;
    while (i < 3) { s = S { ...s, n: s.n + 1 }; i = i + 1; }
    return s.n + s.m + s.xs.len();
}`)
	if got := allocReuseCount(funcByName(ip, "main")); got != 1 {
		t.Errorf("self-spread update should reuse the box (one __alloc_reuse), got %d", got)
	}
}

// Does NOT fire: a spread of a DIFFERENT base. Its un-listed fields come from
// another box, so they are not `p.<name>` and the fresh-alloc branch would
// leave them uninitialised — the general StructLit lowering, which copies the
// base's fields properly, still owns this shape.
func TestStructUpdateSpreadForeignBaseDefers(t *testing.T) {
	ip := lowerForTest(t, `struct S { xs: i32[], n: i32, m: i32 }
function main(): i32 {
    var a: S = S { xs: [1, 2, 3], n: 0, m: 7 };
    var b: S = S { xs: [4], n: 1, m: 8 };
    b = S { ...a, n: 5 };
    return b.n + b.m + b.xs.len();
}`)
	if got := allocReuseCount(funcByName(ip, "main")); got != 0 {
		t.Errorf("spread of a foreign base must defer to the general lowering, got %d __alloc_reuse", got)
	}
}
