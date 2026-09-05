package x86_64ssa

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// remove_dir_all and the __fern_io_error it reports failures through: the two
// helpers that took the x86-64 SSA corpus differential from 39 comparable
// programs to 194 (#8570). Almost every corpus program reaches them through
// std/test's import graph without calling either, so the differential proves
// nothing about their behaviour — these do.

// ioErrorField runs __fern_io_error(errno, path) and reads one word of the box
// it builds.
func ioErrorField(t *testing.T, errno int64, path string, off int64, kind ssa.OpKind) int {
	t.Helper()
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	box := callPtrOp(f, e, "__fern_io_error", constOp(f, e, errno), constStr(f, e, path))
	f.SetRet(e, loadMem(f, e, box, off, kind))
	return assembleRunModule(t, map[string]*ssa.Func{"main": f}, "main", 8, nil)
}

// Each errno lands on the variant the natives give it, and the four
// path-carrying variants keep the path they were handed.
func TestAsmRunIoErrorVariants(t *testing.T) {
	for _, tc := range []struct {
		name  string
		errno int64
		tag   int
	}{
		{"ENOENT is NotFound", 2, 0},
		{"EACCES is PermissionDenied", 13, 1},
		{"EEXIST is AlreadyExists", 17, 2},
		{"EILSEQ is InvalidUtf8", 84, 3},
		{"EINTR is Interrupted", 4, 4},
		{"EBADF falls through to Other", 9, 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ioErrorField(t, tc.errno, "/tmp/x", 0, ssa.OpLoad32U); got != tc.tag {
				t.Errorf("errno %d -> tag %d, want %d", tc.errno, got, tc.tag)
			}
		})
	}

	// The path rides the payload slot: its length is read back through the
	// pointer the box holds, which a box built at the wrong offset loses.
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	box := callPtrOp(f, e, "__fern_io_error", constOp(f, e, 2), constStr(f, e, "/tmp/probe"))
	path := loadMem(f, e, box, 8, ssa.OpLoad)
	f.SetRet(e, loadMem(f, e, path, -4, ssa.OpLoad32U))
	if got := assembleRunModule(t, map[string]*ssa.Func{"main": f}, "main", 8, nil); got != len("/tmp/probe") {
		t.Errorf("path length through the box = %d, want %d", got, len("/tmp/probe"))
	}
}

// Other carries glibc's strerror text, from the one table every backend and the
// self-host share (#8265). Read as bytes through the box, since the message is
// the half of Other a wrong offset silently drops.
func TestAsmRunIoErrorOtherMessage(t *testing.T) {
	msgByte := func(errno int64, off int64, kind ssa.OpKind) int {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		box := callPtrOp(f, e, "__fern_io_error", constOp(f, e, errno), constStr(f, e, ""))
		msg := loadMem(f, e, box, 16, ssa.OpLoad)
		f.SetRet(e, loadMem(f, e, msg, off, kind))
		return assembleRunModule(t, map[string]*ssa.Func{"main": f}, "main", 8, nil)
	}
	const want = "Bad file descriptor" // EBADF, 9
	if got := msgByte(9, -4, ssa.OpLoad32U); got != len(want) {
		t.Fatalf("EBADF message length = %d, want %d (%q)", got, len(want), want)
	}
	for i, c := range []byte(want) {
		if got := msgByte(9, int64(i), ssa.OpLoad8U); got != int(c) {
			t.Fatalf("EBADF message byte %d = %d, want %d (%q)", i, got, c, want)
		}
	}
	// A .rodata literal's immortal rc sentinel, so dropping the message does
	// not write to read-only memory.
	if got := msgByte(9, -8, ssa.OpLoad32U); got == 1 {
		t.Error("the strerror literal carries rc 1 — a drop would try to free .rodata")
	}
}

// An errno the table has no text for is rendered rather than dropped, digits
// and all: the path that builds a string at run time instead of naming a
// literal.
func TestAsmRunIoErrorUnknownErrno(t *testing.T) {
	const errno = 4000
	want := "Unknown error 4000"
	read := func(off int64, kind ssa.OpKind) int {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		box := callPtrOp(f, e, "__fern_io_error", constOp(f, e, errno), constStr(f, e, ""))
		msg := loadMem(f, e, box, 16, ssa.OpLoad)
		f.SetRet(e, loadMem(f, e, msg, off, kind))
		return assembleRunModule(t, map[string]*ssa.Func{"main": f}, "main", 8, nil)
	}
	if got := read(-4, ssa.OpLoad32U); got != len(want) {
		t.Fatalf("message length = %d, want %d (%q)", got, len(want), want)
	}
	var sb strings.Builder
	for i := range want {
		sb.WriteByte(byte(read(int64(i), ssa.OpLoad8U)))
	}
	if sb.String() != want {
		t.Errorf("message = %q, want %q", sb.String(), want)
	}
}

// removeAll runs remove_dir_all(path) and returns the Result tag: 0 = Ok,
// 1 = Err.
func removeAll(t *testing.T, path string) int {
	t.Helper()
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	res := callPtrOp(f, e, "remove_dir_all", constStr(f, e, path))
	f.SetRet(e, loadMem(f, e, res, 0, ssa.OpLoad32U))
	return assembleRunModule(t, map[string]*ssa.Func{"main": f}, "main", 8, nil)
}

// The whole tree goes, not just the top: a nested directory with files at two
// depths is what separates a real recursion from an rmdir that happened to
// succeed on an empty directory.
func TestAsmRunRemoveDirAllTree(t *testing.T) {
	root := filepath.Join(t.TempDir(), "tree")
	if err := os.MkdirAll(filepath.Join(root, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		filepath.Join(root, "top.txt"),
		filepath.Join(root, "a", "mid.txt"),
		filepath.Join(root, "a", "b", "deep.txt"),
	} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := removeAll(t, root); got != 0 {
		t.Fatalf("tag = %d, want 0 (Ok)", got)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("%s still exists: %v", root, err)
	}
}

// A plain file is unlinked — the ENOTDIR arm — and a path that was never there
// is a silent success, as os.RemoveAll has it.
func TestAsmRunRemoveDirAllFileAndMissing(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "one.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := removeAll(t, file); got != 0 {
		t.Fatalf("file: tag = %d, want 0 (Ok)", got)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("%s still exists: %v", file, err)
	}
	if got := removeAll(t, filepath.Join(dir, "never-existed")); got != 0 {
		t.Errorf("missing path: tag = %d, want 0 (Ok) — a missing tree is already removed", got)
	}
}

// The sibling of an empty directory case: an empty tree is removed by the
// rmdir at the end of the walk, with nothing to recurse into.
func TestAsmRunRemoveDirAllEmptyDir(t *testing.T) {
	root := filepath.Join(t.TempDir(), "empty")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := removeAll(t, root); got != 0 {
		t.Fatalf("tag = %d, want 0 (Ok)", got)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("%s still exists: %v", root, err)
	}
}

// An open failure that is neither ENOENT nor ENOTDIR is reported rather than
// swallowed: Err, carrying the IoError the errno maps to. A component past
// NAME_MAX is ENAMETOOLONG, which needs no privileges to provoke.
func TestAsmRunRemoveDirAllReportsOpenFailure(t *testing.T) {
	long := filepath.Join(t.TempDir(), strings.Repeat("n", 300))
	if got := removeAll(t, long); got != 1 {
		t.Fatalf("tag = %d, want 1 (Err) — an open failure must be reported", got)
	}

	// The payload is a real IoError box: ENAMETOOLONG (36) has no variant of
	// its own, so it arrives as Other with its strerror text.
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	res := callPtrOp(f, e, "remove_dir_all", constStr(f, e, long))
	ioErr := loadMem(f, e, res, 8, ssa.OpLoad)
	f.SetRet(e, loadMem(f, e, ioErr, 0, ssa.OpLoad32U))
	if got := assembleRunModule(t, map[string]*ssa.Func{"main": f}, "main", 8, nil); got != 6 {
		t.Errorf("IoError tag = %d, want 6 (Other)", got)
	}
}
