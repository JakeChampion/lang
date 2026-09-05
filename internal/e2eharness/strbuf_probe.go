package e2eharness

// StrbufCeilingProbe builds a 72,089,600-byte string through the string
// builder: past the 64 MiB the register backends used to reserve as a fixed
// .bss buffer, whose overrun corrupted the words after it (#8212). The buffer
// doubles from 64 KiB now, so the build grows it repeatedly. It checks the
// length and the bytes at the start, at the old 64 MiB boundary and at the
// end, then that the drained builder still takes a small append. Non-zero
// exits are keyed in StrbufCeilingProbeCodes.
const StrbufCeilingProbe = `function main(): i32 {
    strbuf_reset();
    var i: i32 = 0;
    while (i < 4096) { strbuf_append("0123456789abcdef"); i = i + 1; }
    var chunk: string = strbuf_take();
    if (chunk.len() != 65536) { return 1; }
    var j: i32 = 0;
    while (j < 1100) { strbuf_append(chunk); j = j + 1; }
    var big: string = strbuf_take();
    if (big.len() != 72089600) { return 2; }
    if (big[0] != 48) { return 3; }
    if (big[72089599] != 102) { return 4; }
    if (big[67108864] != 48) { return 5; }
    strbuf_append("tail");
    if (strbuf_take() != "tail") { return 6; }
    return 0;
}
`

// StrbufCeilingProbeCodes explains StrbufCeilingProbe's exit codes.
const StrbufCeilingProbeCodes = "1 = chunk length, 2 = big length, 3/4/5 = byte at 0 / end / 64 MiB, 6 = the builder did not survive the take"
