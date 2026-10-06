package e2ecompiler

import (
	"os"
	"testing"
)

// hexIRCases compile the REAL std/hex module (concatenated with a main, the same
// single-module trick the std/json self-host test uses) through the self-host IR
// path on x86-64 + wasm, confirming std/hex (`hex_encode` / `hex_decode`) lowers
// end-to-end. std/hex builds on `__alloc_u8` + `.with` + bit ops + `string_from_bytes_unchecked`
// — the last of which only started lowering on the wasm IR path with the recent
// helper-gate fix; this is the module-level confirmation.
//
// Each case is oracle-checked against the interpreter and returns a
// non-negative value <= 126 (cf. #2908).
var hexIRCases = []struct {
	name string
	main string
}{
	// The module is compiled alone (no modload), so std/string's `s.bytes()`
	// isn't in scope — the u8[] encoder inputs are written as byte literals.
	{"encode-len", `return hex_encode([104 as u8, 105 as u8]).len();`},                                                 // "6869" -> 4
	{"encode-digit", `return hex_encode([65 as u8])[0] as i32;`},                                                       // 0x41 -> '4' = 52
	{"decode-len", `return hex_decode("6869").len();`},                                                                 // -> "hi" len 2
	{"decode-char", `return hex_decode("7a")[0] as i32;`},                                                              // -> 'z' = 122
	{"roundtrip", `return hex_decode(hex_encode([72 as u8, 105 as u8]))[0] as i32;`},                                   // 'H' = 72
	{"roundtrip-len", `return hex_decode(hex_encode([104 as u8, 101 as u8, 108 as u8, 108 as u8, 111 as u8])).len();`}, // 5
}

// hexSource reads the real std/hex.fern and appends a main (single-module, no
// modload — std/hex has no imports).
func hexSource(t *testing.T, mainBody string) []byte {
	t.Helper()
	src, err := os.ReadFile("../../stdlib/std/hex.fern")
	if err != nil {
		t.Fatalf("read std/hex.fern: %v", err)
	}
	out := append([]byte{}, src...)
	out = append(out, []byte("\nfunction main(): i32 { "+mainBody+" }\n")...)
	return out
}

// TestSelfHostHexIR runs each case through the self-host CLI on x86-64 and
// wasm, oracle-checked against the interpreter.
func TestSelfHostHexIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range hexIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := string(hexSource(t, tc.main))
			want := interpExit(t, interpBin, src)
			for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != want {
					t.Errorf("%s on %s exited %d, want %d (interp oracle)\n%s", tc.name, target, code, want, stderr)
				}
			}
		})
	}
}
