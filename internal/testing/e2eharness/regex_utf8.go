package e2eharness

// RegexUTF8Program keeps byte matching and tests the checked text boundary.
const RegexUTF8Program = `import "std/regex";
import "std/array";
import "std/string";
import "std/utf8";

function same_bytes(a: u8[], b: u8[]): boolean {
    if (a.len() != b.len()) { return false; }
    let i: i32 = 0;
    while (i < a.len()) {
        if (a[i] != b[i]) { return false; }
        i = i + 1;
    }
    return true;
}

function text_is(value: Option[string], expected: string): boolean {
    match (value) {
        Some(text) => { return text == expected && utf8.is_valid_utf8(text); },
        None => { return false; },
    }
}

function text_none(value: Option[string]): boolean {
    match (value) { Some(text) => { return false; }, None => { return true; } }
}

function parts_none(value: Option[string[]]): boolean {
    match (value) { Some(parts) => { return false; }, None => { return true; } }
}

function check(): i32 {
    for text in ["é", "€", "𐀀"] {
        let bytes = text.bytes();
        if (!text_none(regex.regex_replace(".", text, "X"))) { return 1; }
        if (!text_none(regex.regex_replace_all("^.", text, ""))) { return 2; }
        if (!text_none(regex.regex_replace_all("", text, "-"))) { return 3; }
        if (!text_is(regex.regex_replace_all("", text, ""), text)) { return 4; }
        if (!text_is(regex.regex_replace_all(".", text, ""), "")) { return 5; }
        let dots = regex.regex_find_all(".", text);
        if (dots.len() != bytes.len()) { return 6; }
        let captures = regex.regex_captures_all("(.)", text);
        let h = buf_new(bytes.len());
        let i = 0;
        while (i < captures.len()) {
            let c = captures[i];
            if (!text_none(c.group(0)) || !text_none(c.group(1))) { buf_free(h); return 7; }
            if (c.group_start(1) != i || c.group_end(1) != i + 1) { buf_free(h); return 8; }
            let raw = c.group_bytes(1);
            if (raw.len() != 1 || raw[0] != bytes[i]) { buf_free(h); return 9; }
            buf_push_bytes_range(h, raw, 0, raw.len());
            i = i + 1;
        }
        let rebuilt = buf_take_bytes(h);
        buf_free(h);
        if (!same_bytes(rebuilt, bytes)) { return 10; }
        let named = regex.regex_captures("(?<a>.)(?<b>.*)", text);
        if (!text_none(named.group_named("a")) || !text_none(named.group_named("b"))) { return 11; }
        if (!same_bytes(named.group_named_bytes("a"), [bytes[0]])) { return 12; }
        if (!text_is(regex.regex_replace_groups("(?<a>.)(?<b>.*)", text, "${a}${b}"), text)) { return 13; }
        if (!text_is(regex.regex_replace_all_groups("(.)", text, "$1"), text)) { return 14; }
        if (!text_none(regex.regex_replace_all_groups("(.)", text, "$1-"))) { return 15; }
        if (!same_bytes(regex.regex_replace_groups_bytes("(.)", text, "$1"), bytes)) { return 16; }
        if (!same_bytes(regex.regex_replace_all_groups_bytes("(.)", text, "$1"), bytes)) { return 17; }
        let split = regex.regex_split_bytes(text, "^.");
        if (split.len() != 2 || split[0].len() != 0 || split[1].len() != bytes.len() - 1) { return 18; }
        if (!parts_none(regex.regex_split(text, "^."))) { return 19; }
        let empty_parts = regex.regex_split(text, ".");
        match (empty_parts) {
            Some(parts) => {
                if (parts.len() != bytes.len() + 1) { return 20; }
                for part in parts { if (part != "") { return 21; } }
            },
            None => { return 22; },
        }
        let empty = regex.regex_captures_all("()", text);
        for capture in empty {
            if (!text_is(capture.group(1), "")) { return 23; }
        }
        let raw = regex.regex_replace_bytes(".", text, "X");
        if (raw.len() != bytes.len() || raw[0] != 88 as u8 || raw[1] != bytes[1]) { return 24; }
        let all = regex.regex_replace_all_bytes(".", text, "X");
        if (all.len() != bytes.len()) { return 25; }
        for b in all { if (b != 88 as u8) { return 26; } }
    }
    for text in ["", "ascii", "é", "longer Unicode é € 𐀀 input"] {
        if (!text_is(regex.regex_replace("NEVER", text, "!"), text)) { return 27; }
        if (!text_is(regex.regex_replace_all("NEVER", text, "!"), text)) { return 28; }
        if (!text_is(regex.regex_replace_groups("NEVER", text, "$1"), text)) { return 29; }
        if (!text_is(regex.regex_replace_all_groups("NEVER", text, "$1"), text)) { return 30; }
        if (!same_bytes(regex.regex_replace_bytes("NEVER", text, "!"), text.bytes())) { return 31; }
        match (regex.regex_split(text, "NEVER")) {
            Some(parts) => { if (parts.len() != 1 || parts[0] != text) { return 32; } },
            None => { return 33; },
        }
        let c = regex.regex_captures("NEVER", text);
        if (!text_is(c.group(1), "") || !text_is(c.group_named("missing"), "")) { return 34; }
        if (c.group_bytes(1).len() != 0 || c.group_named_bytes("missing").len() != 0) { return 35; }
    }
    let absent = regex.regex_captures("a(b)?", "a");
    if (!text_is(absent.group(1), "") || absent.has_group(1)) { return 36; }
    if (!text_is(regex.regex_replace_groups("(é)", "aéb", "€$$$1𐀀"), "a€$é𐀀b")) { return 37; }
    if (!text_is(regex.regex_replace_groups("(é)", "é", "${missing}$x$"), "$x$")) { return 38; }
    if (!text_is(regex.regex_replace_groups("(é)", "é", "${unclosed"), "${unclosed")) { return 39; }
    if (!text_is(regex.regex_replace_groups("(?<é>é)", "é", "${é}"), "é")) { return 40; }
    if (!text_is(regex.regex_replace_all("é", "ééé", "€"), "é€€")) { return 41; }
    return 0;
}

function main(): i32 { return check(); }
`
