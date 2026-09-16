package x86_64ssa

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// sleep_ms takes an i64. A negative count whose low 32 bits are a positive
// five seconds returns at once; read as its low half it slept those five
// seconds, and a count past 2^32 ms slept its low half instead of the whole.
func TestRunSleepMsReadsTheWholeCount(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	t0 := wideCallOp(f, e, "monotonic_ns")
	ms := constOp(f, e, -(1<<32)+5000)
	e.Ops[len(e.Ops)-1].Width = 64
	callOp(f, e, "sleep_ms", ms)
	t1 := wideCallOp(f, e, "monotonic_ns")
	elapsed := f.AddOp(e, ssa.OpSub, t1, t0)
	e.Ops[len(e.Ops)-1].Width = 64
	limit := constOp(f, e, 1_000_000_000)
	e.Ops[len(e.Ops)-1].Width = 64
	f.SetRet(e, f.AddOp(e, ssa.OpLt, elapsed, limit))
	if got := assembleRun(t, f, 8); got != 1 {
		t.Errorf("sleep_ms of a negative i64 took a second or more, want an immediate return (got %d)", got)
	}
}

// A program whose only heap use is the probe itself still assembles: the
// probe reads the cursor, so it needs the heap section like any allocator.
func TestRunHeapBumpBytesWithoutAllocating(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	f.SetRet(e, wideCallOp(f, e, "__fern_heap_bump_bytes"))
	if got := assembleRun(t, f, 8); got != 0 {
		t.Errorf("heap_bump_bytes with no allocation = %d, want 0", got)
	}
}

// read_dir's second pass re-reads the directory, which can have changed:
// an entry added since the first pass must not be written past the
// container the first pass sized, and the length reflects what the second
// pass found.
func TestReadDirSecondPassStaysInsideTheContainer(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	f.SetRet(e, wideCallOp(f, e, "read_dir", constStr(f, e, ".")))
	asm, err := EmitAsm(f, 8)
	if err != nil {
		t.Fatalf("EmitAsm: %v", err)
	}
	body := asm[strings.Index(asm, "\n"+fnLabel("read_dir")+":"):]
	pass2 := body[strings.Index(body, ".Lssa_rd_g2:"):]
	if i := strings.Index(pass2, "cmp rcx, r15\n\tjae .Lssa_rd_c2skip"); i < 0 || i > strings.Index(pass2, "call "+allocPresSym) {
		t.Errorf("the second pass allocates a string before checking the fill index against the count:\n%s", pass2)
	}
	if !strings.Contains(pass2, ".Lssa_rd_g2d:\n\tmov rcx, [rsp + 16]\n\tmov [r12 - 4], ecx") {
		t.Errorf("the length is not rewritten from the fill index after the second pass:\n%s", pass2)
	}
}
