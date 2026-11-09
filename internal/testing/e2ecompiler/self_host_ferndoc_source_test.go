package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var sourceLink = regexp.MustCompile(`^\[Source\]\((.+):(\d+)\)$`)

// -doc-source links each declaration to its line. For a literate module the
// line is in the document, not in the tangled source.
func TestSelfHostFernDocSourceLinks(t *testing.T) {
	fernBin := buildFernDocCLI(t)
	for _, name := range []string{"shapes.fern", "notes.fern.md"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("testdata", "ferndoc", "std", name)
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(string(src), "\n")
			out, err := exec.Command(fernBin, "-doc", "-doc-source", "{path}:{line}", path).Output()
			if err != nil {
				t.Fatalf("fern -doc -doc-source: %v", err)
			}
			decl, links := "", 0
			for _, l := range strings.Split(string(out), "\n") {
				if strings.HasPrefix(l, "## `") {
					fields := strings.Fields(strings.Trim(strings.TrimPrefix(l, "## "), "`"))
					decl = fields[len(fields)-1]
					continue
				}
				m := sourceLink.FindStringSubmatch(l)
				if m == nil {
					continue
				}
				links++
				n, _ := strconv.Atoi(m[2])
				if m[1] != path || n < 1 || n > len(lines) {
					t.Errorf("%s: link %q names no line of %s", decl, l, path)
					continue
				}
				if got := lines[n-1]; !strings.Contains(got, "pub ") || !strings.Contains(got, decl) {
					t.Errorf("%s: line %d is %q, not its declaration", decl, n, got)
				}
			}
			if links == 0 {
				t.Errorf("no [Source] links in\n%s", out)
			}
		})
	}
}

func TestSelfHostFernDocNoSourceLinksByDefault(t *testing.T) {
	fernBin := buildFernDocCLI(t)
	out, err := exec.Command(fernBin, "-doc", filepath.Join("testdata", "ferndoc", "std", "notes.fern.md")).Output()
	if err != nil {
		t.Fatalf("fern -doc: %v", err)
	}
	if strings.Contains(string(out), "[Source]") {
		t.Errorf("a page without -doc-source has a source link:\n%s", out)
	}
}
