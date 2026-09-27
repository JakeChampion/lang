package e2eselfhost

import "testing"

// f64RecvIRCases exercise scalar f64 VALUE-receiver methods (`(x: f64) m(): f64`,
// called `a.m()`) through the self-host IR path on x86-64 + wasm.
//
// The bug: expr_is_f64 classified a method call `recv.m()` as f64 only when the
// receiver was a STRUCT (expr_struct_type), so a scalar f64 value receiver fell
// through as "not f64". A following `a.m() as i32` then took the integer cast
// path and masked the DOUBLE's low 32 bits (→ 0) instead of truncating via
// f64_to_i32. The fix resolves a scalar receiver's primitive type
// (expr_recv_prim_type) as a fallback so an f64-returning method `<f64>.m` is
// recognised as an f64 value. (i64 value-receiver methods are unaffected — i32
// truncation of an i64 result happens to equal the low-32-bit mask the integer
// path already used; their separate wasm legacy-AST gap is out of scope.)
//
// Each case casts its f64 result to i32 and returns a non-negative value kept
// <= 126 (the wasmtime exit-code truncation gap, cf. #2908), oracle-checked
// against the interpreter.
var f64RecvIRCases = []struct {
	name string
	main string
}{
	// Receiver in f64 arithmetic: 3.5 + 3.5 = 7.
	{"arith", `function (x: f64) dbl(): f64 { return x + x; }
function main(): i32 { var a: f64 = 3.5; return a.dbl() as i32; }`},
	// Identity receiver: the value flows straight back out as f64.
	{"id", `function (x: f64) id(): f64 { return x; }
function main(): i32 { var a: f64 = 5.5; return a.id() as i32; }`},
	// Division in the method body: 9.0 / 2.0 = 4.5 -> 4.
	{"div", `function (x: f64) half(): f64 { return x / 2.0; }
function main(): i32 { var a: f64 = 9.0; return a.half() as i32; }`},
	// Method result feeding further f64 arithmetic: (4+1) + 2 = 7.
	{"chain", `function (x: f64) inc(): f64 { return x + 1.0; }
function main(): i32 { var a: f64 = 4.0; var r: f64 = a.inc() + 2.0; return r as i32; }`},
	// Method body calls an f64 math intrinsic: sqrt(16) = 4.
	{"intrinsic", `function (x: f64) sq(): f64 { return __sqrt_f64(x); }
function main(): i32 { var a: f64 = 16.0; return a.sq() as i32; }`},
}

// TestSelfHostF64RecvIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostF64RecvIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range f64RecvIRCases {
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
