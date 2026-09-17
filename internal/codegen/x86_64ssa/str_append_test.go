package x86_64ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// strLen reads a string's length word.
func strLen(f *ssa.Func, b *ssa.Block, s ssa.Value) ssa.Value {
	return loadMem(f, b, s, -4, ssa.OpLoad32U)
}

// A uniquely held accumulator grows in place while the grown length still
// fits its size class, and moves to a fresh block the first time it does not.
// "ab"+"cd" is a 4-byte string in the 16-byte class (4 + strBlockBytes = 13
// requested), so three more bytes fit exactly and a fourth does not.
func TestStrAppendGrowsInPlaceWhileTheBlockHasRoom(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	acc := wideCallOp(f, e, "__str_concat", constStr(f, e, "ab"), constStr(f, e, "cd"))
	r1 := wideCallOp(f, e, "__fern_str_append", acc, constStr(f, e, "xyz"))
	same1 := f.AddOp(e, ssa.OpEq, r1, acc)
	r2 := wideCallOp(f, e, "__fern_str_append", r1, constStr(f, e, "w"))
	moved := f.AddOp(e, ssa.OpNe, r2, r1)
	bytes := callOp(f, e, "__str_eq", r2, constStr(f, e, "abcdxyzw"))
	length := f.AddOp(e, ssa.OpEq, strLen(f, e, r2), constOp(f, e, 8))
	sum := f.AddOp(e, ssa.OpAdd, same1, f.AddOp(e, ssa.OpShl, moved, constOp(f, e, 1)))
	sum = f.AddOp(e, ssa.OpAdd, sum, f.AddOp(e, ssa.OpShl, bytes, constOp(f, e, 2)))
	sum = f.AddOp(e, ssa.OpAdd, sum, f.AddOp(e, ssa.OpShl, length, constOp(f, e, 3)))
	f.SetRet(e, sum)
	if got := assembleRun(t, f, 8); got != 15 {
		t.Errorf("in place (1) + moved when full (2) + bytes right (4) + length 8 (8) = %d, want 15", got)
	}
}

// A shared accumulator is copied, not grown: the other holder keeps reading
// the bytes and length it had.
func TestStrAppendCopiesASharedAccumulator(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	acc := wideCallOp(f, e, "__str_concat", constStr(f, e, "abc"), constStr(f, e, "de"))
	callOp(f, e, "__fern_rc_inc", acc)
	r := wideCallOp(f, e, "__fern_str_append", acc, constStr(f, e, "x"))
	moved := f.AddOp(e, ssa.OpNe, r, acc)
	kept := f.AddOp(e, ssa.OpEq, strLen(f, e, acc), constOp(f, e, 5))
	bytes := callOp(f, e, "__str_eq", r, constStr(f, e, "abcdex"))
	sum := f.AddOp(e, ssa.OpAdd, moved, f.AddOp(e, ssa.OpShl, kept, constOp(f, e, 1)))
	sum = f.AddOp(e, ssa.OpAdd, sum, f.AddOp(e, ssa.OpShl, bytes, constOp(f, e, 2)))
	f.SetRet(e, sum)
	if got := assembleRun(t, f, 8); got != 7 {
		t.Errorf("moved (1) + the shared holder unchanged (2) + bytes right (4) = %d, want 7", got)
	}
}

// A literal is never grown: it lives in .rodata under an immortal sentinel.
func TestStrAppendCopiesALiteral(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	lit := constStr(f, e, "abc")
	r := wideCallOp(f, e, "__fern_str_append", lit, constStr(f, e, "d"))
	moved := f.AddOp(e, ssa.OpNe, r, lit)
	bytes := callOp(f, e, "__str_eq", r, constStr(f, e, "abcd"))
	f.SetRet(e, f.AddOp(e, ssa.OpAdd, moved, f.AddOp(e, ssa.OpShl, bytes, constOp(f, e, 1))))
	if got := assembleRun(t, f, 8); got != 3 {
		t.Errorf("moved (1) + bytes right (2) = %d, want 3", got)
	}
}

// Growth is amortised: 4096 one-byte appends move the accumulator once per
// size class it outgrows — 16-byte steps to 2048 bytes, then steps of a
// quarter to a half of its size — rather than once per append.
func TestStrAppendMovesOncePerSizeClass(t *testing.T) {
	f := ssa.NewFunc("main")
	entry, header, body, exit := f.NewBlock(), f.NewBlock(), f.NewBlock(), f.NewBlock()
	start := wideCallOp(f, entry, "__str_concat", constStr(f, entry, ""), constStr(f, entry, ""))
	zero := constOp(f, entry, 0)
	f.SetBr(entry, header)
	iNext, accNext, movesNext := f.NewValue(), f.NewValue(), f.NewValue()
	i := f.AddPhi(header, zero, iNext)
	acc := f.AddPhi(header, start, accNext)
	moves := f.AddPhi(header, zero, movesNext)
	f.SetBrIf(header, f.AddOp(header, ssa.OpLt, i, constOp(f, header, 4096)), body, exit)
	grown := f.AddOpNoResult(body, ssa.OpCall, acc, constStr(f, body, "x"))
	grown.Str, grown.Width, grown.Addr, grown.Result = "__fern_str_append", 64, true, accNext
	movedOp := f.AddOpNoResult(body, ssa.OpAdd, moves, f.AddOp(body, ssa.OpNe, accNext, acc))
	movedOp.Result = movesNext
	inc := f.AddOpNoResult(body, ssa.OpAdd, i, constOp(f, body, 1))
	inc.Result = iNext
	f.SetBr(body, header)
	// The exit code is the move count, or 255 when the length is wrong.
	wrong := f.AddOp(exit, ssa.OpNe, strLen(f, exit, acc), constOp(f, exit, 4096))
	f.SetRet(exit, f.AddOp(exit, ssa.OpAdd, moves, f.AddOp(exit, ssa.OpMul, wrong, constOp(f, exit, 255))))
	got := assembleRun(t, f, 8)
	if got == 255 {
		t.Fatalf("the accumulator is not 4096 bytes long after 4096 appends")
	}
	// A class of c bytes holds c-8 bytes of string: 127 moves up through the
	// 16-byte classes to 2048, then into 2560, 3072, 3584, 4096 and 5120.
	if got != 132 {
		t.Errorf("the accumulator moved %d times over 4096 appends, want 132", got)
	}
}

// A string's last release hands its block back: the next request of the same
// class is the same block. A shared string is decremented and kept.
func TestStrDecHandsTheBlockBack(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	s := wideCallOp(f, e, "__str_concat", constStr(f, e, "abc"), constStr(f, e, "de"))
	callOp(f, e, "__fern_str_dec", s)
	again := wideCallOp(f, e, "__str_concat", constStr(f, e, "xy"), constStr(f, e, "z")) // the same 16-byte class
	reused := f.AddOp(e, ssa.OpEq, again, s)
	callOp(f, e, "__fern_rc_inc", again)
	callOp(f, e, "__fern_str_dec", again)
	kept := f.AddOp(e, ssa.OpEq, strLen(f, e, again), constOp(f, e, 3))
	fresh := wideCallOp(f, e, "__str_concat", constStr(f, e, "q"), constStr(f, e, "r"))
	elsewhere := f.AddOp(e, ssa.OpNe, fresh, again)
	sum := f.AddOp(e, ssa.OpAdd, reused, f.AddOp(e, ssa.OpShl, kept, constOp(f, e, 1)))
	f.SetRet(e, f.AddOp(e, ssa.OpAdd, sum, f.AddOp(e, ssa.OpShl, elsewhere, constOp(f, e, 2))))
	if got := assembleRun(t, f, 8); got != 7 {
		t.Errorf("block reused (1) + a shared string kept (2) + its block not handed out (4) = %d, want 7", got)
	}
}

// The copy path of an append releases the accumulator it consumed, so the
// block comes back for the next request of its class.
func TestStrAppendCopyPathReleasesTheAccumulator(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	// 23 bytes fills the 32-byte class exactly (23 + strBlockBytes = 32), so
	// the append cannot grow in place and has to copy — which is the path
	// under test. A shorter accumulator would have spare capacity and never
	// move.
	acc := wideCallOp(f, e, "__str_concat", constStr(f, e, "abcdefghijklmnopqrstuv"), constStr(f, e, "w")) // 23 bytes: the 32-byte class, full
	grown := wideCallOp(f, e, "__fern_str_append", acc, constStr(f, e, "x"))                               // 24 bytes: a 48-byte block
	moved := f.AddOp(e, ssa.OpNe, grown, acc)
	again := wideCallOp(f, e, "__str_concat", constStr(f, e, "abcdefghijklmnopqrstu"), constStr(f, e, "vw")) // the 32-byte class again
	reused := f.AddOp(e, ssa.OpEq, again, acc)
	bytes := callOp(f, e, "__str_eq", grown, constStr(f, e, "abcdefghijklmnopqrstuvwx"))
	sum := f.AddOp(e, ssa.OpAdd, moved, f.AddOp(e, ssa.OpShl, reused, constOp(f, e, 1)))
	f.SetRet(e, f.AddOp(e, ssa.OpAdd, sum, f.AddOp(e, ssa.OpShl, bytes, constOp(f, e, 2))))
	if got := assembleRun(t, f, 8); got != 7 {
		t.Errorf("moved (1) + the old block reused (2) + bytes right (4) = %d, want 7", got)
	}
}

// Copying a shared string array retains every element, so the copy and the
// original each own a reference and neither's release frees a string the
// other still holds.
func TestArrCowInplaceStrRetainsTheElements(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	s := wideCallOp(f, e, "__str_concat", constStr(f, e, "abc"), constStr(f, e, "de"))
	base := allocOp(f, e, 24)                                 // 16-byte header + one pointer
	storeMem(f, e, base, 4, constOp(f, e, 1), ssa.OpStore32)  // cap
	storeMem(f, e, base, 8, constOp(f, e, 2), ssa.OpStore32)  // rc: shared, so .with copies
	storeMem(f, e, base, 12, constOp(f, e, 1), ssa.OpStore32) // len
	data := f.AddOp(e, ssa.OpAdd, base, constOp(f, e, 16))
	storeMem(f, e, data, 0, s, ssa.OpStore)
	copied := wideCallOp(f, e, "__fern_arr_cow_inplace_str", data, constOp(f, e, 8))
	moved := f.AddOp(e, ssa.OpNe, copied, data)
	retained := f.AddOp(e, ssa.OpEq, loadMem(f, e, s, -8, ssa.OpLoad32U), constOp(f, e, 2))
	f.SetRet(e, f.AddOp(e, ssa.OpAdd, moved, f.AddOp(e, ssa.OpShl, retained, constOp(f, e, 1))))
	if got := assembleRun(t, f, 8); got != 3 {
		t.Errorf("copied (1) + the element at rc 2 (2) = %d, want 3", got)
	}
}
