package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const heapBumpIsI64Src = `function main(): i32 {
    let before: i64 = __heap_bump_bytes();
    let first: i32 = (before as i32) + 1;
    let a: i32[] = [first, 2, 3];
    let after: i64 = __heap_bump_bytes();
    if (before != (0 as i64)) { return 1; }
    if (after <= before) { return 2; }
    if ((after >> (32 as i64)) != (0 as i64)) { return 3; }
    if ((after & (4294967295 as i64)) != after) { return 4; }
    return a[0] + 6;
}`

func TestX86_64HeapBumpIsI64(t *testing.T) {
	if got := mustRunX86_64FreeOn(t, heapBumpIsI64Src); got != 7 {
		t.Errorf("i64 probe returned %d, want 7", got)
	}
}

func TestArm64HeapBumpIsI64(t *testing.T) {
	if got := mustRunArm64FreeOn(t, heapBumpIsI64Src); got != 7 {
		t.Errorf("i64 probe returned %d, want 7", got)
	}
}

const heapBumpAbove2GiBSrc = `function main(): i32 {
    let a: u8[] = __alloc_u8(1200000000);
    let b: u8[] = __alloc_u8(1200000000);
    let mark: i64 = __heap_bump_bytes();
    if (mark <= (2147483647 as i64)) { return 1; }
    if (mark < (2400000000 as i64)) { return 2; }
    if (mark > (2500000000 as i64)) { return 3; }
    return a.len() / 1200000000 + b.len() / 1200000000;
}`

func TestX86_64HeapBumpAbove2GiB(t *testing.T) {
	var got int
	if err := e2eharness.WithBuildMemoryMB(2600, func() error {
		got = mustRunX86_64FreeOn(t, heapBumpAbove2GiBSrc)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	switch got {
	case 2:
	case 1:
		t.Error("mark above 2 GiB read back as <= 2^31-1 — the i32 truncation is back")
	default:
		t.Errorf("2.4 GB bump probe returned %d, want 2", got)
	}
}
