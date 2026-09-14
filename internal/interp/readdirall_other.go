//go:build !linux && !darwin

package interp

import "os"

// readDirAll on a platform whose directory reader `syscall` does not
// expose in raw form. It does not report `.` and `..` separately, so
// the honest answer is the list read_dir gives — which is what the
// WASI preview-2 backend answers with for the same reason.
func readDirAll(path string) ([]string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	return names, nil
}
