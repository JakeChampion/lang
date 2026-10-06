package e2eharness

const StreamUTF8Program = `import "std/stream" as stream;
import "std/string";
import "std/array";
function main(): i32 {
    let cases: string[] = ["", "a", "é", "€", "𐀀", "é", "aé€𐀀-longer-than-inline"];
    for text in cases {
        let input = stream.stream_from_string(text);
        let (got, next) = input.read_all_string();
        match (got) { Some(value) => { if (value != text) { return 1; } }, None => { return 2; }, }
        if (!next.is_empty() || next.remaining() != 0) { return 3; }
        if (input.remaining() != text.len()) { return 4; }
        let (eof, again) = next.read_all_string();
        match (eof) { Some(value) => { if (value != "") { return 5; } }, None => { return 6; }, }
        if (!again.is_empty()) { return 7; }
    }
    let bad: u8[][] = [[255 as u8], [128 as u8], [192 as u8, 175 as u8],
        [237 as u8, 160 as u8, 128 as u8], [244 as u8, 144 as u8, 128 as u8, 128 as u8],
        [226 as u8, 130 as u8], [241 as u8, 128 as u8, 98 as u8]];
    for bytes in bad {
        let input = stream.stream_from_bytes(bytes);
        let (got, next) = input.read_all_string();
        match (got) { Some(_) => { return 8; }, None => { } }
        if (!next.is_empty() || next.remaining() != 0) { return 9; }
        if (input.remaining() != bytes.len()) { return 10; }
        let (original, unused) = input.read_all();
        if (!original.equal(bytes)) { return 11; }
        let (eof, after) = next.read_all_string();
        match (eof) { Some(value) => { if (value != "") { return 12; } }, None => { return 13; }, }
    }
    let prefixed = stream.stream_from_bytes([255 as u8, 104 as u8, 105 as u8]);
    let (skip, rest) = prefixed.read_n(1);
    let (text, done) = rest.read_all_string();
    match (text) { Some(value) => { if (value != "hi") { return 14; } }, None => { return 15; }, }
    if (rest.remaining() != 2 || !done.is_empty()) { return 16; }
    let full = stream.stream_from_string("é");
    let (lead, continuation) = full.read_n(1);
    let (invalid, consumed) = continuation.read_all_string();
    match (invalid) { Some(_) => { return 17; }, None => { } }
    if (!consumed.is_empty() || continuation.remaining() != 1) { return 18; }
    let (valid, tail) = full.read_all_string();
    match (valid) { Some(value) => { if (value != "é") { return 19; } }, None => { return 20; }, }
    return 0;
}
`
