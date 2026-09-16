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
// "abc"+"de" is a 5-byte string in the 16-byte class (13 bytes requested), so
// three more bytes fit exactly and a fourth does not.
func TestStrAppendGrowsInPlaceWhileTheBlockHasRoom(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	acc := wideCallOp(f, e, "__str_concat", constStr(f, e, "abc"), constStr(f, e, "de"))
	r1 := wideCallOp(f, e, "__fern_str_append", acc, constStr(f, e, "xyz"))
	same1 := f.AddOp(e, ssa.OpEq, r1, acc)
	r2 := wideCallOp(f, e, "__fern_str_append", r1, constStr(f, e, "w"))
	moved := f.AddOp(e, ssa.OpNe, r2, r1)
	bytes := callOp(f, e, "__str_eq", r2, constStr(f, e, "abcdexyzw"))
	length := f.AddOp(e, ssa.OpEq, strLen(f, e, r2), constOp(f, e, 9))
	sum := f.AddOp(e, ssa.OpAdd, same1, f.AddOp(e, ssa.OpShl, moved, constOp(f, e, 1)))
	sum = f.AddOp(e, ssa.OpAdd, sum, f.AddOp(e, ssa.OpShl, bytes, constOp(f, e, 2)))
	sum = f.AddOp(e, ssa.OpAdd, sum, f.AddOp(e, ssa.OpShl, length, constOp(f, e, 3)))
	f.SetRet(e, sum)
	if got := assembleRun(t, f, 8); got != 15 {
		t.Errorf("in place (1) + moved when full (2) + bytes right (4) + length 9 (8) = %d, want 15", got)
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
