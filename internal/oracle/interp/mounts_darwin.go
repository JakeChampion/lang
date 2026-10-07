//go:build darwin

package interp

import "syscall"

const mountTablePath = ""

// mountRows on Darwin is getfsstat(2), which fills one `struct statfs` per
// mount. `dev` is f_fsid's first word, which is the st_dev of every file on
// that filesystem.
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
			dev:    int64(st.Fsid.Val[0]),
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
