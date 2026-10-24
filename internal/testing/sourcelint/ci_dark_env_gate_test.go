package sourcelint

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var envGateRe = regexp.MustCompile(`(?:os\.Getenv|envflag\.On)\("((?:RUN_|FERN_|DIFF_ORACLE_)[A-Z0-9_]+)"\)`)

var ciDarkRe = regexp.MustCompile(`CI-DARK:\s*((?:RUN_|FERN_|DIFF_ORACLE_)[A-Z0-9_]+)\s*[—:-]\s*\S`)

var gateScanRoots = []string{
	filepath.Join("internal", "testing", "e2e"),
	filepath.Join("internal", "testing", "e2ecompiler"),
	filepath.Join("internal", "testing", "e2eharness"),
}

func TestNoSilentlyCIDarkEnvGates(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}

	ciRefs := envNamesReferencedByCI(t, root)

	for _, rel := range gateScanRoots {
		dir := filepath.Join(root, rel)
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			text := string(src)

			acknowledged := map[string]bool{}
			for _, m := range ciDarkRe.FindAllStringSubmatch(text, -1) {
				acknowledged[m[1]] = true
			}
			for _, m := range envGateRe.FindAllStringSubmatch(text, -1) {
				name := m[1]
				if ciRefs[name] || acknowledged[name] {
					continue
				}
				t.Errorf("%s: %s gates test-side behaviour but nothing under .github/ sets it, "+
					"and the file carries no `// CI-DARK: %s — <reason>` marker. "+
					"Either wire a lane that sets it or record why the coverage is knowingly skipped.",
					filepath.Join(rel, e.Name()), name, name)
			}
		}
	}
}

func envNamesReferencedByCI(t *testing.T, root string) map[string]bool {
	t.Helper()
	found := map[string]bool{}
	nameRe := regexp.MustCompile(`(?:RUN_|FERN_|DIFF_ORACLE_)[A-Z0-9_]+`)
	err := filepath.Walk(filepath.Join(root, ".github"), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		src, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		for _, m := range nameRe.FindAllString(string(src), -1) {
			found[m] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk .github: %v", err)
	}
	return found
}
