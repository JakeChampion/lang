package arm64ssa_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// sliceHeaderOf builds `__method_string_as_bytes(<literal>)` and returns the
// header pointer's SSA value, so a test can read either field back.
func sliceHeaderOf(f *ssa.Func, b *ssa.Block, lit string) ssa.Value {
	return addrCallOp(f, b, "__method_string_as_bytes", constStr(f, b, lit))
}

// sliceLen adds the 4-byte load of a slice header's len field at [header+8].
func sliceLen(f *ssa.Func, b *ssa.Block, hdr ssa.Value) ssa.Value {
	v := f.AddOp(b, ssa.OpLoad32U, hdr)
	b.Ops[len(b.Ops)-1].Imm = 8
	return v
}

// __method_string_as_bytes builds a {data_ptr, len} view over the receiver's
// own bytes. The layout has to match the flat backend's __fern_slice_make
// exactly — 8-byte data pointer at +0, i32 len at +8 — because the IR reads
// the length at [slice + ptrW] and __slice_idx_* dereference the same fields.
//
// Each field is read back through a raw load rather than through a helper, so
// a wrong OFFSET fails here rather than being masked by a matching mistake in
// the index helper.
func TestArmRunStringAsBytesLayout(t *testing.T) {
	// len at [header+8].
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	f.SetRet(e, sliceLen(f, e, sliceHeaderOf(f, e, "hello")))
	if got := assembleRunArmModule(t, map[string]*ssa.Func{"main": f}, "main", 12); got != 5 {
		t.Errorf(`as_bytes("hello") len at [+8] = %d, want 5`, got)
	}

	// The data pointer at [header+0] aliases the receiver — not a copy — so
	// the first byte read through it is the literal's own first byte.
	g := ssa.NewFunc("main")
	b := g.NewBlock()
	data := loadOp(g, b, sliceHeaderOf(g, b, "hello"), 0)
	g.SetRet(b, load8u(g, b, data, 0))
	if got := assembleRunArmModule(t, map[string]*ssa.Func{"main": g}, "main", 12); got != 'h' {
		t.Errorf(`as_bytes("hello") first byte through [+0] = %d, want %d`, got, 'h')
	}

	// The empty string still yields a well-formed header with len 0.
	h := ssa.NewFunc("main")
	c := h.NewBlock()
	h.SetRet(c, sliceLen(h, c, sliceHeaderOf(h, c, "")))
	if got := assembleRunArmModule(t, map[string]*ssa.Func{"main": h}, "main", 12); got != 0 {
		t.Errorf(`as_bytes("") len = %d, want 0`, got)
	}
}

// __slice_idx_1 walks the view: every byte of the string is reachable through
// the header at its own index. Summing them checks the stride and the data
// dereference together — an off-by-one in either lands on a different total.
func TestArmRunSliceIdxWalksTheView(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	hdr := sliceHeaderOf(f, e, "abc")
	sum := constOp(f, e, 0)
	for i := int64(0); i < 3; i++ {
		at := addrCallOp(f, e, "__slice_idx_1", hdr, constOp(f, e, i))
		sum = f.AddOp(e, ssa.OpAdd, sum, load8u(f, e, at, 0))
	}
	f.SetRet(e, sum)
	// 'a'+'b'+'c' = 294, which the exit code truncates to 294-256.
	if got := assembleRunArmModule(t, map[string]*ssa.Func{"main": f}, "main", 12); got != 294-256 {
		t.Errorf("sum of as_bytes(\"abc\") through __slice_idx_1 = %d, want %d", got, 294-256)
	}
}

// An index at or past the slice's length aborts with 134 rather than reading
// off the end. The bound is the header's len at [+8]; before this helper
// existed the array sibling's [base-4] would have read whatever preceded the
// header, so "it happened not to trap" is exactly the failure to pin.
func TestArmRunSliceIdxBoundsCheck(t *testing.T) {
	for _, idx := range []int64{3, 99} {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		at := addrCallOp(f, e, "__slice_idx_1", sliceHeaderOf(f, e, "abc"), constOp(f, e, idx))
		f.SetRet(e, load8u(f, e, at, 0))
		if got := assembleRunArmModule(t, map[string]*ssa.Func{"main": f}, "main", 12); got != 134 {
			t.Errorf("__slice_idx_1 at %d past len 3: exit = %d, want 134", idx, got)
		}
	}
}
