package sourcelint

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// mise.toml's min_version is the one mise release the repo runs: the shell
// bootstrap (scripts/toolchain-env) installs exactly it, and every
// jdx/mise-action step in .github/ has to pass the same value as `version:`.
// The action input cannot read the file, so this test is what keeps the two
// from drifting apart — a newer mise on the runners than on a laptop is the
// kind of split that shows up as a lockfile CI rewrites and a developer cannot
// reproduce.
func TestMiseActionPinsTheMiseRelease(t *testing.T) {
	toml, err := os.ReadFile(filepath.Join("..", "..", "mise.toml"))
	if err != nil {
		t.Fatalf("read mise.toml: %v", err)
	}
	m := regexp.MustCompile(`(?m)^min_version = "([^"]+)"`).FindSubmatch(toml)
	if m == nil {
		t.Fatal("mise.toml has no top-level min_version; the mise release is pinned there")
	}
	want := string(m[1])

	steps := 0
	err = filepath.WalkDir(filepath.Join("..", "..", ".github"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".yml") {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lines := strings.Split(string(b), "\n")
		for i, l := range lines {
			if !strings.Contains(l, "uses: jdx/mise-action@") {
				continue
			}
			steps++
			got := ""
			// The `with:` block follows the `uses:` line; the next step starts
			// at or before the `uses:` line's indentation.
			indent := len(l) - len(strings.TrimLeft(l, " "))
			for _, w := range lines[i+1:] {
				if strings.TrimSpace(w) == "" {
					break
				}
				if len(w)-len(strings.TrimLeft(w, " ")) <= indent && strings.TrimSpace(w) != "with:" {
					break
				}
				if v, ok := strings.CutPrefix(strings.TrimSpace(w), "version:"); ok {
					got = strings.TrimSpace(v)
				}
			}
			if got != want {
				t.Errorf("%s:%d: jdx/mise-action step passes version %q, mise.toml min_version is %q", path, i+1, got, want)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk .github: %v", err)
	}
	if steps == 0 {
		t.Fatal("no jdx/mise-action step found under .github; the toolchain is installed through it")
	}
}
