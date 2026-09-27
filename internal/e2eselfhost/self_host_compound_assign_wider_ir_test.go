package e2eselfhost

import "testing"

// compoundAssignWiderIRCases exercise compound assignment (`+= -= *= /= %=`)
// beyond the i32 path through the self-host IR path on x86-64 + wasm. A compound
// assignment is a read-modify-write: the lowering must load the local, apply the
// op, and store back with width-correct semantics — i64 / u32 / u8 (wrap) / f64
// in addition to the full i32 operator set, plus loop accumulation via `+=`.
//
// This pins the wider-type half of the "Compound assignment += -= *= …" audit
// row (docs/FEATURE-AUDIT.md); the i32 path is covered elsewhere. Each case
// is oracle-checked against the interpreter and returns a value <= 120
// (wasmtime exit-code truncation, cf. #2908).
var compoundAssignWiderIRCases = []struct {
	name string
	main string
}{
	// i64 `+=` / `*=`.
	{"i64-plus-eq", `function main(): i32 { var n = 5 as i64; n += 3 as i64; return n as i32; }`},
	{"i64-mul-eq", `function main(): i32 { var n = 4 as i64; n *= 3 as i64; return n as i32; }`},
	// u32 `+=` -> 120 (<= wasm clamp).
	{"u32-plus-eq", `function main(): i32 { var n = 100 as u32; n += 20 as u32; return n as i32; }`},
	// u8 `+=` wraps mod 256: 250 + 10 -> 4.
	{"u8-wrap-eq", `function main(): i32 { var n = 250 as u8; n += 10 as u8; return n as i32; }`},
	// f64 `+=` / `*=`.
	{"f64-plus-eq", `function main(): i32 { var x = 2.5; x += 1.5; return x as i32; }`},
	{"f64-mul-eq", `function main(): i32 { var x = 3.0; x *= 4.0; return x as i32; }`},
	// The rest of the i32 operator set: `-=` / `/=` / `%=`.
	{"i32-minus-eq", `function main(): i32 { var n = 20; n -= 5; return n; }`},
	{"i32-div-eq", `function main(): i32 { var n = 20; n /= 4; return n; }`},
	{"i32-mod-eq", `function main(): i32 { var n = 23; n %= 5; return n; }`},
	// Loop accumulation into an i64 via `+=`: 0+1+2+3+4 = 10.
	{"loop-accum-i64", `function main(): i32 { var s = 0 as i64; var i = 0; while (i < 5) { s += i as i64; i = i + 1; } return s as i32; }`},
}

// TestSelfHostCompoundAssignWiderIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostCompoundAssignWiderIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range compoundAssignWiderIRCases {
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
