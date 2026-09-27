package e2eselfhost

import (
	"testing"
)

// f32IRCases exercise f32 scalar params / returns / locals through the self-host
// IR path on x86-64 + wasm. Fern represents f32 as an f64 internally (f32<->f64
// casts are no-ops and every float op runs at double width), so an f32 value is
// an 8-byte IEEE double at runtime. The bug these guard against: lower_func and
// f64_ret_fns_of only recognised the literal type name "f64", so an f32 param /
// return / local slipped through as a plain i32 slot — its 8-byte float bit
// pattern was then passed/returned/cast through the 4-byte integer path and
// miscompiled (e.g. `id32(5.5 as f32)` returned 0 instead of 5). The fix routes
// "f32"/"float" through the same 8-byte-float slot marking as "f64"
// (is_f64_scalar_type_name).
//
// These are the std/float f32-method shapes (abs/sqrt/floor/round via
// `__*_f64(x as f64) as f32`), written as free functions.
//
// Each case casts its f32 result to i32 and returns a non-negative value kept
// <= 126 (the wasmtime exit-code truncation gap, cf. #2908), oracle-checked
// against the interpreter. FEATURE-AUDIT std/float row.
var f32IRCases = []struct {
	name string
	main string
}{
	// Pass-through param + return: the core "f32 slipped through as i32" bug.
	{"id", `function id32(x: f32): f32 { return x; }
function main(): i32 { var a: f32 = 5.5 as f32; return id32(a) as i32; }`},
	// f32 arithmetic across a call boundary.
	{"add", `function add32(a: f32, b: f32): f32 { return a + b; }
function main(): i32 { return add32(2.5 as f32, 3.0 as f32) as i32; }`},
	{"mul", `function mul32(a: f32, b: f32): f32 { return a * b; }
function main(): i32 { return mul32(2.0 as f32, 3.5 as f32) as i32; }`},
	// std/float f32-method shapes as free functions: __*_f64(x as f64) as f32.
	{"abs", `function fabs32(x: f32): f32 { return __abs_f64(x as f64) as f32; }
function main(): i32 { var a: f32 = 0.0 - 5.5 as f32; return fabs32(a) as i32; }`},
	{"sqrt", `function fsqrt32(x: f32): f32 { return __sqrt_f64(x as f64) as f32; }
function main(): i32 { return fsqrt32(16.0 as f32) as i32; }`},
	{"floor", `function ffloor32(x: f32): f32 { return __floor_f64(x as f64) as f32; }
function main(): i32 { return ffloor32(7.8 as f32) as i32; }`},
	{"round", `function fround32(x: f32): f32 { return __round_f64(x as f64) as f32; }
function main(): i32 { return fround32(2.5 as f32) as i32; }`},
	// f32 local round-trip feeding an intrinsic.
	{"via-local", `function main(): i32 { var y: f32 = 9.99 as f32; var f: f32 = __floor_f64(y as f64) as f32; return f as i32; }`},
	// f32 ARRAY: element load round-trips an 8-byte (f64-backed) f32 slot.
	{"array", `function main(): i32 { var a: f32[] = [1.0 as f32, 2.0 as f32, 3.0 as f32]; return a[1] as i32; }`},
	// f32 array summed in a loop — exercises repeated 8-byte element load + f32 add.
	{"array-sum", `function main(): i32 {
    var a: f32[] = [1.5 as f32, 2.5 as f32, 3.0 as f32];
    var s: f32 = 0.0 as f32; var i: i32 = 0;
    while (i < a.len()) { s = s + a[i]; i = i + 1; }
    return s as i32;
}`},
	// f32 STRUCT FIELDS: two f32 fields read back and added (8-byte field slots).
	{"struct-field", `struct P { x: f32, y: f32 }
function main(): i32 { var p: P = P { x: 3.0 as f32, y: 4.0 as f32 }; return (p.x + p.y) as i32; }`},
	// f32 in a TUPLE alongside an i32 — mixed-width tuple element layout.
	{"tuple", `function main(): i32 { var t: (f32, i32) = (3.5 as f32, 2); return (t.0 as i32) + t.1; }`},
	// f32 passed THROUGH a struct field into a call and back.
	{"struct-field-call", `struct P { v: f32 }
function dbl(p: P): f32 { return p.v + p.v; }
function main(): i32 { var p: P = P { v: 5.5 as f32 }; return dbl(p) as i32; }`},
	// u64 STRUCT FIELD with bits above 2^32: guards the SAME struct_make 8-byte
	// field store the f32 fix touches — before it, a u64 field stored via the
	// 4-byte i32.store path, truncating the high word, so the read-back != the
	// literal. 4294967297 == 0x1_0000_0001; a low-word-only store reads back 1.
	{"u64-struct-field", `struct B { v: u64 }
function main(): i32 { var b: B = B { v: 4294967297 as u64 }; if (b.v == 4294967297 as u64) { return 7; } return 0; }`},
}

// TestSelfHostF32IR runs each case through the self-host CLI on x86-64, arm64
// and wasm, oracle-checked against the interpreter.
func TestSelfHostF32IR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range f32IRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.main + "\n"
			want := interpExit(t, interpBin, src)
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != want {
					t.Errorf("%s on %s exited %d, want %d (interp oracle)\n%s", tc.name, target, code, want, stderr)
				}
			}
		})
	}
}
