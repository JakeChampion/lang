package e2ecompiler

import "testing"

var f64ArrayAppendIRCases = []struct {
	name string
	main string
}{
	{"f64-append-len", `function main(): i32 { let a: f64[] = [1.0]; a = a.append(2.0); a = a.append(3.0); return a.len(); }`},
	{"f64-append-index", `function main(): i32 { let a: f64[] = [1.5]; a = a.append(2.5); return a[1] as i32; }`},
	{"f64-append-empty-sum", `function main(): i32 { let a: f64[] = []; a = a.append(1.5); a = a.append(2.5); let s = 0.0; for x in a { s = s + x; } return s as i32; }`},
	{"f64-append-grow-many", `function main(): i32 { let a: f64[] = []; let i = 0; while (i < 10) { a = a.append((i as f64) + 0.5); i = i + 1; } return a[7] as i32; }`},
	{"i64-append-regress", `function main(): i32 { let a: i64[] = [1]; a = a.append(2); return a[1] as i32; }`},
}

func TestSelfHostF64ArrayAppendIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range f64ArrayAppendIRCases {
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
