//go:build darwin

package interp

import "syscall"

func fsStatFields(path string) (rawFsStat, error) {
	const (
		pcNameMax = 4
		pcPathMax = 5
	)
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return rawFsStat{}, err
	}
	nameMax, err := syscall.Pathconf(path, pcNameMax)
	if err != nil {
		return rawFsStat{}, err
	}
	pathMax, err := syscall.Pathconf(path, pcPathMax)
	if err != nil {
		return rawFsStat{}, err
	}
	var typeName []byte
	for _, value := range st.Fstypename {
		if value == 0 {
			break
		}
		typeName = append(typeName, byte(value))
	}
	return rawFsStat{
		blockSize:   int64(st.Bsize),
		blocks:      int64(st.Blocks),
		blocksFree:  int64(st.Bfree),
		blocksAvail: int64(st.Bavail),
		files:       int64(st.Files),
		filesFree:   int64(st.Ffree),
		nameMax:     int64(nameMax),
		pathMax:     int64(pathMax),
		fsType:      int64(st.Type),
		fsid:        fsidWord(st.Fsid.Val),
		fragSize:    int64(st.Bsize),
		fsTypeName:  string(typeName),
	}, nil
}
