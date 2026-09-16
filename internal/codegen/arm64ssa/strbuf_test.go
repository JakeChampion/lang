package arm64ssa_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

func load8(f *ssa.Func, b *ssa.Block, base ssa.Value, offset int64) ssa.Value {
	v := f.AddOp(b, ssa.OpLoad8U, base)
	b.Ops[len(b.Ops)-1].Imm = offset
	return v
}

// The builder grows past its first block and hands back exactly what was
// appended: two 5000-byte appends outgrow the 4096-byte floor and then the
// first doubling, and the take is 10000 bytes with the right ends; after a
// reset the next build is independent.
func TestArmStrbufGrowsAndTakesWhatWasAppended(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	long := constStr(f, e, strings.Repeat("a", 4999)+"z")
	callOp(f, e, "strbuf_reset")
	callOp(f, e, "strbuf_append", long)
	callOp(f, e, "strbuf_append", long)
	s := wideCallOp(f, e, "strbuf_take")
	length := f.AddOp(e, ssa.OpEq, strLen(f, e, s), constOp(f, e, 10000))
	first := f.AddOp(e, ssa.OpEq, load8(f, e, s, 0), constOp(f, e, 'a'))
	seam := f.AddOp(e, ssa.OpEq, load8(f, e, s, 4999), constOp(f, e, 'z'))
	last := f.AddOp(e, ssa.OpEq, load8(f, e, s, 9999), constOp(f, e, 'z'))
	callOp(f, e, "strbuf_append", constStr(f, e, "xyz"))
	again := wideCallOp(f, e, "strbuf_take")
	bytes := callOp(f, e, "__str_eq", again, constStr(f, e, "xyz"))
	callOp(f, e, "strbuf_reset")
	empty := f.AddOp(e, ssa.OpEq, strLen(f, e, wideCallOp(f, e, "strbuf_take")), constOp(f, e, 0))
	sum := f.AddOp(e, ssa.OpAdd, length, f.AddOp(e, ssa.OpShl, first, constOp(f, e, 1)))
	sum = f.AddOp(e, ssa.OpAdd, sum, f.AddOp(e, ssa.OpShl, seam, constOp(f, e, 2)))
	sum = f.AddOp(e, ssa.OpAdd, sum, f.AddOp(e, ssa.OpShl, last, constOp(f, e, 3)))
	sum = f.AddOp(e, ssa.OpAdd, sum, f.AddOp(e, ssa.OpShl, bytes, constOp(f, e, 4)))
	sum = f.AddOp(e, ssa.OpAdd, sum, f.AddOp(e, ssa.OpShl, empty, constOp(f, e, 5)))
	f.SetRet(e, sum)
	if got := assembleRunArm(t, f, 8); got != 63 {
		t.Errorf("length 10000 (1) + first byte (2) + seam byte (4) + last byte (8) + the next build (16) + empty after reset (32) = %d, want 63", got)
	}
}

// Growth frees the outgrown buffer: after the builder has doubled, a request
// of the first buffer's class comes back with the first buffer's block, which
// sits below everything allocated after it.
func TestArmStrbufGrowthFreesTheOutgrownBuffer(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	callOp(f, e, "strbuf_reset")
	callOp(f, e, "strbuf_append", constStr(f, e, "abc")) // the first buffer: 4096 bytes
	firstTake := wideCallOp(f, e, "strbuf_take")
	callOp(f, e, "strbuf_append", constStr(f, e, strings.Repeat("b", 5000))) // outgrows it: 8192
	callOp(f, e, "strbuf_take")
	reused := wideCallOp(f, e, "__alloc", constOp(f, e, 4096))
	below := f.AddOp(e, ssa.OpLt, reused, firstTake)
	f.SetRet(e, f.AddOp(e, ssa.OpAdd, below, f.AddOp(e, ssa.OpShl, f.AddOp(e, ssa.OpNe, reused, constOp(f, e, 0)), constOp(f, e, 1))))
	if got := assembleRunArm(t, f, 8); got != 3 {
		t.Errorf("the freed first buffer came back for the next 4096-byte request (1) and is a block (2) = %d, want 3", got)
	}
}
