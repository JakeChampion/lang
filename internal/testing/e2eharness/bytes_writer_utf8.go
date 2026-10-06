package e2eharness

const BytesWriterUTF8Program = `import "std/io_buffered" as io;
import "std/string";
import "std/array";
function main(): i32 {
    let empty = io.bytes_writer_new();
    match (empty.into_string()) { Some(s) => { if (s != "") { return 1; } }, None => { return 2; }, }
    let b: i32 = 0;
    while (b < 256) {
        let w = empty.write_byte(b);
        match (w.into_string()) {
            Some(s) => { if (b >= 128 || s.len() != 1 || s[0] as i32 != b) { return 3; } },
            None => { if (b < 128) { return 4; } },
        }
        if (w.len() != 1 || w.into_bytes()[0] as i32 != b) { return 5; }
        b = b + 1;
    }
    let source: string = "aé€𐀀é-longer-than-inline";
    let split = empty;
    for x in source.bytes() { split = split.write_byte(x as i32); }
    match (split.into_string()) { Some(s) => { if (s != source) { return 6; } }, None => { return 7; }, }
    let partial = empty.write_bytes([240 as u8, 144 as u8, 128 as u8]);
    match (partial.into_string()) { Some(_) => { return 8; }, None => { } }
    let completed = partial.write_byte(128);
    match (completed.into_string()) { Some(s) => { if (s != "𐀀") { return 9; } }, None => { return 10; }, }
    if (partial.len() != 3 || completed.len() != 4) { return 11; }
    match (partial.into_string()) { Some(_) => { return 12; }, None => { } }
    let bad: u8[][] = [[192 as u8, 175 as u8], [237 as u8, 160 as u8, 128 as u8],
        [244 as u8, 144 as u8, 128 as u8, 128 as u8], [226 as u8, 130 as u8],
        [241 as u8, 128 as u8, 98 as u8]];
    for bytes in bad {
        let w = empty.write_bytes(bytes);
        match (w.into_string()) { Some(_) => { return 13; }, None => { } }
        if (!w.into_bytes().equal(bytes)) { return 14; }
        match (w.reset().into_string()) { Some(s) => { if (s != "") { return 15; } }, None => { return 16; }, }
    }
    let max = empty.write_bytes([244 as u8, 143 as u8, 191 as u8, 191 as u8]);
    match (max.into_string()) {
        Some(s) => { if (s != string.string_from_codepoint(1114111 as char)) { return 17; } },
        None => { return 18; },
    }
    if (!empty.is_empty()) { return 19; }
    return 0;
}
`
