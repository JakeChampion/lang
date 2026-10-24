package e2ecompiler

import (
	"os"
	"testing"
)

var base64IRCases = []struct {
	name string
	main string
}{
	{"encode-len-pad", `return base64_encode([104 as u8, 105 as u8]).len();`},
	{"encode-len-exact", `return base64_encode([77 as u8, 97 as u8, 110 as u8]).len();`},
	{"encode-digit", `return base64_encode([77 as u8, 97 as u8, 110 as u8])[0] as i32;`},
	{"decode-len", `return base64_decode("aGk=").len();`},
	{"roundtrip", `return base64_decode(base64_encode([72 as u8, 105 as u8]))[0] as i32;`},
	{"roundtrip-len", `return base64_decode(base64_encode([104 as u8, 101 as u8, 108 as u8, 108 as u8, 111 as u8])).len();`},
}

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
