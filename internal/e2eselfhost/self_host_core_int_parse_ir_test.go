package e2eselfhost

import "testing"

// coreIntParseIRCases exercise core/int's radix parse direction
// (parse_int_radix + __radix_digit) through the self-host IR path on x86-64 +
// wasm (the `core/int` row was fully unaudited). The two functions are inlined
// verbatim from `internal/stdlib/core/int.fern` rather than imported (no
// reserved type names involved). This verifies the constructs the parse
// direction lowers to compile on the IR path: `Option[i32]` `Some`/`None`
// returns with a payload-binding `match`, string indexing (`s[i]`) with
// char-class comparisons, a multiply-accumulate `while` loop, sign handling, and
// negation. Each program returns a small deterministic int (<= 126);
// expectations are oracle-checked against the native interpreter. The
// `to_string` direction is not covered (it pokes raw memory via `__alloc_u8` /
// `__memcpy` / `usize`), mirroring the std/u64 `to_string` caveat. FEATURE-AUDIT core/int row (parse direction).
const coreIntParseIRPrelude = `function __radix_digit(c: i32): i32 {
    if (c >= 48 && c <= 57)  { return c - 48; }
    if (c >= 97 && c <= 122) { return c - 87; }
    if (c >= 65 && c <= 90)  { return c - 55; }
    return 0 - 1;
}
function parse_int_radix(s: string, base: i32): Option[i32] {
    if (base < 2 || base > 36) { return None; }
    var n: i32 = s.len();
    if (n == 0) { return None; }
    var neg: boolean = false;
    var i: i32 = 0;
    if (s[0] == 45) { neg = true; i = 1; }
    else if (s[0] == 43) { i = 1; }
    if (i >= n) { return None; }
    var v: i32 = 0;
    while (i < n) {
        var d: i32 = __radix_digit(s[i] as i32);
        if (d < 0 || d >= base) { return None; }
        v = v * base + d;
        i = i + 1;
    }
    if (neg) { v = 0 - v; }
    return Some(v);
}
function parse_or(s: string, base: i32, dflt: i32): i32 {
    match (parse_int_radix(s, base)) {
        Some(v) => { return v; },
        None => { return dflt; },
    }
    return dflt;
}
`

var coreIntParseIRCases = []struct {
	name string
	main string
	want int
}{
	// hex parse with a lowercase a-f digit: 0x5a = 90 (kept <=125 so wasmtime
	// doesn't normalise the exit code: codes >=126 come back as 1 under WASI).
	{"hex", `return parse_or("5a", 16, 0);`, 90}, // 0x5a = 90
	// base-10 with leading '+' sign.
	{"plus-sign", `return parse_or("+42", 10, 0);`, 42},
	// binary "1100100" = 100.
	{"binary", `return parse_or("1100100", 2, 0);`, 100},
	// base-36 "z" = 35.
	{"base36", `return parse_or("z", 36, 0);`, 35},
	// invalid digit for the base -> None -> default 7.
	{"invalid-digit", `return parse_or("12x", 10, 7);`, 7},
	// empty string -> None -> default 9.
	{"empty", `return parse_or("", 10, 9);`, 9},
	// out-of-range base -> None -> default 5.
	{"bad-base", `return parse_or("10", 99, 5);`, 5},
	// negative result, mapped back to a small positive via arithmetic: -3 + 100 = 97.
	{"negative", `return parse_or("-3", 10, 0) + 100;`, 97},
}

func coreIntParseIRSrc(mainBody string) string {
	return coreIntParseIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostCoreIntParseIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code.
func TestSelfHostCoreIntParseIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range coreIntParseIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, coreIntParseIRSrc(tc.main), target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
