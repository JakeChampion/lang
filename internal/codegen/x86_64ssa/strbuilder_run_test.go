package x86_64ssa

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// The eight-byte push on the real-asm path (#9221). Its whole point is the
// single store, so what is checked is the byte ORDER it lands in, a value with
// the high bit set (a sign-extension anywhere shows as 0xff filler), the
// interleaving with a byte push, and a grow that a wrong length would corrupt.
func TestAsmRunBufPushU64(t *testing.T) {
	// constOp leaves Width at the i32 default, which would truncate the
	// immediate before the helper ever saw it.
	const64 := func(f *ssa.Func, b *ssa.Block, imm int64) ssa.Value {
		v := constOp(f, b, imm)
		b.Ops[len(b.Ops)-1].Width = 64
		return v
	}
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	h := callPtrOp(f, e, "buf_new", constOp(f, e, 16))
	callOp(f, e, "buf_push_u64", h, const64(f, e, 0x0807060504030201))
	callOp(f, e, "buf_push_byte", h, constOp(f, e, '!'))
	s1 := callPtrOp(f, e, "buf_take", h)

	callOp(f, e, "buf_push_u64", h, const64(f, e, -0x00ffffffffffff80)) // 0xff00000000000080
	s2 := callPtrOp(f, e, "buf_take", h)

	// Sixteen pushes out of a 16-byte re-arm: the buffer doubles three times.
	for i := 0; i < 16; i++ {
		callOp(f, e, "buf_push_u64", h, const64(f, e, 0x0202020202020202))
	}
	s3 := callPtrOp(f, e, "buf_take", h)
	callOp(f, e, "buf_free", h)

	bits := []ssa.Value{
		f.AddOp(e, ssa.OpEq, callOp(f, e, "__str_len", s1), constOp(f, e, 9)),
		callOp(f, e, "__str_eq", s1, constStr(f, e, "\x01\x02\x03\x04\x05\x06\x07\x08!")),
		callOp(f, e, "__str_eq", s2, constStr(f, e, "\x80\x00\x00\x00\x00\x00\x00\xff")),
		f.AddOp(e, ssa.OpEq, callOp(f, e, "__str_len", s3), constOp(f, e, 128)),
		callOp(f, e, "__str_eq", s3, constStr(f, e, strings.Repeat("\x02", 128))),
	}
	sum := bits[0]
	for i := 1; i < len(bits); i++ {
		sum = f.AddOp(e, ssa.OpAdd, sum, f.AddOp(e, ssa.OpMul, bits[i], constOp(f, e, int64(1)<<i)))
	}
	f.SetRet(e, sum)

	want := 1<<len(bits) - 1
	if got := assembleRunModule(t, map[string]*ssa.Func{"main": f}, "main", 8, nil); got != want {
		t.Errorf("exit=%d (bits: len 9, little-endian bytes, high bit set, grown len 128, grown bytes), want %d", got, want)
	}
}

// The string-builder helpers on the real-asm path: pushes of a whole string, a
// byte range and a single byte, a grow past the initial capacity on each of the
// push paths, a take, the builder's state after it, and an empty take. Each
// fact is one bit of the exit code, so a failure names the helper that broke
// rather than the sum.
//
// The take copies out and the builder keeps its buffer (#9542). Both halves are
// pinned here: handing the buffer over instead is what stranded a block under
// the class for its length, and a take that copied but dropped the buffer would
// cost an allocation per round without being caught by the byte assertions.
func TestAsmRunStrBuilder(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	h := callPtrOp(f, e, "buf_new", constOp(f, e, 4)) // floored to 16
	callOp(f, e, "buf_push", h, constStr(f, e, "Hello, "))
	callOp(f, e, "buf_push_range", h, constStr(f, e, "xxworldyy"), constOp(f, e, 2), constOp(f, e, 7))
	callOp(f, e, "buf_push_byte", h, constOp(f, e, '!'))
	len1 := callOp(f, e, "buf_len", h)
	callOp(f, e, "buf_push", h, constStr(f, e, "0123456789ABCDEFGHIJ")) // 33 bytes: past the 16
	data := f.AddOp(e, ssa.OpLoad, h)                                   // the buffer the builder must keep
	s1 := callPtrOp(f, e, "buf_take", h)
	lenAfterTake := callOp(f, e, "buf_len", h)
	dataAfterTake := f.AddOp(e, ssa.OpLoad, h)

	// The retained buffer already spans 33 bytes, so these 17 fit without a
	// grow; the grow paths are covered by the pushes above and by
	// TestAsmRunBufPushU64's sixteen-push doubling.
	callOp(f, e, "buf_push", h, constStr(f, e, "0123456789ABCDEF"))
	callOp(f, e, "buf_push_byte", h, constOp(f, e, 'Z'))
	s2 := callPtrOp(f, e, "buf_take", h)

	callOp(f, e, "buf_push_range", h, constStr(f, e, "abc"), constOp(f, e, 2), constOp(f, e, 1)) // inverted: no-op
	s3 := callPtrOp(f, e, "buf_take", h)
	callOp(f, e, "buf_free", h)

	bits := []ssa.Value{
		f.AddOp(e, ssa.OpEq, len1, constOp(f, e, 13)),
		f.AddOp(e, ssa.OpEq, callOp(f, e, "__str_len", s1), constOp(f, e, 33)),
		callOp(f, e, "__str_eq", s1, constStr(f, e, "Hello, world!0123456789ABCDEFGHIJ")),
		f.AddOp(e, ssa.OpNe, s1, data),
		f.AddOp(e, ssa.OpEq, dataAfterTake, data),
		f.AddOp(e, ssa.OpEq, lenAfterTake, constOp(f, e, 0)),
		callOp(f, e, "__str_eq", s2, constStr(f, e, "0123456789ABCDEFZ")),
		f.AddOp(e, ssa.OpEq, callOp(f, e, "__str_len", s3), constOp(f, e, 0)),
	}
	sum := bits[0]
	for i := 1; i < len(bits); i++ {
		sum = f.AddOp(e, ssa.OpAdd, sum, f.AddOp(e, ssa.OpMul, bits[i], constOp(f, e, int64(1)<<i)))
	}
	f.SetRet(e, sum)

	want := 1<<len(bits) - 1
	if got := assembleRunModule(t, map[string]*ssa.Func{"main": f}, "main", 8, nil); got != want {
		t.Errorf("exit=%d (bits: len 13, take len 33, take bytes, take is NOT the buffer, "+
			"builder kept its buffer, empty after take, push after take, empty take), want %d", got, want)
	}
}
