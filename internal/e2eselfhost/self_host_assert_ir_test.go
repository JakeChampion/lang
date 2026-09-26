package e2eselfhost

import "testing"

// #4416: the `assert(cond)` / `assert(cond, msg)` builtin desugars in the
// self-host parser (mirroring the native parser) to
// `if (!cond) { eprint("assertion failed[: msg]"); exit(1); }`, so it lowers
// on the self-host IR path with no dedicated codegen — the constructs it uses
// (`!`, string `+`, `eprint`, `exit`, `if`) all already lower there. Each
// program is compiled by the self-hosted compiler and the resulting binary
// run: a passing assert falls through to the program's `return N` (exit N), a
// failing one aborts with `exit(1)`. Both are observable because the self-host
// CLI produces a real executable (x86-64) / a `wasmtime run` CLI module (wasm),
// unlike the native wasmbin `--invoke main` harness.
var assertIRCases = []struct {
	name string
	main string
	want int
}{
	// Two passing asserts, then a normal return → exit 7.
	{"pass", `function main(): i32 { assert(5 > 0, "pos"); assert(1 < 2); return 7; }`, 7},
	// A failing assert with a message aborts before the return → exit 1.
	{"fail-msg", `function main(): i32 { assert(0 > 1, "boom"); return 42; }`, 1},
	// A failing assert with no message → exit 1.
	{"fail-no-msg", `function main(): i32 { assert(1 > 2); return 42; }`, 1},
	// The condition is a runtime call, evaluated once; passes → exit 3.
	{"runtime-cond", `function pos(x: i32): boolean { return x > 0; }
function main(): i32 { assert(pos(9), "pos"); return 3; }`, 3},
}

// TestSelfHostAssertIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code.
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
