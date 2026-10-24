//go:build !linux

package interp

import (
	"os"
	"syscall"
)

func accessEffective(path string, mode int) error {
	const (
		xOK = 1
		wOK = 2
		rOK = 4
	)
	info, err := os.Stat(path)
	if err != nil {
		if pe, ok := err.(*os.PathError); ok {
			return pe.Err
		}
		return err
	}
	if mode == 0 {
		return nil
	}
	st := statFields(info, statOrigin{})
	euid, egid := os.Geteuid(), os.Getegid()
	perm := st.mode & 0o777

	if euid == 0 {
		if mode&xOK != 0 && !info.IsDir() && perm&0o111 == 0 {
			return syscall.EACCES
		}
		return nil
	}

	var granted uint32
	switch {
	case uint32(euid) == st.uid:
		granted = (perm >> 6) & 7
	case uint32(egid) == st.gid || inSupplementaryGroup(st.gid):
		granted = (perm >> 3) & 7
	default:
		granted = perm & 7
	}

	var want uint32
	if mode&rOK != 0 {
		want |= 4
	}
	if mode&wOK != 0 {
		want |= 2
	}
	if mode&xOK != 0 {
		want |= 1
	}
	if granted&want != want {
		return syscall.EACCES
	}
	return nil
}

func inSupplementaryGroup(gid uint32) bool {
	gids, err := os.Getgroups()
	if err != nil {
		return false
	}
	for _, g := range gids {
		if uint32(g) == gid {
			return true
		}
	}
	return false
}
