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

// ParseVersion parses "MAJOR.MINOR.PATCH". Each part must be a plain run of
// decimal digits: the signed spellings strconv.Atoi also accepts ("1.+2.3",
// "1.-0.3") are rejected here, matching manifest.isVersion. Admitting them
// let an index key parse as a version whose String() spelled it differently,
// so the entry could never be looked up again.
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

// isDigits reports whether s is a non-empty run of ASCII decimal digits.
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
