package e2eharness

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"testing"
	"testing/fstest"
)

func status(ppid string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte("Name:\tsleep\nState:\tS (sleeping)\nPid:\t1\nPPid:\t" + ppid + "\nTracerPid:\t0\n")}
}

func TestChildrenByPPid(t *testing.T) {
	proc := fstest.MapFS{
		"1/status":    status("0"),
		"9/status":    status("7"),
		"100/status":  status("7"),
		"12/status":   status("8"),
		"self/status": status("7"),
		"13/stat":     {Data: []byte("13 (gone) Z 7")},
	}
	got, err := childrenByPPid(proc, 7)
	if err != nil {
		t.Fatal(err)
	}
	// 13 exited between the listing and the read; self is not a pid.
	if want := []string{"9", "100"}; !slices.Equal(got, want) {
		t.Errorf("children of 7: %q, want %q", got, want)
	}
}

func TestChildrenByPPidRefusesBadRecords(t *testing.T) {
	for name, proc := range map[string]fs.FS{
		"no PPid line":    fstest.MapFS{"5/status": {Data: []byte("Name:\tx\nPid:\t5\n")}},
		"non-numeric":     fstest.MapFS{"5/status": status("seven")},
		"unreadable read": unreadable{fstest.MapFS{"5/status": status("7")}},
	} {
		if got, err := childrenByPPid(proc, 7); err == nil {
			t.Errorf("%s: children %q, want an error", name, got)
		}
	}
}

// unreadable fails every status read the way a permission error does.
type unreadable struct{ fstest.MapFS }

func (u unreadable) ReadFile(name string) ([]byte, error) {
	return nil, &fs.PathError{Op: "read", Path: name, Err: fs.ErrPermission}
}

func (u unreadable) Open(name string) (fs.File, error) {
	if name == "." {
		return u.MapFS.Open(name)
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: errors.New("permission denied")}
}

// The PPid scan finds this process's real children on the running kernel,
// and agrees with the children file where the kernel provides one.
func TestChildPIDsOnTheKernel(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc")
	}
	var want []string
	for i := 0; i < 2; i++ {
		cmd := exec.Command("sleep", "30")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
		want = append(want, strconv.Itoa(cmd.Process.Pid))
	}
	scanned, err := childrenByPPid(os.DirFS("/proc"), os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	listed, err := ChildPIDs(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	for _, pid := range want {
		if !slices.Contains(scanned, pid) || !slices.Contains(listed, pid) {
			t.Errorf("child %s: scanned %q, listed %q", pid, scanned, listed)
		}
	}
	if _, err := ChildPIDs(1 << 30); err == nil {
		t.Error("a parent that does not exist has children, want an error")
	}
}
