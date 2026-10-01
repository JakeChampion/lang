package e2eharness

const StringByteTransformsProgram = `import "std/string";
import "std/array";
import "std/utf8" as utf8;
function main(): i32 {
    var reversed: u8[] = "é".reverse_bytes();
    if (!reversed.equal([169 as u8, 195 as u8])) { return 1; }
    match (utf8.from_bytes(reversed)) { Some(_) => { return 2; }, None => { } }
    var removed: u8[] = "éê".without_byte(195);
    if (!removed.equal([169 as u8, 170 as u8])) { return 3; }
    var replaced: u8[] = "éê".replace_byte(195, 255);
    if (!replaced.equal([255 as u8, 169 as u8, 255 as u8, 170 as u8])) { return 4; }
    var shifted: u8[] = "é".shift_byte(0 - 196);
    if (!shifted.equal([255 as u8, 229 as u8])) { return 5; }
    var b: i32 = 0;
    while (b < 256) {
        var one: u8[] = "A".shift_byte(b - 65);
        if (one.len() != 1 || one[0] as i32 != b) { return 6; }
        var two: u8[] = "AA".replace_byte(65, b);
        if (two.len() != 2 || two[0] as i32 != b || two[1] as i32 != b) { return 7; }
        b = b + 1;
    }
    var samples: string[] = ["", "a", "é", "€", "𐀀", "é", "éê€𐀀-longer-than-inline"];
    for s in samples {
        if (!s.reverse_bytes().reverse().equal(s.bytes())) { return 8; }
        if (!s.shift_byte(256).equal(s.bytes())) { return 9; }
        if (!s.replace_byte(999, 0).equal(s.bytes())) { return 10; }
        if (!s.without_byte(999).equal(s.bytes())) { return 11; }
        var dropped: u8[] = s.without_byte(195);
        for x in dropped { if (x == 195) { return 12; } }
    }
    var hay: string = "é".repeat(1024);
    var needle: string = "é".repeat(63) + "ê";
    if (hay.last_index_of(needle) != 0 - 1) { return 13; }
    if (hay.index_of(needle) != 0 - 1) { return 14; }
    var joined: string = needle + hay;
    if (joined.last_index_of(needle) != 0) { return 15; }
    if ((hay + needle).index_of(needle) != hay.len()) { return 16; }
    return 0;
}
`
