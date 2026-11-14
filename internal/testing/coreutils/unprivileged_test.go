package coreutils

import (
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// A case with `unprivileged` set runs as the user nobody when the test runs
// as root. Root reads a directory of mode 000, so a test host that runs as
// root does not see the errors that a normal user sees. When the test does
// not run as root, the case runs as the user that runs the test.

// nobody gives the user and the group of nobody.
func nobody(t *testing.T) (int, int) {
	t.Helper()
	u, err := user.Lookup("nobody")
	if err != nil {
		t.Fatalf("an unprivileged case runs as nobody, and there is no such user: %v", err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		t.Fatal(err)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		t.Fatal(err)
	}
	return uid, gid
}

// handOver gives each file of the tree that root owns to nobody, and lets
// nobody search the temporary directories above the tree. Other owners
// stay, because a case can test them.
func handOver(t *testing.T, dir string) {
	t.Helper()
	if os.Geteuid() != 0 {
		return
	}
	uid, gid := nobody(t)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		var st syscall.Stat_t
		if err := syscall.Lstat(p, &st); err != nil {
			return err
		}
		owner, group := -1, -1
		if st.Uid == 0 {
			owner = uid
		}
		if st.Gid == 0 {
			group = gid
		}
		if owner < 0 && group < 0 {
			return nil
		}
		return os.Lchown(p, owner, group)
	})
	if err != nil {
		t.Fatalf("give %s to nobody: %v", dir, err)
	}
	openAbove(t, dir)
}

// openAbove lets others search each directory above path that is inside
// the temporary directory, so that nobody can reach path.
func openAbove(t *testing.T, path string) {
	t.Helper()
	tmp := filepath.Clean(os.TempDir())
	for d := filepath.Dir(filepath.Clean(path)); strings.HasPrefix(d, tmp+string(filepath.Separator)); d = filepath.Dir(d) {
		fi, err := os.Stat(d)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm()&0o001 == 0 {
			if err := os.Chmod(d, fi.Mode().Perm()|0o001); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// asNobody runs the command as nobody, with no other groups, when the test
// runs as root.
func asNobody(t *testing.T, cmd *exec.Cmd, bin string) {
	t.Helper()
	if os.Geteuid() != 0 {
		return
	}
	uid, gid := nobody(t)
	openAbove(t, bin)
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Credential = &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid), Groups: []uint32{}}
}
