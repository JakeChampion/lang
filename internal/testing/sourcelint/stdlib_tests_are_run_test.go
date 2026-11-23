package sourcelint

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Each program in tests/stdlib whose name ends in _test.fern must be named in
// a Go test or in a script, by its path from the repository root or by its
// file name in quotes. Such a
// program that nothing names runs nowhere: its cases can fail, or leak, and
// no gate reports it.
func TestEveryStdlibTestProgramIsRun(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	var named strings.Builder
	read := func(dir string, keep func(string) bool) {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if entry.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !keep(path) {
				return nil
			}
			text, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			named.Write(text)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	read("internal/testing", func(path string) bool { return strings.HasSuffix(path, ".go") })
	read("scripts", func(string) bool { return true })
	all := named.String()

	programs, err := filepath.Glob(filepath.Join(root, "tests", "stdlib", "*_test.fern"))
	if err != nil {
		t.Fatal(err)
	}
	if len(programs) == 0 {
		t.Fatal("no programs in tests/stdlib")
	}
	for _, program := range programs {
		rel := "tests/stdlib/" + filepath.Base(program)
		if !strings.Contains(all, rel) && !strings.Contains(all, `"`+filepath.Base(program)+`"`) {
			t.Errorf("%s is named in no Go test under internal/testing and in no script: add it to selfHostStdTestCases in internal/testing/e2ecompiler/self_host_stdtest_test.go", rel)
		}
	}
}
