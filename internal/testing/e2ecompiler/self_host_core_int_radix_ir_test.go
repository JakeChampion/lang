package e2ecompiler

import "testing"

const coreIntRadixIRPrelude = `function radix_digit(c: i32): i32 {
    if (c >= 48 && c <= 57)  { return c - 48; }
    if (c >= 97 && c <= 122) { return c - 87; }
    if (c >= 65 && c <= 90)  { return c - 55; }
    return 0 - 1;
}
function radix_char(d: i32): i32 {
    if (d < 10) { return 48 + d; }
    return 97 + (d - 10);
}
function int_to_string_radix(n: i32, base: i32): string {
    if (base < 2 || base > 36) { return ""; }
    if (n == 0) { return "0"; }
    let neg: boolean = (n < 0);
    let mag: i64 = n as i64;
    if (neg) { mag = 0 - mag; }
    let digits: u8[] = __alloc_u8(33);
    let k: i32 = 0;
    let b64: i64 = base as i64;
    while (mag > (0 as i64)) {
        let d: i32 = (mag % b64) as i32;
        digits = digits.with(k, radix_char(d) as u8);
        k = k + 1;
        mag = mag / b64;
    }
    let out_len: i32 = k;
    if (neg) { out_len = k + 1; }
    let buf: u8[] = __alloc_u8(out_len);
    let bi: i32 = 0;
    if (neg) { buf = buf.with(0, 45 as u8); bi = 1; }
    let j: i32 = k - 1;
    while (j >= 0) { buf = buf.with(bi, digits[j]); bi = bi + 1; j = j - 1; }
    return string_from_bytes_unchecked(buf);
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
    let v: i32 = 0;
    while (i < n) {
        let d: i32 = radix_digit(s[i] as i32);
        if (d < 0 || d >= base) { return None; }
        v = v * base + d;
        i = i + 1;
    }
    if (neg) { v = 0 - v; }
    return Some(v);
}
`

var coreIntRadixIRCases = []struct {
	name string
	main string
	want int
}{
	{"hex-len", `let s: string = int_to_string_radix(255, 16); return s.len();`, 2},
	{"hex-byte", `let s: string = int_to_string_radix(255, 16); return s[0] as i32;`, 102},
	{"binary-len", `let s: string = int_to_string_radix(10, 2); return s.len();`, 4},
	{"base36-byte", `let s: string = int_to_string_radix(30, 36); return s[0] as i32;`, 117},
	{"zero", `let s: string = int_to_string_radix(0, 16); return s.len();`, 1},
	{"neg-len", `let s: string = int_to_string_radix(0 - 255, 16); return s.len();`, 3},
	{"neg-sign", `let s: string = int_to_string_radix(0 - 255, 16); return s[0] as i32;`, 45},
	{"bad-base", `let s: string = int_to_string_radix(10, 99); return s.len();`, 0},
	{"round-trip", `let s: string = int_to_string_radix(100, 16); match (parse_int_radix(s, 16)) { Some(v) => { return v; }, None => { return 0; } } return 0;`, 100},
}

func coreIntRadixIRSrc(mainBody string) string {
	return coreIntRadixIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

func TestSelfHostCoreIntRadixIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range coreIntRadixIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, coreIntRadixIRSrc(tc.main), target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
