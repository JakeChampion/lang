package e2eharness

// ReaderBytesInput starts with valid multibyte text, then exercises every byte
// value repeatedly. The read sizes split the four-byte scalar after its lead.
func ReaderBytesInput() []byte {
	out := make([]byte, 1032)
	copy(out, []byte{0, 0xc3, 0xa9, 0xf0, 0x9f, 0x99, 0x82, 0xff})
	for i := 8; i < len(out); i++ {
		out[i] = byte(i - 8)
	}
	return out
}

const ReaderBytesProgram = `import "std/array";
function main(): i32 {
    let prefix: u8[] = [0 as u8, 195 as u8, 169 as u8, 240 as u8, 159 as u8, 153 as u8, 130 as u8, 255 as u8];
    let r = stdin();
    match (r.read_chunk_bytes(0 - 1)) { Ok(_) => { return 1; }, Err(_) => { } }
    // WASI Preview 1 may report EINTR for a zero-length stdin read. Preserve
    // that real host error; either outcome must leave the cursor unchanged.
    match (r.read_chunk_bytes(0)) {
        Ok(bs) => { if (bs.len() != 0) { return 2; } },
        Err(e) => { match (e) { Interrupted => { }, _ => { return 3; } } }
    }
    let first: u8[] = [];
    match (r.read_chunk_bytes(3)) { Ok(bs) => { first = bs; }, Err(_) => { return 4; } }
    if (first.len() != 3 || first[0] != 0 || first[1] != 195 || first[2] != 169) { return 5; }
    let total: i32 = 3;
    let turn: i32 = 0;
    let sizes: i32[] = [1, 2, 3, 7, 17, 511];
    while (true) {
        let n: i32 = sizes[turn % sizes.len()];
        match (r.read_chunk_bytes(n)) {
            Err(_) => { return 6; },
            Ok(bs) => {
                if (bs.len() > n) { return 7; }
                if (bs.len() == 0) { break; }
                let i: i32 = 0;
                while (i < bs.len()) {
                    let expected: u8 = ((total + i - 8) % 256) as u8;
                    if (total + i < 8) { expected = prefix[total + i]; }
                    if (bs[i] != expected) { return 8; }
                    i = i + 1;
                }
                total = total + bs.len();
            }
        }
        turn = turn + 1;
    }
    if (total != 1032 || first.len() != 3 || first[0] != 0 || first[1] != 195 || first[2] != 169) { return 9; }
    let alias = first;
    let changed = first.with(0, 255 as u8);
    if (alias[0] != 0 || changed[0] != 255) { return 10; }
    match (r.read_chunk_bytes(9)) { Ok(bs) => { if (bs.len() != 0) { return 11; } }, Err(_) => { return 12; } }
    match (r.close()) { Some(_) => { return 13; }, None => { } }
    match (r.read_chunk_bytes(1)) { Ok(_) => { return 14; }, Err(_) => { } }
    match (r.read_chunk_bytes(0)) { Ok(_) => { return 15; }, Err(_) => { } }
    return 0;
}
`
