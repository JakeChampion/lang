package sourcelint

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// scripts/shard-tests partitions a test list into NSHARD buckets. The one
// property every lane that uses it relies on is that the buckets cover the
// list: a name in no bucket is a test no job runs, and nothing else notices.
// It once lost the whole list when the weights file was empty (the /dev/null
// fallback for a lane with no weights, or a file of comments): awk's
// `NR == FNR` idiom read every test name as a weight row and printed nothing,
// which the self-host lane's shard step then reported as a green shard with
// no tests. So the script is run here with and without weights, and the
// buckets are checked to be a partition of the input.
func TestShardTestsCoversTheListWithAndWithoutWeights(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "scripts", "shard-tests")
	names := []string{"TestA", "TestB", "TestC", "TestD", "TestE", "TestF", "TestG"}
	input := strings.Join(names, "\n") + "\n"

	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	comments := filepath.Join(dir, "comments.txt")
	if err := os.WriteFile(comments, []byte("# weights\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	weighted := filepath.Join(dir, "weights.txt")
	if err := os.WriteFile(weighted, []byte("# weights\nTestG 100\nTestA 50\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	run := func(shard, nshard int, weights string) []string {
		t.Helper()
		cmd := exec.Command(script, strconv.Itoa(shard), strconv.Itoa(nshard), weights)
		cmd.Stdin = strings.NewReader(input)
		cmd.Env = ciEnv()
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("shard-tests %d %d %s: %v", shard, nshard, weights, err)
		}
		return strings.Fields(string(out))
	}
	for _, weights := range []string{empty, comments, filepath.Join(dir, "missing.txt"), "/dev/null", weighted} {
		var all []string
		for shard := 0; shard < 3; shard++ {
			got := run(shard, 3, weights)
			if len(got) == 0 {
				t.Errorf("weights %s: shard %d/3 is empty for a seven-name list", filepath.Base(weights), shard)
			}
			all = append(all, got...)
		}
		sort.Strings(all)
		if strings.Join(all, ",") != strings.Join(names, ",") {
			t.Errorf("weights %s: the three buckets are %v, not a partition of %v", filepath.Base(weights), all, names)
		}
	}
	// The weights steer: the two heaviest names land in different buckets,
	// and the heaviest alone fills its bucket before the light names start.
	g, a := -1, -1
	for shard := 0; shard < 3; shard++ {
		for _, n := range run(shard, 3, weighted) {
			switch n {
			case "TestG":
				g = shard
			case "TestA":
				a = shard
			}
		}
	}
	if g < 0 || a < 0 || g == a {
		t.Errorf("weighted: TestG (100) landed in shard %d and TestA (50) in shard %d; LPT puts the two heaviest in different buckets", g, a)
	}
}
