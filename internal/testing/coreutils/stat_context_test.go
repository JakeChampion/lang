//go:build linux

package coreutils

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestStatFileContext pins `%C` against the host's own answer rather than the
// oracle's. CI's GNU is built --without-selinux, where getfilecon is gnulib's
// stub and every file is ENOTSUP; a libselinux build (any distribution's)
// reads `security.selinux`, which is what this build does. So the expected
// output comes from lgetxattr(2) itself: the label up to its NUL on an
// SELinux host, and `?` with the errno's text everywhere else.
func TestStatFileContext(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "f")
	if err := os.WriteFile(f, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("f", filepath.Join(dir, "l")); err != nil {
		t.Fatal(err)
	}
	label, errText := hostFileContext(t, f)
	bin := fernBin(t, "stat")
	for _, c := range []struct {
		args []string
		out  string
	}{
		{[]string{"-c", "%C", "f"}, label},
		{[]string{"-c", "[%10C]", "f"}, "[" + strings.Repeat(" ", max(0, 10-len(label))) + label + "]"},
		{[]string{"-L", "-c", "%n|%C|%s", "l"}, "l|" + label + "|3"},
	} {
		cmd := exec.Command(bin, c.args...)
		cmd.Dir = dir
		var stdout, stderr strings.Builder
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		if got := strings.TrimSuffix(stdout.String(), "\n"); got != c.out {
			t.Errorf("stat %q: stdout %q, want %q", c.args, got, c.out)
		}
		if errText == "" {
			if err != nil || stderr.Len() != 0 {
				t.Errorf("stat %q: %v, stderr %q; want a clean run", c.args, err, stderr.String())
			}
			continue
		}
		name := c.args[len(c.args)-1]
		want := "failed to get security context of '" + name + "': " + errText
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stat %q: stderr %q, want it to say %q", c.args, stderr.String(), want)
		}
		if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
			t.Errorf("stat %q: %v, want exit 1", c.args, err)
		}
	}
}

// hostFileContext is what getfilecon(3) answers for path: the label up to
// its NUL, or "?" and the errno's text.
func hostFileContext(t *testing.T, path string) (string, string) {
	t.Helper()
	buf := make([]byte, 4096)
	n, err := syscall.Getxattr(path, "security.selinux", buf)
	switch {
	case err == syscall.ENODATA:
		return "?", "No data available"
	case err == syscall.ENOTSUP:
		return "?", "Operation not supported"
	case err != nil:
		t.Skipf("getxattr security.selinux: %v", err)
	}
	v := string(buf[:n])
	if i := strings.IndexByte(v, 0); i >= 0 {
		v = v[:i]
	}
	if v == "" {
		return "?", "Operation not supported"
	}
	return v, ""
}
