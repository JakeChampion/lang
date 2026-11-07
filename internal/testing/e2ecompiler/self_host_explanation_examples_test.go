package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

var explanationExample = regexp.MustCompile("(?s)## Example\\s*\\n+```[a-z]*\\n(.*?)```")

// TestSelfHostExplanationExamplesReportTheirCode runs the example of each
// `fern -explain` page that marks a line `error[CODE]` with the page's own
// code, and requires the compiler to report that code. A check that runs
// only when code is lowered (E068) is reached by a compile for the host.
func TestSelfHostExplanationExamplesReportTheirCode(t *testing.T) {
	fern := e2eharness.ShippedFern(t)
	pages, err := filepath.Glob(e2eharness.RepoPath("internal", "syntax", "diag", "explanations", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cache := filepath.Join(dir, "cache")
	checked := 0
	for _, page := range pages {
		code := strings.TrimSuffix(filepath.Base(page), ".md")
		text, err := os.ReadFile(page)
		if err != nil {
			t.Fatal(err)
		}
		m := explanationExample.FindSubmatch(text)
		marker := "error[" + code + "]"
		if m == nil || !strings.Contains(string(m[1]), marker) {
			continue
		}
		checked++
		src := writeShippedSrc(t, dir, code+".fern", string(m[1]))
		out, _ := runFern(t, fern, cache, "-check", src)
		if !strings.Contains(out, marker) {
			out, _ = runFern(t, fern, cache, "-target", e2eharness.HostSelfHostTarget(), "-o", filepath.Join(dir, code+".out"), src)
		}
		if !strings.Contains(out, marker) {
			t.Errorf("%s: the example does not report %s:\n%s", filepath.Base(page), code, out)
		}
	}
	if checked < 40 {
		t.Fatalf("checked %d explanation examples; the page format moved", checked)
	}
}
