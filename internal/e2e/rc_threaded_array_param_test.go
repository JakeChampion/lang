// Memory-and-correctness guard for the threaded borrowed ARRAY parameter —
// `function f(buf: i32[], ..): i32[] { buf = buf.append(..); .. return buf; }`,
// called in a loop by an owner that self-reassigns. It is the shape every
// byte-emitter in the self-host compiler is built from (arm64_native's
// `arm64_le32` runs it four times per assembled instruction word), and it has
// now been broken in both directions:
//
//   - Before #6021 the overwrite dec released a reference the caller never
//     handed over: __rc_underflow_count() reports 1 here, and downstream that
//     count-steal frees a live buffer at whichever site legitimately owns it
//     (a ~50% segfault in __fern_alloc's freelist pop, from a change that
//     merely shifted allocation sizes).
//
//   - #6021 balanced it with the entry retain that structs / tuples / enums
//     use, which for an ARRAY costs the in-place append: rc==1 is exactly the
//     uniqueness test __fern_arr_push_grow gates on, so a retained buffer
//     enters at rc 2 and every append clones it. Arena traffic went 14x here
//     and 490x on the arm64 assembler (18 MB -> 8.9 GB on ~900 KB of input).
//
// So neither number alone is the contract — the program asserts both. The
// return value is arena bytes per live byte, which is flat in N when the
// appends stay in place (4) and grows with the buffer when they do not
// (44..94 over the range below, rising as N shrinks). A ceiling of 8 sits an
// order of magnitude clear of both regimes at every size.
package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// threadedArrayParamSrc threads an accumulator through a borrowed array param
// that self-appends and hands it back, N times.
func threadedArrayParamSrc(n, twoN string) string {
	return `function le32(buf: i32[], v: i32): i32[] {
    buf = buf.append(v & 255);
    buf = buf.append((v >> 8) & 255);
    return buf;
}
function main(): i32 {
    var before: i32 = __heap_bump_bytes();
    var acc: i32[] = [];
    var i: i32 = 0;
    while (i < ` + n + `) { acc = le32(acc, i); i = i + 1; }
    if (acc.len() != ` + twoN + `) { return 254; }
    if (acc[0] != 0 || acc[2] != 1 || acc[6] != 3 || acc[7] != 0) { return 253; }
    if (__rc_underflow_count() != 0) { return 200; }
    return (__heap_bump_bytes() - before) / (acc.len() * 4);
}`
}

// threadedArrayParamOverheadCeiling is arena bytes per live byte. Measured:
// 4 with the in-place append at every N below; 44..94 with it forced off.
const threadedArrayParamOverheadCeiling = 8

func checkThreadedArrayParam(t *testing.T, backend string, n, got int) {
	t.Helper()
	switch got {
	case 254:
		t.Fatalf("%s: N=%d wrong accumulator length — the threading itself is broken", backend, n)
	case 253:
		t.Fatalf("%s: N=%d wrong accumulator CONTENTS — an append wrote to a buffer the caller no longer holds", backend, n)
	case 200:
		t.Fatalf("%s: N=%d __rc_underflow_count() != 0 — the overwrite dec released the caller's "+
			"reference (the #6021 count-steal)", backend, n)
	}
	if got > threadedArrayParamOverheadCeiling {
		t.Errorf("%s: N=%d allocated %d arena bytes per live byte, over the %d ceiling — the threaded "+
			"array param is copying the whole buffer per append instead of growing it in place",
			backend, n, got, threadedArrayParamOverheadCeiling)
	}
}

// threadedArrayParamSizes spans a 4x range: the in-place regime is flat
// across it and the copying one is not, so a single ceiling separates them
// without depending on which N a future change happens to run.
var threadedArrayParamSizes = []struct {
	n    int
	src  string
	twoN string
}{
	{200, "200", "400"},
	{800, "800", "1600"},
}

func TestX86_64ThreadedArrayParamBounded(t *testing.T) {
	for _, sz := range threadedArrayParamSizes {
		_, got := compileAndRunX86_64FreeOn(t, threadedArrayParamSrc(sz.src, sz.twoN))
		checkThreadedArrayParam(t, "x86-64", sz.n, got)
	}
}

func TestWASMThreadedArrayParamBounded(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	for _, sz := range threadedArrayParamSizes {
		got := runWasm(t, threadedArrayParamSrc(sz.src, sz.twoN))
		checkThreadedArrayParam(t, "wasm", sz.n, got)
	}
}
