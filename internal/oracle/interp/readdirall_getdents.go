//go:build linux || darwin

package interp

import (
	"os"
	"syscall"
	"unsafe"
)

var direntNameOffset = int(unsafe.Offsetof(syscall.Dirent{}.Name))

func readDirAll(path string) ([]dirent, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fd := int(f.Fd())
	buf := make([]byte, 64<<10)
	entries := []dirent{}
	for {
		n, err := syscall.ReadDirent(fd, buf)
		if err != nil {
			return nil, &os.PathError{Op: "readdirent", Path: path, Err: err}
		}
		if n <= 0 {
			return entries, nil
		}
		for off := 0; off < n; {
			d := (*syscall.Dirent)(unsafe.Pointer(&buf[off]))
			reclen := int(d.Reclen)
			if reclen <= direntNameOffset || off+reclen > n {
				return entries, nil
			}
			rec := buf[off+direntNameOffset : off+reclen]
			off += reclen
			if d.Ino == 0 {
				continue
			}
			end := 0
			for end < len(rec) && rec[end] != 0 {
				end++
			}
			entries = append(entries, dirent{name: string(rec[:end]), ino: uint64(d.Ino), kind: uint32(d.Type) << 12})
		}
	}
}
