package e2ecompiler

import "testing"

// u64TupArrIRCases exercise a u64 element of an array-of-tuples (`(u64, i32)[]`)
// read via `xs[i].N` and chained in an unsigned op. The element read has to
// keep the tuple type's "u64", not coarsen it to a 64-bit width: read as i64,
// `xs[i].N >> k` / `xs[i].N > x` take a SIGNED shift / compare and diverge once
// bit 63 is set. An all-scalar tuple array needs no drop, so the read cannot
// lean on reclaim bookkeeping for the type. Sibling of the u64[][] case
// (#5206).
//
// Oracle-checked against the interpreter, values <= 120 (the wasmtime
// exit-code gap #2908). The wide element 18000000000000000000 has bit 63 set,
// so signed vs unsigned give DIFFERENT results.
var u64TupArrIRCases = []struct {
	name string
	main string
}{
	// xs[0].0 >> 58: unsigned = 0xF9CCD8A1C5080000 >> 58 = 62; signed (arith) = 254.
	{"index-field-shr", `function main(): i32 { let xs: (u64, i32)[] = [(18000000000000000000 as u64, 1)]; return (xs[0].0 >> 58) as i32; }`},
	// Same, but a BARE oversized literal typed only by the (u64, i32)[] annotation
	// (no `as u64`) — exercises the annotation-driven tuple-type recording.
	{"index-field-shr-bare", `function main(): i32 { let xs: (u64, i32)[] = [(18000000000000000000, 1)]; return (xs[0].0 >> 58) as i32; }`},
	// u64 as the SECOND tuple element: the sign must be recovered per-position.
	{"second-elem-shr", `function main(): i32 { let xs: (i32, u64)[] = [(1, 18000000000000000000 as u64)]; return (xs[0].1 >> 58) as i32; }`},
	// Unsigned compare: unsigned true (7); signed (negative) false (9).
	{"index-field-cmp", `function main(): i32 { let xs: (u64, i32)[] = [(18000000000000000000 as u64, 1)]; if (xs[0].0 > (100 as u64)) { return 7; } return 9; }`},
	// Bind the tuple out of the array first (`let t = xs[0]`), then read `t.0`: the
	// "u64" tag must propagate across the element-binding.
	{"bound-elem-shr", `function main(): i32 { let xs: (u64, i32)[] = [(18000000000000000000 as u64, 1)]; let t = xs[0]; return (t.0 >> 58) as i32; }`},
	// i64 tuple-array element width regression: the 8-byte read must stay full-width
	// (value fits, so signed/unsigned agree — this guards against truncation). 7.
	{"i64-elem-width-regress", `function main(): i32 { let xs: (i64, i32)[] = [(5000000007, 1)]; return (xs[0].0 % 1000) as i32; }`},
}

// TestSelfHostU64TupArrIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostU64TupArrIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range u64TupArrIRCases {
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
