package e2eharness

// Shared UTF-8 lead/continuation bytes must not make distinct scalars members
// of the same set or let filtering and trimming split a character (#5626 D9).
const StringCharacterSetProgram = `
import "std/string";
import "std/utf8" as utf8;

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
    return 0;
}
`
