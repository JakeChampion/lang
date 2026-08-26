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

// sliceLen reads a slice header's len field at [header+8].
func sliceLen(f *ssa.Func, b *ssa.Block, hdr ssa.Value) ssa.Value {
	return load32u(f, b, hdr, 8)
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

// statOf builds `stat(<literal path>)` and returns the Result box pointer.
func statOf(f *ssa.Func, b *ssa.Block, path string) ssa.Value {
	return addrCallOp(f, b, "stat", constStr(f, b, path))
}

// stat(path) -> Result[FileStat, IoError]. The Result box is {tag@+0,
// payload@+8}; the Ok payload is a FileStat box {is_file@+0, is_dir@+4,
// size@+8} — the same layout the flat backend builds, since the IR reads
// these fields the same way whichever backend produced them.
//
// The three cases below pin the two things the helper decodes out of the
// 128-byte struct stat: the S_IFMT bits of st_mode (u32 at +16), and the
// error path. Reading st_mode at the wrong offset would still land inside
// the buffer and yield a plausible-looking answer, so a directory and a
// regular file are BOTH checked — one alone passes if the mode word is
// misread as zero.
func TestArmRunStat(t *testing.T) {
	// "/" is a directory: Ok, is_dir set, is_file clear.
	dirCase := func(field int64) int {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		fs := loadOp(f, e, statOf(f, e, "/"), 8)
		f.SetRet(e, load32u(f, e, fs, field))
		return assembleRunArmModule(t, map[string]*ssa.Func{"main": f}, "main", 12)
	}
	if got := dirCase(0); got != 0 {
		t.Errorf(`stat("/").is_file = %d, want 0`, got)
	}
	if got := dirCase(4); got != 1 {
		t.Errorf(`stat("/").is_dir = %d, want 1`, got)
	}

	// A regular file: is_file set. /proc/version is S_IFREG on every Linux
	// this runs on, and needs no fixture to exist.
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	fs := loadOp(f, e, statOf(f, e, "/proc/version"), 8)
	f.SetRet(e, load32u(f, e, fs, 0))
	if got := assembleRunArmModule(t, map[string]*ssa.Func{"main": f}, "main", 12); got != 1 {
		t.Errorf(`stat("/proc/version").is_file = %d, want 1`, got)
	}

	// A missing path is Err (tag 1), not a crash and not a zeroed Ok.
	g := ssa.NewFunc("main")
	b := g.NewBlock()
	g.SetRet(b, load32u(g, b, statOf(g, b, "/no/such/path/here"), 0))
	if got := assembleRunArmModule(t, map[string]*ssa.Func{"main": g}, "main", 12); got != 1 {
		t.Errorf(`stat("/no/such/path/here") tag = %d, want 1 (Err)`, got)
	}

	// And the success tag really is 0, so the Err check above is not just
	// reading a field that happens to be 1.
	h := ssa.NewFunc("main")
	c := h.NewBlock()
	h.SetRet(c, load32u(h, c, statOf(h, c, "/"), 0))
	if got := assembleRunArmModule(t, map[string]*ssa.Func{"main": h}, "main", 12); got != 0 {
		t.Errorf(`stat("/") tag = %d, want 0 (Ok)`, got)
	}
}
