package sourcelint

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/stdlib"
)

func TestEveryStdlibModuleHasAModuleIntroduction(t *testing.T) {
	root := filepath.Join("..", "..", "stdlib")
	count := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || stdlib.ModuleName(path) == "" || strings.HasPrefix(entry.Name(), "_") {
			return nil
		}
		text, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.HasSuffix(path, ".fern.md") {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			module, err := stdlib.ReadModule(stdlib.ModuleName(filepath.ToSlash(rel)))
			if err != nil {
				return err
			}
			text = []byte(module.Code)
		}
		count++
		lines := strings.Split(string(text), "\n")
		end := 0
		content := false
		for end < len(lines) && strings.HasPrefix(lines[end], "//") {
			content = content || strings.TrimSpace(strings.TrimPrefix(lines[end], "//")) != ""
			end++
		}
		if !content {
			t.Errorf("%s: add a module introduction at line 1", path)
		} else if end < len(lines) && strings.TrimSpace(lines[end]) != "" && !strings.HasPrefix(lines[end], "import ") && !strings.HasPrefix(lines[end], "pub use ") {
			t.Errorf("%s:%d: separate the module introduction from code with a blank line", path, end+1)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Fatal("no standard library modules found")
	}
	t.Logf("checked %d module introductions", count)
}
