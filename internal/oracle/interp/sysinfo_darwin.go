//go:build darwin

package interp

import "syscall"

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
