package x86_64ssa

// Every runtime-helper entry must be 16-byte aligned. See emitRuntimeHelpers
// for why: the alignment of `__fern_rc_inc` and its siblings is worth 2x on an
// rc-heavy program, and without this gate it is decided by the byte length of
// whatever helper happens to be emitted before them.

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

func TestRuntimeHelperEntriesAreAligned(t *testing.T) {
	names := make([]string, 0, len(runtimeHelperEmitters))
	for name := range runtimeHelperEmitters {
		names = append(names, name)
	}
	sort.Strings(names)

	var b strings.Builder
	w := func(format string, args ...any) {
		fmt.Fprintf(&b, format, args...)
		b.WriteByte('\n')
	}
	emitRuntimeHelpers(w, names)

	want := map[string]bool{}
	for _, name := range names {
		want[fnLabel(name)+":"] = true
	}

	lines := strings.Split(b.String(), "\n")
	seen := 0
	for i, line := range lines {
		if !want[strings.TrimSpace(line)] {
			continue
		}
		seen++
		// Blank lines between the directive and the label are fine: the
		// assembler pads at the directive, so the label still lands on the
		// boundary. Anything else in between is not.
		aligned := false
		for j := i - 1; j >= 0; j-- {
			prev := strings.TrimSpace(lines[j])
			if prev == "" {
				continue
			}
			aligned = prev == ".p2align 4"
			break
		}
		if !aligned {
			t.Errorf("helper entry %s is not preceded by `.p2align 4`", strings.TrimSpace(line))
		}
	}
	if seen != len(names) {
		t.Errorf("found %d helper entry labels, expected %d — the label spelling moved and this gate stopped looking at anything", seen, len(names))
	}
}
