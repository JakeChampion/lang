package e2eselfhost

import "testing"

// urlCodecIRCases exercise std/url's percent-encoding — `url_encode` /
// `url_decode` — through the self-host IR path on x86-64 + wasm (the `std/url`
// row was unaudited for self-host). The two functions are inlined with their
// byte helpers rather than imported; this verifies the constructs the codec
// lowers to compile on the IR path: byte classification, bit ops (`>>` / `&` /
// `<<` / `|`), `u8[]` array literals with `as u8` element casts, and the
// `string_from_bytes_unchecked(u8[])` builtin packing bytes into a string. Each
// program returns a small deterministic int (kept <= 126); expectations are
// hardcoded, verified against the native interp + x86-64 backends.
// FEATURE-AUDIT std/url row.
const urlCodecIRPrelude = `function url_hex_char(d: i32): i32 { if (d < 10) { return d + 48; } return d + 55; }
function url_unreserved(b: i32): boolean {
    if (b >= 65 && b <= 90) { return true; }
    if (b >= 97 && b <= 122) { return true; }
    if (b >= 48 && b <= 57) { return true; }
    if (b == 45 || b == 46 || b == 95 || b == 126) { return true; }
    return false;
}
function url_encode(s: string): string {
    var n: i32 = s.len();
    var out: string = "";
    var i: i32 = 0;
    while (i < n) {
        var b: i32 = s[i] as i32;
        if (url_unreserved(b)) { out = out + slice_unchecked(s, i, i+1).to_owned(); }
        else {
            var hi: i32 = (b >> 4) & 15;
            var lo: i32 = b & 15;
            var trip: u8[] = [37 as u8, url_hex_char(hi) as u8, url_hex_char(lo) as u8];
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
    var n: i32 = s.len();
    var out: string = "";
    var i: i32 = 0;
    while (i < n) {
        var b: i32 = s[i] as i32;
        var emit: string = slice_unchecked(s, i, i+1).to_owned();
        var consumed: i32 = 1;
        if (b == 37 && i + 2 < n) {
            var h1: i32 = url_hex_val(s[i+1] as i32);
            var h2: i32 = url_hex_val(s[i+2] as i32);
            if (h1 >= 0 && h2 >= 0) {
                var by: u8[] = [((h1 << 4) | h2) as u8];
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
	// "a b/c" -> "a%20b%2Fc": space + '/' each become 3 bytes -> 9.
	{"encode-escapes", `return url_encode("a b/c").len();`, 9},
	// all-unreserved input is passed through verbatim -> 5.
	{"encode-passthrough", `return url_encode("Hello").len();`, 5},
	// two spaces -> "%20%20" -> 6.
	{"encode-two-spaces", `return url_encode("  ").len();`, 6},
	// '/' (47) -> "%2F"; the low nibble's hex digit is UPPERCASE 'F' (70).
	{"encode-uppercase-hex", `var e: string = url_encode("/"); return e[2] as i32;`, 70},
	// "a%20b%2Fc" decodes back to "a b/c" -> 5.
	{"decode-roundtrip", `return url_decode("a%20b%2Fc").len();`, 5},
	// an invalid escape ("%zz") is left literal -> 3.
	{"decode-invalid", `return url_decode("%zz").len();`, 3},
	// no escapes -> verbatim -> 3.
	{"decode-passthrough", `return url_decode("abc").len();`, 3},
}

func urlCodecIRSrc(mainBody string) string {
	return "import \"std/string\";\n" + urlCodecIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostUrlCodecIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code.
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
