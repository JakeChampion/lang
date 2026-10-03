package e2eharness

import "strings"

// RmemchrBytesSource checks reverse scan bounds, raw values and borrowing.
func RmemchrBytesSource(allocations bool) string {
	probe := ""
	if allocations {
		probe = `let before: i64 = __heap_alloc_count();
    let repeat: i32 = 0;
    while (repeat < 1000) {
        if (find(all, 128, all.len()) != 128) { return 11; }
        repeat = repeat + 1;
    }
    if (__heap_alloc_count() != before) { return 12; }`
	}
	return strings.Replace(rmemchrBytesProgram, "// Allocation probe", probe, 1)
}

const rmemchrBytesProgram = `function slow(bytes: u8[], byte: i32, from: i32): i32 {
    let at = from;
    if (at >= bytes.len()) { at = bytes.len() - 1; }
    while (at >= 0) {
        if (bytes[at] as i32 == byte) { return at; }
        at = at - 1;
    }
    return 0 - 1;
}
fip function find(bytes: u8[], byte: i32, from: i32): i32 {
    return __rmemchr_bytes(bytes, byte, from);
}
function check(bytes: u8[], byte: i32, from: i32): boolean {
    return find(bytes, byte, from) == slow(bytes, byte, from);
}
function main(): i32 {
    let all: u8[] = __alloc_u8(256);
    let b = 0;
    while (b < 256) { all = all.with(b, b as u8); b = b + 1; }
    let held = all;
    // Allocation probe
    b = 0;
    while (b < 256) {
        if (find(all, b, 256) != b || find(all, b, b) != b) { return 1; }
        if (find(all, b, b - 1) != 0 - 1) { return 2; }
        b = b + 1;
    }
    let lengths: i32[] = [0, 1, 2, 7, 15, 16, 17, 31, 32, 33, 47, 48, 63, 64, 65, 79, 80, 95, 96, 97, 300];
    for n in lengths {
        let bytes: u8[] = __alloc_u8(n);
        let at = 0;
        while (at < n) { bytes = bytes.with(at, 255 as u8); at = at + 1; }
        // No load may include allocator padding or the array header.
        if (find(bytes, 0, n) != 0 - 1 || find(bytes, 255, n) != n - 1) { return 3; }
        for from in [0 - 2147483647 - 1, 0 - 1, 0, n - 1, n, 2147483647] {
            for byte in [0 - 2147483647 - 1, 0 - 1, 0, 255, 256, 2147483647] {
                if (!check(bytes, byte, from)) { return 4; }
            }
        }
        at = 0;
        while (at < n) {
            bytes = bytes.with(at, 128 as u8);
            for from in [at - 1, at, at + 1, n, 2147483647] {
                if (!check(bytes, 128, from) || !check(bytes, 255, from)) { return 5; }
            }
            bytes = bytes.with(at, 255 as u8);
            at = at + 1;
        }
    }
    all = all.with(128, 17 as u8);
    if (find(held, 128, 256) != 128 || find(all, 128, 256) != 0 - 1) { return 6; }
    if (find(held, 17, 256) != 17 || find(all, 17, 256) != 128) { return 7; }
    let literal: u8[] = [255 as u8, 0 as u8, 255 as u8];
    if (find(literal, 255, 3) != 2 || find(literal, 255, 1) != 0) { return 8; }
    return 0;
}`
