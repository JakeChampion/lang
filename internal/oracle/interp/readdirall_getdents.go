//go:build linux || darwin

package interp

import (
	"os"
	"syscall"
	"unsafe"
)

// direntNameOffset is where d_name sits within one directory record.
// Linux's getdents64 packs d_ino, d_off, d_reclen and d_type ahead of
// it; XNU's getdirentries64 packs d_ino, d_seekoff, d_reclen, d_namlen
// and d_type, so the two do not agree and the offset is read from the
// platform's own struct.
var direntNameOffset = int(unsafe.Offsetof(syscall.Dirent{}.Name))

// readDirAll lists every name a directory holds — `.` and `..`
// included — in the order the kernel reports them.
//
// `os.ReadDir` and `File.Readdirnames` both drop the dot entries, and
// the first also sorts, so neither can answer this. The raw drain is
// getdents, whose records `syscall.ParseDirent` would walk except that
// it applies the same filter, so the record walk is here.
func readDirAll(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fd := int(f.Fd())
	buf := make([]byte, 64<<10)
	names := []string{}
	for {
		n, err := syscall.ReadDirent(fd, buf)
		if err != nil {
			return nil, &os.PathError{Op: "readdirent", Path: path, Err: err}
		}
		if n <= 0 {
			return names, nil
		}
		for off := 0; off < n; {
			d := (*syscall.Dirent)(unsafe.Pointer(&buf[off]))
			reclen := int(d.Reclen)
			if reclen <= direntNameOffset || off+reclen > n {
				return names, nil
			}
			rec := buf[off+direntNameOffset : off+reclen]
			off += reclen
			// A record the filesystem has vacated reads back with a
			// zero inode; glibc's readdir and Go's own both skip it.
			if d.Ino == 0 {
				continue
			}
			end := 0
			for end < len(rec) && rec[end] != 0 {
				end++
			}
			names = append(names, string(rec[:end]))
		}
	}
}
