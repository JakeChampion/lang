//go:build !linux

package interp

import (
	"os"
	"syscall"
)

// accessEffective answers `access(path, mode)` against the EFFECTIVE
// user and group ids on a platform whose `syscall` package has no
// `Faccessat`. It is glibc's own `euidaccess` fallback, which is the
// same evaluation the kernel would do from the mode word: pick the
// owner / group / other triad the effective ids land in, and require
// every requested bit in it.
//
// What this cannot see is what the mode word does not encode — ACLs, a
// read-only mount, an immutable flag. `faccessat2` sees those, which is
// why the Linux build calls it rather than sharing this body.
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
	if mode == 0 { // F_OK — existence, which the stat just proved
		return nil
	}
	st := statFields(info)
	euid, egid := os.Geteuid(), os.Getegid()
	perm := st.mode & 0o777

	if euid == 0 {
		// Root reads and writes anything; it executes only what
		// somebody may execute.
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

// inSupplementaryGroup reports whether gid is one of the process's
// supplementary groups. A failure to read them is treated as "no",
// which can only ever make the answer stricter.
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
