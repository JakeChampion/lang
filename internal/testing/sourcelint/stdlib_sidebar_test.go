package sourcelint

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/stdlib"
)

var stdlibGroupsRe = regexp.MustCompile(`(?s)const STDLIB_GROUPS = \[(.*?)\n\];`)

var quotedRe = regexp.MustCompile(`"([^"]+)"`)

func TestEveryStdlibModuleHasASidebarGroup(t *testing.T) {
	cfg, err := os.ReadFile(filepath.Join("..", "..", "..", "site", "astro.config.mjs"))
	if err != nil {
		t.Fatalf("read astro.config.mjs: %v", err)
	}
	m := stdlibGroupsRe.FindSubmatch(cfg)
	if m == nil {
		t.Fatal("STDLIB_GROUPS not found in site/astro.config.mjs — if the sidebar stopped being hand-grouped, delete this test with the change that did it")
	}
	grouped := map[string]bool{}
	for _, q := range quotedRe.FindAllSubmatch(m[1], -1) {
		grouped[string(q[1])] = true
	}

	root := filepath.Join("..", "..", "stdlib", "std")
	var missing []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() || stdlib.ModuleName(name) == "" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		slug := strings.ReplaceAll(stdlib.ModuleName(rel), string(filepath.Separator), "_")
		if strings.HasPrefix(slug, "_") {
			return nil
		}
		if !grouped[slug] {
			missing = append(missing, slug)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/stdlib/std: %v", err)
	}
	if len(missing) != 0 {
		sort.Strings(missing)
		t.Errorf("stdlib modules with no STDLIB_GROUPS entry in site/astro.config.mjs: %s\n"+
			"Each falls into the sidebar's \"Other\" group, which site/test/playwright/docs.spec.ts asserts is empty — "+
			"so this lands as a red docs-build lane rather than as anything readable. Give each a group.",
			strings.Join(missing, ", "))
	}
}
