package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/pkg/platforms"
	"github.com/jakechampion/lang/internal/testing/corpus"
)

// Every `-target NAME` a user-facing file tells someone to type must be a
// target the CLI accepts. #6572 renamed the whole vocabulary with a sed and
// applied it twice in places, so `examples/wasm/README.md` shipped
// `-target wasm32-wasi32-wasi32-wasi-http` and `examples/cli/README.md`
// shipped `-target x86-64-linux-linux` — commands that cannot run, in the
// files a newcomer copies from first.
//
// The scope is deliberately the surfaces that address a USER: the READMEs,
// the diagnostic explanations, and the header comments of runnable examples.
// `docs/` is excluded because it is a working record where a line may
// legitimately quote a target that no longer exists.
func TestUserFacingTargetNamesResolve(t *testing.T) {
	root := repoRoot(t)

	// `-target NAME` or `-target=NAME`, not preceded by another dash
	// (`clang --target=aarch64-linux-gnu` is a different compiler's flag).
	re := regexp.MustCompile(`(^|[^-\w])-target[= ]+([A-Za-z0-9][A-Za-z0-9._-]*)`)

	var checked int
	for _, path := range userFacingFiles(t, root) {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		rel, _ := filepath.Rel(root, path)
		for i, line := range strings.Split(string(b), "\n") {
			for _, m := range re.FindAllStringSubmatch(line, -1) {
				name := m[2]
				checked++
				if platforms.ForTarget(name) == nil {
					t.Errorf("%s:%d: `-target %s` is not a target `fern -targets` lists\n\t%s",
						rel, i+1, name, strings.TrimSpace(line))
				}
			}
		}
	}
	// A rename that moved the mentions elsewhere would otherwise leave this
	// test green over nothing.
	if checked < 10 {
		t.Errorf("only %d -target mentions found; the scan lost its corpus", checked)
	}
}

func userFacingFiles(t *testing.T, root string) []string {
	t.Helper()
	skip := map[string]bool{
		filepath.Join(root, "tests", "proposals"): true,
	}
	var out []string
	add := func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skip[path] {
				return filepath.SkipDir
			}
			return nil
		}
		switch {
		case d.Name() == "README.md", strings.HasSuffix(d.Name(), ".fern"), strings.HasSuffix(d.Name(), ".md") && strings.Contains(path, filepath.Join("diag", "explanations")):
			out = append(out, path)
		}
		return nil
	}
	var dirs []string
	for _, r := range corpus.Programs {
		dirs = append(dirs, filepath.Join(root, r))
	}
	for _, dir := range append(dirs, filepath.Join(root, "internal", "syntax", "diag", "explanations")) {
		if err := filepath.WalkDir(dir, add); err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	return append(out, filepath.Join(root, "README.md"))
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := corpus.RepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	return root
}
