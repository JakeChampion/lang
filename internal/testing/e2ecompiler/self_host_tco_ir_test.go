package e2ecompiler

import "testing"

var tcoIRCases = []struct {
	name string
	src  string
	want int
}{
	{
		"deep-tail-accumulator",
		"function sum_to(n: i32, acc: i32): i32 { if (n == 0) { return acc; } return sum_to(n - 1, acc + n); } " +
			"function main(): i32 { return sum_to(1000000, 0) % 100; }",
		64,
	},
	{
		"deep-tail-in-else",
		"function f(n: i32, acc: i32): i32 { if (n == 0) { return acc; } else { return f(n - 1, acc + 1); } } " +
			"function main(): i32 { return f(500003, 0) % 100; }",
		3,
	},
	{"non-tail-factorial", "function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } function main(): i32 { return fact(5); }", 120},
	{"shallow-tail", "function count(n: i32, acc: i32): i32 { if (n == 0) { return acc; } return count(n - 1, acc + 2); } function main(): i32 { return count(20, 0); }", 40},
}

func TestSelfHostTcoIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		for _, tc := range tcoIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.src, target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
