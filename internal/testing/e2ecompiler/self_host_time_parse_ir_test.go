package e2ecompiler

import "testing"

const timeParseIRPrelude = `struct Civil { year: i32, month: i32, day: i32 }
function parse_digits(s: string, start: i32, end: i32): i32 {
    if (start >= end) { return -1; }
    let acc: i32 = 0;
    let i: i32 = start;
    while (i < end) {
        let b: i32 = s[i] as i32;
        if (b < 48 || b > 57) { return -1; }
        acc = acc * 10 + (b - 48);
        i = i + 1;
    }
    return acc;
}
function date_parse_iso(s: string): Option[Civil] {
    if (s.len() != 10) { return None; }
    if (s[4] != 45 || s[7] != 45) { return None; }
    let y: i32 = parse_digits(s, 0, 4);
    let m: i32 = parse_digits(s, 5, 7);
    let d: i32 = parse_digits(s, 8, 10);
    if (y < 0 || m < 0 || d < 0) { return None; }
    return Some(Civil { year: y, month: m, day: d });
}
`

var timeParseIRCases = []struct {
	name string
	main string
}{
	{"valid-monthday", `match (date_parse_iso("2026-06-13")) { Some(d) => { return d.month + d.day; }, None => { return 100; }, }`},
	{"valid-year", `match (date_parse_iso("2026-06-13")) { Some(d) => { return d.year - 2000; }, None => { return 100; }, }`},
	{"bad-length", `match (date_parse_iso("2026-6-13")) { Some(d) => { return 0; }, None => { return 7; }, }`},
	{"bad-separator", `match (date_parse_iso("2026/06/13")) { Some(d) => { return 0; }, None => { return 8; }, }`},
	{"bad-digit", `match (date_parse_iso("20x6-06-13")) { Some(d) => { return 0; }, None => { return 9; }, }`},
	{"valid-leapday", `match (date_parse_iso("2024-02-29")) { Some(d) => { return d.day; }, None => { return 100; }, }`},
}

func timeParseIRSrc(mainBody string) string {
	return timeParseIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

func TestSelfHostTimeParseIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range timeParseIRCases {
		src := timeParseIRSrc(tc.main)
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
