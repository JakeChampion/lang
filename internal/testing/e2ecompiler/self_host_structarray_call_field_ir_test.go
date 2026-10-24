package e2ecompiler

import "testing"

var structArrayCallFieldIRCases = []struct {
	name string
	main string
}{
	{"call-idx-field-sum", `struct P { x: i32, y: i32 } function mk(): P[] { return [P { x: 1, y: 2 }, P { x: 3, y: 4 }]; } function main(): i32 { return mk()[1].x + mk()[0].y; }`},
	{"call-idx-field-x", `struct P { x: i32, y: i32 } function mk(): P[] { return [P { x: 1, y: 2 }, P { x: 3, y: 4 }]; } function main(): i32 { return mk()[0].x; }`},
	{"call-idx-field-y", `struct P { x: i32, y: i32 } function mk(): P[] { return [P { x: 1, y: 2 }, P { x: 3, y: 4 }]; } function main(): i32 { return mk()[1].y; }`},
	{"bind-first", `struct P { x: i32, y: i32 } function mk(): P[] { return [P { x: 1, y: 2 }, P { x: 3, y: 4 }]; } function main(): i32 { let a: P[] = mk(); return a[1].x + a[0].y; }`},
	{"index-only", `struct P { x: i32, y: i32 } function mk(): P[] { return [P { x: 1, y: 2 }, P { x: 3, y: 4 }]; } function main(): i32 { let p = mk()[1]; return p.x; }`},
}

func TestSelfHostStructArrayCallFieldIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range structArrayCallFieldIRCases {
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
