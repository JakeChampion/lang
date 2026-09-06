package arm64ssa_test

import (
	"os"
	"testing"

	"github.com/jakechampion/lang/internal/ir"
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
// payload@+8}; the Ok payload is a FileStat laid out by ir.FileStat — the same
// layout the flat backend builds, since the IR reads these fields the same way
// whichever backend produced them.
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

// sliceOver builds `__slice_make(data, len)` — the header a `a[lo:hi]`
// expression ends at — over a caller-supplied buffer.
func sliceOver(f *ssa.Func, b *ssa.Block, data ssa.Value, length int64) ssa.Value {
	return addrCallOp(f, b, "__slice_make", data, constOp(f, b, length))
}

// The stride of each __slice_idx_* variant, pinned one element in so a wrong
// shift lands on a different value rather than on the same first element.
//
// Every one of these is reachable now that `a[lo:hi]` compiles: at this
// emitter's ptr-width the IR emits __slice_idx_1 for a byte slice,
// __slice_idx for i32, and __slice_idx_8 for i64 AND for string (a one-word
// element here), which is why there is no _16.
func TestArmRunSliceIdxStrides(t *testing.T) {
	cases := []struct {
		helper string
		stride int64
		store  ssa.OpKind
	}{
		{"__slice_idx_1", 1, ssa.OpStore8},
		{"__slice_idx", 4, ssa.OpStore32},
		{"__slice_idx_8", 8, ssa.OpStore},
	}
	for _, c := range cases {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		// A two-element buffer holding {7, 9} at the helper's stride.
		buf := f.AddOp(e, ssa.OpAlloc, constOp(f, e, 64))
		st := f.AddOpNoResult(e, c.store, buf, constOp(f, e, 7))
		st.Imm = 0
		st = f.AddOpNoResult(e, c.store, buf, constOp(f, e, 9))
		st.Imm = c.stride
		// Index element 1 through the helper: a wrong shift reads element 0
		// (or past the buffer) and cannot answer 9.
		at := addrCallOp(f, e, c.helper, sliceOver(f, e, buf, 2), constOp(f, e, 1))
		f.SetRet(e, load8u(f, e, at, 0))
		if got := assembleRunArmModule(t, map[string]*ssa.Func{"main": f}, "main", 12); got != 9 {
			t.Errorf("%s: element 1 of {7,9} at stride %d = %d, want 9", c.helper, c.stride, got)
		}
	}
}

// __slice_range(lo, hi, len) returns the new length and traps on any bound that
// would let the view escape its source. The negative cases are the ones that
// need the sign-extension: compared as raw 32-bit values a negative low bound
// looks small and passes.
func TestArmRunSliceRange(t *testing.T) {
	rangeOf := func(lo, hi, length int64) int {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		f.SetRet(e, callOp(f, e, "__slice_range",
			constOp(f, e, lo), constOp(f, e, hi), constOp(f, e, length)))
		return assembleRunArmModule(t, map[string]*ssa.Func{"main": f}, "main", 12)
	}
	for _, c := range []struct {
		lo, hi, len int64
		want        int
	}{
		{1, 3, 4, 2},    // in range
		{0, 4, 4, 4},    // the whole source
		{2, 2, 4, 0},    // empty
		{0, 5, 4, 134},  // hi > len
		{3, 1, 4, 134},  // lo > hi
		{-1, 3, 4, 134}, // lo < 0
		{0, -1, 4, 134}, // hi < 0
	} {
		if got := rangeOf(c.lo, c.hi, c.len); got != c.want {
			t.Errorf("__slice_range(%d, %d, %d) = %d, want %d", c.lo, c.hi, c.len, got, c.want)
		}
	}
}

// The rest of the `stat(2)` record — everything past the kind and the size —
// on the SSA backend. Each field comes from a different offset in the kernel's
// 128-byte struct, and a wrong one is silent: it lands inside a buffer the
// kernel filled and reads a real number out of the neighbouring field. So the
// assertions are on values that are FIXED for the path being stat'd rather
// than on "not zero".
//
// /proc/version is the fixture for the same reason TestArmRunStat uses it: it
// exists on every Linux this runs on, needs no setup, and procfs gives it a
// known mode (0444), owner (root) and link count (1).
func TestArmRunStatFields(t *testing.T) {
	field := func(path string, offset int64, wide bool) int {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		fs := loadOp(f, e, statOf(f, e, path), 8)
		if wide {
			f.SetRet(e, loadOp(f, e, fs, offset))
		} else {
			f.SetRet(e, load32u(f, e, fs, offset))
		}
		return assembleRunArmModule(t, map[string]*ssa.Func{"main": f}, "main", 12)
	}
	const procVersion = "/proc/version"
	// A program's answer reaches this harness as an exit STATUS, which is one
	// byte, so every expectation is the low byte of the real value.
	lowByte := func(v int) int { return v & 0xff }

	// st_mode: S_IFREG | 0444 = 0o100444, whose low byte is 0o044.
	if got, want := field(procVersion, int64(ir.FileStat.Mode), false), lowByte(0o100444); got != want {
		t.Errorf("stat(%q).mode low byte = %#o, want %#o", procVersion, got, want)
	}
	// st_nlink: a procfs file has exactly one link.
	if got := field(procVersion, int64(ir.FileStat.Nlink), false); got != 1 {
		t.Errorf("stat(%q).nlink = %d, want 1", procVersion, got)
	}
	// st_uid / st_gid: procfs entries belong to root.
	if got := field(procVersion, int64(ir.FileStat.UID), false); got != 0 {
		t.Errorf("stat(%q).uid = %d, want 0", procVersion, got)
	}
	if got := field(procVersion, int64(ir.FileStat.GID), false); got != 0 {
		t.Errorf("stat(%q).gid = %d, want 0", procVersion, got)
	}
	// st_rdev: a regular file names no device node.
	if got := field(procVersion, int64(ir.FileStat.Rdev), true); got != 0 {
		t.Errorf("stat(%q).rdev = %d, want 0", procVersion, got)
	}
	// /dev/null IS a device node, so the same slot is non-zero there — which
	// is what distinguishes "read the rdev field" from "read a zero". Its
	// device id is makedev(1, 3), and 3 is the minor in the low byte.
	if got := field("/dev/null", int64(ir.FileStat.Rdev), true); got != 3 {
		t.Errorf("stat(/dev/null).rdev low byte = %d, want 3 (minor of char 1:3)", got)
	}
	// st_mtime: seconds since the epoch. The exit status truncates it, so
	// assert the field is READ rather than its value — paired with the
	// nanosecond slot below, which must stay inside one second whatever the
	// seconds count is, an off-by-one row in the offset table shows up.
	if got := field(procVersion, int64(ir.FileStat.MtimeNsec), true); got < 0 || got > 255 {
		t.Errorf("stat(%q).mtime_nsec truncated to %d — not a sub-second remainder", procVersion, got)
	}
}

// `access` and the effective-id pair on the SSA backend. The X_OK case carries
// signal whatever uid the suite runs as: the execute bit is checked even for
// root, so /proc/version (0444) is not executable and /bin/sh is.
func TestArmRunAccessAndIds(t *testing.T) {
	accessTag := func(path string, mode int64) int {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		r := addrCallOp(f, e, "access", constStr(f, e, path), constOp(f, e, mode))
		f.SetRet(e, load32u(f, e, r, 0))
		return assembleRunArmModule(t, map[string]*ssa.Func{"main": f}, "main", 12)
	}
	// tag 0 = Ok, tag 1 = Err.
	if got := accessTag("/proc/version", 0); got != 0 {
		t.Errorf("access(/proc/version, F_OK) tag = %d, want 0 (Ok)", got)
	}
	if got := accessTag("/proc/version", 4); got != 0 {
		t.Errorf("access(/proc/version, R_OK) tag = %d, want 0 (Ok)", got)
	}
	if got := accessTag("/proc/version", 1); got != 1 {
		t.Errorf("access(/proc/version, X_OK) tag = %d, want 1 (Err — 0444 has no x bit)", got)
	}
	if got := accessTag("/no/such/path/here", 0); got != 1 {
		t.Errorf("access(missing, F_OK) tag = %d, want 1 (Err)", got)
	}

	id := func(callee string) int {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		f.SetRet(e, callOp(f, e, callee))
		return assembleRunArmModule(t, map[string]*ssa.Func{"main": f}, "main", 12)
	}
	// The value comes back as the process's exit status, which is one byte:
	// a runner whose uid is 1001 reads back as 233.
	if got, want := id("geteuid"), os.Geteuid()&0xFF; got != want {
		t.Errorf("geteuid() = %d, want %d (uid %d truncated to the exit byte)", got, want, os.Geteuid())
	}
	if got, want := id("getegid"), os.Getegid()&0xFF; got != want {
		t.Errorf("getegid() = %d, want %d (gid %d truncated to the exit byte)", got, want, os.Getegid())
	}
}
