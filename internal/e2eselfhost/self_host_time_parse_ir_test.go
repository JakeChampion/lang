package e2eselfhost

import "testing"

// timeParseIRCases exercise std/time's `date_parse_iso` — an `Option`-returning
// parser — through the self-host IR path on x86-64 + wasm. This is the
// `Option`-method piece of the std/time "self-host pending" audit gap (after
// the pure-i32 helpers in TestSelfHostTimeIR and the Date struct methods in
// TestSelfHostTimeDateIR).
//
// The body is std/time's `date_parse_iso` verbatim except the struct is named
// `Civil` (the built-in `Date` name is reserved, E010). The coverage that
// matters: a function returning `Option[Civil]` constructs `Some(Civil{...})`
// / `None`, and `main` discriminates the result with a `match` that binds the
// struct payload and reads its fields — `Option` construction + payload-binding
// match + struct field access, all routed through the IR path.
//
// No imports are needed for the interpreter oracle (the parser is pure
// builtins + struct/Option). Each case returns a value kept <= 126 and is
// oracle-checked against the interpreter. FEATURE-AUDIT std/time row.
const timeParseIRPrelude = `struct Civil { year: i32, month: i32, day: i32 }
function parse_digits(s: string, start: i32, end: i32): i32 {
    if (start >= end) { return -1; }
    var acc: i32 = 0;
    var i: i32 = start;
    while (i < end) {
        var b: i32 = s[i] as i32;
        if (b < 48 || b > 57) { return -1; }
        acc = acc * 10 + (b - 48);
        i = i + 1;
    }
    return acc;
}
function date_parse_iso(s: string): Option[Civil] {
    if (s.len() != 10) { return None; }
    if (s[4] != 45 || s[7] != 45) { return None; }
    var y: i32 = parse_digits(s, 0, 4);
    var m: i32 = parse_digits(s, 5, 7);
    var d: i32 = parse_digits(s, 8, 10);
    if (y < 0 || m < 0 || d < 0) { return None; }
    return Some(Civil { year: y, month: m, day: d });
}
`

var timeParseIRCases = []struct {
	name string
	main string
}{
	// Valid parse -> Some; sum month + day (6 + 13 = 19).
	{"valid-monthday", `match (date_parse_iso("2026-06-13")) { Some(d) => { return d.month + d.day; }, None => { return 100; }, }`},
	// Valid parse -> Some; read the year field (2026 - 2000 = 26).
	{"valid-year", `match (date_parse_iso("2026-06-13")) { Some(d) => { return d.year - 2000; }, None => { return 100; }, }`},
	// Wrong length ("2026-6-13" is 9 chars) -> None -> sentinel 7.
	{"bad-length", `match (date_parse_iso("2026-6-13")) { Some(d) => { return 0; }, None => { return 7; }, }`},
	// Wrong separators (slashes) -> None -> sentinel 8.
	{"bad-separator", `match (date_parse_iso("2026/06/13")) { Some(d) => { return 0; }, None => { return 8; }, }`},
	// Non-digit byte in the year slice -> None -> sentinel 9.
	{"bad-digit", `match (date_parse_iso("20x6-06-13")) { Some(d) => { return 0; }, None => { return 9; }, }`},
	// Leap-day parses structurally (no calendar validation) -> Some -> day 29.
	{"valid-leapday", `match (date_parse_iso("2024-02-29")) { Some(d) => { return d.day; }, None => { return 100; }, }`},
}

func timeParseIRSrc(mainBody string) string {
	return timeParseIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostTimeParseIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
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
