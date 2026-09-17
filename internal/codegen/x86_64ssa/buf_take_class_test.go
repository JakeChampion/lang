package x86_64ssa

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// buf_take hands the builder's buffer out as an owned string, so the builder is
// a string producer and has to allocate at the same strBlockBytes every other
// producer uses. It does: buf_new asks for cap+1 payload bytes and
// emitBufStrBlock adds the 8-byte header, so the block is cap+strBlockBytes,
// and __fern_str_dec frees the taken string at len+strBlockBytes with
// len <= cap.
//
// This pins that agreement rather than fixing it, because the failure it
// guards against is worse than the leak #9568 was about. __free derives the
// size CLASS from the number it is handed, so a buffer allocated one byte short
// of the free's size is pushed onto the class ABOVE the one it came from where
// that byte crosses a boundary, and the next request of that larger class gets
// a block too small for it and writes past the end. A leak loses memory; this
// corrupts a live block. The arm64 twin shipped exactly that way for one
// revision of #9558.
//
// cap = 24 is such a capacity: 24+8 would fill the 32-byte class exactly, while
// the free at 24+strBlockBytes = 33 lands in the 48-byte class. The builder is
// filled to exactly cap, which buf_push permits — it grows only when the new
// length is strictly greater than cap.
//
// The canary is allocated AFTER the buffer, so it sits adjacent to it in the
// bump region. With the buffer under-sized, the 48-byte-class string that pops
// it writes 16 bytes past its end and into the canary.
func TestX86BufTakeFreesIntoTheClassItWasAllocatedFrom(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()

	h := wideCallOp(f, e, "buf_new", constOp(f, e, 24))
	callOp(f, e, "buf_push", h, constStr(f, e, strings.Repeat("b", 24)))

	// Adjacent to the buffer, and in a different class so it cannot be the
	// block the take releases.
	canary := wideCallOp(f, e, "__str_concat", constStr(f, e, strings.Repeat("c", 60)), constStr(f, e, ""))

	taken := wideCallOp(f, e, "buf_take", h)
	tookBytes := callOp(f, e, "__str_eq", taken, constStr(f, e, strings.Repeat("b", 24)))
	callOp(f, e, "__fern_str_dec", taken)

	// 39 bytes: 39 + strBlockBytes = 48, so this requests the class the freed
	// buffer was pushed onto and pops that block.
	big := wideCallOp(f, e, "__str_concat", constStr(f, e, strings.Repeat("d", 20)), constStr(f, e, strings.Repeat("d", 19)))
	bigBytes := callOp(f, e, "__str_eq", big, constStr(f, e, strings.Repeat("d", 39)))
	intact := callOp(f, e, "__str_eq", canary, constStr(f, e, strings.Repeat("c", 60)))

	sum := f.AddOp(e, ssa.OpAdd, tookBytes, f.AddOp(e, ssa.OpShl, bigBytes, constOp(f, e, 1)))
	sum = f.AddOp(e, ssa.OpAdd, sum, f.AddOp(e, ssa.OpShl, intact, constOp(f, e, 2)))
	f.SetRet(e, sum)
	if got := assembleRun(t, f, 8); got != 7 {
		t.Errorf("take round-trips (1) + the popped block holds 39 bytes (2) + the neighbouring block is intact (4) = %d, want 7 — "+
			"the builder's buffer must be allocated at the same strBlockBytes a string is freed at, or buf_take releases it "+
			"into a class whose next request overflows it", got)
	}
}
