package x86_64ssa

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// strbuf_take allocates a fresh string for the builder's bytes, so it is a
// string producer and has to allocate at the same strBlockBytes
// __fern_str_dec frees a string at.
//
// It is the mirror of buf_take_class_test.go and the more dangerous direction.
// A producer that allocates one byte SHORT of the free's size is pushed, where
// that byte crosses a 16-byte boundary, onto the class ABOVE the one it came
// from; the next request of that larger class pops a block too small for it and
// writes past the end. A producer one byte long merely wastes the byte.
//
// len = 24 is such a length: 24+8 fills the 32-byte class exactly, while the
// free at 24+strBlockBytes = 33 lands in the 48-byte class.
//
// The canary is allocated after the take, adjacent to it in the bump region.
// With the take block under-sized, the 48-byte-class string that pops it writes
// 16 bytes past its end and into the canary.
func TestStrbufTakeAllocatesAtTheSizeItIsFreedAt(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()

	callOp(f, e, "strbuf_reset")
	callOp(f, e, "strbuf_append", constStr(f, e, strings.Repeat("b", 24)))
	taken := wideCallOp(f, e, "strbuf_take")
	tookBytes := callOp(f, e, "__str_eq", taken, constStr(f, e, strings.Repeat("b", 24)))

	// Adjacent to the taken block, and in a different class so it cannot be
	// the block the release hands back.
	canary := wideCallOp(f, e, "__str_concat", constStr(f, e, strings.Repeat("c", 60)), constStr(f, e, ""))

	callOp(f, e, "__fern_str_dec", taken)

	// 39 bytes: 39 + strBlockBytes = 48, so this requests the class the freed
	// take block was pushed onto and pops that block.
	big := wideCallOp(f, e, "__str_concat", constStr(f, e, strings.Repeat("d", 20)), constStr(f, e, strings.Repeat("d", 19)))
	bigBytes := callOp(f, e, "__str_eq", big, constStr(f, e, strings.Repeat("d", 39)))
	intact := callOp(f, e, "__str_eq", canary, constStr(f, e, strings.Repeat("c", 60)))

	sum := f.AddOp(e, ssa.OpAdd, tookBytes, f.AddOp(e, ssa.OpShl, bigBytes, constOp(f, e, 1)))
	sum = f.AddOp(e, ssa.OpAdd, sum, f.AddOp(e, ssa.OpShl, intact, constOp(f, e, 2)))
	f.SetRet(e, sum)
	if got := assembleRun(t, f, 8); got != 7 {
		t.Errorf("take round-trips (1) + the popped block holds 39 bytes (2) + the neighbouring block is intact (4) = %d, want 7 — "+
			"strbuf_take must allocate at the same strBlockBytes a string is freed at, or it releases its block "+
			"into a class whose next request overflows it", got)
	}
}
