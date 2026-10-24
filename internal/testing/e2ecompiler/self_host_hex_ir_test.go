package e2ecompiler

import "testing"

var hexIRCases = []struct {
	name string
	main string
}{
	{"encode-len", `return hex.encode([104 as u8, 105 as u8]).len();`},
	{"encode-digit", `return hex.encode([65 as u8])[0] as i32;`},
	{"decode-len", `let Ok(bytes) = hex.decode("6869") else { return 127; }; return bytes.len();`},
	{"decode-char", `let Ok(bytes) = hex.decode("7a") else { return 127; }; return bytes[0] as i32;`},
	{"roundtrip", `let Ok(bytes) = hex.decode(hex.encode([72 as u8, 105 as u8])) else { return 127; }; return bytes[0] as i32;`},
	{"roundtrip-len", `let Ok(bytes) = hex.decode(hex.encode([104 as u8, 101 as u8, 108 as u8, 108 as u8, 111 as u8])) else { return 127; }; return bytes.len();`},
}

func hexSource(t *testing.T, mainBody string) []byte {
	t.Helper()
	return []byte("import \"std/hex\";\nfunction main(): i32 { " + mainBody + " }\n")
}

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
