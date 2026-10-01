package e2eharness

const BuilderBytesProgram = `import "std/array";
function same_bytes(a: u8[], b: u8[]): boolean {
    if (a.len() != b.len()) { return false; }
    var i: i32 = 0;
    while (i < a.len()) {
        if (a[i] != b[i]) { return false; }
        i = i + 1;
    }
    return true;
}
function main(): i32 {
    var h: usize = buf_new(1);
    var empty: u8[] = buf_take_bytes(h);
    if (empty.len() != 0 || buf_len(h) != 0) { return 1; }
    var i: i32 = 0;
    while (i < 256) { buf_push_byte(h, i); i = i + 1; }
    var first: u8[] = buf_take_bytes(h);
    if (first.len() != 256 || buf_len(h) != 0) { return 2; }
    i = 0;
    while (i < 256) { if (first[i] != i as u8) { return 3; } i = i + 1; }
    buf_push(h, "é");
    buf_push_byte(h, 255);
    buf_push_u64(h, 578437695752307201 as u64);
    var second: u8[] = buf_take_bytes(h);
    if (!same_bytes(second, [195 as u8, 169 as u8, 255 as u8, 1 as u8, 2 as u8, 3 as u8, 4 as u8, 5 as u8, 6 as u8, 7 as u8, 8 as u8])) { return 4; }
    if (buf_take_bytes(h).len() != 0 || buf_len(h) != 0) { return 5; }
    buf_push(h, "text");
    if (buf_take(h) != "text") { return 6; }
    buf_push_byte(h, 42);
    var third = buf_take_bytes(h);
    buf_free(h);
    // Reuse and free of the builder cannot change extracted snapshots.
    i = 0;
    while (i < 256) { if (first[i] != i as u8) { return 7; } i = i + 1; }
    if (!same_bytes(third, [42 as u8]) || second[2] != 255) { return 8; }
    var alias = first;
    var changed = first.with(0, 99 as u8);
    if (alias[0] != 0 || changed[0] != 99) { return 9; }
    // Repeated short takes after a large reserve exercise allocation classes
    // and result ownership, not just the initial growth path.
    var large = buf_new(65536);
    i = 0;
    while (i < 200) {
        buf_push_u64(large, i as u64);
        var bytes = buf_take_bytes(large);
        if (bytes.len() != 8 || bytes[0] != i as u8 || bytes[7] != 0) { return 10; }
        i = i + 1;
    }
    buf_free(large);
    return 0;
}
`
