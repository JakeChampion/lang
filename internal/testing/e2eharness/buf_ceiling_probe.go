package e2eharness

// BufCeilingProbe builds a 72,089,600-byte string through a builder, past
// 64 MiB (#8212), growing the buffer repeatedly from its 64 KiB start. It
// checks the length and the bytes at the start, at the 64 MiB boundary and at
// the end, then that the drained builder still takes a small append. Non-zero
// exits are keyed in BufCeilingProbeCodes.
const BufCeilingProbe = `function main(): i32 {
    let h: usize = buf_new(65536);
    let i: i32 = 0;
    while (i < 4096) { buf_push(h, "0123456789abcdef"); i = i + 1; }
    let chunk: string = buf_take(h);
    if (chunk.len() != 65536) { return 1; }
    let j: i32 = 0;
    while (j < 1100) { buf_push(h, chunk); j = j + 1; }
    let big: string = buf_take(h);
    if (big.len() != 72089600) { return 2; }
    if (big[0] != 48) { return 3; }
    if (big[72089599] != 102) { return 4; }
    if (big[67108864] != 48) { return 5; }
    buf_push(h, "tail");
    if (buf_take(h) != "tail") { return 6; }
    buf_free(h);
    return 0;
}
`

// BufCeilingProbeCodes explains BufCeilingProbe's exit codes.
const BufCeilingProbeCodes = "1 = chunk length, 2 = big length, 3/4/5 = byte at 0 / end / 64 MiB, 6 = the builder did not survive the take"
