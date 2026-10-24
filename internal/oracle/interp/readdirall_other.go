//go:build !linux && !darwin

package interp

import "os"

func readDirAll(path string) ([]dirent, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	out := make([]dirent, len(entries))
	for i, e := range entries {
		out[i] = dirent{name: e.Name(), kind: modeKind(e.Type())}
	}
	return out, nil
}

func modeKind(m os.FileMode) uint32 {
	switch {
	case m&os.ModeDir != 0:
		return 0o040000
	case m&os.ModeSymlink != 0:
		return 0o120000
	case m&os.ModeNamedPipe != 0:
		return 0o010000
	case m&os.ModeSocket != 0:
		return 0o140000
	case m&os.ModeCharDevice != 0:
		return 0o020000
	case m&os.ModeDevice != 0:
		return 0o060000
	case m.IsRegular():
		return 0o100000
	}
	return 0
}
