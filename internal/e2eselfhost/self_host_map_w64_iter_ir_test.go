package e2eselfhost

import (
	"testing"
)

// mapW64IterIRCases cover map ITERATION over 64-bit value columns (#5253):
// both `for (k, v) in m` and the single-var `for v in m.values()` form bound
// the value with a 32-bit element read (op_arr_get(32)) and no width/sign/f64
// mark on the binding — so an i64/u64/f64-valued map iterated to truncated
// (and, for u64, signed) garbage ON the IR path (a silent wrong answer, not
// a bail: measured 113/199/146/255 against interp oracles 62/7/18/5
// on a pre-fix driver). The fix reads the full 8-byte element (arr_get_i64
// for i64/u64; arr_get width 64 — f64.load — for f64) and marks the binding
// i64/u64/f64, so body uses route through the 64-bit/float ops with the right
// sign. String/i32 columns keep the 4-byte/pointer path — pinned by the
// regression cases.
//
// Run through the CLI on x86-64 and wasm, oracle-checked against the interpreter.
var mapW64IterIRCases = []struct {
	name string
	main string
}{
	// u64 2-var iteration, shift in the body: 62 (was 113).
	{"forin-u64-shr", `import "core/map";
function main(): i32 { let m: Map[i32, u64] = Map { 1: 18000000000000000000 as u64 }; let acc: i32 = 0; for (k, v) in m { acc = acc + ((v >> 58) as i32); } return acc; }`},
	// i64 2-var iteration, wide values summed: 18 (was 146).
	{"forin-i64-sum", `import "core/map";
function main(): i32 { let m: Map[i32, i64] = Map { 1: 5000000007, 2: 6000000011 }; let acc: i64 = 0; for (k, v) in m { acc = acc + (v % 1000); } return acc as i32; }`},
	// i64 single-var values() iteration: 7 (was 199).
	{"forin-values-i64", `import "core/map";
function main(): i32 { let m: Map[i32, i64] = Map { 1: 5000000007 }; let acc: i32 = 0; for v in m.values() { acc = acc + ((v % 1000) as i32); } return acc; }`},
	// f64 2-let iteration: 5 (was 255).
	{"forin-f64", `import "core/map";
function main(): i32 { let m: Map[i32, f64] = Map { 1: 2.5 }; let acc: f64 = 0.0; for (k, v) in m { acc = acc + v; } return (acc * 2.0) as i32; }`},
	// String-valued 2-var regression (pointer column path unchanged): 10.
	{"forin-str-regress", `import "core/map";
function main(): i32 { let m: Map[i32, string] = Map { 1: "hello", 2: "xy" }; let acc: i32 = 0; for (k, v) in m { acc = acc + v.len() + k; } return acc; }`},
	// i32-valued 2-var + single-var keys() regression (snapshot column): 36.
	{"forin-i32-regress", `import "core/map";
function main(): i32 { let m: Map[i32, i32] = Map { 1: 10, 2: 20 }; let acc: i32 = 0; for (k, v) in m { acc = acc + v + k; } for k2 in m.keys() { acc = acc + k2; } return acc; }`},
}

func TestSelfHostMapW64IterIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range mapW64IterIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				src := []byte(tc.main + "\n")
				want := interpExit(t, interpBin, string(src))
				if stderr, code := cli.exitOf(t, string(src), target); code != want {
					t.Errorf("%s exited %d, want %d (interp oracle)\n%s", tc.name, code, want, stderr)
				}
			})
		}
	}
}
