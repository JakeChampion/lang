package e2eharness

// Shared UTF-8 lead/continuation bytes must not make distinct scalars members
// of the same set or let filtering and trimming split a character (#5626 D9).
const StringCharacterSetProgram = `
import "std/string" as str;
import "std/utf8" as utf8;

function in_set(cp: char, chars: char[]): boolean {
    for c in chars { if (cp == c) { return true; } }
    return false;
}

// A forward-only oracle: derive byte offsets from maximal-subpart walking,
// then select units by their decoded chars. Reverse trimming must agree.
function check_malformed(s: string, set: string): boolean {
    var units = s.chars();
    var chars = set.chars();
    var positions: i32[] = [0];
    var pos: i32 = 0;
    while (pos < s.len()) {
        pos = pos + str.__utf8_step(s, pos, s.len());
        positions = positions.append(pos);
    }
    var low: i32 = 0;
    var high: i32 = units.len();
    while (low < high && in_set(units[low], chars)) { low = low + 1; }
    while (high > 0 && in_set(units[high - 1], chars)) { high = high - 1; }
    var both_high: i32 = high;
    if (both_high < low) { both_high = low; }
    if (s.trim_chars(set) != slice_unchecked(s, positions[low], positions[both_high])) { return false; }
    if (s.trim_start_chars(set) != slice_unchecked(s, positions[low], s.len())) { return false; }
    if (s.trim_end_chars(set) != slice_unchecked(s, 0, positions[high])) { return false; }
    var filtered: string = "";
    var count: i32 = 0;
    var i: i32 = 0;
    while (i < units.len()) {
        if (in_set(units[i], chars)) { count = count + 1; }
        else { filtered = filtered + slice_unchecked(s, positions[i], positions[i + 1]); }
        i = i + 1;
    }
    return s.without_chars(set) == filtered && s.count_chars_in(set) == count && s.contains_only(set) == (count == units.len());
}

function main(): i32 {
    if ("éê".without_chars("é") != "ê") { return 1; }
    if ("éê".trim_start_chars("é") != "ê") { return 2; }
    if ("éΩ".trim_end_chars("Ω") != "é") { return 3; }
    if ("éêΩ".trim_chars("éΩ") != "ê") { return 4; }
    if ("éê".contains_only("é")) { return 5; }
    if (!"éê".contains_only("êéé")) { return 6; }
    if ("éêé".count_chars_in("éé") != 2) { return 7; }
    if ("abc".without_chars("ac") != "b") { return 8; }
    if ("abba".trim_chars("a") != "bb") { return 9; }
    if (!"".contains_only("")) { return 10; }
    if ("é".contains_only("")) { return 11; }
    if ("é".without_chars("") != "é") { return 12; }
    if ("é".trim_chars("") != "é") { return 13; }
    if ("é".count_chars_in("") != 0) { return 14; }
    var samples: string[] = ["", "ASCII", "éêΩ", "€中", "𐀀𐀁", "é", "éêΩéêΩéêΩéêΩ"];
    var sets: string[] = ["", "AI", "é", "Ω", "€", "𐀀", "́", "ééΩ𐀀"];
    for s in samples {
        for set in sets {
            var filtered: string = s.without_chars(set);
            if (!utf8.is_valid_utf8(filtered)) { return 15; }
            if (!utf8.is_valid_utf8(s.trim_chars(set))) { return 16; }
            if (!utf8.is_valid_utf8(s.trim_start_chars(set))) { return 17; }
            if (!utf8.is_valid_utf8(s.trim_end_chars(set))) { return 18; }
            if (filtered.codepoint_count() + s.count_chars_in(set) != s.codepoint_count()) { return 19; }
            if (s.contains_only(set) != (filtered.len() == 0)) { return 20; }
        }
    }
    if ("𐀀𐀁".without_chars("𐀀") != "𐀁") { return 21; }
    if ("éé".trim_chars("é") != "") { return 22; }
    if ("é".without_chars("́") != "e") { return 23; }
    if ("é".without_chars("é") != "é") { return 24; }
    var stray = string_from_bytes_unchecked([130 as u8]);
    var broken = "x" + stray;
    if (broken.trim_chars("x") != stray) { return 25; }
    if (broken.trim_end_chars("x") != broken) { return 26; }
    if (broken.trim_end_chars("�") != "x") { return 27; }
    if (broken.trim_chars("x�") != "") { return 28; }
    var malformed: string[] = [
        broken, stray, stray.repeat(128), "é" + stray, "𐀀" + stray,
        string_from_bytes_unchecked([241 as u8, 128 as u8, 98 as u8]),
        string_from_bytes_unchecked([241 as u8, 128 as u8, 128 as u8]),
        string_from_bytes_unchecked([224 as u8, 128 as u8, 128 as u8]),
        string_from_bytes_unchecked([237 as u8, 160 as u8, 128 as u8]),
        string_from_bytes_unchecked([244 as u8, 144 as u8, 128 as u8, 128 as u8]),
        string_from_bytes_unchecked([192 as u8, 175 as u8, 255 as u8]),
        string_from_bytes_unchecked([195 as u8]),
        "�", "x" + stray + "�x", "é" + stray + "é"
    ];
    var malformed_sets: string[] = ["", "x", "�", "x�", "é", "é�", "𐀀", stray, broken];
    for s in malformed {
        for set in malformed_sets {
            if (!check_malformed(s, set)) { return 29; }
        }
    }
    return 0;
}
`
