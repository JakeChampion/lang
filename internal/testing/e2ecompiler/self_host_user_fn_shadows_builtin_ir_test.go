package e2ecompiler

import "testing"

type userFnShadowCase struct {
	name string
	src  string
}

var userFnShadowCases = []userFnShadowCase{
	{"len-enum", `
enum L { C(i32, L), N }
function len(l: L): i32 {
    match (l) { C(h, t) => { return 1 + len(t); }, N => { return 0; } }
}
function main(): i32 {
    let l: L = C(1, C(2, C(3, N)));
    return len(l);   // 3
}`},
	{"string-method-repeat", `
function (s: string) repeat(n: i32): i32 { return n * 2; }
function main(): i32 { return "x".repeat(21); }   // 42`},
	{"map-new-i32", `
function map_new_i32(n: i32): i32 { return n * 2; }
function main(): i32 { return map_new_i32(21); }   // 42`},
	{"string-method-trim", `
function (s: string) trim(n: i32): i32 { return n * 2; }
function main(): i32 { return "x".trim(21); }   // 42`},
	{"string-method-trim-same-arity", `
function (s: string) trim(): string { return "user"; }
function main(): i32 { return " x ".trim().len(); }   // 4`},
}

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
