package x86_64ssa

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// wideCallOp is callOp for a callee whose result fills the register: an
// address, which must not be masked back to 32 bits.
func wideCallOp(f *ssa.Func, b *ssa.Block, callee string, args ...ssa.Value) ssa.Value {
	v := callOp(f, b, callee, args...)
	b.Ops[len(b.Ops)-1].Width, b.Ops[len(b.Ops)-1].Addr = 64, true
	return v
}

// A block released to its class comes back for the next request of that
// class, and not for one of another class.
func TestFreelistHandsAReleasedBlockBack(t *testing.T) {
	// same(n, m) = __alloc(m) after __free(__alloc(n), n) is the same block.
	same := func(n, m int64) int {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		first := wideCallOp(f, e, "__alloc", constOp(f, e, n))
		callOp(f, e, "__free", first, constOp(f, e, n))
		second := wideCallOp(f, e, "__alloc", constOp(f, e, m))
		f.SetRet(e, f.AddOp(e, ssa.OpEq, first, second))
		return assembleRun(t, f, 8)
	}
	for _, tc := range []struct {
		n, m int64
		want int
	}{
		{24, 24, 1}, // the same class
		{17, 32, 1}, // 17..32 is one class
		{24, 40, 0}, // 33..48 is another
		{3000, 3000, 1},
		{3000, 3072, 1}, // the large tier rounds to 3 significant bits: 2561..3072
		{3000, 2560, 0}, // 2049..2560 is the class below
		{0, 16, 1},      // a zero-byte request still owns the 16-byte class
		{3000, 4096, 0}, // the class above
		{2049, 2560, 1}, // 2049..2560 is the first large class
		{2560, 2561, 0},
		{5000, 5120, 1}, // 4097..5120: a 1024-byte granule
		{5000, 5121, 0},
		{5000, 4096, 0},
	} {
		if got := same(tc.n, tc.m); got != tc.want {
			t.Errorf("free(alloc(%d)); alloc(%d) hands the same block back = %d, want %d", tc.n, tc.m, got, tc.want)
		}
	}
}

// A churn loop that allocates and releases a box every iteration holds the
// high-water mark flat: __fern_box_free returns each block and __alloc reuses
// it, so the cursor moves for the first iteration only.
func TestFreelistKeepsAChurnLoopFlat(t *testing.T) {
	f := ssa.NewFunc("main")
	entry, header, body, exit := f.NewBlock(), f.NewBlock(), f.NewBlock(), f.NewBlock()
	// One allocation before the measurement so the reservation exists.
	warm := allocOp(f, entry, 40)
	callOp(f, entry, "__fern_box_free", f.AddOp(entry, ssa.OpAdd, warm, constOp(f, entry, 8)), constOp(f, entry, 32))
	before := wideCallOp(f, entry, "__fern_heap_bump_bytes")
	zero := constOp(f, entry, 0)
	f.SetBr(entry, header)
	iNext := f.NewValue()
	i := f.AddPhi(header, zero, iNext)
	f.SetBrIf(header, f.AddOp(header, ssa.OpLt, i, constOp(f, header, 1000)), body, exit)
	base := allocOp(f, body, 40)
	data := f.AddOp(body, ssa.OpAdd, base, constOp(f, body, 8))
	storeMem(f, body, base, 0, constOp(f, body, 1), ssa.OpStore32) // rc = 1, as the IR writes it
	callOp(f, body, "__fern_box_free", data, constOp(f, body, 32))
	inc := f.AddOpNoResult(body, ssa.OpAdd, i, constOp(f, body, 1))
	inc.Result = iNext
	f.SetBr(body, header)
	after := wideCallOp(f, exit, "__fern_heap_bump_bytes")
	f.SetRet(exit, f.AddOp(exit, ssa.OpSub, after, before))
	if got := assembleRun(t, f, 8); got != 0 {
		t.Errorf("the high-water mark moved %d bytes over 1000 alloc/free rounds, want 0", got)
	}
}

// The trampoline is what every allocation goes through, and a module that
// allocates carries it and __alloc whether or not the program names either.
func TestEveryAllocationGoesThroughTheTrampoline(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	f.SetRet(e, f.AddOp(e, ssa.OpAnd, allocOp(f, e, 24), constOp(f, e, 15)))
	asm, err := EmitAsm(f, 8)
	if err != nil {
		t.Fatalf("EmitAsm: %v", err)
	}
	for _, want := range []string{"\tcall " + allocPresSym + "\n", "\n" + allocPresSym + ":", "\n" + fnLabel("__alloc") + ":", "\n" + freelistSym + ":"} {
		if !strings.Contains(asm, want) {
			t.Errorf("a module with one OpAlloc lacks %q:\n%s", strings.TrimSpace(want), asm)
		}
	}
	if strings.Contains(asm, "mov [rip + "+heapPtrSym+"], ") && strings.Count(asm, "mov [rip + "+heapPtrSym+"], ") > 2 {
		t.Errorf("a cursor store outside _start and __alloc: an allocation bypassed the allocator:\n%s", asm)
	}
	if got := assembleRun(t, f, 8); got != 0 {
		t.Errorf("OpAlloc's block is not 16-aligned: low bits %d", got)
	}
}
