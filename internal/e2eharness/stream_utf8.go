package e2eharness

const StreamUTF8Program = `import "std/stream" as stream;
import "std/string";
import "std/array";
function main(): i32 {
    var cases: string[] = ["", "a", "é", "€", "𐀀", "é", "aé€𐀀-longer-than-inline"];
    for text in cases {
        var input = stream.stream_from_string(text);
        var (got, next) = input.read_all_string();
        match (got) { Some(value) => { if (value != text) { return 1; } }, None => { return 2; }, }
        if (!next.is_empty() || next.remaining() != 0) { return 3; }
        if (input.remaining() != text.len()) { return 4; }
        var (eof, again) = next.read_all_string();
        match (eof) { Some(value) => { if (value != "") { return 5; } }, None => { return 6; }, }
        if (!again.is_empty()) { return 7; }
    }
    var bad: u8[][] = [[255 as u8], [128 as u8], [192 as u8, 175 as u8],
        [237 as u8, 160 as u8, 128 as u8], [244 as u8, 144 as u8, 128 as u8, 128 as u8],
        [226 as u8, 130 as u8], [241 as u8, 128 as u8, 98 as u8]];
    for bytes in bad {
        var input = stream.stream_from_bytes(bytes);
        var (got, next) = input.read_all_string();
        match (got) { Some(_) => { return 8; }, None => { } }
        if (!next.is_empty() || next.remaining() != 0) { return 9; }
        if (input.remaining() != bytes.len()) { return 10; }
        var (original, unused) = input.read_all();
        if (!original.equal(bytes)) { return 11; }
        var (eof, after) = next.read_all_string();
        match (eof) { Some(value) => { if (value != "") { return 12; } }, None => { return 13; }, }
    }
    var prefixed = stream.stream_from_bytes([255 as u8, 104 as u8, 105 as u8]);
    var (skip, rest) = prefixed.read_n(1);
    var (text, done) = rest.read_all_string();
    match (text) { Some(value) => { if (value != "hi") { return 14; } }, None => { return 15; }, }
    if (rest.remaining() != 2 || !done.is_empty()) { return 16; }
    var full = stream.stream_from_string("é");
    var (lead, continuation) = full.read_n(1);
    var (invalid, consumed) = continuation.read_all_string();
    match (invalid) { Some(_) => { return 17; }, None => { } }
    if (!consumed.is_empty() || continuation.remaining() != 1) { return 18; }
    var (valid, tail) = full.read_all_string();
    match (valid) { Some(value) => { if (value != "é") { return 19; } }, None => { return 20; }, }
    return 0;
}
`
