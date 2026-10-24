package interp

import "os"

type rawStat struct {
	mode                               uint32
	nlink, uid, gid                    uint32
	dev, rdev, ino, blksize, blocks    int64
	atime, atimeNsec, mtime, mtimeNsec int64
	ctime, ctimeNsec                   int64
	btime, btimeNsec                   int64
}

type statOrigin struct {
	path   string
	follow bool
	file   *os.File
}
