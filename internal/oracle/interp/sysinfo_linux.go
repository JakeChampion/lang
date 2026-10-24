//go:build linux

package interp

import "syscall"

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
