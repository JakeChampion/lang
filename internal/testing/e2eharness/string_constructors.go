package e2eharness

const StringConstructorsProgram = `import "std/string";
import "std/i32";
function main(): i32 {
    let parts: string[] = "aé€𐀀".to_array();
    if (parts.len() != 4) { return 1; }
    if (parts[0] != "a" || parts[1] != "é" || parts[2] != "€" || parts[3] != "𐀀") { return 2; }
    if ("".to_array().len() != 0) { return 3; }
    if ("é".to_array().len() != 2) { return 4; }
    if ("aé€𐀀".replace_at(1, "中more") != "a中€𐀀") { return 5; }
    if ("aé€𐀀".replace_at(2, "x") != "aé€𐀀") { return 6; }
    if ("aé€𐀀".replace_at(3, "") != "aé𐀀") { return 7; }
    if ("aé€𐀀".replace_at(6, "é") != "aé€é") { return 8; }
    if ("aé".replace_at(3, "x") != "aé") { return 9; }
    if ("aé".replace_at(0 - 1, "x") != "aé") { return 10; }
    if ("abc".replace_at(1, "XYZ") != "aXc") { return 11; }
    if ("é".replace_at(0, "") != "") { return 12; }
    if ("".replace_at(0, "x") != "") { return 13; }
    if (string.repeat_char(65 as char, 3) != "AAA") { return 17; }
    if (string.repeat_char(233 as char, 2) != "éé") { return 18; }
    if (string.repeat_char(8364 as char, 2) != "€€") { return 19; }
    if (string.repeat_char(65536 as char, 2) != "𐀀𐀀") { return 20; }
    if (string.repeat_char(233 as char, 0) != "") { return 21; }
    if (string.repeat_char(233 as char, 0 - 1) != "") { return 22; }
    let truncated: string = string_from_bytes_unchecked([241 as u8, 128 as u8, 98 as u8]);
    let short: string = string_from_bytes_unchecked([226 as u8, 130 as u8]);
    let overlong: string = string_from_bytes_unchecked([192 as u8, 175 as u8]);
    let surrogate: string = string_from_bytes_unchecked([237 as u8, 160 as u8, 128 as u8]);
    let high: string = string_from_bytes_unchecked([244 as u8, 144 as u8, 128 as u8, 128 as u8]);
    if (truncated.split("").len() != 2 || short.split("").len() != 1) { return 23; }
    if (overlong.split("").len() != 2 || surrogate.split("").len() != 3) { return 24; }
    if (high.split("").len() != 4) { return 25; }
    return 0;
}
`
