package e2ecompiler

import "testing"

var floatNanIRCases = []struct {
	name string
	main string
	want int
}{
	{"nan-ne-self", `let n: f64 = 0.0 / 0.0; if (n != n) { return 1; } return 0;`, 1},
	{"nan-eq-self", `let n: f64 = 0.0 / 0.0; if (n == n) { return 1; } return 0;`, 0},
	{"nan-lt", `let n: f64 = 0.0 / 0.0; if (n < 1.0) { return 1; } return 0;`, 0},
	{"nan-gt", `let n: f64 = 0.0 / 0.0; if (n > 1.0) { return 1; } return 0;`, 0},
	{"nan-ge", `let n: f64 = 0.0 / 0.0; if (n >= 1.0) { return 1; } return 0;`, 0},
	{"nan-le", `let n: f64 = 0.0 / 0.0; if (n <= 1.0) { return 1; } return 0;`, 0},
	{"nan-lt-negated", `let n: f64 = 0.0 / 0.0; if (n < 1.0) { return 0; } return 9;`, 9},
	{"ordered-eq", `let a: f64 = 1.5; if (a == a) { return 7; } return 0;`, 7},
	{"ordered-lt", `if (1.0 < 2.0) { return 5; } return 0;`, 5},
}

func floatNanIRSrc(mainBody string) string {
	return "function main(): i32 { " + mainBody + " }\n"
}

func TestSelfHostFloatNanIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range floatNanIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, floatNanIRSrc(tc.main), target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
