package sourcelint

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A `_test.go` file whose name ends in a GOOS or GOARCH is constrained to that
// platform by the FILENAME, with no build tag to read and no error anywhere: Go
// simply leaves it out of the package. `foo_wasm_test.go` is a wasm-only file,
// `foo_arm64_test.go` an arm64-only one — so a test written on an amd64 host,
// named after the backend it exercises, compiles, vets and reports `ok` while
// never being built at all.
//
// This is #8470 / #8471's family — a test that runs nowhere — with a cause
// those two gates cannot see, because it is upstream of every workflow
// selector: the test is not missing from a lane's list, it is missing from the
// binary the lane runs.
//
// The rule is the narrow one: every `_test.go` on disk must be in some
// package's build here, unless it carries an explicit `//go:build` line. An
// explicit tag is a decision somebody wrote down (internal/strerror's
// `//go:build darwin` errno table is the only one today); a filename that
// happens to end in a platform name is not.
func TestEveryTestFileIsInSomeBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("runs `go list ./...`; not a -short test")
	}
	root := filepath.Join("..", "..")
	cmd := exec.Command("go", "list", "-e", "-json=Dir,TestGoFiles,XTestGoFiles", "./...")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list ./...: %v", err)
	}

	built := map[string]bool{}
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for dec.More() {
		var pkg struct {
			Dir          string
			TestGoFiles  []string
			XTestGoFiles []string
		}
		if derr := dec.Decode(&pkg); derr != nil {
			t.Fatalf("decode go list output: %v", derr)
		}
		for _, f := range append(append([]string{}, pkg.TestGoFiles...), pkg.XTestGoFiles...) {
			abs, aerr := filepath.Abs(filepath.Join(pkg.Dir, f))
			if aerr != nil {
				t.Fatalf("abs %s: %v", f, aerr)
			}
			built[abs] = true
		}
	}
	if len(built) == 0 {
		t.Fatal("go list reported no test files at all, which cannot be right")
	}

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" || d.Name() == "testdata" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		abs, aerr := filepath.Abs(path)
		if aerr != nil {
			return aerr
		}
		if built[abs] {
			return nil
		}
		if hasBuildConstraint(t, path) {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		t.Errorf("%s is not in any package's build and carries no //go:build line, so nothing compiles it "+
			"and nothing runs its tests. A `_test.go` name ending in a GOOS or GOARCH (`_wasm`, `_arm64`, "+
			"`_darwin`, …) is a filename build constraint — rename it, or add an explicit //go:build line "+
			"if the constraint is intended.", rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}

// hasBuildConstraint reports whether the file opens with a //go:build line.
// The constraint must precede the package clause, so the scan stops there.
func hasBuildConstraint(t *testing.T, path string) bool {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "//go:build ") {
			return true
		}
		if strings.HasPrefix(line, "package ") {
			return false
		}
	}
	return false
}
