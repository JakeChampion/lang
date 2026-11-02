//go:build darwin || linux

package interp

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestOpenAtFileDirectoryIdentity(t *testing.T) {
	for _, tc := range []struct {
		name  string
		entry string
		flags int
		write string
		want  string
	}{
		{"read", "file", os.O_RDONLY, "", "original"},
		{"write", "file", os.O_WRONLY, "changed!", "changed!"},
		{"truncate", "file", os.O_WRONLY | os.O_TRUNC, "x", "x"},
		{"create", "new", os.O_WRONLY | os.O_CREATE | os.O_EXCL, "created", "created"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path, moved := filepath.Join(root, "dir"), filepath.Join(root, "moved")
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(path, "file"), []byte("original"), 0o600); err != nil {
				t.Fatal(err)
			}
			dir, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer dir.Close()
			if err := os.Rename(path, moved); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(path, "file"), []byte("replacement"), 0o600); err != nil {
				t.Fatal(err)
			}
			file, err := openAtFile(dir, filepath.Join(path, tc.entry), tc.entry, tc.flags|oNofollow)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			fdFlags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, file.Fd(), syscall.F_GETFD, 0)
			if errno != 0 || fdFlags&syscall.FD_CLOEXEC == 0 {
				t.Errorf("descriptor flags = %d, %v, want close-on-exec", fdFlags, errno)
			}
			if tc.write == "" {
				got, err := io.ReadAll(file)
				if err != nil || string(got) != tc.want {
					t.Fatalf("read = %q, %v, want %q", got, err, tc.want)
				}
			} else if _, err := file.WriteString(tc.write); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(filepath.Join(moved, tc.entry))
			if err != nil || string(got) != tc.want {
				t.Errorf("original directory file = %q, %v, want %q", got, err, tc.want)
			}
			got, err = os.ReadFile(filepath.Join(path, "file"))
			if err != nil || string(got) != "replacement" {
				t.Errorf("replacement directory file changed: %q, %v", got, err)
			}
			if tc.entry == "new" {
				if _, err := os.Stat(filepath.Join(path, tc.entry)); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("created an entry in the replacement directory: %v", err)
				}
			}
		})
	}
}

func TestOpenAtFileRefusals(t *testing.T) {
	path := t.TempDir()
	dir, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	if err := os.Symlink("missing", filepath.Join(path, "link")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		want error
	}{
		{"link", syscall.ELOOP},
		{"missing", syscall.ENOENT},
		{"bad\x00name", syscall.EINVAL},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file, err := openAtFile(dir, filepath.Join(path, tc.name), tc.name, os.O_RDONLY|oNofollow)
			if file != nil {
				file.Close()
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("error = %v, want %v", err, tc.want)
			}
		})
	}
}
