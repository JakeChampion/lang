package sourcelint

import (
	"regexp"
	"strings"
	"testing"
)

var perfLaneLabels = []string{
	"perf-${{ matrix.host }}",
	"append-cliff-x86_64",
	"selfhost-size-x86_64",
	"selfhost-alloc-x86_64",
	"profile-costs-x86_64",
	"certify-census-x86_64",
}

// recordedPerfLanes returns the LANE of each LANE REPORT pair that a
// `perf-history record` call in src names, line continuations included.
func recordedPerfLanes(src string) []string {
	src = strings.ReplaceAll(src, "\\\n", " ")
	word := regexp.MustCompile(`(?:\$\{\{[^}]*\}\}|"[^"]*"|[^\s"])+`)
	var lanes []string
	for _, m := range regexp.MustCompile(`perf-history record ([^\n]*)`).FindAllStringSubmatch(src, -1) {
		for i, w := range word.FindAllString(m[1], -1) {
			if i%2 == 0 {
				lanes = append(lanes, w)
			}
		}
	}
	return lanes
}

func TestPerfLaneLabelsAreStable(t *testing.T) {
	recorded := recordedPerfLanes(workflowSource(t, "perf.yml"))
	if len(recorded) == 0 {
		t.Fatal("perf.yml records no perf history at all — the trend is gone")
	}
	seen := map[string]bool{}
	for _, l := range recorded {
		seen[l] = true
	}
	known := map[string]bool{}
	for _, label := range perfLaneLabels {
		known[label] = true
		if !seen[label] {
			t.Errorf("perf.yml no longer records the %q lane. If the metric is gone, "+
				"drop it from perfLaneLabels; if it was renamed, rename it back — "+
				"refs/notes/perf is keyed on this string and the old history does not "+
				"follow", label)
		}
	}
	for _, l := range recorded {
		if !known[l] {
			t.Errorf("perf.yml records an unknown lane %q — add it to perfLaneLabels "+
				"so the name is pinned like the others", l)
		}
	}
}
