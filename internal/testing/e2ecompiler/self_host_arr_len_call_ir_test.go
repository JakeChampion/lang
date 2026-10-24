package e2ecompiler

import "testing"

var arrLenCallIRCases = []struct {
	name string
	main string
}{
	{"struct-arr-len", `struct S { x: i32 }
function mkS(): S[] { return [S { x: 1 }, S { x: 2 }, S { x: 3 }]; }
function main(): i32 { return mkS().len(); }`},
	{"struct-arr-len-twice", `struct S { x: i32 }
function mkS(): S[] { return [S { x: 1 }, S { x: 2 }, S { x: 3 }, S { x: 4 }]; }
function main(): i32 { return mkS().len() * 10 + mkS().len(); }`},
	{"named-struct-arr-len", `struct Pair { a: i32, b: i32 }
function mk(): Pair[] { return [Pair { a: 1, b: 2 }, Pair { a: 3, b: 4 }]; }
function main(): i32 { return mk().len() + 40; }`},
	{"struct-value-user-len", `struct Box { v: i32 }
function (b: Box) len(): i32 { return b.v + 100; }
function mk(): Box { return Box { v: 5 }; }
function main(): i32 { let b: Box = mk(); return b.len(); }`},
}

func TestSelfHostArrLenCallIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range arrLenCallIRCases {
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
