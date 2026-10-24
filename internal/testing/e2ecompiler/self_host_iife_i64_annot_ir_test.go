package e2ecompiler

import "testing"

var iifeI64AnnotIRCases = []struct {
	name string
	main string
}{
	{"ifexpr-i64-runtime", `function main(): i32 { let n = 5; let x: i64 = if (n > 3) { 5 } else { 9 }; return x as i32; }`},
	{"u64-ifexpr-runtime", `function main(): i32 { let n = 5; let x: u64 = if (n > 3) { 5 } else { 9 }; return x as i32; }`},
	{"matchexpr-i64", `function main(): i32 { let n = 1; let x: i64 = match (n) { 1 => 5, _ => 9 }; return x as i32; }`},
	{"matchexpr-i64-wild", `function main(): i32 { let n = 2; let x: i64 = match (n) { 1 => 7, 2 => 5, _ => 9 }; return x as i32; }`},
	{"ifexpr-i64-arith", `function main(): i32 { let n = 5; let x: i64 = if (n > 3) { 5 } else { 9 }; let y: i64 = x + 1; return y as i32; }`},
	{"nested-elseif-i64", `function main(): i32 { let n = 1; let x: i64 = if (n > 5) { 1 } else if (n > 0) { 7 } else { 9 }; return x as i32; }`},
	{"ifexpr-i64-bigbranch", `function main(): i32 { let n = 5; let x: i64 = if (n > 3) { 5000000000 } else { 1 }; return (x % 7) as i32; }`},

	{"matchexpr-i64-binary-arm", `function main(): i32 { let x: i64 = match ((604i64) /? (478i64)) { Some(w) => w, None => (3i64 * 5i64) }; return x as i32; }`},
	{"matchexpr-i64-binary-arm-taken", `function main(): i32 { let x: i64 = match ((1i64) /? (0i64)) { Some(w) => w, None => (5000000000i64 / 1000000000i64) }; return x as i32; }`},
	{"matchexpr-i64-shift-arm", `function main(): i32 { let x: i64 = match ((1i64) /? (0i64)) { Some(w) => w, None => (5000000000i64 >> 30i64) }; return x as i32; }`},
	{"matchexpr-cast-arm-narrows", `function main(): i32 { return (match ((1i64) /? (0i64)) { Some(w) => (w as i32), None => (((5000000000i64 >> 30i64) as i32) & 7i32) } & 255i32); }`},
	{"ifexpr-bool-not-arm", `function main(): i32 { let n = 5; let b: boolean = if (n > 3) { !(n > 100) } else { false }; let x: i32 = 0; if (b) { x = 5; } return x; }`},
}

func TestSelfHostIifeI64AnnotIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range iifeI64AnnotIRCases {
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
