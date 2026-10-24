package coreutils

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"syscall"
	"testing"
)

func cpBasic(t *testing.T, dir string) {
	t.Helper()
	seedWrite(t, dir, "f1", "hello\n")
	seedWrite(t, dir, "f2", "world!!\n")
	if err := os.Chmod(filepath.Join(dir, "f2"), 0o741); err != nil {
		t.Fatalf("chmod f2: %v", err)
	}
	seedMkdir(t, dir, "d1/sub")
	seedWrite(t, dir, "d1/a", "one\n")
	seedWrite(t, dir, "d1/sub/s1", "sub\n")
	seedMkdir(t, dir, "d2")
	seedSymlink(t, dir, "f1", "sl")
	seedSymlink(t, dir, "nowhere", "dangle")
	seedHardlink(t, dir, "f1", "hard")
}

func cpOne(t *testing.T, dir string) {
	t.Helper()
	seedWrite(t, dir, "f1", "hello\n")
}

func cpSelf(t *testing.T, dir string) {
	t.Helper()
	seedMkdir(t, dir, "empty")
	seedMkdir(t, dir, "full")
	seedWrite(t, dir, "full/x", "X\n")
	seedMkdir(t, dir, "taken/taken")
}

func cpLinks(t *testing.T, dir string) {
	t.Helper()
	seedWrite(t, dir, "f1", "hello\n")
	seedSymlink(t, dir, "f1", "sl")
	seedSymlink(t, dir, "nowhere", "dangle")
	seedMkdir(t, dir, "tree")
	seedWrite(t, dir, "tree/t", "T\n")
	seedSymlink(t, dir, "t", "tree/ln")
	seedSymlink(t, dir, "nowhere", "tree/bad")
	seedSymlink(t, dir, "tree", "dl")
}

func cpModes(t *testing.T, dir string) {
	t.Helper()
	seedWrite(t, dir, "src", "S\n")
	if err := os.Chmod(filepath.Join(dir, "src"), 0o777); err != nil {
		t.Fatalf("chmod src: %v", err)
	}
	seedWrite(t, dir, "taken", "T\n")
	if err := os.Chmod(filepath.Join(dir, "taken"), 0o700); err != nil {
		t.Fatalf("chmod taken: %v", err)
	}
	seedMkdir(t, dir, "sd")
	seedWrite(t, dir, "sd/x", "X\n")
	if err := os.Chmod(filepath.Join(dir, "sd"), 0o777); err != nil {
		t.Fatalf("chmod sd: %v", err)
	}
	seedMkdir(t, dir, "sdtaken")
	if err := os.Chmod(filepath.Join(dir, "sdtaken"), 0o700); err != nil {
		t.Fatalf("chmod sdtaken: %v", err)
	}
	seedTouch(t, dir, "src", 1400000000, 123456789)
}

func cpUpdate(t *testing.T, dir string) {
	t.Helper()
	seedWrite(t, dir, "old", "OLD\n")
	seedWrite(t, dir, "new", "NEW\n")
	seedWrite(t, dir, "same", "SAME\n")
	seedWrite(t, dir, "dest", "DEST\n")
	seedTouch(t, dir, "dest", 1500000000, 200000000)
	seedTouch(t, dir, "old", 1400000000, 0)
	seedTouch(t, dir, "new", 1600000000, 0)
	seedTouch(t, dir, "same", 1500000000, 200000000)
}

func cpBackup(t *testing.T, dir string) {
	t.Helper()
	seedWrite(t, dir, "a", "A\n")
	seedWrite(t, dir, "b", "B\n")
}

func cpBackupTaken(t *testing.T, dir string) {
	t.Helper()
	cpBackup(t, dir)
	seedWrite(t, dir, "b~", "OLD\n")
	seedWrite(t, dir, "b.~1~", "ONE\n")
	seedWrite(t, dir, "b.~2~", "TWO\n")
}

func cpSparse(t *testing.T, dir string) {
	t.Helper()
	path := filepath.Join(dir, "sp")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create sp: %v", err)
	}
	if err := f.Truncate(1 << 20); err != nil {
		t.Fatalf("truncate sp: %v", err)
	}
	if _, err := f.WriteAt([]byte("end"), (1<<20)-3); err != nil {
		t.Fatalf("write sp tail: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close sp: %v", err)
	}
	seedWrite(t, dir, "dense", "abcdefghij")
	mixed, err := os.Create(filepath.Join(dir, "mixed"))
	if err != nil {
		t.Fatalf("create mixed: %v", err)
	}
	block := strings.Repeat("A", 4096) + strings.Repeat("\x00", 8192) + strings.Repeat("B", 4096)
	if _, err := mixed.WriteString(block); err != nil {
		t.Fatalf("write mixed: %v", err)
	}
	if _, err := mixed.WriteAt([]byte("tail"), 1<<20); err != nil {
		t.Fatalf("write mixed tail: %v", err)
	}
	if err := mixed.Truncate(2 << 20); err != nil {
		t.Fatalf("truncate mixed: %v", err)
	}
	if err := mixed.Close(); err != nil {
		t.Fatalf("close mixed: %v", err)
	}
}

func cpGroup(t *testing.T, dir string) {
	t.Helper()
	seedMkdir(t, dir, "lk")
	seedWrite(t, dir, "lk/one", "shared\n")
	seedHardlink(t, dir, "lk/one", "lk/two")
	seedWrite(t, dir, "f1", "hello\n")
	seedHardlink(t, dir, "f1", "hard")
	seedMkdir(t, dir, "d2")
}

func cpFifo(t *testing.T, dir string) {
	t.Helper()
	seedFifo(t, filepath.Join(dir, "fifo"))
	seedWrite(t, dir, "f1", "hello\n")
	seedMkdir(t, dir, "hold")
	seedFifo(t, filepath.Join(dir, "hold/inner"))
	seedWrite(t, dir, "hold/plain", "P\n")
}

func cpSlash(t *testing.T, dir string) {
	t.Helper()
	seedMkdir(t, dir, "d1/sub")
	seedWrite(t, dir, "d1/a", "A\n")
	seedWrite(t, dir, "ff", "F\n")
	seedMkdir(t, dir, "dst")
	seedSymlink(t, dir, "nowhere/deeper", "dangledest")
}

func init() {
	registerCorpus("cp", cpCases)
}

func cpWalk(t *testing.T, dir string) {
	t.Helper()
	seedMkdir(t, dir, "chain/sub")
	seedWrite(t, dir, "chain/sub/leaf", "l\n")
	seedMkdir(t, dir, "nest/hole")
	seedMkdir(t, dir, "empty")
	seedMkdir(t, dir, "part/sub")
	seedWrite(t, dir, "part/sub/old", "o\n")
}

func cpCases(t *testing.T) []invocation {
	t.Helper()
	var out []invocation

	for _, c := range []struct {
		name string
		args []string
	}{
		{"no-operands", nil},
		{"one-operand", []string{"f1"}},
		{"missing-after-dashdash", []string{"--", "f1"}},
		{"target-not-a-directory", []string{"f1", "f1", "f1"}},
		{"target-missing", []string{"f1", "f1", "nosuchdir"}},
		{"t-not-a-directory", []string{"-t", "f1", "f1"}},
		{"t-and-T", []string{"-t", "d1", "-T", "f1"}},
		{"T-extra-operand", []string{"-T", "f1", "f1", "out"}},
		{"source-missing", []string{"nosuch", "out"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: cpOne})
	}

	for _, pre := range []string{"--a", "--c", "--co", "--d", "--n", "--no", "--p", "--r", "--re", "--s", "--v", "--ver", "--zzz", "--x", "--Z"} {
		out = append(out, invocation{name: "prefix" + pre, args: []string{pre, "f1", "out"}, seedTree: cpBasic})
	}
	out = append(out,
		invocation{name: "unknown-short", args: []string{"-k", "f1", "out"}, seedTree: cpBasic},
		invocation{name: "prefix-unique-backup", args: []string{"--b", "f1", "f2"}, seedTree: cpBasic},
		invocation{name: "prefix-unique-target", args: []string{"--ta", "d2", "f1"}, seedTree: cpBasic},
		invocation{name: "prefix-unique-strip", args: []string{"--st", "f1", "out"}, seedTree: cpBasic},
	)

	for _, c := range []struct {
		name string
		args []string
	}{
		{"bad-backup", []string{"--backup=zz", "f1", "f2"}},
		{"bad-preserve", []string{"--preserve=zz", "f1", "out"}},
		{"bad-no-preserve", []string{"--no-preserve=zz", "f1", "out"}},
		{"bad-update", []string{"--update=zz", "f1", "f2"}},
		{"bad-sparse", []string{"--sparse=zz", "f1", "out"}},
		{"bad-reflink", []string{"--reflink=zz", "f1", "out"}},
		{"ambiguous-sparse-value", []string{"--sparse", "a", "f1"}},
		{"prefix-sparse-value", []string{"--sparse=al", "f1", "out"}},
		{"prefix-reflink-value", []string{"--reflink=a", "f1", "out"}},
		{"prefix-update-value", []string{"--update=o", "f1", "f2"}},
		{"missing-no-preserve-arg", []string{"--no-preserve", "f1", "out"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: cpBasic})
	}

	for _, c := range []struct {
		name string
		args []string
	}{
		{"file-to-new", []string{"f1", "out"}},
		{"file-over-file", []string{"f1", "f2"}},
		{"file-into-directory", []string{"f1", "d2"}},
		{"file-into-directory-slash", []string{"f1", "d2/"}},
		{"two-into-directory", []string{"f1", "f2", "d2"}},
		{"target-directory", []string{"-t", "d2", "f1", "f2"}},
		{"target-directory-glued", []string{"-td2", "f1"}},
		{"target-directory-long", []string{"--target-directory=d2", "f1"}},
		{"target-directory-dashdash", []string{"-t", "d2", "--", "f1"}},
		{"no-target-directory", []string{"-T", "f1", "out"}},
		{"no-target-directory-over-dir", []string{"-T", "d1", "out"}},
		{"same-file", []string{"f1", "f1"}},
		{"same-file-dotted", []string{"f1", "./f1"}},
		{"same-file-hardlink", []string{"f1", "hard"}},
		{"directory-without-r", []string{"d1", "out"}},
		{"directory-without-r-into-dir", []string{"d1", "d2"}},
		{"recursive", []string{"-r", "d1", "out"}},
		{"recursive-upper", []string{"-R", "d1", "out"}},
		{"recursive-long", []string{"--recursive", "d1", "out"}},
		{"recursive-into-directory", []string{"-r", "d1", "d2"}},
		{"recursive-over-file", []string{"-r", "d1", "f1"}},
		{"recursive-to-missing-parent", []string{"-r", "d1", "d2/x/y"}},
		{"verbose", []string{"-v", "f1", "out"}},
		{"verbose-into-directory", []string{"-v", "f1", "f2", "d2"}},
		{"clustered", []string{"-rp", "d1", "out"}},
		{"clustered-upper", []string{"-Rp", "d1", "out"}},
		{"dashdash", []string{"--", "f1", "out"}},
		{"link", []string{"-l", "f1", "out"}},
		{"link-recursive", []string{"-l", "-r", "d1", "out"}},
		{"symbolic-link", []string{"-s", "f1", "out"}},
		{"attributes-only-new", []string{"--attributes-only", "f1", "out"}},
		{"attributes-only-existing", []string{"--attributes-only", "f1", "f2"}},
		{"remove-destination", []string{"--remove-destination", "f1", "f2"}},
		{"remove-destination-verbose", []string{"-v", "--remove-destination", "f1", "f2"}},
		{"remove-destination-verbose-new", []string{"-v", "--remove-destination", "f1", "out"}},
		{"force-verbose", []string{"-v", "-f", "f1", "f2"}},
		{"one-file-system", []string{"-x", "-r", "d1", "out"}},
		{"selinux-Z", []string{"-Z", "f1", "out"}},
		{"selinux-context", []string{"--context", "f1", "out"}},
		{"selinux-context-value", []string{"--context=x", "f1", "out"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: cpBasic})
	}

	for _, c := range []struct {
		name string
		args []string
	}{
		{"self-empty", []string{"-r", "empty", "empty"}},
		{"self-empty-verbose", []string{"-rv", "empty", "empty"}},
		{"self-taken", []string{"-r", "taken", "taken"}},
		{"self-taken-verbose", []string{"-rv", "taken", "taken"}},
		{"self-into-subdir", []string{"-r", "full", "full/x"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: cpSelf})
	}

	for _, c := range []struct {
		name string
		args []string
	}{
		{"link-default", []string{"sl", "out"}},
		{"link-P", []string{"-P", "sl", "out"}},
		{"link-d", []string{"-d", "sl", "out"}},
		{"link-H", []string{"-H", "sl", "out"}},
		{"link-L", []string{"-L", "sl", "out"}},
		{"dangling-default", []string{"dangle", "out"}},
		{"dangling-P", []string{"-P", "dangle", "out"}},
		{"dangling-L", []string{"-L", "dangle", "out"}},
		{"link-r-default", []string{"-r", "sl", "out"}},
		{"link-r-P", []string{"-rP", "sl", "out"}},
		{"link-r-H", []string{"-rH", "sl", "out"}},
		{"link-r-L", []string{"-rL", "sl", "out"}},
		{"tree-r-default", []string{"-r", "tree", "out"}},
		{"tree-r-H", []string{"-rH", "tree", "out"}},
		{"tree-r-L", []string{"-rL", "tree", "out"}},
		{"tree-r-P", []string{"-rP", "tree", "out"}},
		{"tree-archive", []string{"-a", "tree", "out"}},
		{"dirlink-r-default", []string{"-r", "dl", "out"}},
		{"dirlink-r-H", []string{"-rH", "dl", "out"}},
		{"dirlink-r-L", []string{"-rL", "dl", "out"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: cpLinks})
	}

	for _, c := range []struct {
		name string
		args []string
	}{
		{"mode-fresh", []string{"src", "plain"}},
		{"mode-existing", []string{"src", "taken"}},
		{"mode-existing-preserve-timestamps", []string{"--preserve=timestamps", "src", "taken"}},
		{"mode-existing-preserve-ownership", []string{"--preserve=ownership", "src", "taken"}},
		{"mode-existing-preserve-mode", []string{"--preserve=mode", "src", "taken"}},
		{"mode-existing-preserve-all", []string{"--preserve=all", "src", "taken"}},
		{"mode-existing-attributes-only", []string{"--attributes-only", "src", "taken"}},
		{"mode-existing-attributes-only-p", []string{"--attributes-only", "-p", "src", "taken"}},
		{"mode-dir-existing-timestamps", []string{"-rT", "--preserve=timestamps", "sd", "sdtaken"}},
		{"mode-dir-existing-plain", []string{"-rT", "sd", "sdtaken"}},
		{"mode-dir-existing-p", []string{"-rT", "-p", "sd", "sdtaken"}},
		{"mode-dir-existing-ownership", []string{"-rT", "--preserve=ownership", "sd", "sdtaken"}},
		{"mode-fresh-p", []string{"-p", "src", "plain"}},
		{"mode-existing-p", []string{"-p", "src", "taken"}},
		{"mode-preserve-mode", []string{"--preserve=mode", "src", "plain"}},
		{"mode-preserve-timestamps", []string{"--preserve=timestamps", "src", "plain"}},
		{"mode-preserve-all", []string{"--preserve=all", "src", "plain"}},
		{"mode-preserve-bare", []string{"--preserve", "src", "plain"}},
		{"mode-no-preserve-mode", []string{"-p", "--no-preserve=mode", "src", "plain"}},
		{"mode-no-preserve-all", []string{"-a", "--no-preserve=all", "sd", "out"}},
		{"mode-dir-fresh", []string{"-r", "sd", "out"}},
		{"mode-dir-p", []string{"-rp", "sd", "out"}},
		{"mode-dir-archive", []string{"-a", "sd", "out"}},
		{"mode-remove-destination", []string{"--remove-destination", "src", "taken"}},
		{"mode-attributes-only", []string{"--attributes-only", "src", "plain"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: cpModes})
	}

	for _, c := range []struct {
		name  string
		args  []string
		stdin string
	}{
		{"interactive-yes", []string{"-i", "old", "dest"}, "y\n"},
		{"interactive-no", []string{"-i", "old", "dest"}, "n\n"},
		{"interactive-eof", []string{"-i", "old", "dest"}, ""},
		{"interactive-upper", []string{"-i", "old", "dest"}, "Y\n"},
		{"interactive-word", []string{"-i", "old", "dest"}, "yes\n"},
		{"interactive-other", []string{"-i", "old", "dest"}, "q\n"},
		{"interactive-verbose", []string{"-iv", "old", "dest"}, "y\n"},
		{"interactive-then-force", []string{"-i", "-f", "old", "dest"}, ""},
		{"force-then-interactive", []string{"-f", "-i", "old", "dest"}, "n\n"},
	} {
		out = append(out, invocation{name: c.name, args: c.args, stdin: c.stdin, seedTree: cpUpdate})
	}
	for _, c := range []struct {
		name string
		args []string
	}{
		{"no-clobber", []string{"-n", "old", "dest"}},
		{"update-none-fail", []string{"--update=none-fail", "old", "dest"}},
		{"update-none-fail-verbose", []string{"--update=none-fail", "-v", "old", "dest"}},
		{"update-none-fail-nothing-there", []string{"--update=none-fail", "-v", "old", "gone"}},
		{"debug-names-a-skip", []string{"--debug", "-n", "old", "dest"}},
		{"debug-names-an-update-none-skip", []string{"--debug", "--update=none", "old", "dest"}},
		{"backup-and-update-none-conflict", []string{"-b", "--update=none", "old", "dest"}},
		{"backup-and-update-none-fail-conflict", []string{"-b", "--update=none-fail", "old", "dest"}},
		{"backup-and-update-all-do-not", []string{"-bv", "--update=all", "old", "dest"}},
		{"suffix-turns-the-backup-conflict-on", []string{"-S", ".x", "-n", "old", "dest"}},
		{"no-clobber-long", []string{"--no-clobber", "old", "dest"}},
		{"update-none", []string{"--update=none", "old", "dest"}},
		{"update-all", []string{"--update=all", "old", "dest"}},
		{"update-older-older", []string{"-u", "old", "dest"}},
		{"update-older-newer", []string{"-u", "new", "dest"}},
		{"update-older-same", []string{"-u", "same", "dest"}},
		{"update-bare-long", []string{"--update", "old", "dest"}},
		{"update-verbose-skip", []string{"-uv", "same", "dest"}},
		{"update-verbose-copy", []string{"-uv", "new", "dest"}},
		{"no-clobber-then-interactive", []string{"-n", "-i", "old", "dest"}},
		{"interactive-then-no-clobber", []string{"-i", "-n", "old", "dest"}},
		{"backup-with-no-clobber", []string{"-b", "-n", "old", "dest"}},
		{"no-clobber-with-backup", []string{"-n", "-b", "old", "dest"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: cpUpdate})
	}

	for _, c := range []struct {
		name string
		args []string
		env  []string
	}{
		{name: "backup-simple", args: []string{"-b", "a", "b"}},
		{name: "backup-verbose", args: []string{"-vb", "a", "b"}},
		{name: "backup-long", args: []string{"--backup", "a", "b"}},
		{name: "backup-suffix", args: []string{"-b", "-S", ".bak", "a", "b"}},
		{name: "backup-suffix-long", args: []string{"--suffix=.bak", "a", "b"}},
		{name: "backup-suffix-empty", args: []string{"-S", "", "a", "b"}},
		{name: "backup-suffix-slash", args: []string{"-S", "x/", "a", "b"}},
		{name: "backup-numbered", args: []string{"--backup=numbered", "a", "b"}},
		{name: "backup-t", args: []string{"--backup=t", "a", "b"}},
		{name: "backup-existing", args: []string{"--backup=existing", "a", "b"}},
		{name: "backup-nil", args: []string{"--backup=nil", "a", "b"}},
		{name: "backup-simple-word", args: []string{"--backup=simple", "a", "b"}},
		{name: "backup-never", args: []string{"--backup=never", "a", "b"}},
		{name: "backup-none", args: []string{"--backup=none", "a", "b"}},
		{name: "backup-off", args: []string{"--backup=off", "a", "b"}},
		{name: "backup-env-control", args: []string{"-b", "a", "b"}, env: []string{"VERSION_CONTROL=numbered"}},
		{name: "backup-env-suffix", args: []string{"-b", "a", "b"}, env: []string{"SIMPLE_BACKUP_SUFFIX=.env"}},
		{name: "backup-env-bogus", args: []string{"-b", "a", "b"}, env: []string{"VERSION_CONTROL=bogus"}},
		{name: "backup-env-unused", args: []string{"a", "b"}, env: []string{"VERSION_CONTROL=bogus"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, env: c.env, seedTree: cpBackup})
	}
	for _, c := range []struct {
		name string
		args []string
	}{
		{"backup-existing-taken", []string{"--backup=existing", "a", "b"}},
		{"backup-numbered-taken", []string{"--backup=numbered", "a", "b"}},
		{"backup-simple-taken", []string{"--backup=simple", "a", "b"}},
		{"backup-default-taken", []string{"-b", "a", "b"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: cpBackupTaken})
	}

	for _, c := range []struct {
		name string
		args []string
	}{
		{"sparse-default", []string{"sp", "out"}},
		{"sparse-auto", []string{"--sparse=auto", "sp", "out"}},
		{"sparse-never", []string{"--sparse=never", "sp", "out"}},
		{"sparse-always", []string{"--sparse=always", "sp", "out"}},
		{"sparse-preserve", []string{"-p", "sp", "out"}},
		{"sparse-dense-default", []string{"dense", "out"}},
		{"sparse-dense-always", []string{"--sparse=always", "dense", "out"}},
		{"sparse-into-directory", []string{"sp", "dense", "out"}},
		{"sparse-written-zeros-auto", []string{"mixed", "out"}},
		{"sparse-written-zeros-always", []string{"--sparse=always", "mixed", "out"}},
		{"sparse-written-zeros-never", []string{"--sparse=never", "mixed", "out"}},
	} {
		inv := invocation{name: c.name, args: c.args, seedTree: cpSparse, sparse: true}
		if c.name == "sparse-into-directory" {
			inv.seedTree = func(t *testing.T, dir string) {
				cpSparse(t, dir)
				seedMkdir(t, dir, "out")
			}
		}
		out = append(out, inv)
	}

	for _, c := range []struct {
		name string
		args []string
	}{
		{"links-plain", []string{"-r", "lk", "out"}},
		{"links-preserve", []string{"-r", "--preserve=links", "lk", "out"}},
		{"links-archive", []string{"-a", "lk", "out"}},
		{"links-d", []string{"-rd", "lk", "out"}},
		{"links-two-operands", []string{"--preserve=links", "f1", "hard", "d2"}},
		{"links-two-operands-plain", []string{"f1", "hard", "d2"}},
		{"links-hardlink-option", []string{"-l", "f1", "hard", "d2"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: cpGroup})
	}

	for _, c := range []struct {
		name string
		args []string
	}{
		{"fifo-recursive", []string{"-r", "fifo", "out"}},
		{"fifo-recursive-upper", []string{"-R", "fifo", "out"}},
		{"fifo-in-tree", []string{"-r", "hold", "out"}},
		{"fifo-in-tree-archive", []string{"-a", "hold", "out"}},
		{"fifo-preserve", []string{"-rp", "fifo", "out"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: cpFifo})
	}

	for _, c := range []struct {
		name string
		args []string
	}{
		{"parents-flat", []string{"--parents", "ff", "dst"}},
		{"parents-nested", []string{"--parents", "d1/a", "dst"}},
		{"parents-deep", []string{"--parents", "d1/sub", "dst"}},
		{"parents-recursive", []string{"--parents", "-r", "d1/sub", "dst"}},
		{"parents-verbose", []string{"--parents", "-v", "d1/a", "dst"}},
		{"parents-not-a-directory", []string{"--parents", "ff", "ff"}},
		{"parents-missing-directory", []string{"--parents", "ff", "nosuchdir"}},
		{"parents-target-directory", []string{"--parents", "-t", "dst", "d1/a"}},
		{"parents-with-T", []string{"--parents", "-T", "ff", "out"}},
		{"slash-directory-no-r", []string{"d1/", "out"}},
		{"slash-directory-r", []string{"-r", "d1/", "out"}},
		{"slash-directory-into", []string{"-r", "d1/", "dst"}},
		{"slash-file", []string{"ff/", "out"}},
		{"strip-directory-no-r", []string{"--strip-trailing-slashes", "d1/", "out"}},
		{"strip-directory-into", []string{"--strip-trailing-slashes", "-r", "d1/", "dst"}},
		{"strip-file-direct", []string{"--strip-trailing-slashes", "ff/", "out"}},
		{"strip-file-into", []string{"--strip-trailing-slashes", "ff/", "dst"}},
		{"strip-target-directory", []string{"--strip-trailing-slashes", "-t", "dst", "ff/"}},
		{"no-strip-target-directory", []string{"-t", "dst", "ff/"}},
		{"slash-missing-source", []string{"-r", "nosuch/", "dst"}},
		{"dangling-dest", []string{"ff", "dangledest"}},
		{"dangling-dest-force", []string{"-f", "ff", "dangledest"}},
		{"dangling-dest-remove-destination", []string{"--remove-destination", "ff", "dangledest"}},
		{"strip-missing-source", []string{"--strip-trailing-slashes", "-r", "nosuch/", "dst"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: cpSlash})
	}

	for _, c := range []struct {
		name string
		args []string
	}{
		{"reflink-auto", []string{"--reflink=auto", "f1", "out"}},
		{"reflink-never", []string{"--reflink=never", "f1", "out"}},
		{"reflink-always", []string{"--reflink=always", "f1", "out"}},
		{"reflink-bare", []string{"--reflink", "f1", "out"}},
		{"reflink-auto-recursive", []string{"--reflink=auto", "-r", "d1", "out"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: cpBasic})
	}

	for _, c := range []struct {
		name    string
		args    []string
		fixture func(*testing.T, string)
	}{
		{"verbose-recursive", []string{"-v", "-r", "d1", "out"}, cpBasic},
		{"clustered-verbose", []string{"-vrp", "d1", "out"}, cpBasic},
		{"reflink-always-recursive", []string{"--reflink=always", "-r", "d1", "out"}, cpBasic},
		{"fifo-in-tree-verbose", []string{"-rv", "hold", "out"}, cpFifo},
		{"strip-directory-verbose", []string{"--strip-trailing-slashes", "-vr", "d1/", "dst"}, cpSlash},
		{"symbolic-link-recursive", []string{"-s", "-r", "d1", "out"}, cpBasic},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: c.fixture, unordered: true})
	}

	for _, c := range []struct {
		name string
		args []string
	}{
		{"walk-into-nested", []string{"-v", "-r", "nest", "nest/hole"}},
		{"walk-into-nested-plain", []string{"-r", "nest", "nest/hole"}},
		{"walk-into-nested-archive", []string{"-a", "-v", "nest", "nest/hole"}},
		{"walk-into-nested-then-operand", []string{"-v", "-r", "nest", "chain", "nest/hole"}},
		{"walk-chain-into-directory", []string{"-v", "-r", "chain", "empty"}},
		{"walk-dir-line-on-creation", []string{"-v", "-r", "-T", "chain", "empty"}},
		{"walk-dir-line-not-on-existing", []string{"-v", "-r", "-T", "chain", "part"}},
		{"walk-dir-line-archive", []string{"-a", "-v", "-T", "chain", "part"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: cpWalk})
	}

	for _, c := range []struct {
		name string
		args []string
	}{
		{"order-copy-then-failure", []string{"-v", "f1", "nosuch", "d2"}},
		{"order-failure-then-copy", []string{"-v", "nosuch", "f1", "d2"}},
		{"order-between-two-copies", []string{"-v", "f1", "nosuch", "f2", "d2"}},
		{"order-parents", []string{"--parents", "-v", "d1/a", "nosuch", "d2"}},
		{"order-no-verbose", []string{"f1", "nosuch", "d2"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: cpBasic, merged: true})
	}

	for _, c := range []struct {
		name string
		args []string
	}{
		{"order-recursive", []string{"-v", "-r", "chain", "nosuch", "empty"}},
		{"order-into-itself", []string{"-v", "-r", "nest", "nest/hole"}},
		{"order-into-itself-then-operand", []string{"-v", "-r", "nest", "chain", "nest/hole"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: cpWalk, merged: true})
	}

	return out
}

func TestCpParity(t *testing.T) {
	requireParity(t, "cp", cpCases(t))
}

func TestCpHelpVersion(t *testing.T) {
	requireHelp(t, "cp", []string{"--help"}, 0)
	requireHelp(t, "cp", []string{"--hel"}, 0)
	requireHelp(t, "cp", []string{"--help", "a", "b"}, 0)
	requireHelp(t, "cp", []string{"a", "b", "--help"}, 0)
	requireHelp(t, "cp", []string{"-r", "--help"}, 0)
	requireVersion(t, "cp", []string{"--version"}, 0)
	requireVersion(t, "cp", []string{"--vers"}, 0)
	requireVersion(t, "cp", []string{"a", "b", "--version"}, 0)
	requireVersion(t, "cp", []string{"-p", "--version", "-r"}, 0)
}

func runCpIn(t *testing.T, bin, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Args = append([]string{"cp"}, args...)
	cmd.Dir = dir
	cmd.Env = []string{"LC_ALL=C", "PATH=/usr/bin:/bin"}
	out, err := cmd.CombinedOutput()
	var exit int
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("run %s %v: %v", bin, args, err)
		}
		exit = ee.ExitCode()
	}
	return fmt.Sprintf("exit %d\n%s", exit, out)
}

func TestCpProcFile(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("/proc is Linux's")
	}
	want, err := os.ReadFile("/proc/version")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if got := runCpIn(t, fernBin(t, "cp"), dir, "/proc/version", "out"); got != "exit 0\n" {
		t.Fatalf("cp /proc/version printed %q", got)
	}
	got, err := os.ReadFile(filepath.Join(dir, "out"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("cp /proc/version wrote %q, want %q", got, want)
	}
}

func inodeOrder(t *testing.T, dir string) []string {
	t.Helper()
	des, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	type ent struct {
		name string
		ino  uint64
	}
	var es []ent
	for _, de := range des {
		info, err := de.Info()
		if err != nil {
			t.Fatalf("stat %s/%s: %v", dir, de.Name(), err)
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			t.Fatalf("no stat for %s/%s", dir, de.Name())
		}
		es = append(es, ent{de.Name(), st.Ino})
	}
	sort.Slice(es, func(i, j int) bool { return es[i].ino < es[j].ino })
	var names []string
	for _, e := range es {
		names = append(names, e.name)
	}
	return names
}

func readdirOrder(t *testing.T, dir string) []string {
	t.Helper()
	f, err := os.Open(dir)
	if err != nil {
		t.Fatalf("open %s: %v", dir, err)
	}
	defer f.Close()
	names, err := f.Readdirnames(-1)
	if err != nil {
		t.Fatalf("readdirnames %s: %v", dir, err)
	}
	return names
}

func seedWalkOrderTree(t *testing.T, dir string) []string {
	t.Helper()
	var filler []string
	for i := 0; i < 12; i++ {
		filler = append(filler, fmt.Sprintf("f%02d", i))
	}
	for i, n := range filler {
		if i == 2 || i == 9 {
			seedMkdir(t, dir, n)
			seedWrite(t, dir, n+"/inner", "i\n")
			continue
		}
		seedWrite(t, dir, n, n+"\n")
	}
	kept := append([]string(nil), filler[6:]...)
	for _, n := range filler[:6] {
		if err := os.RemoveAll(filepath.Join(dir, n)); err != nil {
			t.Fatalf("remove %s: %v", n, err)
		}
	}
	for i := 0; i < 6; i++ {
		n := fmt.Sprintf("a%02d", i)
		if i%2 == 1 {
			n = fmt.Sprintf("z%02d", i)
		}
		if i == 3 {
			seedMkdir(t, dir, n)
			seedWrite(t, dir, n+"/inner", "i\n")
		} else {
			seedWrite(t, dir, n, n+"\n")
		}
		kept = append(kept, n)
	}
	return kept
}

func requireDistinctOrders(t *testing.T, dir string, byInode, byReaddir []string) {
	t.Helper()
	if strings.Join(byInode, "\x00") == strings.Join(byReaddir, "\x00") {
		t.Fatalf("%s handed its entries back in inode order (%v), so the case cannot tell the two walks apart — the fixture needs a different construction on this filesystem", dir, byReaddir)
	}
	if sort.StringsAreSorted(byInode) {
		t.Fatalf("%s numbered its entries in name order (%v), so the case cannot tell inode order from sorted names", dir, byInode)
	}
}

func TestCpWalkOrder(t *testing.T) {
	gnu := referenceBin(t, "cp")
	ours := fernBin(t, "cp")
	dir := t.TempDir()

	seedMkdir(t, dir, "src")
	seedWalkOrderTree(t, filepath.Join(dir, "src"))

	srcDir := filepath.Join(dir, "src")
	want := inodeOrder(t, srcDir)
	requireDistinctOrders(t, srcDir, want, readdirOrder(t, srcDir))

	var lines []string
	for _, name := range want {
		lines = append(lines, fmt.Sprintf("'src/%s' -> 'out/%s'", name, name))
	}

	for _, side := range []struct {
		label string
		bin   string
	}{{"gnu", gnu}, {"fern", ours}} {
		if err := os.RemoveAll(filepath.Join(dir, "out")); err != nil {
			t.Fatalf("clear out: %v", err)
		}
		got := runCpIn(t, side.bin, dir, "-v", "-r", "src", "out")
		top := map[string]bool{}
		for _, name := range want {
			top["'src/"+name+"'"] = true
		}
		var seen []string
		for _, ln := range strings.Split(got, "\n") {
			src, _, ok := strings.Cut(ln, " -> ")
			if ok && top[src] {
				seen = append(seen, ln)
			}
		}
		if strings.Join(seen, "\n") != strings.Join(lines, "\n") {
			t.Errorf("%s did not walk src by inode\n want: %v\n  got: %v", side.label, lines, seen)
		}
	}
}

func TestCpIntoItself(t *testing.T) {
	gnu := referenceBin(t, "cp")
	ours := fernBin(t, "cp")

	for _, c := range []struct {
		name string
		args []string
		made []string
	}{
		{"flat", []string{"-v", "-r", "src", "src"}, []string{"src/src"}},
		{"nested", []string{"-v", "-r", "src", "src/bdir"}, []string{"src/bdir/src"}},
		{"nested-plain", []string{"-r", "src", "src/bdir"}, []string{"src/bdir/src"}},
		{"nested-archive", []string{"-a", "-v", "src", "src/bdir"}, []string{"src/bdir/src"}},
		{"then-operand", []string{"-v", "-r", "src", "other", "src/bdir"}, []string{"src/bdir/src", "src/bdir/other"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			seedMkdir(t, dir, "src/adir")
			seedWrite(t, dir, "src/adir/x", "x\n")
			seedMkdir(t, dir, "src/bdir")
			seedWrite(t, dir, "src/bdir/inner", "i\n")
			seedWrite(t, dir, "src/zfile", "z\n")
			seedMkdir(t, dir, "other")
			seedWrite(t, dir, "other/o", "o\n")
			original := treePaths(t, dir)
			entries := make(map[string]cpOracleEntry)
			contents := make(map[string]string)
			for _, p := range original {
				info, err := os.Stat(filepath.Join(dir, p))
				if err != nil {
					t.Fatal(err)
				}
				entries[p] = cpOracleEntry{ino: info.Sys().(*syscall.Stat_t).Ino, dir: info.IsDir()}
				if !info.IsDir() {
					data, err := os.ReadFile(filepath.Join(dir, p))
					if err != nil {
						t.Fatal(err)
					}
					contents[p] = string(data)
				}
			}

			restore := func() {
				for _, p := range c.made {
					if err := os.RemoveAll(filepath.Join(dir, p)); err != nil {
						t.Fatalf("restore %s: %v", p, err)
					}
				}
			}
			for _, side := range []struct{ name, bin string }{{"gnu", gnu}, {"fern", ours}} {
				restore()
				got := runCpIn(t, side.bin, dir, c.args...)
				target := c.made[0]
				info, err := os.Stat(filepath.Join(dir, target))
				if err != nil {
					t.Fatalf("%s did not create %s: %v", side.name, target, err)
				}
				entries[target] = cpOracleEntry{ino: info.Sys().(*syscall.Stat_t).Ino, dir: true}
				prefix, stopped := cpOraclePrefix(entries, "src", target)
				delete(entries, target)
				if !stopped {
					t.Fatal("fixture did not encounter its destination")
				}
				verbose := false
				for _, arg := range c.args {
					verbose = verbose || arg == "-v"
				}
				want := "exit 1\n"
				paths := append([]string(nil), original...)
				checkCopies := func(copies []cpOracleCopy) {
					for _, copy := range copies {
						if verbose {
							want += fmt.Sprintf("'%s' -> '%s'\n", copy.src, copy.dest)
						}
						paths = append(paths, copy.dest)
						if content, file := contents[copy.src]; file {
							data, err := os.ReadFile(filepath.Join(dir, copy.dest))
							if err != nil || string(data) != content {
								t.Errorf("%s copied %s incorrectly: %q, %v", side.name, copy.dest, data, err)
							}
						}
					}
				}
				checkCopies(prefix)
				want += fmt.Sprintf("cp: cannot copy a directory, 'src', into itself, '%s'\n", target)
				if len(c.made) == 2 {
					checkCopies([]cpOracleCopy{{"other", c.made[1]}, {"other/o", c.made[1] + "/o"}})
				}
				for path, content := range contents {
					data, err := os.ReadFile(filepath.Join(dir, path))
					if err != nil || string(data) != content {
						t.Errorf("%s changed source %s: %q, %v", side.name, path, data, err)
					}
				}
				sort.Strings(paths)
				if got != want {
					t.Errorf("%s output for cp %v\nwant: %q\n got: %q", side.name, c.args, want, got)
				}
				if gotTree := treePaths(t, dir); strings.Join(paths, "\n") != strings.Join(gotTree, "\n") {
					t.Errorf("%s copied the wrong inode-ordered prefix\nwant: %v\n got: %v", side.name, paths, gotTree)
				}
			}
		})
	}
}

type cpOracleEntry struct {
	ino uint64
	dir bool
}

type cpOracleCopy struct{ src, dest string }

func cpOraclePrefix(entries map[string]cpOracleEntry, source, destination string) ([]cpOracleCopy, bool) {
	var out []cpOracleCopy
	var visit func(string, string) bool
	visit = func(src, dest string) bool {
		if src == destination {
			return true
		}
		out = append(out, cpOracleCopy{src, dest})
		if !entries[src].dir {
			return false
		}
		var children []string
		for p := range entries {
			if filepath.Dir(p) == src {
				children = append(children, p)
			}
		}
		sort.Slice(children, func(i, j int) bool { return entries[children[i]].ino < entries[children[j]].ino })
		for _, p := range children {
			if visit(p, filepath.Join(dest, filepath.Base(p))) {
				return true
			}
		}
		return false
	}
	stopped := visit(source, destination)
	return out, stopped
}

func TestCpOraclePrefixUsesAllocatedDestinationInode(t *testing.T) {
	for _, tc := range []struct {
		name string
		ino  uint64
		want []cpOracleCopy
	}{
		{"before children", 5, []cpOracleCopy{{"src", "src/src"}}},
		{"between children", 15, []cpOracleCopy{{"src", "src/src"}, {"src/a", "src/src/a"}}},
		{"after children", 25, []cpOracleCopy{{"src", "src/src"}, {"src/a", "src/src/a"}, {"src/b", "src/src/b"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries := map[string]cpOracleEntry{"src": {dir: true}, "src/a": {ino: 10}, "src/b": {ino: 20}, "src/src": {ino: tc.ino, dir: true}}
			got, stopped := cpOraclePrefix(entries, "src", "src/src")
			if !stopped || !slices.Equal(got, tc.want) {
				t.Fatalf("prefix = %v, stopped=%v; want %v", got, stopped, tc.want)
			}
		})
	}
}

func treePaths(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel != "." {
			out = append(out, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(out)
	return out
}

func TestCpDebug(t *testing.T) {
	for _, src := range []string{"dense", "sp"} {
		t.Run(src, func(t *testing.T) {
			want := "copy offload: yes, reflink: unsupported, sparse detection: no"
			run := func(bin string) []string {
				dir := t.TempDir()
				cpSparse(t, dir)
				var st syscall.Stat_t
				if err := syscall.Stat(filepath.Join(dir, src), &st); err != nil {
					t.Fatal(err)
				}
				if st.Blocks*512 < st.Size {
					want = "copy offload: yes, reflink: unsupported, sparse detection: SEEK_HOLE"
				}
				if runtime.GOOS == "darwin" {
					want = "copy offload: no, reflink: yes, sparse detection: no"
				}
				return strings.Split(strings.TrimSuffix(runCpIn(t, bin, dir, "--debug", src, "out"), "\n"), "\n")
			}
			gnu := run(referenceBin(t, "cp"))
			ours := run(fernBin(t, "cp"))
			if len(ours) != 3 || len(gnu) != 3 {
				t.Fatalf("--debug printed\n gnu: %q\nfern: %q\nwant the exit line plus two", gnu, ours)
			}
			if ours[0] != gnu[0] || ours[1] != gnu[1] {
				t.Errorf("--debug differs from GNU before the report line\n gnu: %q\nfern: %q", gnu[:2], ours[:2])
			}
			if ours[2] != want {
				t.Errorf("--debug's report is %q, want %q", ours[2], want)
			}
		})
	}
}
