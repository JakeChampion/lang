package e2ecompiler

import (
	"os"
	"testing"
)

// base64IRCases compile the REAL std/base64 module (concatenated with a main, the
// single-module trick the std/json / std/hex self-host tests use) through the
// self-host IR path on x86-64 + wasm, confirming `base64_encode` / `base64_decode`
// lower end-to-end. Like std/hex, std/base64 builds on `__alloc_u8` + `.with` +
// bit ops + `string_from_bytes_unchecked` (unblocked on wasm by the recent helper-gate fix).
//
// Each case is oracle-checked against the interpreter and returns a
// non-negative value <= 126 (cf. #2908).
var base64IRCases = []struct {
	name string
	main string
}{
	// The module is compiled alone (no modload), so std/string's `s.bytes()`
	// isn't in scope — the u8[] encoder inputs are written as byte literals.
	{"encode-len-pad", `return base64_encode([104 as u8, 105 as u8]).len();`},                                                // "aGk=" -> 4
	{"encode-len-exact", `return base64_encode([77 as u8, 97 as u8, 110 as u8]).len();`},                                     // "TWFu" -> 4
	{"encode-digit", `return base64_encode([77 as u8, 97 as u8, 110 as u8])[0] as i32;`},                                     // 'T' = 84
	{"decode-len", `return base64_decode("aGk=").len();`},                                                                    // "hi" -> 2
	{"roundtrip", `return base64_decode(base64_encode([72 as u8, 105 as u8]))[0] as i32;`},                                   // 'H' = 72
	{"roundtrip-len", `return base64_decode(base64_encode([104 as u8, 101 as u8, 108 as u8, 108 as u8, 111 as u8])).len();`}, // 5
}

// base64Source reads the real std/base64.fern and appends a main (single-module,
// no modload — std/base64 has no imports).
func base64Source(t *testing.T, mainBody string) []byte {
	t.Helper()
	src, err := os.ReadFile("../../stdlib/std/base64.fern")
	if err != nil {
		t.Fatalf("read std/base64.fern: %v", err)
	}
	out := append([]byte{}, src...)
	out = append(out, []byte("\nfunction main(): i32 { "+mainBody+" }\n")...)
	return out
}

// TestSelfHostBase64IR runs each case through the self-host CLI on x86-64 and
// wasm, oracle-checked against the interpreter.
func TestSelfHostBase64IR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range base64IRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := string(base64Source(t, tc.main))
			want := interpExit(t, interpBin, src)
			for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != want {
					t.Errorf("%s on %s exited %d, want %d (interp oracle)\n%s", tc.name, target, code, want, stderr)
				}
			}
		})
	}
}
