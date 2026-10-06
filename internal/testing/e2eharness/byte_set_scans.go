package e2eharness

import "strings"

// ByteSetScansSource compares raw scans with element-wise oracles, including
// short membership tables, non-boolean entries and chunk boundary state.
func ByteSetScansSource(allocations bool) string {
	probe := ""
	if allocations {
		probe = `let before = __heap_alloc_count();
    let repeat = 0;
    while (repeat < 1000) {
        if (scan(all, 0, all) != 1 || runs(all, 0, all) != 1) { return 10; }
        repeat = repeat + 1;
    }
    if (__heap_alloc_count() != before) { return 11; }`
	}
	return strings.Replace(byteSetScansProgram, "// Allocation probe", probe, 1)
}

const byteSetScansProgram = `fip function scan(bytes: u8[], from: i32, set: u8[]): i32 {
    return __scan_set_bytes(bytes, from, set);
}
fip function runs(bytes: u8[], inside: i32, set: u8[]): i32 {
    return __count_runs_bytes(bytes, inside, set);
}
function member(b: u8, set: u8[]): boolean {
    return b as i32 < set.len() && set[b as i32] != 0 as u8;
}
function slow_scan(bytes: u8[], from: i32, set: u8[]): i32 {
    let i = from;
    if (i < 0) { i = 0; }
    while (i < bytes.len()) {
        if (member(bytes[i], set)) { return i; }
        i = i + 1;
    }
    return bytes.len();
}
function slow_runs(bytes: u8[], inside: i32, set: u8[]): i32 {
    let prev = inside != 0;
    let count = 0;
    for b in bytes {
        let cur = member(b, set);
        if (cur && !prev) { count = count + 1; }
        prev = cur;
    }
    return count;
}
function main(): i32 {
    let all: u8[] = __alloc_u8(256);
    let b = 0;
    while (b < 256) { all = all.with(b, b as u8); b = b + 1; }
    let held = all;
    // Allocation probe
    let set: u8[] = __alloc_u8(256);
    b = 0;
    while (b < 256) {
        set = set.with(b, 255 as u8);
        if (scan(all, 0, set) != b || scan(all, b, set) != b) { return 1; }
        if (scan(all, b + 1, set) != 256 || runs(all, 0, set) != 1) { return 2; }
        if (runs(all, 1, set) != slow_runs(all, 1, set)) { return 3; }
        set = set.with(b, 0 as u8);
        b = b + 1;
    }
    for n in [0, 1, 2, 3, 4, 7, 15, 16, 17, 31, 32, 33, 63, 64, 65, 255, 256, 257, 300] {
        let bytes: u8[] = __alloc_u8(n);
        let i = 0;
        while (i < n) { bytes = bytes.with(i, ((i / 3 * 73) % 256) as u8); i = i + 1; }
        for width in [0, 1, 31, 127, 255, 256, 300] {
            let table: u8[] = __alloc_u8(width);
            i = 0;
            while (i < width) {
                if (i % 3 != 0) { table = table.with(i, ((i % 255) + 1) as u8); }
                i = i + 1;
            }
            for from in [0 - 2147483647 - 1, 0 - 1, 0, 1, 3, 15, 16, 31, n - 1, n, 2147483647] {
                if (scan(bytes, from, table) != slow_scan(bytes, from, table)) { return 4; }
            }
            for inside in [0 - 2147483647 - 1, 0 - 1, 0, 1, 255, 2147483647] {
                if (runs(bytes, inside, table) != slow_runs(bytes, inside, table)) { return 5; }
            }
        }
    }
    // A member run crossing the read boundary counts only once.
    let only: u8[] = __alloc_u8(256);
    only = only.with(255, 2 as u8);
    let first: u8[] = [0 as u8, 255 as u8, 255 as u8];
    let last: u8[] = [255 as u8, 0 as u8, 255 as u8];
    if (runs(first, 0, only) + runs(last, 1, only) != 2) { return 6; }
    let alias = only;
    only = only.with(255, 0 as u8);
    all = all.with(255, 0 as u8);
    if (scan(held, 0, alias) != 255 || scan(all, 0, alias) != 256 || runs(last, 0, only) != 0) { return 7; }
    if (scan([255 as u8], 0, []) != 1 || runs([], 1, alias) != 0) { return 8; }
    return 0;
}`
