package e2eselfhost

import "testing"

// tcoIRCases pin tail-call optimisation on the self-host IR path (#4328). A
// self-recursive call in tail position (`return f(args)`) must reuse the
// current activation via a loop, so recursion that would blow an 8 MB stack
// (~1M frames) completes in O(1) stack instead of SIGSEGV-ing. Non-tail
// recursion (factorial) is a control: it must NOT be rewritten and must still
// give the right answer. Expectations are the native-compiler results (native
// has TCO, so it is the oracle for the deep cases).
var tcoIRCases = []struct {
	name string
	src  string
	want int
}{
	// Deep self-tail recursion (~1M frames). Without TCO the emitted binary
	// SIGSEGVs (exit 139); with it, it loops. 1000000*1000001/2 mod 100 = 64.
	{
		"deep-tail-accumulator",
		"function sum_to(n: i32, acc: i32): i32 { if (n == 0) { return acc; } return sum_to(n - 1, acc + n); } " +
			"function main(): i32 { return sum_to(1000000, 0) % 100; }",
		64,
	},
	// Tail call nested inside an else (depth-2 branch to the wrapper loop).
	{
		"deep-tail-in-else",
		"function f(n: i32, acc: i32): i32 { if (n == 0) { return acc; } else { return f(n - 1, acc + 1); } } " +
			"function main(): i32 { return f(500003, 0) % 100; }",
		3,
	},
	// Non-tail recursion must be left alone (n * fact(n-1) is not a tail call).
	{"non-tail-factorial", "function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } function main(): i32 { return fact(5); }", 120},
	// A shallow tail call still returns the right value (TCO fires but the
	// answer is unchanged).
	{"shallow-tail", "function count(n: i32, acc: i32): i32 { if (n == 0) { return acc; } return count(n - 1, acc + 2); } function main(): i32 { return count(20, 0); }", 40},
}

// TestSelfHostTcoIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code.
func TestSelfHostTcoIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range tcoIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.src, target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
