package e2ecompiler

import "testing"

var nestedTupleRetIRCases = []struct {
	name string
	main string
}{
	{"ret-right-nest", "function f(): (i32, (i32, i32)) { return (1, (2, 3)); }\nfunction main(): i32 { let t = f(); return t.1.1; }"},
	{"ret-sum-across", "function f(): (i32, (i32, i32)) { return (1, (2, 3)); }\nfunction main(): i32 { let t = f(); return t.0 + t.1.0 + t.1.1; }"},
	{"ret-left-nest-str", "function f(): ((i32, i32), string) { return ((4, 5), \"ab\"); }\nfunction main(): i32 { let t = f(); return t.0.0 + t.0.1 + t.1.len(); }"},
	{"param-nested", "function f(t: (i32, (i32, i32))): i32 { return t.0 + t.1.1; }\nfunction main(): i32 { return f((1, (2, 3))); }"},
	{"ret-flat", "function f(): (i32, i32) { return (3, 4); }\nfunction main(): i32 { let t = f(); return t.0 + t.1; }"},
}

func TestSelfHostNestedTupleRetIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range nestedTupleRetIRCases {
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
