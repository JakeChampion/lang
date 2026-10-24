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
}

func TestPerfLaneLabelsAreStable(t *testing.T) {
	src := workflowSource(t, "perf.yml")

	for _, label := range perfLaneLabels {
		if !strings.Contains(src, "perf-history record "+label+" ") {
			t.Errorf("perf.yml no longer records the %q lane. If the metric is gone, "+
				"drop it from perfLaneLabels; if it was renamed, rename it back — "+
				"refs/notes/perf is keyed on this string and the old history does not "+
				"follow", label)
		}
	}

	recorded := regexp.MustCompile(
		`perf-history record ((?:\$\{\{[^}]*\}\}|[^\s"])+)`).FindAllStringSubmatch(src, -1)
	if len(recorded) == 0 {
		t.Fatal("perf.yml records no perf history at all — the trend is gone")
	}
	known := map[string]bool{}
	for _, l := range perfLaneLabels {
		known[l] = true
	}
	for _, m := range recorded {
		if !known[m[1]] {
			t.Errorf("perf.yml records an unknown lane %q — add it to perfLaneLabels "+
				"so the name is pinned like the others", m[1])
		}
	}
}
