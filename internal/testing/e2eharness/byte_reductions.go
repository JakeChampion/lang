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
    let wrapped: hash.SysvSum = hash.sysv_sum_new().update(large).update(large);
    if (wrapped.sum != 508 || wrapped.byte_count() != (33686020 as u64)) { return 14; }
