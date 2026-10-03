package e2eharness

import "strings"

// ScanSetBytesSource checks __scan_set_bytes against a scalar reference over
// full, short and empty sets, every start from below zero to past the end,
// and a set held in a `const`, with an allocation probe when `allocations`.
func ScanSetBytesSource(allocations bool) string {
	probe := ""
	if allocations {
		probe = `let before: i64 = __heap_alloc_count();
    let repeat: i32 = 0;
    while (repeat < 1000) {
        if (scan(all, 1, odd) != 1) { return 11; }
        repeat = repeat + 1;
    }
    if (__heap_alloc_count() != before) { return 12; }`
	}
	return strings.Replace(scanSetBytesProgram, "// Allocation probe", probe, 1)
}

const scanSetBytesProgram = `const DIGITS: u8[] = [0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
    0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
    0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
    1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8];
function slow(bytes: u8[], from: i32, set: u8[]): i32 {
    let i: i32 = from;
    if (i < 0) { i = 0; }
    while (i < bytes.len()) {
        let b: i32 = bytes[i] as i32;
        if (b < set.len() && set[b] != 0u8) { return i; }
        i = i + 1;
    }
    return bytes.len();
}
function check(bytes: u8[], from: i32, set: u8[]): boolean {
    return __scan_set_bytes(bytes, from, set) == slow(bytes, from, set);
}
fip function scan(bytes: u8[], from: i32, set: u8[]): i32 {
    return __scan_set_bytes(bytes, from, set);
}
function set_of(n: i32, every: i32): u8[] {
    let set: u8[] = __alloc_u8(n);
    let i: i32 = 0;
    while (i < n) {
        if (every > 0 && i % every == 0) { set = set.with(i, 1u8); }
        i = i + 1;
    }
    return set;
}
function main(): i32 {
    let all: u8[] = __alloc_u8(256);
    let b: i32 = 0;
    while (b < 256) { all = all.with(b, b as u8); b = b + 1; }
    let odd: u8[] = set_of(256, 2);
    odd = odd.with(1, 1u8);
    let sets: u8[][] = [set_of(256, 0), set_of(256, 7), set_of(256, 1), set_of(128, 3), set_of(10, 4), set_of(0, 0), odd];
    // Allocation probe
    for set in sets {
        b = 0;
        while (b <= 257) {
            if (!check(all, b, set) || !check(all, 0 - b, set)) { return 1; }
            b = b + 1;
        }
        if (!check(all, 0 - 2147483647 - 1, set) || !check(all, 2147483647, set)) { return 2; }
    }
    let lengths: i32[] = [0, 1, 3, 4, 5, 7, 8, 9, 15, 16, 17, 33];
    for n in lengths {
        let bytes: u8[] = __alloc_u8(n);
        let at: i32 = 0;
        while (at < n) {
            bytes = bytes.with(at, 200 as u8);
            at = at + 1;
        }
        at = 0;
        while (at < n) {
            bytes = bytes.with(at, 51 as u8);
            for set in sets {
                if (!check(bytes, 0, set) || !check(bytes, at, set) || !check(bytes, at + 1, set)) { return 3; }
            }
            if (!check(bytes, 0, DIGITS) || __scan_set_bytes(bytes, 0, DIGITS) != at) { return 4; }
            bytes = bytes.with(at, 200 as u8);
            at = at + 1;
        }
        if (__scan_set_bytes(bytes, 0, DIGITS) != n) { return 5; }
    }
    return 0;
}`
