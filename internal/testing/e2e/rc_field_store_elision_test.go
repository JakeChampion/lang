package e2e

import (
	"testing"
)

const fieldElisionPtrCarriedSrc = `struct Box { id: i32, items: i32[] }
function main(): i32 {
    let b: Box = Box { id: 0, items: [1, 2, 3] };
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 300) {
        b = Box { id: b.id + 1, items: b.items };
        acc = acc + b.items[0] + b.items[2];
        i = i + 1;
    }
    if (b.id != 300) { return 901; }
    if (acc != 1200) { return 902; } // (items[0]+items[2]) = 1+3 = 4 per iter * 300
    return __rc_underflow_count();
}`

const fieldElisionAliasedSrc = `struct Box { id: i32, items: i32[] }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let b: Box = Box { id: 5, items: [1, 2, 3] };
        let b2: Box = b;
        b = Box { id: b.id + 1, items: b.items };
        let junk: i32[] = [9, 9, 9];
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
	if got := runWasm(t, fieldElisionPtrCarriedSrc); got != 0 {
		t.Errorf("carried pointer field: got %d", got)
	}
	if got := runWasm(t, fieldElisionAliasedSrc); got != 0 {
		t.Errorf("aliased struct COW: got %d", got)
	}
}
