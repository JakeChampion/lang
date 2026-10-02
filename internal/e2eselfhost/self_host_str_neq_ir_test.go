package e2eselfhost

import "testing"

// strNeqIRCases pin string inequality (`a != b`) used as an `if` condition to the
// self-host IR path on x86-64 + wasm. String `==` is already pinned via the
// literal-match desugar (self_host_match_literal_ir_test.go — a `"a" => …` arm
// lowers to an `==` if-chain); `!=` is the negation arm of the same string-compare
// lowering, so this pins an existing-but-untested code path rather than asking for
// new compiler work. The eq-ne-mix case additionally guards that `==` and `!=`
// coexist in one module without bailing.
//
// Each case is oracle-checked against the interpreter; every result is <= 126
// (wasmtime exit-code truncation, cf. #2908). Mirrors
// self_host_nested_tuple_ir_test.go.
var strNeqIRCases = []struct {
	name string
	main string
}{
	// Inequality holds -> take the branch.
	{"ne-true", `function main(): i32 { let a: string = "foo"; if (a != "bar") { return 9; } return 0; }`},
	// Inequality is false (equal strings) -> fall through.
	{"ne-false", `function main(): i32 { let a: string = "foo"; if (a != "foo") { return 1; } return 5; }`},
	// `!=` between two string variables.
	{"ne-var", `function main(): i32 { let a: string = "abc"; let b: string = "abd"; if (a != b) { return 7; } return 0; }`},
	// `==` and `!=` coexisting in one module.
	{"eq-ne-mix", `function main(): i32 { let a: string = "hi"; let n = 0; if (a == "hi") { n = n + 3; } if (a != "bye") { n = n + 4; } return n; }`},
}

// TestSelfHostStrNeqIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostStrNeqIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range strNeqIRCases {
		src := tc.main + "\n"
		want := interpExit(t, interpBin, src)
		for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, src, target); code != want {
					t.Errorf("exited %d, want %d (interp oracle)\n%s", code, want, stderr)
				}
			})
		}
	}
}
