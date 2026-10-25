package e2eharness

const RegexUTF8Program = `import "std/regex";
import "std/option";
import "std/result";
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

function text_is(value: Result[string, regex.TextError], expected: string): boolean {
    match (value) {
        Ok(text) => { return text == expected && utf8.is_valid(text); },
        Err(_) => { return false; },
    }
}

function at(value: Option[i32], want: i32): boolean {
    match (value) { Some(v) => { return v == want; }, None => { return false; } }
}

function text_error(value: Result[string, regex.TextError]): boolean {
    match (value) { Err(InvalidUtf8Output()) => { return true; }, _ => { return false; } }
}
function capture_error(value: Result[Option[string], regex.TextError]): boolean {
    match (value) { Err(InvalidUtf8Output()) => { return true; }, _ => { return false; } }
}
function capture_text(value: Result[Option[string], regex.TextError], expected: string): boolean {
    match (value) { Ok(Some(text)) => { return text == expected; }, _ => { return false; } }
}
function capture_absent(value: Result[Option[string], regex.TextError]): boolean {
    match (value) { Ok(None()) => { return true; }, _ => { return false; } }
}
function parts_error(value: Result[string[], regex.TextError]): boolean {
    match (value) { Err(InvalidUtf8Output()) => { return true; }, _ => { return false; } }
}

function check(): i32 {
    for text in ["é", "€", "𐀀"] {
        let bytes = text.bytes();
        if (!text_error({ let __checked_regex_32: regex.Regex = match (regex.compile(".")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_32.replace(text, "X") })) { return 1; }
        if (!text_error({ let __checked_regex_31: regex.Regex = match (regex.compile("^.")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_31.replace_all(text, "") })) { return 2; }
        if (!text_error({ let __checked_regex_30: regex.Regex = match (regex.compile("")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_30.replace_all(text, "-") })) { return 3; }
        if (!text_is({ let __checked_regex_29: regex.Regex = match (regex.compile("")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_29.replace_all(text, "") }, text)) { return 4; }
        if (!text_is({ let __checked_regex_28: regex.Regex = match (regex.compile(".")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_28.replace_all(text, "") }, "")) { return 5; }
        let dots = { let __checked_regex_27: regex.Regex = match (regex.compile(".")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_27.find_all(text) };
        if (dots.len() != bytes.len()) { return 6; }
        let captures = { let __checked_regex_26: regex.Regex = match (regex.compile("(.)")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_26.captures_all(text) };
        let h = buf_new(bytes.len());
        let i = 0;
        while (i < captures.len()) {
            let c = captures[i];
            if (!capture_error(c.group(0)) || !capture_error(c.group(1))) { buf_free(h); return 7; }
            if (!at(c.group_start(1), i) || !at(c.group_end(1), i + 1)) { buf_free(h); return 8; }
            let raw = c.group_bytes(1).unwrap_or([]);
            if (raw.len() != 1 || raw[0] != bytes[i]) { buf_free(h); return 9; }
            buf_push_bytes_range(h, raw, 0, raw.len());
            i = i + 1;
        }
        let rebuilt = buf_take_bytes(h);
        buf_free(h);
        if (!same_bytes(rebuilt, bytes)) { return 10; }
        let Some(named) = { let __checked_regex_25: regex.Regex = match (regex.compile("(?<a>.)(?<b>.*)")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_25.captures(text) } else { return 11; };
        if (!capture_error(named.group_named("a")) || !capture_error(named.group_named("b"))) { return 11; }
        if (!same_bytes(named.group_named_bytes("a").unwrap_or([]), [bytes[0]])) { return 12; }
        if (!text_is({ let __checked_regex_24: regex.Regex = match (regex.compile("(?<a>.)(?<b>.*)")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_24.replace_groups(text, "${a}${b}") }, text)) { return 13; }
        if (!text_is({ let __checked_regex_23: regex.Regex = match (regex.compile("(.)")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_23.replace_all_groups(text, "$1") }, text)) { return 14; }
        if (!text_error({ let __checked_regex_22: regex.Regex = match (regex.compile("(.)")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_22.replace_all_groups(text, "$1-") })) { return 15; }
        if (!same_bytes({ let __checked_regex_21: regex.Regex = match (regex.compile("(.)")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_21.replace_groups_bytes(text, "$1") }, bytes)) { return 16; }
        if (!same_bytes({ let __checked_regex_20: regex.Regex = match (regex.compile("(.)")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_20.replace_all_groups_bytes(text, "$1") }, bytes)) { return 17; }
        let split = { let __checked_regex_19: regex.Regex = match (regex.compile("^.")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_19.split_bytes(text) };
        if (split.len() != 2 || split[0].len() != 0 || split[1].len() != bytes.len() - 1) { return 18; }
        if (!parts_error({ let __checked_regex_18: regex.Regex = match (regex.compile("^.")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_18.split(text) })) { return 19; }
        let empty_parts = { let __checked_regex_17: regex.Regex = match (regex.compile(".")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_17.split(text) };
        match (empty_parts) {
            Ok(parts) => {
                if (parts.len() != bytes.len() + 1) { return 20; }
                for part in parts { if (part != "") { return 21; } }
            },
            Err(_) => { return 22; },
        }
        let empty = { let __checked_regex_16: regex.Regex = match (regex.compile("()")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_16.captures_all(text) };
        for capture in empty {
            if (!capture_text(capture.group(1), "")) { return 23; }
        }
        let raw = { let __checked_regex_15: regex.Regex = match (regex.compile(".")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_15.replace_bytes(text, "X") };
        if (raw.len() != bytes.len() || raw[0] != 88 as u8 || raw[1] != bytes[1]) { return 24; }
        let all = { let __checked_regex_14: regex.Regex = match (regex.compile(".")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_14.replace_all_bytes(text, "X") };
        if (all.len() != bytes.len()) { return 25; }
        for b in all { if (b != 88 as u8) { return 26; } }
    }
    for text in ["", "ascii", "é", "longer Unicode é € 𐀀 input"] {
        if (!text_is({ let __checked_regex_13: regex.Regex = match (regex.compile("NEVER")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_13.replace(text, "!") }, text)) { return 27; }
        if (!text_is({ let __checked_regex_12: regex.Regex = match (regex.compile("NEVER")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_12.replace_all(text, "!") }, text)) { return 28; }
        if (!text_is({ let __checked_regex_11: regex.Regex = match (regex.compile("NEVER")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_11.replace_groups(text, "$1") }, text)) { return 29; }
        if (!text_is({ let __checked_regex_10: regex.Regex = match (regex.compile("NEVER")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_10.replace_all_groups(text, "$1") }, text)) { return 30; }
        if (!same_bytes({ let __checked_regex_9: regex.Regex = match (regex.compile("NEVER")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_9.replace_bytes(text, "!") }, text.bytes())) { return 31; }
        match ({ let __checked_regex_8: regex.Regex = match (regex.compile("NEVER")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_8.split(text) }) {
            Ok(parts) => { if (parts.len() != 1 || parts[0] != text) { return 32; } },
            Err(_) => { return 33; },
        }
        let __checked_regex_7: regex.Regex = match (regex.compile("NEVER")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } };
        if let Some(_) = __checked_regex_7.captures(text) { return 34; }
        if ({ let __checked_regex_6: regex.Regex = match (regex.compile("NEVER")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_6.captures_all(text) }.len() != 0) { return 35; }
    }
    let Some(absent) = { let __checked_regex_5: regex.Regex = match (regex.compile("a(b)?")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_5.captures("a") } else { return 36; };
    if (!capture_absent(absent.group(1)) || absent.has_group(1) || !capture_absent(absent.group_named("missing")) || absent.group_named_bytes("missing").unwrap_or([]).len() != 0) { return 36; }
    if (!text_is({ let __checked_regex_4: regex.Regex = match (regex.compile("(é)")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_4.replace_groups("aéb", "€$$$1𐀀") }, "a€$é𐀀b")) { return 37; }
    if (!text_is({ let __checked_regex_3: regex.Regex = match (regex.compile("(é)")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_3.replace_groups("é", "${missing}$x$") }, "$x$")) { return 38; }
    if (!text_is({ let __checked_regex_2: regex.Regex = match (regex.compile("(é)")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_2.replace_groups("é", "${unclosed") }, "${unclosed")) { return 39; }
    if (!text_is({ let __checked_regex_1: regex.Regex = match (regex.compile("(?<é>é)")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_1.replace_groups("é", "${é}") }, "é")) { return 40; }
    if (!text_is({ let __checked_regex_0: regex.Regex = match (regex.compile("é")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_0.replace_all("ééé", "€") }, "é€€")) { return 41; }
    return 0;
}

function main(): i32 { return check(); }
`
