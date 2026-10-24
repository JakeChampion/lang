package corpus

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

var Programs = []string{"examples", "tests", "bench"}

var Sources = append(append([]string{}, Programs...), "compiler", "internal/stdlib")

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
