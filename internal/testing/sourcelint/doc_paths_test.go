package sourcelint

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var docInlineCode = regexp.MustCompile("`([^`\\n]+)`")
var docPathPosition = regexp.MustCompile(`:(?:[0-9]+(?:[-–][0-9]+)?|[A-Za-z_][A-Za-z_0-9]*)$`)
var docPackageMember = regexp.MustCompile(`\.[A-Z][A-Za-z_0-9]*$`)
var docDatedPath = regexp.MustCompile(`(?:^|/)[^/]*20[0-9]{2}-[0-9]{2}-[0-9]{2}`)

// TestLivingDocPathsExist checks source references in current documentation.
// Dated records and declared plans retain their original source references.
func TestLivingDocPathsExist(t *testing.T) {
	root := mustRepoRoot(t)
	paths := []string{"README.md"}
	for _, dir := range []string{"docs", "spec", "internal/syntax/diag/explanations"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			if d.IsDir() && (rel == "docs/rc-log" || rel == "docs/ssa-log") {
				return filepath.SkipDir
			}
			if !d.IsDir() && strings.HasSuffix(path, ".md") {
				paths = append(paths, rel)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	checked := 0
	for _, rel := range paths {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		if !livingDocPath(rel, string(data)) {
			continue
		}
		checked++
		for _, ref := range docSourcePaths(string(data)) {
			if !docSourcePathExists(root, rel, ref.path) {
				t.Errorf("%s:%d: repository path %q does not exist. Update the reference or remove the obsolete claim.", rel, ref.line, ref.path)
			}
		}
	}
	if checked < 100 {
		t.Fatalf("only %d living documents checked", checked)
	}
}

func livingDocPath(rel, text string) bool {
	if rel == "README.md" || rel == "docs/README.md" || strings.HasPrefix(rel, "spec/") || strings.HasPrefix(rel, "internal/syntax/diag/explanations/") {
		return true
	}
	lines := strings.SplitN(text, "\n", 16)
	for i, line := range lines {
		if i == 15 {
			break
		}
		if m := docsStatusTag.FindStringSubmatch(line); m != nil {
			return m[1] == "policy" || m[1] == "reference" || m[1] == "tracker"
		}
	}
	return !docDatedPath.MatchString(rel)
}

type docSourcePath struct {
	path string
	line int
}

func docSourcePaths(text string) []docSourcePath {
	var refs []docSourcePath
	fence := byte(0)
	fenceWidth := 0
	for i, line := range strings.Split(text, "\n") {
		trim := strings.TrimSpace(line)
		if len(trim) > 0 && (trim[0] == '`' || trim[0] == '~') {
			width := 1
			for width < len(trim) && trim[width] == trim[0] {
				width++
			}
			if width >= 3 {
				if fence == 0 {
					fence, fenceWidth = trim[0], width
				} else if trim[0] == fence && width >= fenceWidth && strings.TrimSpace(trim[width:]) == "" {
					fence = 0
				}
				continue
			}
		}
		if fence != 0 {
			continue
		}
		for _, m := range docInlineCode.FindAllStringSubmatch(line, -1) {
			path := docPathPosition.ReplaceAllString(strings.TrimRight(m[1], ".,;"), "")
			path = strings.TrimPrefix(path, "./")
			if strings.ContainsAny(path, " \t()=<>|$") || strings.Contains(path, "://") {
				continue
			}
			head, _, slash := strings.Cut(path, "/")
			qualified := slash && repoDocPathRoot(head)
			namedDoc := strings.HasSuffix(path, ".md") && !strings.HasSuffix(path, ".fern.md") && !strings.HasPrefix(path, "YYYY-")
			if !qualified && !namedDoc {
				continue
			}
			if strings.HasPrefix(path, ".") && !slash {
				continue
			}
			refs = append(refs, docSourcePath{path: path, line: i + 1})
		}
	}
	return refs
}

func repoDocPathRoot(head string) bool {
	switch head {
	case "internal", "compiler", "docs", "spec", "scripts", "site", "examples", "conformance", "bench", "benchmarks", "bootstrap", "runtime", "cmd", "tools", ".github", "records", "contexts", "measurements", "proposals", "rc-log", "ssa-log", "compiler-log", "string-log", "coreutils-log", "typed-log", "ci-log":
		return true
	}
	return false
}

func docSourcePathExists(root, doc, path string) bool {
	path = strings.TrimSuffix(path, "/...")
	for _, base := range []string{root, filepath.Join(root, filepath.Dir(doc)), filepath.Join(root, "docs")} {
		all := true
		for _, part := range docExpandBraces(path) {
			target := filepath.Join(base, filepath.FromSlash(part))
			if _, err := os.Stat(target); err == nil {
				continue
			}
			if matches, err := filepath.Glob(target); err == nil && len(matches) > 0 {
				continue
			}
			pkg := docPackageMember.ReplaceAllString(target, "")
			if pkg != target {
				if info, err := os.Stat(pkg); err == nil && info.IsDir() {
					continue
				}
			}
			all = false
			break
		}
		if all {
			return true
		}
	}
	return false
}

func docExpandBraces(path string) []string {
	open := strings.IndexByte(path, '{')
	if open < 0 {
		return []string{path}
	}
	end := strings.IndexByte(path[open:], '}')
	if end < 0 {
		return []string{path}
	}
	end += open
	var out []string
	for _, part := range strings.Split(path[open+1:end], ",") {
		out = append(out, docExpandBraces(path[:open]+part+path[end+1:])...)
	}
	return out
}

func TestDocSourcePathSyntax(t *testing.T) {
	source := "A `compiler/fern.fern:12` and `docs/{TRAITS,TRY}.md`.\n```fern\n// `internal/not-a-source`\n```\nThe suffix `.fern.md` and `greet.fern.md` are examples. `Map[T]` is a type.\n"
	source += "````markdown\n```fern\n// `internal/also-not-a-source`\n```\n````\n"
	refs := docSourcePaths(source)
	if got := fmt.Sprint(refs); got != "[{compiler/fern.fern 1} {docs/{TRAITS,TRY}.md 1}]" {
		t.Fatalf("references: %s", got)
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"A.md", "B.md"} {
		if err := os.WriteFile(filepath.Join(root, "docs", name), nil, 0644); err != nil {
			t.Fatal(err)
		}
	}
	if !docSourcePathExists(root, "docs/README.md", "{A,B}.md") {
		t.Fatal("relative document alternatives not found")
	}
	if docSourcePathExists(root, "docs/README.md", "{A,Missing}.md") {
		t.Fatal("missing document alternative accepted")
	}
	if !livingDocPath("docs/CURRENT.md", "Status: [reference]\n") {
		t.Fatal("reference skipped")
	}
	if livingDocPath("docs/HISTORY.md", "Status: [record]\n") {
		t.Fatal("record treated as current")
	}
	if livingDocPath("docs/log/2026-10-23-example.md", "# Historical results\n") {
		t.Fatal("dated record treated as current")
	}
}
