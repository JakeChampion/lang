package e2ecompiler

import "testing"

var assertIRCases = []struct {
	name string
	main string
	want int
}{
	{"pass", `function main(): i32 { assert(5 > 0, "pos"); assert(1 < 2); return 7; }`, 7},
	{"fail-msg", `function main(): i32 { assert(0 > 1, "boom"); return 42; }`, 1},
	{"fail-no-msg", `function main(): i32 { assert(1 > 2); return 42; }`, 1},
	{"runtime-cond", `function pos(x: i32): boolean { return x > 0; }
function main(): i32 { assert(pos(9), "pos"); return 3; }`, 3},
}

func TestSelfHostAssertIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range assertIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.main+"\n", target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
