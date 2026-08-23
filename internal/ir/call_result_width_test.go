// The result width every call to a backend-provided callee carries.
//
// internal/ssa reads a call's result width off the callee's ssa.Func. A
// builtin or a runtime helper has no ssa.Func, so without a width its result
// is sign-extended from 32 bits — which turns a heap pointer negative (both
// arenas are based at 0x4_0000_0000, so every address is above 32 bits) and
// leaves an i64 or an f64 bit pattern with only its low half.
//
// That classification used to live in a hand-written name table in
// internal/ssa/width.go, and a helper nobody added to it was silently narrow.
// Each case below is a helper that WAS missing from it, lowered from the
// source shape that reaches it.
package ir_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

func callWidth(t *testing.T, ip *ir.Program, callee string) (int, bool) {
	t.Helper()
	for _, fn := range ip.Funcs {
		for _, op := range fn.Ops {
			if op.Kind == ir.OpCallDirect && op.Str == callee {
				return op.Width, true
			}
		}
	}
	return 0, false
}

func TestProvidedCalleeResultWidth(t *testing.T) {
	for _, tc := range []struct {
		name   string
		callee string
		want   int
		src    string
	}{
		{
			// `arr.with(i, v)` over a pointer-element array: the
			// copy-on-write helper hands back the buffer to store into.
			name: "array cow-in-place for pointer elements", callee: "__fern_arr_cow_inplace_ptr", want: ir.ResAddr,
			src: `
struct P { x: i32 }
function main(): i32 {
    var a = [P { x: 1 }, P { x: 2 }];
    a = a.with(0, P { x: 3 });
    return a[0].x;
}`,
		},
		{
			name: "string as_bytes", callee: "__method_string_as_bytes", want: ir.ResAddr,
			src: `
function main(): i32 {
    var b = "hi".as_bytes();
    return b.len();
}`,
		},
		{
			// A nanosecond clock reading does not fit in 32 bits, and the
			// truncation is not visible in the answer's shape — it is just a
			// smaller number.
			name: "monotonic clock", callee: "monotonic_ns", want: ir.ResWide,
			src: `
function main(): i32 {
    var t: i64 = monotonic_ns();
    return (t / 1000000) as i32;
}`,
		},
		{
			name: "wall clock", callee: "now_unix_ms", want: ir.ResWide,
			src: `
function main(): i32 {
    var t: i64 = now_unix_ms();
    return (t / 1000) as i32;
}`,
		},
		{
			name: "heap bump counter", callee: "__fern_heap_bump_bytes", want: ir.ResWide,
			src: `
function main(): i32 {
    var n: i64 = __heap_bump_bytes();
    return n as i32;
}`,
		},
		{
			// The other direction: a genuine i32 result must KEEP the mask,
			// or the backend hands on whatever the helper left above bit 31.
			name: "byte search index", callee: "__fern_memchr", want: ir.ResNarrow,
			src: `
function main(): i32 {
    return __memchr("abc", 98, 0);
}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ip := lowerForTest(t, tc.src+"\n")
			got, found := callWidth(t, ip, tc.callee)
			if !found {
				t.Fatalf("no call to %s in the lowered program — the shape it is "+
					"reached through has moved, so this case no longer covers it", tc.callee)
			}
			if got != tc.want {
				t.Errorf("%s result width = %d, want %d (ResNarrow %d / ResWide %d / ResAddr %d)",
					tc.callee, got, tc.want, ir.ResNarrow, ir.ResWide, ir.ResAddr)
			}
		})
	}
}
