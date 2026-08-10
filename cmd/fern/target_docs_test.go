package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/platforms"
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
//
// examples/self_host is excluded for a different and sharper reason: the
// self-hosted compiler keeps its OWN -target vocabulary (`x86-64`, `arm64`,
// `wasm`, `wasm-bin`, `wasm-component`, the `-asm` variants), which this
// package's table does not and should not describe. That overlap is exactly
// what the rename got wrong in both directions, so the boundary is drawn
// once, here.
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
		filepath.Join(root, "examples", "self_host"): true,
		filepath.Join(root, "examples", "proposals"): true,
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
	for _, dir := range []string{
		filepath.Join(root, "examples"),
		filepath.Join(root, "internal", "diag", "explanations"),
	} {
		if err := filepath.WalkDir(dir, add); err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	return append(out, filepath.Join(root, "README.md"))
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	root := filepath.Join(wd, "..", "..")
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repo root %s has no go.mod: %v", root, err)
	}
	return root
}
