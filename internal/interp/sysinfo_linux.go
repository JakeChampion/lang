//go:build linux

package interp

import "syscall"

// unameFields is the five `struct utsname` fields the compiled backends
// read out of uname(2), in the order the record holds them: sysname,
// nodename, release, version, machine. A refused uname answers five
// empty strings, as the backends do.
func unameFields() [5]string {
	var u syscall.Utsname
	if err := syscall.Uname(&u); err != nil {
		return [5]string{}
	}
	return [5]string{
		utsString(u.Sysname[:]),
		utsString(u.Nodename[:]),
		utsString(u.Release[:]),
		utsString(u.Version[:]),
		utsString(u.Machine[:]),
	}
}

// utsString is one NUL-terminated field of the record as a string.
func utsString(f []int8) string {
	b := make([]byte, 0, len(f))
	for _, c := range f {
		if c == 0 {
			break
		}
		b = append(b, byte(c))
	}
	return string(b)
}
