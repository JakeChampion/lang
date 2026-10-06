package e2ecompiler

import "testing"

// #3710, #10344: a user-defined free function whose name collides with a
// builtin must be called, not lowered as the builtin's op. Each case returns a
// value the builtin form could never produce. Oracle-checked against the
// interpreter.
type userFnShadowCase struct {
	name string
	src  string
}

var userFnShadowCases = []userFnShadowCase{
	// #3710's repro: a user `len` over an enum linked list. `len` is no
	// builtin contract, so the typed path lowers it as a plain call.
	{"len-enum", `
enum L { C(i32, L), N }
function len(l: L): i32 {
    match (l) { C(h, t) => { return 1 + len(t); }, N => { return 0; } }
}
function main(): i32 {
    let l: L = C(1, C(2, C(3, N)));
    return len(l);   // 3
}`},
	// #10344: `string.repeat` is a builtin contract, so the call is only a plain
	// call because the module declares it (ssasem.Func.shadows).
	{"string-method-repeat", `
function (s: string) repeat(n: i32): i32 { return n * 2; }
function main(): i32 { return "x".repeat(21); }   // 42`},
	// #10364: native has no `map_new_i32`, so the name is the user's. The
	// self-host's map-literal desugar spells its constructor `__map_new_i32`.
	{"map-new-i32", `
function map_new_i32(n: i32): i32 { return n * 2; }
function main(): i32 { return map_new_i32(21); }   // 42`},
	// #10363: a method on string named like a runtime helper, with std/string
	// not loaded, is the user's own; native calls it.
	{"string-method-trim", `
function (s: string) trim(n: i32): i32 { return n * 2; }
function main(): i32 { return "x".trim(21); }   // 42`},
	// The same, at the helper's own arity.
	{"string-method-trim-same-arity", `
function (s: string) trim(): string { return "user"; }
function main(): i32 { return " x ".trim().len(); }   // 4`},
}

// TestSelfHostUserFnShadowsBuiltinIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostUserFnShadowsBuiltinIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range userFnShadowCases {
		src := tc.src
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
