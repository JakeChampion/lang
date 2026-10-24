package e2ecompiler

import "testing"

const stringFromBytesPrelude = `function hex_lc(n: i32): i32 { if (n < 10) { return 48 + n; } return 97 + (n - 10); }
function hexenc(s: string): string {
    let n: i32 = s.len();
    if (n == 0) { return ""; }
    let buf: u8[] = __alloc_u8(n * 2);
    let i: i32 = 0;
    while (i < n) {
        let b: i32 = s[i] as i32;
        buf = buf.with(i * 2, hex_lc((b >> 4) & 15) as u8);
        buf = buf.with(i * 2 + 1, hex_lc(b & 15) as u8);
        i = i + 1;
    }
    return string_from_bytes_unchecked(buf);
}
`

var stringFromBytesIRCases = []struct {
	name string
	main string
}{
	{"direct-len", `function main(): i32 { let b: u8[] = __alloc_u8(2); b = b.with(0, 72 as u8); b = b.with(1, 105 as u8); return string_from_bytes_unchecked(b).len(); }`},
	{"direct-byte", `function main(): i32 { let b: u8[] = __alloc_u8(1); b = b.with(0, 65 as u8); return string_from_bytes_unchecked(b)[0] as i32; }`},
	{"hex-digit0", stringFromBytesPrelude + `function main(): i32 { return hexenc("A")[0] as i32; }`},
	{"hex-digit1", stringFromBytesPrelude + `function main(): i32 { return hexenc("z")[1] as i32; }`},
	{"hex-len", stringFromBytesPrelude + `function main(): i32 { return hexenc("hello").len(); }`},
}

func TestSelfHostStringFromBytesIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range stringFromBytesIRCases {
		src := tc.main + "\n"
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
