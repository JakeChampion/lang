package arm64ssa_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// buf_take hands the builder's buffer out as an owned string, so the builder is
// a string producer and has to allocate at the same strBlockBytes every other
// producer uses. It did not: the buffer was allocated at cap+8 while
// __fern_str_dec frees a string at cap+strBlockBytes.
//
// __free derives the size CLASS from the number it is handed, so where cap+8
// lands on a class boundary the block is pushed onto the class ABOVE the one it
// was allocated from — and the next request of that larger class gets a block
// too small for it and writes past the end. That is worse than the leak #9558
// was about: a leak loses memory, this corrupts a live block.
//
// cap = 24 is such a capacity: 24+8 = 32 fills the 32-byte class exactly, while
// a free at 24+9 = 33 lands in the 48-byte class. The builder is filled to
// exactly cap, which buf_push permits — it grows only when the new length is
// strictly greater than cap.
//
// The canary is allocated AFTER the buffer, so it sits adjacent to it in the
// bump region. With the buffer under-sized, the 48-byte-class string that pops
// it writes 16 bytes past its end and into the canary.
func TestArmBufTakeFreesIntoTheClassItWasAllocatedFrom(t *testing.T) {
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
	if got := assembleRunArm(t, f, 8); got != 7 {
		t.Errorf("take round-trips (1) + the popped block holds 39 bytes (2) + the neighbouring block is intact (4) = %d, want 7 — "+
			"the builder's buffer must be allocated at the same strBlockBytes a string is freed at, or buf_take releases it "+
			"into a class whose next request overflows it", got)
	}
}
