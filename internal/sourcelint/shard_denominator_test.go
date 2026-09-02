package sourcelint

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// DIFF_ORACLE_SHARD is `I/N`: the differential test claims `seed % N == I`, so
// N is what decides which slice of the corpus a job sweeps. It is set in the
// workflow, and for a long time the `/N` half was a literal maintained by hand
// beside the matrix that produced the jobs — test-e2e-differential.yml even
// carried "keep DIFF_ORACLE_SHARD's /N in sync" as a comment, which is a note
// asking a person to be the gate.
//
// Nothing was that gate, and the two ways it breaks are both silent. Set N
// above the job count and the seeds in the missing buckets are swept by
// nobody, while every job that does run reports green — the corpus quietly
// shrinks. Set it below and buckets are swept twice, which only wastes a
// runner. Neither shows up as a failure, so the lane goes on reporting on a
// corpus smaller than the one it names.
//
// This repository has been bitten by that exact shape twice: `go test -run`
// exits 0 for a name matching nothing, so the arm64 lane silently ran 15 of
// the 17 tests it listed (#6310), and four driver-size reports sharing a
// filename merged down to one (#7519). Both were a hand-maintained count
// drifting from the thing it described. The fix each time was to derive the
// number rather than restate it; this pins that the derivation stays.
func TestShardDenominatorMatchesMatrix(t *testing.T) {
	dir := filepath.Join("..", "..", ".github", "workflows")
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read workflows: %v", err)
	}

	// A literal denominator, e.g. `DIFF_ORACLE_SHARD: "${{ matrix.shard }}/2"`.
	literal := regexp.MustCompile(`DIFF_ORACLE_SHARD:\s*"[^"]*/(\d+)"`)
	// The derived form, where the denominator is carried by the matrix.
	derived := regexp.MustCompile(`DIFF_ORACLE_SHARD:\s*"[^"]*/\$\{\{\s*matrix\.[A-Za-z0-9_.]*nshard\s*\}\}"`)

	var checked int
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yml") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		src := string(b)
		if !strings.Contains(src, "DIFF_ORACLE_SHARD") {
			continue
		}
		checked++

		// Scoped per job, not per file: nightly-fuzz.yml alone runs three
		// sharded jobs at two different counts, so a file-wide set of "counts
		// that appear somewhere" would accept one job's denominator on
		// another's matrix — which is the drift this exists to catch.
		var sawEnv bool
		for jobName, job := range jobBlocks(src) {
			if !strings.Contains(job, "DIFF_ORACLE_SHARD") {
				continue
			}
			sawEnv = true
			buckets := shardBucketCounts(job)
			for _, m := range literal.FindAllStringSubmatch(job, -1) {
				n, _ := strconv.Atoi(m[1])
				if !containsInt(buckets, n) {
					t.Errorf("%s job %q: DIFF_ORACLE_SHARD names %d buckets but its "+
						"matrix schedules %v jobs. Seeds in a bucket no job claims "+
						"are swept by nobody and every job still reports green. "+
						"Carry the denominator on the matrix "+
						"(`${{ matrix.nshard }}`, see test-e2e-differential.yml) "+
						"rather than restating it here", e.Name(), jobName, n, buckets)
				}
			}
			if !literal.MatchString(job) && !derived.MatchString(job) {
				t.Errorf("%s job %q: sets DIFF_ORACLE_SHARD in a shape this guard "+
					"does not recognise, so its denominator is no longer checked "+
					"against the matrix — update the patterns here alongside the "+
					"workflow", e.Name(), jobName)
			}
		}
		if !sawEnv {
			t.Errorf("%s: sets DIFF_ORACLE_SHARD but not inside any job block this "+
				"guard can find — did the `jobs:` layout change?", e.Name())
		}
	}

	if checked == 0 {
		t.Fatal("no workflow sets DIFF_ORACLE_SHARD — did the env var get renamed?")
	}
}

// jobBlocks splits a workflow's `jobs:` mapping into one entry per job — the
// body indented under `  <name>:` up to the next key at that indent.
func jobBlocks(src string) map[string]string {
	out := map[string]string{}
	lines := strings.Split(src, "\n")
	start := -1
	for i, l := range lines {
		if l == "jobs:" {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return out
	}
	head := regexp.MustCompile(`^  ([A-Za-z0-9_-]+):\s*$`)
	name := ""
	var b strings.Builder
	flush := func() {
		if name != "" {
			out[name] = b.String()
		}
		b.Reset()
	}
	for _, l := range lines[start:] {
		if m := head.FindStringSubmatch(l); m != nil {
			flush()
			name = m[1]
			continue
		}
		if strings.TrimSpace(l) != "" && !strings.HasPrefix(l, "  ") {
			break // left the jobs mapping
		}
		b.WriteString(l)
		b.WriteString("\n")
	}
	flush()
	return out
}

// shardBucketCounts returns every shard-count a workflow's matrices schedule:
// the length of each `shard: [...]` inline list, and each distinct `nshard:`
// value in an include list.
func shardBucketCounts(src string) []int {
	var out []int
	inline := regexp.MustCompile(`(?m)^\s*shard:\s*\[([^\]]*)\]`)
	for _, m := range inline.FindAllStringSubmatch(src, -1) {
		n := len(strings.Split(m[1], ","))
		if !containsInt(out, n) {
			out = append(out, n)
		}
	}
	for _, m := range regexp.MustCompile(`nshard:\s*(\d+)`).FindAllStringSubmatch(src, -1) {
		n, _ := strconv.Atoi(m[1])
		if !containsInt(out, n) {
			out = append(out, n)
		}
	}
	return out
}

func containsInt(xs []int, want int) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
