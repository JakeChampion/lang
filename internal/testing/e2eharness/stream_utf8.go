package e2eharness

const StreamUTF8Program = `import "std/stream" as stream;
import "std/string";
import "std/array";
function main(): i32 {
    let cases: string[] = ["", "a", "é", "€", "𐀀", "é", "aé€𐀀-longer-than-inline"];
    for text in cases {
        let input = stream.from_string(text);
        let (got, next) = input.read_all_string();
        match (got) { Ok(value) => { if (value != text) { return 1; } }, Err(_) => { return 2; }, }
        if (!next.is_empty() || next.remaining() != 0) { return 3; }
        if (input.remaining() != text.len()) { return 4; }
        let (eof, again) = next.read_all_string();
        match (eof) { Ok(value) => { if (value != "") { return 5; } }, Err(_) => { return 6; }, }
        if (!again.is_empty()) { return 7; }
    }
    let bad: u8[][] = [[255 as u8], [128 as u8], [192 as u8, 175 as u8],
        [237 as u8, 160 as u8, 128 as u8], [244 as u8, 144 as u8, 128 as u8, 128 as u8],
        [226 as u8, 130 as u8], [241 as u8, 128 as u8, 98 as u8]];
    for bytes in bad {
        let input = stream.from_bytes(bytes);
        let (got, next) = input.read_all_string();
        match (got) { Ok(_) => { return 8; }, Err(_) => { } }
        if (!next.is_empty() || next.remaining() != 0) { return 9; }
        if (input.remaining() != bytes.len()) { return 10; }
        let (original, unused) = input.read_all();
        if (!original.equal(bytes)) { return 11; }
        let (eof, after) = next.read_all_string();
        match (eof) { Ok(value) => { if (value != "") { return 12; } }, Err(_) => { return 13; }, }
    }
    let prefixed = stream.from_bytes([255 as u8, 104 as u8, 105 as u8]);
    let (skip, rest) = prefixed.read_n(1);
    let (text, done) = rest.read_all_string();
    match (text) { Ok(value) => { if (value != "hi") { return 14; } }, Err(_) => { return 15; }, }
    if (rest.remaining() != 2 || !done.is_empty()) { return 16; }
    let full = stream.from_string("é");
    let (lead, continuation) = full.read_n(1);
    let (invalid, consumed) = continuation.read_all_string();
    match (invalid) { Ok(_) => { return 17; }, Err(_) => { } }
    if (!consumed.is_empty() || continuation.remaining() != 1) { return 18; }
    let (valid, tail) = full.read_all_string();
    match (valid) { Ok(value) => { if (value != "é") { return 19; } }, Err(_) => { return 20; }, }
    return 0;
}
`
