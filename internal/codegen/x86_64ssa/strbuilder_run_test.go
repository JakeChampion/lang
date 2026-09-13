package x86_64ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// The string-builder helpers on the real-asm path: pushes of a whole string, a
// byte range and a single byte, a grow past the initial capacity on each of the
// push paths, a zero-copy take, a re-arm from the reserve after it, and an
// empty take. Each fact is one bit of the exit code, so a failure names the
// helper that broke rather than the sum.
func TestAsmRunStrBuilder(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	h := callPtrOp(f, e, "buf_new", constOp(f, e, 4)) // floored to 16
	callOp(f, e, "buf_push", h, constStr(f, e, "Hello, "))
	callOp(f, e, "buf_push_range", h, constStr(f, e, "xxworldyy"), constOp(f, e, 2), constOp(f, e, 7))
	callOp(f, e, "buf_push_byte", h, constOp(f, e, '!'))
	len1 := callOp(f, e, "buf_len", h)
	callOp(f, e, "buf_push", h, constStr(f, e, "0123456789ABCDEFGHIJ")) // 33 bytes: past the 16
	data := f.AddOp(e, ssa.OpLoad, h)                                   // the buffer buf_take must hand back
	s1 := callPtrOp(f, e, "buf_take", h)
	lenAfterTake := callOp(f, e, "buf_len", h)

	// Re-armed from the reserve: 16 bytes fit exactly, the byte grows it.
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
		f.AddOp(e, ssa.OpEq, s1, data),
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
		t.Errorf("exit=%d (bits: len 13, take len 33, take bytes, take is the buffer, empty after take, "+
			"re-arm+byte grow, empty take), want %d", got, want)
	}
}
