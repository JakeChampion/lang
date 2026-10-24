package corpus

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/jakechampion/lang/internal/tools/literate"
)

func Materialize(repoRoot, dst string, roots []string) error {
	var documents []string
	for _, root := range roots {
		err := filepath.WalkDir(filepath.Join(repoRoot, root), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(repoRoot, path)
			if err != nil {
				return err
			}
			target := filepath.Join(dst, rel)
			if entry.IsDir() {
				return os.MkdirAll(target, 0o755)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.HasSuffix(path, ".fern.md") {
				documents = append(documents, rel)
			}
			return os.WriteFile(target, data, 0o644)
		})
		if err != nil {
			return err
		}
	}
	for _, rel := range documents {
		if _, err := os.Stat(filepath.Join(repoRoot, strings.TrimSuffix(rel, ".md"))); err == nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(repoRoot, rel))
		if err != nil {
			return err
		}
		doc := literate.Parse(string(data))
		if !doc.IsProgram() {
			continue
		}
		var files []literate.FileResult
		if doc.HasFiles() {
			files, err = doc.TangleFiles()
		} else {
			var code string
			code, _, err = doc.Tangle()
			files = []literate.FileResult{{Path: strings.TrimSuffix(filepath.Base(rel), ".md"), Code: code}}
		}
		if err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		for _, file := range files {
			target := filepath.Join(filepath.Dir(rel), filepath.FromSlash(file.Path))
			if !filepath.IsLocal(target) {
				return fmt.Errorf("%s: module path must stay inside the corpus", rel)
			}
			if _, err := os.Stat(filepath.Join(repoRoot, target)); err == nil {
				continue
			}
			if err := os.MkdirAll(filepath.Dir(filepath.Join(dst, target)), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(dst, target), []byte(file.Code), 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}
