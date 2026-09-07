package sourcelint

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// unionMergedPaths returns the repo-relative paths .gitattributes marks
// `merge=union`, which is the list every check below derives from rather than
// repeating. A hand-kept copy of a list that lives somewhere else is the drift
// this package exists to catch.
func unionMergedPaths(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", ".gitattributes"))
	if err != nil {
		t.Fatalf("read .gitattributes: %v", err)
	}
	var out []string
	for line := range strings.SplitSeq(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		for _, attr := range fields[1:] {
			if attr == "merge=union" {
				out = append(out, fields[0])
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("no merge=union paths in .gitattributes — did the format change, " +
			"or was the last registry removed? If the latter, delete this test with it")
	}
	sort.Strings(out)
	return out
}

// registryKeys returns a registry file's row KEYS — the first
// whitespace-separated field of every non-comment line.
func registryKeys(t *testing.T, path string) (keys []string) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		keys = append(keys, strings.Fields(line)[0])
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return keys
}

// `merge=union` keeps BOTH sides of a colliding line, which is exactly right
// for two PRs appending different rows and exactly wrong for two PRs giving one
// row different values — that yields a duplicated key.
//
// Nothing else would notice. Every reader of these files builds a map keyed on
// the row name (censusRowNames in this package, readRejectionGaps in
// internal/e2eselfhost), so a duplicate collapses to whichever row was read
// last, silently, and the census gates keep passing while pinning a verdict
// nobody chose. That is the whole cost of the union attribute, so it is paid
// here rather than left to be discovered.
func TestUnionMergedRegistriesHaveNoDuplicateKeys(t *testing.T) {
	for _, rel := range unionMergedPaths(t) {
		t.Run(rel, func(t *testing.T) {
			keys := registryKeys(t, filepath.Join("..", "..", rel))
			seen := map[string]int{}
			for _, k := range keys {
				seen[k]++
			}
			var dupes []string
			for k, n := range seen {
				if n > 1 {
					dupes = append(dupes, k)
				}
			}
			sort.Strings(dupes)
			if len(dupes) > 0 {
				t.Errorf("%s has %d duplicated key(s): %s\n"+
					"This file is `merge=union`, so a merge that changed the same row on "+
					"both sides kept BOTH. Its readers key a map on the row name, so one of "+
					"these is being silently ignored — keep the row the merged tree "+
					"measures and delete the other.", rel, len(dupes), strings.Join(dupes, ", "))
			}
		})
	}
}

// Every `merge=union` path must exist. A stale pattern silences nothing and
// looks like coverage — the same false-green shape as a workflow filter naming
// a lane that no longer runs.
func TestUnionMergedPathsExist(t *testing.T) {
	for _, rel := range unionMergedPaths(t) {
		if _, err := os.Stat(filepath.Join("..", "..", rel)); err != nil {
			t.Errorf("%s is marked merge=union but does not exist: %v", rel, err)
		}
	}
}
