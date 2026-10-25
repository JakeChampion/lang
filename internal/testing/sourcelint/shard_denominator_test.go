package sourcelint

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var shardEnvs = []string{"DIFF_ORACLE_SHARD", "FERNSMITH_SHRINK_SHARD", "FERN_PRINTER_CORPUS_SHARD"}

func TestShardDenominatorMatchesMatrix(t *testing.T) {
	for _, env := range shardEnvs {
		t.Run(env, func(t *testing.T) { checkShardDenominators(t, env) })
	}
}

func checkShardDenominators(t *testing.T, env string) {
	dir := filepath.Join("..", "..", "..", ".github", "workflows")
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read workflows: %v", err)
	}

	literal := regexp.MustCompile(env + `:\s*"[^"]*/(\d+)"`)
	derived := regexp.MustCompile(env + `:\s*"[^"]*/\$\{\{\s*matrix\.[A-Za-z0-9_.]*nshard\s*\}\}"`)
	// The unit lane's heavy group takes its denominator from the group:
	// `${{ matrix.group == 'heavy' && 4 || 1 }}`, where 4 must be the
	// matrix's shard count.
	grouped := regexp.MustCompile(env + `:\s*"[^"]*/\$\{\{\s*matrix\.group == 'heavy' && (\d+) \|\| 1\s*\}\}"`)

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
		if !strings.Contains(src, env) {
			continue
		}
		checked++

		var sawEnv bool
		for jobName, job := range jobBlocks(src) {
			if !strings.Contains(job, env) {
				continue
			}
			sawEnv = true
			buckets := shardBucketCounts(job)
			for _, m := range literal.FindAllStringSubmatch(job, -1) {
				n, _ := strconv.Atoi(m[1])
				if !containsInt(buckets, n) {
					t.Errorf("%s job %q: %s names %d buckets but its "+
						"matrix schedules %v jobs. Seeds in a bucket no job claims "+
						"are swept by nobody and every job still reports green. "+
						"Carry the denominator on the matrix "+
						"(`${{ matrix.nshard }}`, see test-e2e-differential.yml) "+
						"rather than restating it here", e.Name(), jobName, env, n, buckets)
				}
			}
			for _, m := range grouped.FindAllStringSubmatch(job, -1) {
				n, _ := strconv.Atoi(m[1])
				if !containsInt(buckets, n) {
					t.Errorf("%s job %q: %s derives %d heavy buckets from the group but its "+
						"matrix schedules %v shards", e.Name(), jobName, env, n, buckets)
				}
			}
			if !literal.MatchString(job) && !derived.MatchString(job) && !grouped.MatchString(job) {
				t.Errorf("%s job %q: sets %s in a shape this guard "+
					"does not recognise, so its denominator is no longer checked "+
					"against the matrix — update the patterns here alongside the "+
					"workflow", e.Name(), jobName, env)
			}
		}
		if !sawEnv {
			t.Errorf("%s: sets %s but not inside any job block this "+
				"guard can find — did the `jobs:` layout change?", e.Name(), env)
		}
	}

	if checked == 0 {
		t.Fatalf("no workflow sets %s — did the env var get renamed?", env)
	}
}

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
			break
		}
		b.WriteString(l)
		b.WriteString("\n")
	}
	flush()
	return out
}

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
