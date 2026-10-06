// Package corpus names the repository's Fern sources by role, so every test
// that sweeps them reads one list instead of keeping its own.
package corpus

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// Programs are the roots of the runnable programs: the examples, the Fern-side
// tests and probes, and the benchmarks.
var Programs = []string{"examples", "tests", "bench"}

// Sources are the roots of every Fern source the formatter and printer must
// round-trip: the programs, the compiler, and the standard library.
var Sources = append(append([]string{}, Programs...), "compiler", "internal/stdlib")

// Files lists every `.fern` file under roots, repo-relative with slash
// separators, sorted. A root that contributes no file is an error, so a moved
// directory fails the sweep instead of shrinking it.
func Files(repoRoot string, roots []string) ([]string, error) {
	var out []string
	for _, root := range roots {
		n := len(out)
		err := filepath.WalkDir(filepath.Join(repoRoot, root), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || filepath.Ext(path) != ".fern" {
				return nil
			}
			rel, err := filepath.Rel(repoRoot, path)
			if err != nil {
				return err
			}
			out = append(out, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			return nil, err
		}
		if len(out) == n {
			return nil, fmt.Errorf("corpus root %s holds no .fern file under %s", root, repoRoot)
		}
	}
	sort.Strings(out)
	return out, nil
}

// RepoRoot is the nearest directory at or above the working directory that
// holds go.mod.
func RepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above %s", dir)
		}
		dir = parent
	}
}
