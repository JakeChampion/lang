//go:build !linux && !darwin

package interp

import "os"

// readDirAll on a platform whose directory reader `syscall` does not
// expose in raw form. It does not report `.` and `..` separately, so
// the honest answer is the list read_dir gives — which is what the
// WASI preview-2 backend answers with for the same reason. Nor does it
// hand back an inode, so every `ino` is 0, as on preview 2.
func readDirAll(path string) ([]dirent, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	out := make([]dirent, len(entries))
	for i, e := range entries {
		out[i] = dirent{name: e.Name()}
	}
	return out, nil
}
