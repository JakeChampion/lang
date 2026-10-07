//go:build darwin

package interp

import "syscall"

const mountTablePath = ""

// mountRows on Darwin is getfsstat(2), which fills one `struct statfs` per
// mount. f_fsid is not the st_dev a stat of the mount point reports, so `dev`
// stats the mount point, and is -1 when that fails.
func mountRows() ([]rawMount, error) {
	const mntNowait = 2
	n, err := syscall.Getfsstat(nil, mntNowait)
	if err != nil {
		return nil, err
	}
	buf := make([]syscall.Statfs_t, n)
	n, err = syscall.Getfsstat(buf, mntNowait)
	if err != nil {
		return nil, err
	}
	out := make([]rawMount, 0, n)
	for _, st := range buf[:n] {
		out = append(out, rawMount{
			source: cString(st.Mntfromname[:]),
			target: cString(st.Mntonname[:]),
			fstype: cString(st.Fstypename[:]),
			dev:    mountPointDev(cString(st.Mntonname[:])),
		})
	}
	return out, nil
}

func cString(b []int8) string {
	out := make([]byte, 0, len(b))
	for _, c := range b {
		if c == 0 {
			break
		}
		out = append(out, byte(c))
	}
	return string(out)
}

func mountPointDev(target string) int64 {
	var st syscall.Stat_t
	if err := syscall.Stat(target, &st); err != nil {
		return -1
	}
	return int64(st.Dev)
}
