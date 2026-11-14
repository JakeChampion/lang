package sourcelint

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var pinHistoryRe = regexp.MustCompile(`^#\s*(20\d\d-\d\d-\d\d|Re-?pinned|Re-?banked|Measured 20)`)

func pinFiles(t *testing.T, root string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(root, ".github", "*-baseline*.txt"))
	if err != nil || len(files) < 5 {
		t.Fatalf("glob .github/*-baseline*.txt: %d files, %v", len(files), err)
	}
	return append(files,
		filepath.Join(root, ".github", "selfhost-driver-sizes.txt"),
		filepath.Join(root, "internal", "testing", "e2e", "testdata", "conformance-leak-census.txt"))
}

// Each pull request that moves a pin edits its file. A note per move puts each
// of those edits on the same lines, and two open pull requests then conflict.
func TestPinFilesHoldNoHistory(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	for _, path := range pinFiles(t, root) {
		rel, _ := filepath.Rel(root, path)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		seen := map[string]int{}
		for i, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") {
				if pinHistoryRe.MatchString(trimmed) {
					t.Errorf("%s:%d: a note about one move of a pin. Put the reason in the commit message; "+
						"the file holds only the current values.", rel, i+1)
				}
				continue
			}
			fields := strings.Fields(trimmed)
			if len(fields) == 0 {
				continue
			}
			if first, dup := seen[fields[0]]; dup {
				t.Errorf("%s:%d: %s already has a row at line %d", rel, i+1, fields[0], first)
			}
			seen[fields[0]] = i + 1
		}
	}
}
