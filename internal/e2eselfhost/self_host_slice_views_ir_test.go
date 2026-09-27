package e2eselfhost

import "testing"

// sliceViewIRCases exercise slice views `[T]` — a borrowed window `a[i:j]` over
// an owned array — through the self-host IR path on x86-64 + wasm. A slice is a
// leak-only (fat-pointer) view: `.len()`, element indexing (`s[k]`), `for x in
// s` iteration, slice-of-slice (`s[a:b]`), empty windows, and `[string]`
// element slices all stay on the IR path.
//
// This pins the foundational "Slice views `[T]`" audit row (docs/FEATURE-AUDIT.md)
// on the self-hosted compiler. Each case is oracle-checked against the
// interpreter and returns a value <= 126 (wasmtime exit-code truncation, cf.
// #2908).
var sliceViewIRCases = []struct {
	name string
	main string
}{
	// a[1:3] of [10,20,30,40] -> [20,30], len 2.
	{"slice-len", `function main(): i32 { var a: i32[] = [10,20,30,40]; var s: [i32] = a[1:3]; return s.len(); }`},
	// s[0] of that window is 20.
	{"slice-index", `function main(): i32 { var a: i32[] = [10,20,30,40]; var s: [i32] = a[1:3]; return s[0]; }`},
	// Sum a window via `for x in s`: a[1:4] of [1..5] = [2,3,4] -> 9.
	{"slice-iter-sum", `function main(): i32 { var a: i32[] = [1,2,3,4,5]; var s: [i32] = a[1:4]; var t: i32 = 0; for x in s { t = t + x; } return t; }`},
	// A slice of a slice: a[1:5]=[2,3,4,5], then s[0:2]=[2,3] -> len 2.
	{"slice-of-slice", `function main(): i32 { var a: i32[] = [1,2,3,4,5]; var s: [i32] = a[1:5]; var s2: [i32] = s[0:2]; return s2.len(); }`},
	// A slice passed as a `[i32]` parameter and consumed by the callee.
	{"slice-as-param", `function sum(s: [i32]): i32 { var t: i32 = 0; for x in s { t = t + x; } return t; } function main(): i32 { var a: i32[] = [4,5,6,7]; return sum(a[1:3]); }`},
	// Index with a computed offset off the slice length: last element.
	{"slice-last", `function main(): i32 { var a: i32[] = [9,8,7]; var s: [i32] = a[0:3]; return s[s.len()-1]; }`},
	// An empty window a[2:2] has length 0.
	{"empty-slice", `function main(): i32 { var a: i32[] = [1,2,3]; var s: [i32] = a[2:2]; return s.len(); }`},
	// A `[string]` element slice: strs[0:2] = ["ab","cde"], s[1].len() = 3.
	{"string-elem-slice", `function main(): i32 { var a: string[] = ["ab","cde","f"]; var s: [string] = a[0:2]; return s[1].len(); }`},
}

// TestSelfHostSliceViewsIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostSliceViewsIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range sliceViewIRCases {
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
