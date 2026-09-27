package e2eselfhost

import "testing"

// matchGuardIRCases pin match-arm GUARDS (`Pattern when <cond> => …`) to the
// self-host IR path on x86-64 + wasm. A guarded arm lowers through IR — irlower
// emits `lower_expr(guard)` + a not/br_if skip and propagates `.ok`, so the
// module stays IR-eligible — for both the enum-payload-variant arm and the
// literal-match arm. No other self-host test exercises a `when` guard at all;
// these cases check each exit code against the interp oracle, mirroring
// self_host_block_expr_ir_test.go.
//
// Scope notes (kept to the shapes that lower AND type-check natively, so the
// interp oracle agrees): exactly ONE guarded arm per match plus an unguarded
// fallback of the same pattern. A SECOND guarded arm on the same variant bails
// the module to AST; a bool-scrutinee guard needs an unguarded `_` (E030); and a
// guarded wildcard `_ when …` must be last so it can't precede a catch-all
// (E026) — all excluded. Every guard is a scalar i32/bool comparison and every
// result is <= 126 (wasmtime exit-code truncation, cf. #2908).
var matchGuardIRCases = []struct {
	name string
	main string
}{
	// Enum-payload variant-arm guard with same-name re-bind across the guarded
	// and unguarded arm (#2644 — the slot-allocation-prone shape). Has(7): n>5
	// -> 7; Has(2): else -> 102; Nil -> 0. 7 + 102 + 0 = 109.
	{"variant-rebind-guard", `enum Opt { Has(i32), Nil }
function pick(o: Opt): i32 { match (o) { Has(n) when n > 5 => { return n; }, Has(n) => { return n + 100; }, Nil => { return 0; } } }
function main(): i32 { var a = pick(Has(7)); var b = pick(Has(2)); var c = pick(Nil); return a + b + c; }`},
	// Enum-payload variant-arm guard with an equality predicate. Has(0): n==0
	// -> 7; Has(5): else -> 5; Nil -> 9. 7 + 5 + 9 = 21.
	{"variant-eq-guard", `enum Opt { Has(i32), Nil }
function pick(o: Opt): i32 { match (o) { Has(n) when n == 0 => { return 7; }, Has(n) => { return n; }, Nil => { return 9; } } }
function main(): i32 { var a = pick(Has(0)); var b = pick(Has(5)); var c = pick(Nil); return a + b + c; }`},
	// Literal-match arm guard on a bool flag: `0 when big`. f(0,true)=100,
	// f(0,false)=1, f(5,_)=9. 100 + 1 + 9 = 110.
	{"litmatch-guard", `function f(tag: i32, big: boolean): i32 { match (tag) { 0 when big => { return 100; }, 0 => { return 1; }, _ => { return 9; } } }
function main(): i32 { var a = f(0, true); var b = f(0, false); var c = f(5, false); return a + b + c; }`},
	// A second literal-match arm guard (different literal + flag): `1 when flag`.
	// f(1,true)=50, f(1,false)=5, f(2,_)=9. 50 + 5 + 9 = 64.
	{"litmatch-guard-flag", `function f(tag: i32, flag: boolean): i32 { match (tag) { 1 when flag => { return 50; }, 1 => { return 5; }, _ => { return 9; } } }
function main(): i32 { var a = f(1, true); var b = f(1, false); var c = f(2, true); return a + b + c; }`},
}

// TestSelfHostMatchGuardIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostMatchGuardIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range matchGuardIRCases {
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
