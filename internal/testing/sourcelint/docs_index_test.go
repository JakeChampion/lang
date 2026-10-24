package sourcelint

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	docsIndexRow  = regexp.MustCompile("^\\| `([^`/]+\\.md)` \\| \\[([a-z]+)\\] \\|")
	docsStatusTag = regexp.MustCompile(`^Status: \[([a-z]+)\]`)
	docsTags      = map[string]bool{"policy": true, "reference": true, "tracker": true, "plan": true, "research": true, "record": true}
)

func TestDocsIndexCoversEveryTopLevelDoc(t *testing.T) {
	docs := filepath.Join("..", "..", "..", "docs")
	index, err := os.ReadFile(filepath.Join(docs, "README.md"))
	if err != nil {
		t.Fatalf("read docs/README.md: %v", err)
	}
	rows := map[string]string{}
	for _, line := range strings.Split(string(index), "\n") {
		m := docsIndexRow.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if _, dup := rows[m[1]]; dup {
			t.Errorf("docs/README.md lists %s twice", m[1])
		}
		rows[m[1]] = m[2]
	}
	entries, err := os.ReadDir(docs)
	if err != nil {
		t.Fatalf("read docs/: %v", err)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".md") || name == "README.md" {
			continue
		}
		seen[name] = true
		tag := docStatusTag(t, filepath.Join(docs, name))
		switch {
		case tag == "":
			t.Errorf("docs/%s has no `Status: [tag]` line in its first 15 lines", name)
		case !docsTags[tag]:
			t.Errorf("docs/%s has status [%s], which is not one of the index's tags", name, tag)
		}
		row, ok := rows[name]
		switch {
		case !ok:
			t.Errorf("docs/%s has no row in docs/README.md", name)
		case tag != "" && row != tag:
			t.Errorf("docs/README.md lists %s as [%s], but the doc says [%s]", name, row, tag)
		}
	}
	for name := range rows {
		if !seen[name] {
			t.Errorf("docs/README.md lists %s, which is not a top-level doc", name)
		}
	}
}

func docStatusTag(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	lines := strings.SplitN(string(b), "\n", 16)
	for i, line := range lines {
		if i == 15 {
			break
		}
		if m := docsStatusTag.FindStringSubmatch(line); m != nil {
			return m[1]
		}
	}
	return ""
}
