package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// __heap_bump_bytes() is declared i64. It used to be declared i32 while every
// backend's runtime computed the offset in 64 bits, so the declared result
// silently truncated: a quadratic sweep read 141 MB / 555 MB / -2.09 GB /
// 202 MB, and only the third of those four readings looks wrong. The arena is
// 16 GiB, so the mark passes 2^31 on any run this probe exists to measure.
//
// Two things need pinning, and they fail differently:
//
//   - the DECLARED type. heapBumpIsI64Src binds the probe to an i64 local and
//     does 64-bit-only arithmetic on it, so a re-narrowing to i32 is a compile
//     error, not a subtly wrong number. Cheap; runs on all three backends.
//   - the VALUE above 2^31. Only reachable on the natives (wasm32's whole
//     linear memory is under 4 GiB and its arena far smaller), and it costs a
//     real 2.4 GB of zero-filled allocation — see TestX86_64HeapBumpAbove2GiB.

// heapBumpIsI64Src binds the probe to i64 locals and shifts/masks in 64 bits.
// It also checks the value still reads correctly small: `before` is 0 (nothing
// allocated yet), `after` is a positive one-array high-water whose high 32 bits
// are clear. Returns 7 when every leg holds.
const heapBumpIsI64Src = `function main(): i32 {
    var before: i64 = __heap_bump_bytes();
    var a: i32[] = [1, 2, 3];
    var after: i64 = __heap_bump_bytes();
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

// The wasm leg is the one that needs a real instruction rather than a wider
// register: the cursor and the seed are both i32 linear-memory addresses, so
// the helper subtracts in i32 and zero-extends to the declared i64. A
// sign-extend there would turn exactly the >2 GiB mark this widening exists
// for back into a negative number.
func TestWasmHeapBumpIsI64(t *testing.T) {
	if got := runWasm(t, heapBumpIsI64Src); got != 7 {
		t.Errorf("i64 probe returned %d, want 7", got)
	}
}

// heapBumpAbove2GiBSrc bumps the cursor past 2^31 and reads it back. Two
// __alloc_u8 calls rather than one because the length argument is i32, so a
// single allocation cannot reach the threshold on its own.
//
// The allocations are never read, only kept live so the freelist cannot
// recycle them out from under the high-water mark. Under the old i32 result
// the 2400000032-byte mark read back as -1894967264 and the first check fired.
const heapBumpAbove2GiBSrc = `function main(): i32 {
    var a: u8[] = __alloc_u8(1200000000);
    var b: u8[] = __alloc_u8(1200000000);
    var mark: i64 = __heap_bump_bytes();
    if (mark <= (2147483647 as i64)) { return 1; }
    if (mark < (2400000000 as i64)) { return 2; }
    if (mark > (2500000000 as i64)) { return 3; }
    return a.len() / 1200000000 + b.len() / 1200000000;
}`

// x86-64 only. The two allocations are zero-filled by __alloc_u8 (a freelist
// block can carry stale bytes, so the AOT backends match the interpreter's
// zeroed u8[]), which means the run really does fault in and write 2.4 GB —
// about 13s native, and multiples of that under qemu-aarch64. The arm64
// helper reads the same 64-bit x0 and is covered by TestArm64HeapBumpIsI64;
// paying the qemu cost for a second reading of the same register buys nothing.
func TestX86_64HeapBumpAbove2GiB(t *testing.T) {
	// The program's own RSS peak has to be budgeted against concurrent
	// heavy driver builds, or the two together can OOM the host.
	var got int
	if err := e2eharness.WithBuildMemoryMB(2600, func() error {
		got = mustRunX86_64FreeOn(t, heapBumpAbove2GiBSrc)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	switch got {
	case 2:
		// pass
	case 1:
		t.Error("mark above 2 GiB read back as <= 2^31-1 — the i32 truncation is back")
	default:
		t.Errorf("2.4 GB bump probe returned %d, want 2", got)
	}
}
