package e2ecompiler

import "testing"

const coreIntParseIRPrelude = `function __radix_digit(c: i32): i32 {
    if (c >= 48 && c <= 57)  { return c - 48; }
    if (c >= 97 && c <= 122) { return c - 87; }
    if (c >= 65 && c <= 90)  { return c - 55; }
    return 0 - 1;
}
function parse_int_radix(s: string, base: i32): Option[i32] {
    if (base < 2 || base > 36) { return None; }
    let n: i32 = s.len();
    if (n == 0) { return None; }
    let neg: boolean = false;
    let i: i32 = 0;
    if (s[0] == 45) { neg = true; i = 1; }
    else if (s[0] == 43) { i = 1; }
    if (i >= n) { return None; }
    return __radix_value(s, i, base, neg);
}
function __radix_value(s: string, from: i32, base: i32, neg: boolean): Option[i32] {
    let limit: i64 = 2147483647 as i64;
    if (neg) { limit = limit + 1 as i64; }
    let v: i64 = 0 as i64;
    let i: i32 = from;
    while (i < s.len()) {
        let d: i32 = __radix_digit(s[i] as i32);
        if (d < 0 || d >= base) { return None; }
        v = v * (base as i64) + d as i64;
        if (v > limit) { return None; }
        i = i + 1;
    }
    if (neg) { v = 0 as i64 - v; }
    return Some(v as i32);
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
	{"hex", `return parse_or("5a", 16, 0);`, 90},
	{"plus-sign", `return parse_or("+42", 10, 0);`, 42},
	{"binary", `return parse_or("1100100", 2, 0);`, 100},
	{"base36", `return parse_or("z", 36, 0);`, 35},
	{"invalid-digit", `return parse_or("12x", 10, 7);`, 7},
	{"empty", `return parse_or("", 10, 9);`, 9},
	{"bad-base", `return parse_or("10", 99, 5);`, 5},
	{"negative", `return parse_or("-3", 10, 0) + 100;`, 97},
	{"int-max", `return parse_or("2147483647", 10, 0) - 2147483600;`, 47},
	{"int-min", `return parse_or("-2147483648", 10, 0) + 2147483647 + 53;`, 52},
	{"overflow", `return parse_or("99999999999999999999", 10, 11);`, 11},
	{"overflow-by-one", `return parse_or("2147483648", 10, 13);`, 13},
}

func coreIntParseIRSrc(mainBody string) string {
	return coreIntParseIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

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
