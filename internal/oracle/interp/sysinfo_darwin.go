//go:build darwin

package interp

import "syscall"

// unameFields is the five utsname fields as Darwin reports them. There
// is no uname(2) there — libc builds the record from these five sysctl
// names, and so does the arm64-darwin backend.
func unameFields() [5]string {
	var out [5]string
	for i, name := range [5]string{"kern.ostype", "kern.hostname", "kern.osrelease", "kern.version", "hw.machine"} {
		v, err := syscall.Sysctl(name)
		if err != nil {
			continue
		}
		out[i] = v
	}
	return out
}
