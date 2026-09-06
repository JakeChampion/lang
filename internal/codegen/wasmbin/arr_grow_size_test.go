package wasmbin

import (
	"bytes"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/convert"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/numeric"
)

// Every __fern_arr_push_grow* body sized its allocation in i32 (#8587): the
// capacity doubling went negative past 2^30 elements (the max(.., 4) floor
// then set cap = 4 under a length near 1e9) and cap * stride wrapped past
// 4 GiB — both small enough to sail through __fern_alloc's own ceiling. wasm32
// cannot widen the size it hands the allocator, so the copy path now checks
// the doubled total in i64 and traps past maxAllocRequest.
func TestArrPushGrowChecksDoubledSizeInI64(t *testing.T) {
	// Every name resolves to 0 — the bodies are inspected for shape, never run.
	idxs := map[string]uint32{}
	bodies := map[string][]byte{
		"__fern_arr_push_grow":          buildArrPushGrowBody(idxs),
		"__fern_arr_push_grow_ptr":      buildArrPushGrowPtrBody(idxs),
		"__fern_arr_push_grow_move_ptr": buildArrPushGrowMovePtrBody(idxs),
		"__fern_arr_push_grow_str":      buildArrPushGrowStrBody(idxs),
		"__fern_arr_push_grow_move_str": buildArrPushGrowMoveStrBody(idxs),
	}
	// (i64)newLen << 1 — the doubling taken at 64 bits …
	var widen []byte
	widen = inst.InstLocalGet(widen, 3)
	widen = convert.InstI64ExtendI32U(widen)
	widen = inst.InstI64Const(widen, 1)
	widen = numeric.InstI64Shl(widen)
	// … compared unsigned against the allocator's ceiling, trapping past it.
	var trap []byte
	trap = inst.InstI64Const(trap, maxAllocRequest)
	trap = numeric.InstI64GtU(trap)
	trap = inst.InstIfStart(trap, inst.BlocktypeEmpty)
	trap = inst.InstUnreachable(trap)
	trap = inst.InstEnd(trap)
	for name, body := range bodies {
		if !bytes.Contains(body, widen) {
			t.Errorf("%s does not double the new length in i64", name)
		}
		if !bytes.Contains(body, trap) {
			t.Errorf("%s does not trap on a request past maxAllocRequest", name)
		}
	}
}
