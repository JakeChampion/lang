//go:build linux

package interp

import "os"

const mountTablePath = "/proc/self/mountinfo"

func mountRows() ([]rawMount, error) {
	text, err := os.ReadFile(mountTablePath)
	if err != nil {
		return nil, err
	}
	return parseMountinfo(string(text)), nil
}
