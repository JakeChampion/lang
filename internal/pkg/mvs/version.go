package mvs

import (
	"fmt"
	"strconv"
	"strings"
)

type Version struct{ Major, Minor, Patch int }

func ParseVersion(s string) (Version, error) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("invalid version %q (want MAJOR.MINOR.PATCH)", s)
	}
	var v Version
	for i, dst := range []*int{&v.Major, &v.Minor, &v.Patch} {
		if !isDigits(parts[i]) {
			return Version{}, fmt.Errorf("invalid version %q", s)
		}
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return Version{}, fmt.Errorf("invalid version %q", s)
		}
		*dst = n
	}
	return v, nil
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (v Version) String() string { return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch) }

func (v Version) Compare(o Version) int {
	for _, d := range [][2]int{{v.Major, o.Major}, {v.Minor, o.Minor}, {v.Patch, o.Patch}} {
		if d[0] != d[1] {
			if d[0] < d[1] {
				return -1
			}
			return 1
		}
	}
	return 0
}
