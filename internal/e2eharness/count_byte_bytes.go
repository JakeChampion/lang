package e2eharness

import "strings"

// CountByteBytesSource exercises raw values, dense counts and vector tails.
// The interpreter has no allocation counter, so only compiled targets use it.
func CountByteBytesSource(allocations bool) string {
	probe := ""
	if allocations {
		probe = `var before: i64 = __heap_alloc_count();
    var repeat: i32 = 0;
    while (repeat < 1000) {
        if (count(all, 128) != 1) { return 11; }
        repeat = repeat + 1;
    }
    if (__heap_alloc_count() != before) { return 12; }`
	}
	return strings.Replace(countByteBytesProgram, "// Allocation probe", probe, 1)
}

const countByteBytesProgram = `function slow(bytes: u8[], byte: i32): i32 {
    var n: i32 = 0;
    for b in bytes { if (b as i32 == byte) { n = n + 1; } }
    return n;
}
function check(bytes: u8[], byte: i32): boolean {
    return __count_byte_bytes(bytes, byte) == slow(bytes, byte);
}
fip function count(bytes: u8[], byte: i32): i32 {
    return __count_byte_bytes(bytes, byte);
}
function main(): i32 {
    var all: u8[] = __alloc_u8(256);
    var b: i32 = 0;
    while (b < 256) { all = all.with(b, b as u8); b = b + 1; }
    var held: u8[] = all;
    // Allocation probe
    b = 0;
    while (b < 256) {
        if (!check(all, b) || count(all, b) != 1) { return 1; }
        b = b + 1;
    }
    if (!check(all, 0 - 1) || !check(all, 256)) { return 2; }
    if (!check(all, 0 - 2147483647 - 1) || !check(all, 2147483647)) { return 3; }
    var lengths: i32[] = [0, 1, 2, 7, 15, 16, 17, 31, 32, 33, 47, 48, 63, 64, 65, 79, 80, 95, 96, 97, 300];
    for n in lengths {
        var bytes: u8[] = __alloc_u8(n);
        var at: i32 = 0;
        while (at < n) { bytes = bytes.with(at, 255 as u8); at = at + 1; }
        // Zero-filled allocator padding must not contribute to the count.
        if (count(bytes, 0) != 0 || count(bytes, 255) != n) { return 4; }
        at = 0;
        while (at < n) {
            bytes = bytes.with(at, 128 as u8);
            if (count(bytes, 128) != 1 || count(bytes, 255) != n - 1) { return 5; }
            bytes = bytes.with(at, 255 as u8);
            at = at + 1;
        }
        at = 0;
        while (at < n) {
            bytes = bytes.with(at, ((at % 3) + 128) as u8);
            at = at + 1;
        }
        if (!check(bytes, 128) || !check(bytes, 129) || !check(bytes, 130)) { return 6; }
    }
    all = all.with(128, 17 as u8);
    if (count(held, 128) != 1 || count(all, 128) != 0) { return 7; }
    if (count(held, 17) != 1 || count(all, 17) != 2) { return 8; }
    var literal: u8[] = [255 as u8, 0 as u8, 255 as u8];
    if (count(literal, 255) != 2 || count(literal, 0) != 1) { return 9; }
    return 0;
}`
