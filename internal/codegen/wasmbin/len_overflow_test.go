package wasmbin

// Length-ceiling guards (#8457). A string's byte count and an array's element
// count live in a 4-byte prefix whose top bit is the inline-form flag, so both
// cap at 2^31-1. wasm32's index type is i32 and there is nothing wider to widen
// the arithmetic into, so each construction site checks its total instead. The
// failures pinned here need a ~2 GiB operand to reach, which is why they are
// asserted on the emitted bytes rather than by running the module.

import (
	"bytes"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/memory"
	"github.com/jakechampion/lang/internal/wasm/numeric"
)

// stubIdxs gives every runtime helper a distinct function index so the bodies
// under test encode their calls without colliding.
func stubIdxs() map[string]uint32 {
	names := []string{
		"__fern_str_len", "__fern_str_byte", "__fern_alloc", "__fern_alloc_rc1",
		"__str_concat", "__fern_str_dec",
	}
	m := make(map[string]uint32, len(names))
	for i, n := range names {
		m[n] = uint32(i)
	}
	return m
}

// TestStrConcatChecksLengthCeiling — a total at or above 2^31 has no
// representable heap length: stored in the length word its top bit reads as the
// inline-form flag, so every later reader decodes the buffer as packed bytes.
func TestStrConcatChecksLengthCeiling(t *testing.T) {
	body := buildStrConcatBody(stubIdxs())
	var check []byte
	check = inst.InstLocalGet(check, 4)
	check = inst.InstLocalGet(check, 5)
	check = numeric.InstI32Add(check)
	check = inst.InstI32Const(check, maxStrLen)
	check = numeric.InstI32GtU(check)
	check = inst.InstIfStart(check, inst.BlocktypeEmpty)
	check = inst.InstUnreachable(check)
	check = inst.InstEnd(check)
	if !bytes.Contains(body, check) {
		t.Error("__str_concat does not trap on a total past the i32 length ceiling")
	}
}

// TestStrAppendDeclinesPastLengthCeiling — the in-place grow's size-class test
// can still match once the total crosses the ceiling, which would stamp a
// top-bit-set length onto the accumulator in place. Over the ceiling the fast
// path bails to __str_concat, which traps.
func TestStrAppendDeclinesPastLengthCeiling(t *testing.T) {
	body := buildStrAppendBody(stubIdxs())
	var check []byte
	check = inst.InstLocalGet(check, 6)
	check = inst.InstI32Const(check, maxStrLen)
	check = numeric.InstI32GtU(check)
	check = inst.InstBrIf(check, 0)
	if !bytes.Contains(body, check) {
		t.Error("__fern_str_append grows in place past the i32 length ceiling")
	}
}

// TestAllocU8RejectsWrappedLength — the 16-byte header add wraps a negative n
// back into __fern_alloc's accepted range, so the allocator's own guard never
// sees it: the buffer comes back short and memory.fill then runs on the
// unwrapped count. This is `repeat`'s wrapped i32 product (#8457).
func TestAllocU8RejectsWrappedLength(t *testing.T) {
	body := buildAllocU8Body(stubIdxs())
	var check []byte
	check = inst.InstLocalGet(check, 0)
	check = inst.InstI32Const(check, maxAllocRequest-arrHeaderBytes)
	check = numeric.InstI32GtU(check)
	check = inst.InstIfStart(check, inst.BlocktypeEmpty)
	check = inst.InstUnreachable(check)
	check = inst.InstEnd(check)
	if !bytes.Contains(body, check) {
		t.Error("__alloc_u8 accepts a length whose header add wraps")
	}
	// A guard after the allocation it protects is no guard at all.
	if i, j := bytes.Index(body, check), bytes.Index(body, memory.InstMemoryFill(nil)); i > j {
		t.Errorf("__alloc_u8's length guard (offset %d) comes after the fill it protects (offset %d)", i, j)
	}
}
