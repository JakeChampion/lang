package e2eharness

import "strings"

func ByteReductionsSource(compiled bool) string {
	probe := ""
	if compiled {
		probe = `let before: i64 = __heap_alloc_count();
    let repeat: i32 = 0;
    while (repeat < 1000) {
        if (sum(all) != 32640 || bsd(all, 12345) != slow_bsd(all, 12345)) { return 10; }
        repeat = repeat + 1;
    }
    if (__heap_alloc_count() != before) { return 11; }
    // 16,843,010 bytes of 255 sum to 2^32 + 254. Build in bounded chunks.
    let h: usize = buf_new(16843010);
    let chunk: u8[] = __alloc_u8(65536);
    let fill: i32 = 0;
    while (fill < chunk.len()) {
        chunk = chunk.with(fill, 255 as u8);
        fill = fill + 1;
    }
    repeat = 0;
    while (repeat < 257) {
        buf_push_bytes_range(h, chunk, 0, chunk.len());
        repeat = repeat + 1;
    }
    buf_push_bytes_range(h, chunk, 0, 258);
    let large: u8[] = buf_take_bytes(h);
    buf_free(h);
    if (large.len() != 16843010 || sum(large) != 254) { return 12; }
    let wrapped: hash.SysvSum = hash.sysv_sum_new().update_array(large).update_array(large);
    if (wrapped.sum != 508 || wrapped.len() != (33686020 as u64)) { return 14; }
    let prefix_builder: usize = buf_new(8421505);
    buf_push_bytes_range(prefix_builder, large, 0, 8421505);
    let prefix: u8[] = buf_take_bytes(prefix_builder);
    buf_free(prefix_builder);
    if (sum(prefix) != 0 - 2147483521) { return 13; }`
	}
	return strings.Replace(byteReductionsProgram, "// Compiled probes", probe, 1)
}

const byteReductionsProgram = `import "std/hash";
fip function sum(bytes: u8[]): i32 {
    return __sum_bytes_array(bytes);
}
fip function bsd(bytes: u8[], seed: i32): i32 {
    return __bsd_sum_bytes(bytes, seed);
}
function slow_sum(bytes: u8[]): i32 {
    let total: u32 = 0;
    for byte in bytes { total = total + (byte as u32); }
    return total as i32;
}
function slow_bsd(bytes: u8[], seed: i32): i32 {
    let total: u32 = (seed as u32) & 0xffff;
    for byte in bytes {
        total = ((total >> 1) | (total << 15)) & 0xffff;
        total = (total + (byte as u32)) & 0xffff;
    }
    return total as i32;
}
function copy_range(bytes: u8[], lo: i32, hi: i32): u8[] {
    let result: u8[] = [];
    let at: i32 = lo;
    while (at < hi) { result = result.append(bytes[at]); at = at + 1; }
    return result;
}
function main(): i32 {
    let all: u8[] = __alloc_u8(256);
    let at: i32 = 0;
    while (at < 256) { all = all.with(at, at as u8); at = at + 1; }
    let held: u8[] = all;
    let lengths: i32[] = [0, 1, 2, 7, 15, 16, 17, 31, 32, 33, 63, 64, 65, 127, 128, 129, 255, 256];
    let seeds: i32[] = [0, 1, 65535, 65536, 65537, 0 - 1, 2147483647, 0 - 2147483647 - 1];
    for n in lengths {
        let bytes: u8[] = copy_range(all, 0, n);
        if (sum(bytes) != slow_sum(bytes)) { return 1; }
        let split: i32 = n / 2;
        let left: u8[] = copy_range(bytes, 0, split);
        let right: u8[] = copy_range(bytes, split, n);
        let bc: hash.BsdSum = hash.bsd_sum_new().update_array(left).update_array(right);
        let sc: hash.SysvSum = hash.sysv_sum_new().update_array(left).update_array(right);
        if (bc.finish() != (bsd(bytes, 0) as u32) || bc.len() != (n as u64)) { return 6; }
        if (sc.sum != (sum(bytes) as u32) || sc.len() != (n as u64)) { return 7; }
        for seed in seeds {
            if (bsd(bytes, seed) != slow_bsd(bytes, seed)) { return 2; }
            let first: u8[] = copy_range(bytes, 0, n / 2);
            let rest: u8[] = copy_range(bytes, n / 2, n);
            if (bsd(rest, bsd(first, seed)) != bsd(bytes, seed)) { return 3; }
        }
    }
    // Compiled probes
    all = all.with(255, 0 as u8);
    if (sum(held) != 32640 || sum(all) != 32385) { return 4; }
    if (bsd(held, 7) != slow_bsd(held, 7) || bsd(all, 7) != slow_bsd(all, 7)) { return 5; }
    return 0;
}`
