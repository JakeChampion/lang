package e2ecompiler

import "testing"

const valueBlockReturnSrc = `function bare(c: boolean): i32 { let t: i32 = { return 5; }; return 100 + t; }
function led(c: boolean): i32 { let t: i32 = { let z: i32 = 0; return 5; }; return 100 + t; }
function nested(c: boolean): i32 { let t: i32 = { if (c) { return 5; } return 6; }; return 100 + t; }
function valued(c: boolean): i32 { let t: i32 = { let z: i32 = 2; z * 3 }; return t; }
function iife(c: boolean): i32 { let t: i32 = ((): i32 => { return 5; })(); return 10 + t; }
function main(): i32 { return bare(true) + led(true) + nested(true) + valued(true) + iife(true); }
`

func TestSelfHostValueBlockReturnLeavesFunction(t *testing.T) {
	const want = 36
	if got := interpExit(t, buildLangBinForInterp(t), valueBlockReturnSrc); got != want {
		t.Fatalf("interpreter exit %d, want %d", got, want)
	}
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, exit := cli.exitOf(t, valueBlockReturnSrc, target); exit != want {
				t.Errorf("exit = %d, want %d\n%s", exit, want, stderr)
			}
		})
	}
}
