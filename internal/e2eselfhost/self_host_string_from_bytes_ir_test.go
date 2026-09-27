package e2eselfhost

import "testing"

// stringFromBytesIRCases exercise `string_from_bytes_unchecked(u8[])` through the self-host
// IR path on x86-64 + wasm.
//
// The wasm IR backend emitted `op_str_from_bytes` as a `call
// $__fern_string_from_bytes`, but `wasm_ir_run` had no gate to actually emit that
// helper (unlike its sibling `str_bytes`), so any IR-path program packing bytes
// into a string failed to link ("unknown func $__fern_string_from_bytes"). x86 /
// arm64 already emitted the helper. The fix adds the missing
// `module_emits_op(mod, "str_from_bytes")` gate (and exports the helper).
//
// Each case is oracle-checked against the interpreter and returns a
// non-negative value <= 126 (cf. #2908).
const stringFromBytesPrelude = `function hex_lc(n: i32): i32 { if (n < 10) { return 48 + n; } return 97 + (n - 10); }
function hexenc(s: string): string {
    var n: i32 = s.len();
    if (n == 0) { return ""; }
    var buf: u8[] = __alloc_u8(n * 2);
    var i: i32 = 0;
    while (i < n) {
        var b: i32 = s[i] as i32;
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
	// Minimal direct use: pack [72, 105] ("Hi") -> length 2.
	{"direct-len", `function main(): i32 { var b: u8[] = __alloc_u8(2); b = b.with(0, 72 as u8); b = b.with(1, 105 as u8); return string_from_bytes_unchecked(b).len(); }`},
	// Round-trip a byte through the packed string: [65]("A")[0] = 65.
	{"direct-byte", `function main(): i32 { var b: u8[] = __alloc_u8(1); b = b.with(0, 65 as u8); return string_from_bytes_unchecked(b)[0] as i32; }`},
	// hex_encode: "A" -> "41"; first digit '4' = 52.
	{"hex-digit0", stringFromBytesPrelude + `function main(): i32 { return hexenc("A")[0] as i32; }`},
	// hex_encode: "z" (0x7a) -> "7a"; second digit 'a' = 97.
	{"hex-digit1", stringFromBytesPrelude + `function main(): i32 { return hexenc("z")[1] as i32; }`},
	// hex_encode length: "hello" -> 10 hex chars.
	{"hex-len", stringFromBytesPrelude + `function main(): i32 { return hexenc("hello").len(); }`},
}

// TestSelfHostStringFromBytesIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
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
