package e2e

import "testing"

// memcpySizeClassesProgram drives `__memcpy` across every length 0..96 at four
// source/destination misalignments and checks two things per copy: that the n
// bytes landed, and that NOTHING outside the destination range moved.
//
// The second half is the one that matters. The native `__fern_memcpy` copies
// each size class with a pair of possibly-overlapping loads anchored at the
// end of the operands, so an off-by-one in a class boundary writes past the
// requested length instead of failing to write far enough — invisible to a
// test that only compares the copied range. The poison bytes catch it.
//
// src[i] is (i+1) mod 256, so no source byte can be confused with the 0xEE
// poison at more than one position, and the copy is verified elementwise
// rather than by a checksum.
//
// No interpreter leg: `u8[] as usize` is not a cast the interpreter models
// (it has no raw address space), so the program cannot run there at all.
const memcpySizeClassesProgram = `
function check(n: i32, soff: i32, doff: i32): i32 {
    var src: u8[] = __alloc_u8(160);
    var i: i32 = 0;
    while (i < 160) { src = src.with(i, ((i + 1) % 256) as u8); i = i + 1; }
    var dst: u8[] = __alloc_u8(160);
    i = 0;
    while (i < 160) { dst = dst.with(i, 0xEE as u8); i = i + 1; }
    __memcpy((dst as usize) + doff, (src as usize) + soff, n);
    var bad: i32 = 0;
    i = 0;
    while (i < 160) {
        if (i >= doff && i < doff + n) {
            if (dst[i] != src[soff + i - doff]) { bad = bad + 1; }
        } else {
            if (dst[i] != 0xEE as u8) { bad = bad + 1; }
        }
        i = i + 1;
    }
    return bad;
}

function main(): i32 {
    var bad: i32 = 0;
    var n: i32 = 0;
    while (n <= 96) {
        bad = bad + check(n, 0, 0);
        bad = bad + check(n, 1, 0);
        bad = bad + check(n, 0, 3);
        bad = bad + check(n, 7, 5);
        n = n + 1;
    }
    return bad;
}
`

func TestX86_64MemcpySizeClasses(t *testing.T) {
	if _, code := compileAndRunX86_64(t, memcpySizeClassesProgram); code != 0 {
		t.Errorf("x86-64 __memcpy size classes: exit = %d, want 0 (bad byte count)", code)
	}
}

func TestArm64MemcpySizeClasses(t *testing.T) {
	if _, code := compileAndRunArm64(t, memcpySizeClassesProgram); code != 0 {
		t.Errorf("arm64 __memcpy size classes: exit = %d, want 0 (bad byte count)", code)
	}
}

func TestWASMMemcpySizeClasses(t *testing.T) {
	if code := runWasm(t, memcpySizeClassesProgram); code != 0 {
		t.Errorf("wasm __memcpy size classes: exit = %d, want 0 (bad byte count)", code)
	}
}
