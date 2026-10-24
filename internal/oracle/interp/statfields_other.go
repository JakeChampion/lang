//go:build !linux && !darwin

package interp

import "os"

func statFields(info os.FileInfo, _ statOrigin) rawStat {
	return rawStat{mode: posixModeBits(info.Mode())}
}

func posixModeBits(m os.FileMode) uint32 {
	out := uint32(m.Perm())
	switch {
	case m&os.ModeDir != 0:
		out |= 0o040000
	case m&os.ModeSymlink != 0:
		out |= 0o120000
	case m&os.ModeNamedPipe != 0:
		out |= 0o010000
	case m&os.ModeSocket != 0:
		out |= 0o140000
	case m&os.ModeCharDevice != 0:
		out |= 0o020000
	case m&os.ModeDevice != 0:
		out |= 0o060000
	default:
		out |= 0o100000
	}
	if m&os.ModeSetuid != 0 {
		out |= 0o4000
	}
	if m&os.ModeSetgid != 0 {
		out |= 0o2000
	}
	if m&os.ModeSticky != 0 {
		out |= 0o1000
	}
	return out
}
