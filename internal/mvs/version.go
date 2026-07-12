// Package mvs implements Minimum Version Selection (Cox/Go) over a
// version index, and the fern.lock it pins — the version-resolution
// layer of the package-management design (docs/PACKAGE-MANAGEMENT-SOTA.md,
// the resolution row). MVS's defining property: the constraint language
// is minimum-version-only, so a satisfying configuration always exists,
// the result is the unique lattice minimum, and resolution is
// deterministic graph reachability — no solver, no conflict-explanation
// UX debt. A dependency declares `dep = "1.2.0"` (its LOWEST acceptable
// version); MVS keeps, per package, the maximum of the declared minimums
// across the whole transitive graph.
package mvs

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a MAJOR.MINOR.PATCH release version (no ranges, no
// pre-release — the deterministic subset MVS operates on).
type Version struct{ Major, Minor, Patch int }

// ParseVersion parses "MAJOR.MINOR.PATCH".
func ParseVersion(s string) (Version, error) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("invalid version %q (want MAJOR.MINOR.PATCH)", s)
	}
	var v Version
	for i, dst := range []*int{&v.Major, &v.Minor, &v.Patch} {
		n, err := strconv.Atoi(parts[i])
		if err != nil || n < 0 {
			return Version{}, fmt.Errorf("invalid version %q", s)
		}
		*dst = n
	}
	return v, nil
}

func (v Version) String() string { return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch) }

// Compare returns -1, 0, +1 by precedence.
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

// Max returns the greater of two versions.
func Max(a, b Version) Version {
	if a.Compare(b) >= 0 {
		return a
	}
	return b
}
