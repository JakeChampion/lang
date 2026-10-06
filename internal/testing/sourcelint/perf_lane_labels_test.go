package sourcelint

import (
	"regexp"
	"strings"
	"testing"
)

// The perf lane labels that key the note blocks under refs/notes/perf.
//
// `scripts/perf-history show METRIC` reads a metric back across the last 50
// main commits by matching `# lane: <label>`, so a renamed label does not move
// a history — it starts an empty one beside the old, and the trend the lane
// exists to show goes quiet with nothing reporting that it has.
//
// The risk is live because these labels no longer sit one-per-job: three of
// them moved into one step when the self-host measurements merged into a single
// job, where a label is just an argument in a shell line and nothing else names
// it. Adding a metric adds a label here; renaming one is the thing to stop.
// `bench` names its lane from the matrix, so its label is the expression as
// written; the note it produces is per host.
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

	// Every label the workflow records has to be one of the known set, so a new
	// metric is a deliberate addition here rather than an unremarked one.
	// A label is one argument, but it may be a `${{ … }}` expression containing
	// spaces — `(\S+)` stops at the first one and reports a truncated name.
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
