package e2eselfhost

import "testing"

// timeRfc3339IRCases exercise std/time's RFC-3339 surface — `format_rfc3339`
// (Instant -> string) and `instant_parse_rfc3339` (string -> Option[Instant]) —
// through the self-host IR path on x86-64 + wasm. This is the last std/time
// "self-host pending" piece (after the pure-i32 helpers, the Date civil-date
// methods, and `date_parse_iso`): it adds an **i64 struct field** (`sec: i64`)
// to the mix — i64 arithmetic (mul/div/mod, `as i64` / `as i32` casts), an
// i64-carrying struct constructor, and `Some(Moment{ sec: <i64>, ... })`.
//
// The bodies are std/time's `format_rfc3339` / `instant_parse_rfc3339` verbatim
// except the structs are named `Civil` / `Moment` (the built-in `Date` /
// `Instant` names are reserved, E010) and `int.int_to_string` is written
// `.to_string()`. `import "std/i32"` lets the interpreter oracle resolve
// `.to_string()`. Each case returns a value kept <= 126 and is oracle-checked
// against the interpreter. FEATURE-AUDIT std/time row.
const timeRfc3339IRPrelude = `import "std/i32";
struct Civil { year: i32, month: i32, day: i32 }
struct Moment { sec: i64, nsec: i32 }
function parse_digits(s: string, start: i32, end: i32): i32 {
    if (start >= end) { return -1; }
    let acc: i32 = 0; let i: i32 = start;
    while (i < end) { let b: i32 = s[i] as i32; if (b < 48 || b > 57) { return -1; } acc = acc * 10 + (b - 48); i = i + 1; }
    return acc;
}
function days_from_civil(y_in: i32, m: i32, d: i32): i32 {
    let y: i32 = y_in; if (m <= 2) { y = y - 1; }
    let era: i32 = 0; if (y >= 0) { era = y / 400; } else { era = (y - 399) / 400; }
    let yoe: i32 = y - era * 400; let mp: i32 = 0;
    if (m > 2) { mp = m - 3; } else { mp = m + 9; }
    let doy: i32 = (153 * mp + 2) / 5 + d - 1;
    let doe: i32 = yoe * 365 + yoe / 4 - yoe / 100 + doy;
    return era * 146097 + doe - 719468;
}
function civil_from_days(z_in: i32): Civil {
    let z: i32 = z_in + 719468; let era: i32 = 0;
    if (z >= 0) { era = z / 146097; } else { era = (z - 146096) / 146097; }
    let doe: i32 = z - era * 146097;
    let yoe: i32 = (doe - doe / 1460 + doe / 36524 - doe / 146096) / 365;
    let y: i32 = yoe + era * 400; let doy: i32 = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp: i32 = (5 * doy + 2) / 153; let d: i32 = doy - (153 * mp + 2) / 5 + 1; let m: i32 = 0;
    if (mp < 10) { m = mp + 3; } else { m = mp - 9; }
    if (m <= 2) { y = y + 1; }
    return Civil { year: y, month: m, day: d };
}
function pad2(n: i32): string { if (n < 10) { return "0" + n.to_string(); } return n.to_string(); }
function pad4(n: i32): string {
    if (n < 10) { return "000" + n.to_string(); }
    if (n < 100) { return "00" + n.to_string(); }
    if (n < 1000) { return "0" + n.to_string(); }
    return n.to_string();
}
function (d: Civil) format_iso(): string { return pad4(d.year) + "-" + pad2(d.month) + "-" + pad2(d.day); }
function (i: Moment) format_rfc3339(): string {
    let sec: i64 = i.sec; let spd: i64 = 86400 as i64;
    let days: i64 = sec / spd; let sec_in_day: i64 = sec - days * spd;
    if (sec_in_day < (0 as i64)) { sec_in_day = sec_in_day + spd; days = days - (1 as i64); }
    let d: Civil = civil_from_days(days as i32);
    let sid: i32 = sec_in_day as i32; let h: i32 = sid / 3600; let rem: i32 = sid - h * 3600;
    let mn: i32 = rem / 60; let s: i32 = rem - mn * 60;
    let head: string = d.format_iso() + "T" + pad2(h) + ":" + pad2(mn) + ":" + pad2(s);
    if (i.nsec == 0) { return head + "Z"; }
    let ns: i32 = i.nsec; let div: i32 = 100000000; let frac: string = "";
    while (div >= 1) {
        let digit: i32 = (ns / div) - (ns / (div * 10)) * 10;
        if (digit == 0) { frac = frac + "0"; } else if (digit == 1) { frac = frac + "1"; }
        else if (digit == 2) { frac = frac + "2"; } else if (digit == 3) { frac = frac + "3"; }
        else if (digit == 4) { frac = frac + "4"; } else if (digit == 5) { frac = frac + "5"; }
        else if (digit == 6) { frac = frac + "6"; } else if (digit == 7) { frac = frac + "7"; }
        else if (digit == 8) { frac = frac + "8"; } else { frac = frac + "9"; }
        if (div == 1) { div = 0; } else { div = div / 10; }
    }
    return head + "." + frac + "Z";
}
function instant_parse_rfc3339(s: string): Option[Moment] {
    let n: i32 = s.len();
    if (n < 20) { return None; }
    if (s[4] != 45 || s[7] != 45) { return None; }
    if (s[10] != 84) { return None; }
    if (s[13] != 58 || s[16] != 58) { return None; }
    let y: i32 = parse_digits(s, 0, 4); let mo: i32 = parse_digits(s, 5, 7); let d: i32 = parse_digits(s, 8, 10);
    let h: i32 = parse_digits(s, 11, 13); let mn: i32 = parse_digits(s, 14, 16); let sc: i32 = parse_digits(s, 17, 19);
    if (y < 0 || mo < 0 || d < 0 || h < 0 || mn < 0 || sc < 0) { return None; }
    let nsec: i32 = 0;
    if (s[19] == 46) {
        let z_idx: i32 = -1; let i: i32 = 20;
        while (i < n) { if (s[i] == 90) { z_idx = i; i = n; } else { i = i + 1; } }
        if (z_idx < 0) { return None; }
        if (z_idx == 20) { return None; }
        if (z_idx - 20 > 9) { return None; }
        let frac: i32 = parse_digits(s, 20, z_idx);
        if (frac < 0) { return None; }
        let pad: i32 = 9 - (z_idx - 20);
        while (pad > 0) { frac = frac * 10; pad = pad - 1; }
        nsec = frac;
    } else if (s[19] == 90) {
    } else { return None; }
    let days: i32 = days_from_civil(y, mo, d); let spd: i64 = 86400 as i64;
    let sec: i64 = (days as i64) * spd + ((h * 3600 + mn * 60 + sc) as i64);
    return Some(Moment { sec: sec, nsec: nsec });
}
`

var timeRfc3339IRCases = []struct {
	name string
	main string
}{
	// Parse -> Some; extract the hour from the i64 sec field (12).
	{"parse-hour", `match (instant_parse_rfc3339("2024-06-13T12:34:56Z")) { Some(m) => { return ((m.sec % (86400 as i64)) / (3600 as i64)) as i32; }, None => { return 100; }, }`},
	// Parse -> Some; the seconds-of-minute field (56).
	{"parse-seconds", `match (instant_parse_rfc3339("2024-06-13T12:34:56Z")) { Some(m) => { return (m.sec % (60 as i64)) as i32; }, None => { return 100; }, }`},
	// Parse with fraction -> Some; nsec = 500000000, /1e8 = 5.
	{"parse-fraction", `match (instant_parse_rfc3339("2024-06-13T12:34:56.5Z")) { Some(m) => { return m.nsec / 100000000; }, None => { return 100; }, }`},
	// Too short -> None -> sentinel 7.
	{"parse-too-short", `match (instant_parse_rfc3339("2024")) { Some(m) => { return 0; }, None => { return 7; }, }`},
	// Missing 'T' separator -> None -> sentinel 8.
	{"parse-bad-sep", `match (instant_parse_rfc3339("2024-06-13X12:34:56Z")) { Some(m) => { return 0; }, None => { return 8; }, }`},
	// format_rfc3339 of a whole-second instant: "YYYY-MM-DDTHH:MM:SSZ" -> 20.
	{"format-len", `let m: Moment = Moment { sec: 1718281496 as i64, nsec: 0 }; return m.format_rfc3339().len();`},
	// format_rfc3339 with a fraction adds ".nnnnnnnnn" (10 chars) -> 30.
	{"format-frac-len", `let m: Moment = Moment { sec: 1718281496 as i64, nsec: 500000000 }; return m.format_rfc3339().len();`},
	// Round-trip: parse then format; first byte is '2' (50).
	{"roundtrip-firstbyte", `match (instant_parse_rfc3339("2024-06-13T12:34:56Z")) { Some(m) => { return m.format_rfc3339()[0] as i32; }, None => { return 100; }, }`},
}

func timeRfc3339IRSrc(mainBody string) string {
	return timeRfc3339IRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostTimeRfc3339IR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostTimeRfc3339IR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range timeRfc3339IRCases {
		src := timeRfc3339IRSrc(tc.main)
		want := interpExit(t, interpBin, src)
		for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, src, target); code != want {
					t.Errorf("exited %d, want %d (interp oracle)\n%s", code, want, stderr)
				}
			})
		}
	}
}
