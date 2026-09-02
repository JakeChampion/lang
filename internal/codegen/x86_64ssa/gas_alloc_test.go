package x86_64ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// callPtrOp is callOp for a helper whose RESULT IS AN ADDRESS. The IR emits
// these calls with ir.ResAddr, which the lift turns into Width 64 + Addr; a
// hand-built test op carries neither, so ResolveWidths is free to treat the
// result as an i32 and the renderer narrows the pointer with a movsxd. That is
// invisible while the result also feeds a load (which forces address-ness on its
// own) and fatal the moment a test's only use of it is passing it on.
func callPtrOp(f *ssa.Func, b *ssa.Block, callee string, args ...ssa.Value) ssa.Value {
	v := callOp(f, b, callee, args...)
	o := b.Ops[len(b.Ops)-1]
	o.Width, o.Addr = 64, true
	return v
}

// __alloc_u8(n) hands back a length-prefixed u8[] whose payload is ZEROED. The
// zero-fill is the part worth pinning: the bump cursor walks memory a previous
// allocation may have written, and the interpreter's u8[] is zeroed, so a
// read-before-write caller (SHA padding, #2768) depends on it.
func TestAsmRunAllocU8(t *testing.T) {
	// alloc(n) then read one i32 header word or one payload byte back.
	probe := func(n int64, off int64, kind ssa.OpKind) int {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		buf := callPtrOp(f, e, "__alloc_u8", constOp(f, e, n))
		f.SetRet(e, loadMem(f, e, buf, off, kind))
		return assembleRunModule(t, map[string]*ssa.Func{"main": f}, "main", 8, nil)
	}
	if got := probe(9, -4, ssa.OpLoad32U); got != 9 {
		t.Errorf("len at [data-4] = %d, want 9", got)
	}
	if got := probe(9, -12, ssa.OpLoad32U); got != 9 {
		t.Errorf("cap at [data-12] = %d, want 9", got)
	}
	if got := probe(9, -8, ssa.OpLoad32U); got != 1 {
		t.Errorf("rc at [data-8] = %d, want 1", got)
	}
	for _, off := range []int64{0, 1, 8} {
		if got := probe(9, off, ssa.OpLoad8U); got != 0 {
			t.Errorf("payload byte %d = %d, want 0 — __alloc_u8 must zero-fill", off, got)
		}
	}
	// n == 0 is a valid header-only buffer, not a fault: the fill runs zero
	// times and the length reads back 0.
	if got := probe(0, -4, ssa.OpLoad32U); got != 0 {
		t.Errorf("len of a zero-length u8[] = %d, want 0", got)
	}
}

// Two allocations must not overlap — the bump has to publish a cursor past the
// whole block, header included. Writing through the first and reading it back
// after the second is what catches a cursor short by the header.
func TestAsmRunAllocU8DoesNotOverlap(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	a := callPtrOp(f, e, "__alloc_u8", constOp(f, e, 8))
	storeMem(f, e, a, 0, constOp(f, e, 0x5a), ssa.OpStore8)
	b := callPtrOp(f, e, "__alloc_u8", constOp(f, e, 8))
	storeMem(f, e, b, 0, constOp(f, e, 0x17), ssa.OpStore8)
	f.SetRet(e, loadMem(f, e, a, 0, ssa.OpLoad8U))
	if got := assembleRunModule(t, map[string]*ssa.Func{"main": f}, "main", 8, nil); got != 0x5a {
		t.Errorf("first buffer's byte reads %#x after a second alloc, want 0x5a — the blocks overlap", got)
	}
}

// string_from_bytes_unchecked(bs) copies a u8[] payload into a fresh string:
// the length moves to [data-4] and the bytes come across intact.
func TestAsmRunStringFromBytes(t *testing.T) {
	// Build a u8[] of the given bytes, convert, then read one word back.
	conv := func(bytes []int64, off int64, kind ssa.OpKind) int {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		buf := callPtrOp(f, e, "__alloc_u8", constOp(f, e, int64(len(bytes))))
		for i, b := range bytes {
			storeMem(f, e, buf, int64(i), constOp(f, e, b), ssa.OpStore8)
		}
		s := callPtrOp(f, e, "string_from_bytes_unchecked", buf)
		f.SetRet(e, loadMem(f, e, s, off, kind))
		return assembleRunModule(t, map[string]*ssa.Func{"main": f}, "main", 8, nil)
	}
	bytes := []int64{'F', 'e', 'r', 'n'}
	if got := conv(bytes, -4, ssa.OpLoad32U); got != 4 {
		t.Errorf("string length = %d, want 4", got)
	}
	if got := conv(bytes, -8, ssa.OpLoad32U); got != 1 {
		t.Errorf("string rc = %d, want 1", got)
	}
	for i, want := range bytes {
		if got := conv(bytes, int64(i), ssa.OpLoad8U); got != int(want) {
			t.Errorf("byte %d = %d, want %d", i, got, want)
		}
	}
	// The empty payload still yields a valid zero-length string rather than a
	// fault, and its header is still written.
	if got := conv(nil, -4, ssa.OpLoad32U); got != 0 {
		t.Errorf("empty string length = %d, want 0", got)
	}
}

// The converted string must be a COPY. __ssa_bcopy running the wrong way, or the
// helper handing back the input pointer, both show up as the string tracking a
// later write through the source array.
func TestAsmRunStringFromBytesCopies(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	buf := callPtrOp(f, e, "__alloc_u8", constOp(f, e, 4))
	for i := int64(0); i < 4; i++ {
		storeMem(f, e, buf, i, constOp(f, e, 'A'+i), ssa.OpStore8)
	}
	s := callPtrOp(f, e, "string_from_bytes_unchecked", buf)
	storeMem(f, e, buf, 0, constOp(f, e, 'Z'), ssa.OpStore8) // mutate the source after
	f.SetRet(e, loadMem(f, e, s, 0, ssa.OpLoad8U))
	if got := assembleRunModule(t, map[string]*ssa.Func{"main": f}, "main", 8, nil); got != 'A' {
		t.Errorf("string's first byte = %d, want %d ('A') — it aliases the source array", got, 'A')
	}
}

// A longer payload crosses rep movsb's boundaries and pins the length rather
// than a happy-path prefix: the last byte of a 300-byte copy is the one a short
// count loses.
func TestAsmRunStringFromBytesLong(t *testing.T) {
	const n = 300
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	buf := callPtrOp(f, e, "__alloc_u8", constOp(f, e, n))
	storeMem(f, e, buf, n-1, constOp(f, e, 0x2a), ssa.OpStore8)
	s := callPtrOp(f, e, "string_from_bytes_unchecked", buf)
	f.SetRet(e, loadMem(f, e, s, n-1, ssa.OpLoad8U))
	if got := assembleRunModule(t, map[string]*ssa.Func{"main": f}, "main", 8, nil); got != 0x2a {
		t.Errorf("last byte of a %d-byte copy = %#x, want 0x2a", n, got)
	}
}
