package e2eharness

const StringConcatCopySearchProgram = `import "std/string";
function prefix(i: i32): string {
    if (i % 2 == 0) { return "abc"; }
    return "ABC";
}
function round(i: i32): i32 {
    var suffix: string = "";
    var n: i32 = 0;
    while (n < 18) {
        var s: string = prefix(i) + suffix;
        var bytes: u8[] = s.bytes();
        if (bytes.len() != s.len()) { return 1; }
        var j: i32 = 0;
        while (j < bytes.len()) {
            if (bytes[j] != s[j]) { return 2; }
            j = j + 1;
        }
        if (s.index_of(prefix(i)) != 0) { return 3; }
        if (s.last_index_of(prefix(i)) != 0) { return 5; }
        var haystack: string = "01234567890123456789012345" + s;
        if (haystack.index_of(s) != 26) { return 4; }
        suffix = suffix + "!";
        n = n + 1;
    }
    return 0;
}
function main(): i32 {
    var i: i32 = 0;
    while (i < 100) {
        var code: i32 = round(i);
        if (code != 0) { return code; }
        i = i + 1;
    }
    var text: string = "é".repeat(2048);
    var needle: string = "ê" + "é".repeat(127);
    if (text.index_of(needle) != 0 - 1) { return 6; }
    if (text.last_index_of(needle) != 0 - 1) { return 7; }
    var present: string = "€" + needle + text;
    if (present.last_index_of(needle) != 3) { return 8; }
    var periodic: string = "é".repeat(128);
    if (text.last_index_of(periodic) != text.len() - periodic.len()) { return 9; }
    return 0;
}`
