package e2ecompiler

import "testing"

var matchGuardIRCases = []struct {
	name string
	main string
}{
	{"variant-rebind-guard", `enum Opt { Has(i32), Nil }
function pick(o: Opt): i32 { match (o) { Has(n) when n > 5 => { return n; }, Has(n) => { return n + 100; }, Nil => { return 0; } } }
function main(): i32 { let a = pick(Has(7)); let b = pick(Has(2)); let c = pick(Nil); return a + b + c; }`},
	{"variant-eq-guard", `enum Opt { Has(i32), Nil }
function pick(o: Opt): i32 { match (o) { Has(n) when n == 0 => { return 7; }, Has(n) => { return n; }, Nil => { return 9; } } }
function main(): i32 { let a = pick(Has(0)); let b = pick(Has(5)); let c = pick(Nil); return a + b + c; }`},
	{"litmatch-guard", `function f(tag: i32, big: boolean): i32 { match (tag) { 0 when big => { return 100; }, 0 => { return 1; }, _ => { return 9; } } }
function main(): i32 { let a = f(0, true); let b = f(0, false); let c = f(5, false); return a + b + c; }`},
	{"litmatch-guard-flag", `function f(tag: i32, flag: boolean): i32 { match (tag) { 1 when flag => { return 50; }, 1 => { return 5; }, _ => { return 9; } } }
function main(): i32 { let a = f(1, true); let b = f(1, false); let c = f(2, true); return a + b + c; }`},
}

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
