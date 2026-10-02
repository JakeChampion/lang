package e2eselfhost

import "testing"

// A call through a local function value named like a builtin types as the
// local's result. The pre-codegen gate (asmcore) read the builtin table first,
// typed `rename(x)` as the fs builtin's Option, and refused the program with an
// E003 the checker never raised (#10923).
func TestSelfHostLocalShadowsBuiltin(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		expected int
	}{
		{"param", `function apply(x: i32, rename: (i32) => string): string {
    let n: string = "";
    n = rename(x);
    return n;
}
function main(): i32 { return apply(3, (v: i32): string => "abc").len(); }`, 3},
		{"local_lambda", `function main(): i32 {
    let rename = (v: i32): string => "ab";
    let n: string = rename(1);
    return n.len();
}`, 2},
	}
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		for _, tc := range cases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.src, target, "FERN_STRICT_IR=1"); code != tc.expected {
					t.Errorf("exited %d, want %d\n%s", code, tc.expected, stderr)
				}
			})
		}
	}
}
