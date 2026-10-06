//go:build darwin

package e2ecompiler

import (
	"syscall"
	"testing"
)

func hostFsTypeName(t *testing.T, path string) string {
	t.Helper()
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		t.Fatal(err)
	}
	var name []byte
	for _, value := range st.Fstypename {
		if value == 0 {
			break
		}
		name = append(name, byte(value))
	}
	return string(name)
}
