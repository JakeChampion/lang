package e2e

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// docs/security/ADVERSARIAL-SUITE.md names every attack-shaped test. A name
// that nothing answers to is a row that covers nothing, so the index is read
// here and every backticked test, fuzz target, conformance case, stdlib suite
// and script must exist in the tree.

var adversarialNamePattern = regexp.MustCompile("`((?:Test|Fuzz)[A-Za-z0-9_]+|conformance/cases/[A-Za-z0-9_]+|tests/stdlib/[A-Za-z0-9_]+\\.fern|scripts/[A-Za-z0-9_./-]+|\\.github/[A-Za-z0-9_./-]+)`")

// adversarialIndexMissing returns the names in doc that the tree does not
// hold: a Go test or fuzz function by name, or a path relative to root.
func adversarialIndexMissing(doc string, goFuncs map[string]bool, root string) []string {
	seen := map[string]bool{}
	var missing []string
	for _, m := range adversarialNamePattern.FindAllStringSubmatch(doc, -1) {
		name := m[1]
		if seen[name] {
			continue
		}
		seen[name] = true
		if strings.HasPrefix(name, "Test") || strings.HasPrefix(name, "Fuzz") {
			if !goFuncs[name] {
				missing = append(missing, name)
			}
			continue
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(name))); err != nil {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing
}

var goTestFuncPattern = regexp.MustCompile(`(?m)^func ((?:Test|Fuzz)[A-Za-z0-9_]+)\(`)

func goTestFunctions(t *testing.T, root string) map[string]bool {
	t.Helper()
	funcs := map[string]bool{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "build", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, m := range goTestFuncPattern.FindAllSubmatch(src, -1) {
			funcs[string(m[1])] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return funcs
}

func TestAdversarialSuiteIndexNamesLiveTests(t *testing.T) {
	root := e2eharness.RepoPath()
	doc, err := os.ReadFile(filepath.Join(root, "docs", "security", "ADVERSARIAL-SUITE.md"))
	if err != nil {
		t.Fatal(err)
	}
	funcs := goTestFunctions(t, root)
	if len(funcs) < 100 {
		t.Fatalf("found %d test functions under %s; the walk is broken", len(funcs), root)
	}
	names := adversarialNamePattern.FindAllString(string(doc), -1)
	if len(names) < 50 {
		t.Fatalf("the index names %d tests; the pattern no longer reads it", len(names))
	}
	if missing := adversarialIndexMissing(string(doc), funcs, root); len(missing) != 0 {
		t.Errorf("docs/security/ADVERSARIAL-SUITE.md names what the tree does not hold:\n  %s", strings.Join(missing, "\n  "))
	}
}

// The guarantees and the release checklist make claims the same way: each
// names the test, case or lane that proves it, and a name nothing answers to
// is a claim without evidence.
func TestSecurityClaimsNameLiveGates(t *testing.T) {
	root := e2eharness.RepoPath()
	funcs := goTestFunctions(t, root)
	for _, doc := range []string{"GUARANTEES.md", "RELEASE-CHECKLIST.md"} {
		src, err := os.ReadFile(filepath.Join(root, "docs", "security", doc))
		if err != nil {
			t.Fatal(err)
		}
		if names := adversarialNamePattern.FindAllString(string(src), -1); len(names) < 10 {
			t.Fatalf("%s names %d gates; the pattern no longer reads it", doc, len(names))
		}
		if missing := adversarialIndexMissing(string(src), funcs, root); len(missing) != 0 {
			t.Errorf("docs/security/%s names what the tree does not hold:\n  %s", doc, strings.Join(missing, "\n  "))
		}
	}
}

// The gate must refuse a row that names nothing, or a stale index reads as
// covered.
func TestAdversarialSuiteIndexGateRefusesAMissingName(t *testing.T) {
	funcs := map[string]bool{"TestSelfHostStackOverflow": true}
	doc := "| `TestSelfHostStackOverflow` | `TestNoSuchProbe` | `conformance/cases/no_such_case` | `FuzzNoSuchTarget` | `.github/workflows/no-such-lane.yml` |"
	got := adversarialIndexMissing(doc, funcs, t.TempDir())
	want := []string{".github/workflows/no-such-lane.yml", "FuzzNoSuchTarget", "TestNoSuchProbe", "conformance/cases/no_such_case"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("missing = %v, want %v", got, want)
	}
	if got := adversarialIndexMissing("| `TestSelfHostStackOverflow` |", funcs, t.TempDir()); len(got) != 0 {
		t.Fatalf("a live name was reported missing: %v", got)
	}
}
