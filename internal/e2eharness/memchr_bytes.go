package e2eharness

import "strings"

// MemchrBytesSource enables allocation accounting only on compiled targets.
// The primary interpreter does not implement __heap_alloc_count.
func MemchrBytesSource(allocations bool) string {
	probe := ""
	if allocations {
		probe = `let before: i64 = __heap_alloc_count();
    let repeat: i32 = 0;
    while (repeat < 1000) {
        if (scan(all, 128, 0) != 128) { return 11; }
        repeat = repeat + 1;
    }
    if (__heap_alloc_count() != before) { return 12; }`
	}
	return strings.Replace(memchrBytesProgram, "// Allocation probe", probe, 1)
}

const memchrBytesProgram = `function slow(bytes: u8[], byte: i32, from: i32): i32 {
    if (byte < 0 || byte > 255) { return 0 - 1; }
    let i: i32 = from;
    if (i < 0) { i = 0; }
    while (i < bytes.len()) {
        if (bytes[i] as i32 == byte) { return i; }
        i = i + 1;
    }
    return 0 - 1;
}
function check(bytes: u8[], byte: i32, from: i32): boolean {
    return __memchr_bytes(bytes, byte, from) == slow(bytes, byte, from);
}
fip function scan(bytes: u8[], byte: i32, from: i32): i32 {
    return __memchr_bytes(bytes, byte, from);
}
function main(): i32 {
    // Both entry points share the bootstrap wasm kernel. An extreme start
    // must miss before the vector-bound calculation can wrap around.
    if (__memchr("0123456789abcdefghijklmnopqrstuv", 48, 2147483647) != 0 - 1) { return 13; }
    let all: u8[] = __alloc_u8(256);
    let b: i32 = 0;
    while (b < 256) { all = all.with(b, b as u8); b = b + 1; }
    let held: u8[] = all;
    // Allocation probe
    b = 0;
    while (b < 256) {
        if (!check(all, b, 0 - 2147483647 - 1)) { return 1; }
        if (!check(all, b, b) || !check(all, b, b + 1)) { return 2; }
        if (!check(all, b, 2147483647)) { return 3; }
        b = b + 1;
    }
    if (!check(all, 0 - 1, 0) || !check(all, 256, 0)) { return 4; }
    if (!check(all, 0 - 2147483647 - 1, 0) || !check(all, 2147483647, 0)) { return 5; }
    let lengths: i32[] = [0, 1, 2, 7, 15, 16, 17, 31, 32, 33, 47, 48, 63, 64, 65, 79, 80, 95, 96, 97];
    for n in lengths {
        let bytes: u8[] = __alloc_u8(n);
        let at: i32 = 0;
        while (at < n) { bytes = bytes.with(at, 255 as u8); at = at + 1; }
        // Zero-filled allocator padding must never appear as an in-bounds hit.
        if (!check(bytes, 0, 0) || !check(bytes, 255, n)) { return 6; }
        at = 0;
        while (at < n) {
            bytes = bytes.with(at, 128 as u8);
            if (!check(bytes, 128, 0 - 1) || !check(bytes, 128, at)) { return 7; }
            if (!check(bytes, 128, at + 1) || !check(bytes, 255, at)) { return 8; }
            bytes = bytes.with(at, 255 as u8);
            at = at + 1;
        }
    }
    all = all.with(128, 17 as u8);
    if (__memchr_bytes(held, 128, 0) != 128 || __memchr_bytes(all, 128, 0) != 0 - 1) { return 9; }
    if (__memchr_bytes(all, 17, 18) != 128 || __memchr_bytes(held, 17, 18) != 0 - 1) { return 10; }
    return 0;
}`
