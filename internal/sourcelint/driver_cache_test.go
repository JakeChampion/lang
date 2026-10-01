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
// .github/actions/selfhost-driver-cache carries the drivers between runs.
// This pins, over EVERY workflow rather than a list of them: that each job
// whose tests can build drivers restores the cache; that the jobs saving it
// are exactly the ones named below, one per runner OS and arch, with the
// matrix guard that picks the saving leg; and that the action keys the cache
// on everything the harness keys a driver on — a key missing one of those
// inputs would hand a job a driver built from other sources, and the tests
// would pass against the wrong compiler.
func TestDriverBuildingJobsRestoreTheDriverCache(t *testing.T) {
	// The savers: workflow file and job id, with the substring the job's
	// `save:` expression must carry. A matrix job counts once however many
	// legs it has, so the guard is what says which leg saves; without it an
	// inverted guard (every x86 leg saving, no ARM64 leg) leaves this test
	// green while two jobs race for one key and another is never written.
	savers := map[string]string{
		"test-e2e-selfhost.yml/driver-sizes": `save: "true"`,
		"test-e2e-selfhost.yml/test":         "matrix.host == 'aarch64' && matrix.shard == 0 && 'true'",
		"macos.yml/test-arm64-darwin":        "matrix.shard == 0 && 'true'",
	}
	seenSavers := map[string]bool{}
	header := regexp.MustCompile(`(?m)^  ([A-Za-z0-9_-]+):\s*$`)
	// A command starting with `go test` or `gotestsum` that names an e2e
	// package within its next lines, a matrix row handing one to such a
	// command, or the action that compiles the e2e test binaries. A path
	// quoted in an echo or a comment is not a test run.
	runsE2E := regexp.MustCompile(`(?m)^\s*(run: )?(go test|gotestsum)\b[\s\S]{0,400}?internal/e2e|pkg: \./internal/e2e|build-e2e-binaries`)
	checked := 0
	for _, file := range workflowFiles(t) {
		src := workflowSource(t, file)
		body, ok := topLevelBlock(src, "jobs")
		if !ok {
			continue
		}
		idx := header.FindAllStringSubmatchIndex(body, -1)
		for n, m := range idx {
			id := body[m[2]:m[3]]
			end := len(body)
			if n+1 < len(idx) {
				end = idx[n+1][0]
			}
			job := body[m[0]:end]
			// A job builds drivers when it runs the e2e packages (or their
			// prebuilt binaries) with the self-host sources present. The lanes
			// that delete those sources right after checkout cannot build one.
			if !runsE2E.MatchString(job) || strings.Contains(job, "drop-selfhost-sources") {
				continue
			}
			checked++
			key := file + "/" + id
			if strings.Contains(job, "FERN_SELFHOST_BUILD_CACHE:") {
				t.Errorf("%s: job %q sets FERN_SELFHOST_BUILD_CACHE by hand; the action exports it, and a hand-set path bypasses the restored cache", file, id)
			}
			if !strings.Contains(job, "- uses: "+driverCacheAction) {
				t.Errorf("%s: job %q runs the e2e packages with the self-host sources present but never restores the driver cache, so it pays every stage0 build from nothing", file, id)
				continue
			}
			saves := strings.Contains(job, "save: ")
			guard, isSaver := savers[key]
			switch {
			case saves && !isSaver:
				t.Errorf("%s: job %q saves the driver cache but is not a listed saver; one job per runner OS and arch saves, or the lane's shards each upload a near-identical directory per tree", file, id)
			case isSaver && !saves:
				t.Errorf("%s: job %q is the saver for its runner OS and arch but passes no `save:`; nothing writes that cache", file, id)
			case isSaver && !strings.Contains(job, guard):
				t.Errorf("%s: job %q saves, but its `save:` does not read %q — the guard is what picks the one leg that saves", file, id, guard)
			}
			if isSaver {
				seenSavers[key] = true
			}
		}
	}
	if checked < 10 {
		t.Fatalf("only %d driver-building job(s) found across the workflows; did the e2e lanes' shape change?", checked)
	}
	var missing []string
	for key := range savers {
		if !seenSavers[key] {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("listed savers not found as driver-building jobs: %v (renamed or no longer running e2e tests?)", missing)
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
