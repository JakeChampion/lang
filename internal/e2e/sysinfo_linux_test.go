//go:build linux

package e2e

import (
	"syscall"
	"testing"
)

// hostUtsname is the five utsname fields as the host reports them, in
// the order the record holds them.
func hostUtsname(t *testing.T) [5]string {
	t.Helper()
	var u syscall.Utsname
	if err := syscall.Uname(&u); err != nil {
		t.Fatalf("uname: %v", err)
	}
	str := func(f []int8) string {
		b := make([]byte, 0, len(f))
		for _, c := range f {
			if c == 0 {
				break
			}
			b = append(b, byte(c))
		}
		return string(b)
	}
	return [5]string{str(u.Sysname[:]), str(u.Nodename[:]), str(u.Release[:]), str(u.Version[:]), str(u.Machine[:])}
}