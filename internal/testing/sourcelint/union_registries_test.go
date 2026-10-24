package sourcelint

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func unionMergedPaths(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", ".gitattributes"))
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

func TestUnionMergedRegistriesHaveNoDuplicateKeys(t *testing.T) {
	for _, rel := range unionMergedPaths(t) {
		t.Run(rel, func(t *testing.T) {
			keys := registryKeys(t, filepath.Join("..", "..", "..", rel))
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

func TestUnionMergedPathsExist(t *testing.T) {
	for _, rel := range unionMergedPaths(t) {
		if _, err := os.Stat(filepath.Join("..", "..", "..", rel)); err != nil {
			t.Errorf("%s is marked merge=union but does not exist: %v", rel, err)
		}
	}
}
