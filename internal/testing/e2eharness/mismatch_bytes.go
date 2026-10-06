package e2eharness

import "strings"

// MismatchBytesSource exercises every mismatch position around vector widths,
// bounds clamping, retained aliases and allocation-free borrowed calls.
func MismatchBytesSource(allocations bool) string {
	probe := ""
	if allocations {
		probe = `let before: i64 = __heap_alloc_count();
    let repeat: i32 = 0;
    while (repeat < 1000) {
        if (scan(all, 0, other, 0, 256) != 256) { return 10; }
        repeat = repeat + 1;
    }
    if (__heap_alloc_count() != before) { return 11; }`
	}
	return strings.Replace(mismatchBytesProgram, "// Allocation probe", probe, 1)
}

const mismatchBytesProgram = `function slow(a: u8[], ao0: i32, b: u8[], bo0: i32, n0: i32): i32 {
    let ao: i32 = ao0;
    let bo: i32 = bo0;
    let n: i32 = n0;
    if (ao < 0) { ao = 0; }
    if (bo < 0) { bo = 0; }
    if (ao > a.len()) { ao = a.len(); }
    if (bo > b.len()) { bo = b.len(); }
    if (n > a.len() - ao) { n = a.len() - ao; }
    if (n > b.len() - bo) { n = b.len() - bo; }
    if (n < 0) { n = 0; }
    let i: i32 = 0;
    while (i < n) {
        if (a[ao + i] != b[bo + i]) { return i; }
        i = i + 1;
    }
    return n;
}
fip function scan(a: u8[], ao: i32, b: u8[], bo: i32, n: i32): i32 {
    return __mismatch_bytes(a, ao, b, bo, n);
}
function check(a: u8[], ao: i32, b: u8[], bo: i32, n: i32): boolean {
    return scan(a, ao, b, bo, n) == slow(a, ao, b, bo, n);
}
function main(): i32 {
    let all: u8[] = __alloc_u8(256);
    let other: u8[] = __alloc_u8(256);
    let i: i32 = 0;
    while (i < 256) { all = all.with(i, i as u8); other = other.with(i, i as u8); i = i + 1; }
    let held: u8[] = all;
    // Allocation probe
    let bounds: i32[] = [0 - 2147483647 - 1, 0 - 1, 0, 1, 15, 16, 31, 32, 255, 256, 257, 2147483647];
    for ao in bounds {
        for bo in bounds {
            for n in bounds {
                if (!check(all, ao, other, bo, n)) { return 1; }
            }
        }
    }
    let lengths: i32[] = [0, 1, 2, 3, 4, 7, 8, 9, 15, 16, 17, 31, 32, 33, 47, 48, 63, 64, 65, 79, 80, 95, 96, 97];
    for n in lengths {
        let a: u8[] = __alloc_u8(n);
        let b: u8[] = __alloc_u8(n);
        let longer: u8[] = __alloc_u8(n + 1);
        if (scan(a, 0, longer, 0, 2147483647) != n || scan(longer, 0, a, 0, 2147483647) != n) { return 9; }
        if (scan(a, 0, b, 0, 2147483647) != n) { return 2; }
        i = 0;
        while (i < n) {
            b = b.with(i, 255 as u8);
            if (scan(a, 0, b, 0, n) != i) { return 3; }
            if (!check(a, 1, b, 0, n) || !check(a, 0, b, 1, n)) { return 4; }
            if (!check(a, i, b, i, n) || !check(a, i + 1, b, i + 1, n)) { return 5; }
            b = b.with(i, 0 as u8);
            i = i + 1;
        }
    }
    i = 0;
    while (i < 256) {
        all = all.with(i, (255 - i) as u8);
        if (scan(all, 0, other, 0, 256) != i) { return 6; }
        all = all.with(i, i as u8);
        i = i + 1;
    }
    all = all.with(128, 17 as u8);
    if (scan(held, 0, other, 0, 256) != 256 || scan(all, 0, other, 0, 256) != 128) { return 7; }
    if (scan([], 0, all, 0, 256) != 0 || scan(all, 0, [], 0, 256) != 0) { return 8; }
    return 0;
}`
