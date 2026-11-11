package e2ecompiler

import "testing"

// Each case is a program that imports one standard library module and returns
// a value from main. FERN_STRICT_IR=1 turns a function the IR lowering refuses
// into a compile error that names the bail site, so a routed program is one
// the CLI compiles, and the interpreter is the oracle for its exit code.
type stdlibIRCase struct {
	name string
	main string
}

var cryptoIRCases = []stdlibIRCase{
	{"sha256-len", `return crypto.sha256("hello".as_bytes()).len();`},
	{"sha256-abc-d0", `return crypto.sha256("abc".as_bytes())[0] as i32 % 100;`},
	{"sha256-empty-d0", `return crypto.sha256("".as_bytes())[0] as i32 % 100;`},
	{"hmac-len", `return crypto.hmac_sha256([107 as u8, 101 as u8, 121 as u8], "msg".as_bytes()).len();`},
}

var pathIRCases = []stdlibIRCase{
	{"join-len", `return path.join(["usr", "local", "bin"]).len();`},
	{"ext-len", `return path.extension("a.b.c.txt").len();`},
	{"filename-len", `return path.file_name("/a/b/c.txt").len();`},
	{"parent-len", `return path.parent("/a/b/c.txt").len();`},
}

var mathIRCases = []stdlibIRCase{
	{"pack-rgb-mid", `return (math.pack_rgb(1, 2, 3) >> 8) & 255;`},
	{"pack-rgb-low", `return math.pack_rgb(255, 128, 64) & 255;`},
	{"tau", `return (math.TAU * 10.0) as i32;`},
}

func stdlibIRProgram(mod, mainBody string) string {
	return "import \"std/" + mod + "\";\nfunction main(): i32 { " + mainBody + " }\n"
}

func runStdlibIR(t *testing.T, target, mod string, cases []stdlibIRCase) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := stdlibIRProgram(mod, tc.main)
			want := interpExit(t, interpBin, src)
			stderr, got := cli.exitOf(t, src, target, "FERN_STRICT_IR=1")
			if got != want {
				t.Errorf("std/%s %s on %s exited %d, want %d (interp oracle)\n%s", mod, tc.name, target, got, want, stderr)
			}
		})
	}
}

func TestSelfHostCryptoIRX86_64(t *testing.T) {
	runStdlibIR(t, "x86-64-linux", "crypto", cryptoIRCases)
}
func TestSelfHostCryptoIRWasm(t *testing.T) { runStdlibIR(t, "wasm32-wasi", "crypto", cryptoIRCases) }
func TestSelfHostPathIRX86_64(t *testing.T) { runStdlibIR(t, "x86-64-linux", "path", pathIRCases) }
func TestSelfHostPathIRWasm(t *testing.T)   { runStdlibIR(t, "wasm32-wasi", "path", pathIRCases) }
func TestSelfHostMathIRX86_64(t *testing.T) { runStdlibIR(t, "x86-64-linux", "math", mathIRCases) }
func TestSelfHostMathIRWasm(t *testing.T)   { runStdlibIR(t, "wasm32-wasi", "math", mathIRCases) }
