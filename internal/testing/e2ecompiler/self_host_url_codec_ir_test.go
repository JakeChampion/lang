package e2ecompiler

import "testing"

const urlCodecIRPrelude = `function url_hex_char(d: i32): i32 { if (d < 10) { return d + 48; } return d + 55; }
function url_unreserved(b: i32): boolean {
    if (b >= 65 && b <= 90) { return true; }
    if (b >= 97 && b <= 122) { return true; }
    if (b >= 48 && b <= 57) { return true; }
    if (b == 45 || b == 46 || b == 95 || b == 126) { return true; }
    return false;
}
function url_encode(s: string): string {
    let n: i32 = s.len();
    let out: string = "";
    let i: i32 = 0;
    while (i < n) {
        let b: i32 = s[i] as i32;
        if (url_unreserved(b)) { out = out + slice_unchecked(s, i, i+1).to_owned(); }
        else {
            let hi: i32 = (b >> 4) & 15;
            let lo: i32 = b & 15;
            let trip: u8[] = [37 as u8, url_hex_char(hi) as u8, url_hex_char(lo) as u8];
            out = out + string_from_bytes_unchecked(trip);
        }
        i = i + 1;
    }
    return out;
}
function url_hex_val(c: i32): i32 {
    if (c >= 48 && c <= 57) { return c - 48; }
    if (c >= 97 && c <= 102) { return c - 87; }
    if (c >= 65 && c <= 70) { return c - 55; }
    return -1;
}
function url_decode(s: string): string {
    let n: i32 = s.len();
    let out: string = "";
    let i: i32 = 0;
    while (i < n) {
        let b: i32 = s[i] as i32;
        let emit: string = slice_unchecked(s, i, i+1).to_owned();
        let consumed: i32 = 1;
        if (b == 37 && i + 2 < n) {
            let h1: i32 = url_hex_val(s[i+1] as i32);
            let h2: i32 = url_hex_val(s[i+2] as i32);
            if (h1 >= 0 && h2 >= 0) {
                let by: u8[] = [((h1 << 4) | h2) as u8];
                emit = string_from_bytes_unchecked(by);
                consumed = 3;
            }
        }
        out = out + emit;
        i = i + consumed;
    }
    return out;
}
`

var urlCodecIRCases = []struct {
	name string
	main string
	want int
}{
	{"encode-escapes", `return url_encode("a b/c").len();`, 9},
	{"encode-passthrough", `return url_encode("Hello").len();`, 5},
	{"encode-two-spaces", `return url_encode("  ").len();`, 6},
	{"encode-uppercase-hex", `let e: string = url_encode("/"); return e[2] as i32;`, 70},
	{"decode-roundtrip", `return url_decode("a%20b%2Fc").len();`, 5},
	{"decode-invalid", `return url_decode("%zz").len();`, 3},
	{"decode-passthrough", `return url_decode("abc").len();`, 3},
}

func urlCodecIRSrc(mainBody string) string {
	return "import \"std/string\";\n" + urlCodecIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

func TestSelfHostUrlCodecIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range urlCodecIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, urlCodecIRSrc(tc.main), target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
