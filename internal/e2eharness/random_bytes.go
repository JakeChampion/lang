package e2eharness

// SeededRandomBytesProgram checks the public byte result against the PCG
// draw API, including every tail length and the state after a discarded tail.
const SeededRandomBytesProgram = `import "std/rand" as rand;
import "std/array";
import "std/utf8" as utf8;
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
    var initial: i64 = rand.rng_seed(4242 as i64);
    for n in [0 - 9, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 15, 16, 17, 63, 64, 65, 257] {
        var actual = rand.rng_bytes(initial, n);
        var owned: u8[] = actual.1;
        var length: i32 = n;
        if (length < 0) { length = 0; }
        if (owned.len() != length) { return 1; }
        var state: i64 = initial;
        var pos: i32 = 0;
        while (pos < length) {
            var low = rand.rng_next(state);
            var high = rand.rng_next(low.0);
            state = high.0;
            for half in [low.1, high.1] {
                var shift: u32 = 0 as u32;
                while (shift < (32 as u32) && pos < length) {
                    if (owned[pos] != ((half >> shift) & (255 as u32)) as u8) { return 2; }
                    shift = shift + (8 as u32);
                    pos = pos + 1;
                }
            }
        }
        if (actual.0 != state) { return 3; }
    }
    // The result can contain malformed UTF-8 without pretending to be text.
    var first = rand.rng_bytes(initial, 64);
    match (utf8.from_bytes(first.1)) { Some(_) => { return 4; }, None => { } }
    var again = rand.rng_bytes(initial, 64);
    if (!same_bytes(first.1, again.1) || first.0 != again.0) { return 5; }
    var saved: u8[] = first.1;
    var changed = first.1.with(0, (first.1[0] ^ (255 as u8)) as u8);
    if (!same_bytes(saved, again.1) || changed[0] == saved[0]) { return 6; }
    var second = rand.rng_bytes(first.0, 64);
    if (same_bytes(second.1, saved) || !same_bytes(saved, again.1)) { return 7; }
    // A partial word advances by a full pair of draws, not by its length.
    var short = rand.rng_bytes(initial, 3);
    var word = rand.rng_bytes(initial, 8);
    var after_short = rand.rng_bytes(short.0, 9);
    var after_word = rand.rng_bytes(word.0, 9);
    if (!same_bytes(after_short.1, after_word.1) || after_short.0 != after_word.0) { return 8; }
    return 0;
}
`
