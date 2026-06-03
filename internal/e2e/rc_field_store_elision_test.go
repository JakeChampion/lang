package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Field-store elision (Perceus reuse specialization) on the struct
// self-overwrite reuse path. A field carried over UNCHANGED (`f: p.f`) keeps
// its value in the reused box, so on the reuse branch its store + retain +
// old-value release are elided (the box already holds it, rc unchanged). The
// store + retain are still emitted on the fresh-alloc path. This is Fern's
// dominant record-update idiom: E048 forbids field assignment, so updating
// one field is written `p = T{ changed: ..., rest: p.rest }`.
//
// The soundness-critical case is a carried POINTER field: eliding its
// retain+release must leave the array/box reference intact (not freed, not
// over-released) across many reuses, and an aliased struct must still copy.

// fieldElisionPtrCarriedSrc: a struct with a carried i32[] field, updated in
// a loop. The carried `items` buffer must survive every reuse untouched.
const fieldElisionPtrCarriedSrc = `struct Box { id: i32, items: i32[] }
function main(): i32 {
    var b: Box = Box { id: 0, items: [1, 2, 3] };
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 300) {
        b = Box { id: b.id + 1, items: b.items };
        acc = acc + b.items[0] + b.items[2];
        i = i + 1;
    }
    if (b.id != 300) { return 901; }
    if (acc != 1200) { return 902; } // (items[0]+items[2]) = 1+3 = 4 per iter * 300
    return __rc_underflow_count();
}`

// fieldElisionAliasedSrc: an aliased struct (var b2 = b) must still COW —
// updating b leaves b2 intact, and the carried-field elision must not corrupt
// the shared box. Returns 0 iff value-correct AND 0 over-releases.
const fieldElisionAliasedSrc = `struct Box { id: i32, items: i32[] }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var b: Box = Box { id: 5, items: [1, 2, 3] };
        var b2: Box = b;
        b = Box { id: b.id + 1, items: b.items };
        var junk: i32[] = [9, 9, 9];
        acc = acc + b.id + b2.id + b.items[0] + junk[0];
        i = i + 1;
    }
    // b.id=6, b2.id=5 (alias intact), b.items[0]=1, junk=9 -> 21 per iter * 200
    if (acc != 4200) { return 999; }
    return __rc_underflow_count();
}`

func TestX86_64FieldStoreElision(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, fieldElisionPtrCarriedSrc); code != 0 {
		t.Errorf("carried pointer field: code=%d", code)
	}
	if _, code := compileAndRunX86_64FreeOn(t, fieldElisionAliasedSrc); code != 0 {
		t.Errorf("aliased struct COW: code=%d", code)
	}
}

func TestArm64FieldStoreElision(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, fieldElisionPtrCarriedSrc); code != 0 {
		t.Errorf("carried pointer field: code=%d", code)
	}
	if _, code := compileAndRunArm64FreeOn(t, fieldElisionAliasedSrc); code != 0 {
		t.Errorf("aliased struct COW: code=%d", code)
	}
}

func TestWASMFieldStoreElision(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, fieldElisionPtrCarriedSrc); got != 0 {
		t.Errorf("carried pointer field: got %d", got)
	}
	if got := runWasm(t, fieldElisionAliasedSrc); got != 0 {
		t.Errorf("aliased struct COW: got %d", got)
	}
}
