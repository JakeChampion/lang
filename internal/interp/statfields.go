package interp

import "os"

// rawStat is the `stat(2)` record behind an `os.FileInfo`, in the shape
// `FileStat` exposes: whole seconds plus a nanosecond remainder for each
// timestamp, and every id widened to the largest type any supported
// platform uses. `statFields` fills it per GOOS, since the field NAMES of
// `syscall.Stat_t` differ (`Atim` on Linux, `Atimespec` on Darwin) even
// where the meaning does not.
//
// A platform with no `syscall.Stat_t` at all leaves everything but `mode`
// zero, which is the same contract the WASI backends carry: a field the
// host does not report reads zero, and a zero `mode` (S_IFMT of 0, which
// no real file has) is how a caller tells "not reported" from "reported".
type rawStat struct {
	mode                               uint32
	nlink, uid, gid                    uint32
	dev, rdev, ino, blksize, blocks    int64
	atime, atimeNsec, mtime, mtimeNsec int64
	ctime, ctimeNsec                   int64
	btime, btimeNsec                   int64
}

// statOrigin names what an os.FileInfo was read from, for the one field
// the FileInfo cannot carry on Linux: the birth time, which only statx(2)
// reports. Either a path (followed or not) or an open file.
type statOrigin struct {
	path   string
	follow bool
	file   *os.File
}
