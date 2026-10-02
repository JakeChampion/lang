package e2eharness

// BufByteRangeProgram checks copy lengths around native word boundaries,
// clamped bounds, multiple growths, independent builders and retained arrays.
const BufByteRangeProgram = `function matches(out: u8[], at: i32, lo: i32, n: i32): boolean {
    var i: i32 = 0;
    while (i < n) {
        if (out[at + i] != (lo + i) as u8) { return false; }
        i = i + 1;
    }
    return true;
}
function main(): i32 {
    var bytes: u8[] = __alloc_u8(256);
    var i: i32 = 0;
    while (i < bytes.len()) { bytes = bytes.with(i, i as u8); i = i + 1; }
    var saved: u8[] = bytes;
    var h: usize = buf_new(1);
    var other: usize = buf_new(0);
    var empty: u8[] = [];
    buf_push_bytes_range(h, empty, 0 - 2147483647 - 1, 2147483647);
    buf_push_bytes_range(h, bytes, 2147483647, 0 - 2147483647 - 1);
    buf_push_bytes_range(h, bytes, -9, -1);
    buf_push_bytes_range(h, bytes, 9, 4);
    buf_push_bytes_range(h, bytes, 300, 400);
    if (buf_len(h) != 0) { return 1; }
    buf_push_byte(h, 99);
    buf_push_bytes_range(h, bytes, 0 - 2147483647 - 1, 2147483647);
    var k: i32 = 0;
    while (k <= 40) {
        buf_push_bytes_range(h, bytes, k % 7, k % 7 + k);
        k = k + 1;
    }
    buf_push_bytes_range(other, bytes, 254, 300);
    var out: u8[] = buf_take_bytes(h);
    var tail: u8[] = buf_take_bytes(other);
    buf_free(other);
    if (out.len() != 1077 || out[0] != 99 || !matches(out, 1, 0, 256)) { return 2; }
    if (tail.len() != 2 || tail[0] != 254 || tail[1] != 255) { return 3; }
    var at: i32 = 257;
    k = 0;
    while (k <= 40) {
        if (!matches(out, at, k % 7, k)) { return 4; }
        at = at + k;
        k = k + 1;
    }
    if (buf_len(h) != 0) { return 5; }
    k = 0;
    while (k < 513) { buf_push_bytes_range(h, bytes, 0, 256); k = k + 1; }
    bytes = bytes.with(0, 88 as u8);
    var big: u8[] = buf_take_bytes(h);
    buf_push_bytes_range(h, bytes, 0, 1);
    var changed: u8[] = buf_take_bytes(h);
    buf_free(h);
    if (changed.len() != 1 || changed[0] != 88 || saved[0] != 0 || out[1] != 0) { return 6; }
    if (big.len() != 131328) { return 7; }
    i = 0;
    while (i < big.len()) { if (big[i] != (i % 256) as u8) { return 8; } i = i + 1; }
    return 0;
}`
