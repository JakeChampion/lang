package sourcelint

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const driverCacheAction = "./.github/actions/selfhost-driver-cache"

// The stage0 compiler builds a self-host driver in 90-300 s, and a job that
// builds its drivers from nothing is that many minutes before its first test.
// .github/actions/selfhost-driver-cache carries the drivers between runs; this
// pins that every job whose tests build drivers restores it, that each lane
// has exactly one job saving it, and that the action keys the cache on
// everything the harness keys a driver on — a key missing one of those inputs
// would hand a job a driver built from other sources, and the tests would pass
// against the wrong compiler.
func TestDriverBuildingJobsRestoreTheDriverCache(t *testing.T) {
	// Lane file -> the jobs that save, one per runner OS and arch the lane
	// builds drivers on (the cache key is per OS and arch, so an arch nobody
	// saves for restores nothing). Every job in the file that calls setup-fern
	// (or, on macOS, installs the toolchain) builds drivers or runs tests that
	// may, so every one of them restores.
	lanes := map[string][]string{
		"test-e2e-selfhost.yml": {"driver-sizes", "test"},
		"test-e2e-other.yml":    nil,
		"test-e2e-arm64.yml":    nil,
		"test-e2e-wasm.yml":     nil,
		"macos.yml":             {"test-arm64-darwin"},
	}
	for file, want := range lanes {
		src := workflowSource(t, file)
		var savers []string
		// Per job, textually: the jobs block split on job headers.
		body, ok := topLevelBlock(src, "jobs")
		if !ok {
			t.Fatalf("%s has no jobs block", file)
		}
		header := regexp.MustCompile(`(?m)^  ([A-Za-z0-9_-]+):\s*$`)
		idx := header.FindAllStringSubmatchIndex(body, -1)
		for n, m := range idx {
			id := body[m[2]:m[3]]
			end := len(body)
			if n+1 < len(idx) {
				end = idx[n+1][0]
			}
			job := body[m[0]:end]
			buildsDrivers := strings.Contains(job, "./.github/actions/setup-fern") || strings.Contains(job, "jdx/mise-action")
			if !buildsDrivers {
				continue
			}
			if strings.Contains(job, `FERN_SELFHOST_BUILD_CACHE:`) {
				t.Errorf("%s: job %q sets FERN_SELFHOST_BUILD_CACHE by hand; the action exports it, and a hand-set path bypasses the restored cache", file, id)
			}
			if !strings.Contains(job, "- uses: "+driverCacheAction) {
				t.Errorf("%s: job %q builds or may build self-host drivers but never restores the driver cache, so it pays every stage0 build from nothing", file, id)
				continue
			}
			if strings.Contains(job, "save: \"true\"") || strings.Contains(job, "save: ${{") {
				savers = append(savers, id)
			}
		}
		sort.Strings(savers)
		if strings.Join(savers, ",") != strings.Join(want, ",") {
			t.Errorf("%s: the driver cache must be saved by exactly %v (one job per runner OS and arch), got %v; "+
				"a lane's other jobs only restore, or its shards each upload a near-identical directory per tree", file, want, savers)
		}
	}

	action, err := os.ReadFile(filepath.Join("..", "..", ".github", "actions", "selfhost-driver-cache", "action.yml"))
	if err != nil {
		t.Fatalf("read the driver cache action: %v", err)
	}
	src := string(action)
	for _, want := range []struct{ needle, why string }{
		{"hashFiles('bootstrap/stage0.lock', 'examples/self_host/**', 'internal/stdlib/**')",
			"the harness keys a driver on the stage0 binary, its source closure and the stdlib (driverCompilerKey); the cache key must cover the same inputs"},
		{"/*.driverbin", "only drivers are carried between runs; the linked test programs (*.bin) are per-test"},
		{"restore-keys:", "without a prefix fallback a tree with any self-host edit starts from nothing, though most of its drivers' closures did not change"},
		{"FERN_SELFHOST_BUILD_CACHE=", "the action is what points the harness at the restored directory"},
		{"-mtime +7", "a saved directory would otherwise accumulate every driver any tree ever built"},
		{"uses: actions/cache@", "the saving job needs the combined action, whose post step saves"},
		{"uses: actions/cache/restore@", "restore-only jobs must not save"},
	} {
		if !strings.Contains(src, want.needle) {
			t.Errorf("selfhost-driver-cache/action.yml no longer contains %q — %s", want.needle, want.why)
		}
	}
}
