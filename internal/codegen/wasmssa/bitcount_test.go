package wasmssa_test

import (
	"testing"
)

// The bit-count intrinsics end-to-end through the SSA → wasm pipeline. wasm has
// all three as single opcodes, per width; the i64 forms return an i64, so the
// emitter wraps back down to the i32 count the SSA contract promises. A missing
// wrap would leave an i64 on the stack where the function's i32 result belongs,
// which wasmtime rejects at validation — so these also pin the wrap.
//
// The `__`-prefixed names are the compiler intrinsics that std/{i32,i64,u32,u64}
// wrap in count_ones() / leading_zeros() / trailing_zeros(); calling them
// directly keeps each test one function, which is all wasmssa emits.
func TestPipelineBitCount32(t *testing.T) {
	cases := []struct {
		src  string
		arg  string
		want int
	}{
		{`function f(n: u32): i32 { return __popcount32(n); }`, "255", 8},
		{`function f(n: u32): i32 { return __popcount32(n); }`, "0", 0},
		{`function f(n: u32): i32 { return __clz32(n); }`, "1", 31},
		{`function f(n: u32): i32 { return __clz32(n); }`, "0", 32},
		{`function f(n: u32): i32 { return __ctz32(n); }`, "8", 3},
		{`function f(n: u32): i32 { return __ctz32(n); }`, "0", 32},
	}
	for _, c := range cases {
		if got := compileAndRun(t, c.src, "f", c.arg); got != c.want {
			t.Errorf("%s with n=%s = %d, want %d", c.src, c.arg, got, c.want)
		}
	}
}

// The i64 widths, where the operand width decides the answer: clz64(1) is 63
// where clz32(1) is 31, so a backend reading the wrong width is off by 32.
func TestPipelineBitCount64(t *testing.T) {
	cases := []struct {
		src  string
		arg  string
		want int
	}{
		{`function f(n: u64): i32 { return __popcount64(n); }`, "255", 8},
		{`function f(n: u64): i32 { return __clz64(n); }`, "1", 63},
		{`function f(n: u64): i32 { return __clz64(n); }`, "0", 64},
		{`function f(n: u64): i32 { return __ctz64(n); }`, "1024", 10},
		{`function f(n: u64): i32 { return __ctz64(n); }`, "0", 64},
	}
	for _, c := range cases {
		if got := compileAndRun(t, c.src, "f", c.arg); got != c.want {
			t.Errorf("%s with n=%s = %d, want %d", c.src, c.arg, got, c.want)
		}
	}
}
