package sourcelint

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestUnitLaneCoversEveryNonSlowPackage(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "scripts", "unit-test-packages"))
	if err != nil {
		t.Fatalf("read unit-test-packages: %v", err)
	}
	src := string(b)
	re := regexp.MustCompile(`grep -vE '/internal/testing/\(([^)]*)\)`)
	m := re.FindStringSubmatch(src)
	if m == nil {
		t.Fatal("could not find the exclusion alternation in scripts/unit-test-packages — if its shape changed, update this gate with it")
	}
	excluded := strings.Split(m[1], "|")
	allowed := map[string]string{
		"e2e":         "test-e2e-{arm64,x86_64,wasm,other}",
		"e2ecompiler": "test-e2e-selfhost",
		"fernsmith":   "test-fernsmith",
		"coreutils":   "test-coreutils",
	}
	for _, pkg := range excluded {
		pkg = strings.TrimSpace(pkg)
		if pkg == "" {
			continue
		}
		if _, ok := allowed[pkg]; !ok {
			t.Errorf("internal/testing/%s is excluded from the unit lane but has no workflow of its own, so nothing runs its tests. "+
				"Either give it a lane or stop excluding it (#8470)", pkg)
		}
	}
	for pkg, lane := range allowed {
		found := false
		for _, e := range excluded {
			if strings.TrimSpace(e) == pkg {
				found = true
			}
		}
		if !found {
			t.Errorf("internal/testing/%s is no longer excluded from the unit lane, but %s still runs it — it would now run twice", pkg, lane)
		}
	}
}

func TestSelfHostShardsSelectEveryTest(t *testing.T) {
	path := filepath.Join("..", "..", "..", ".github", "workflows", "test-e2e-selfhost.yml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read test-e2e-selfhost.yml: %v", err)
	}
	src := string(b)
	line := ""
	for _, l := range strings.Split(src, "\n") {
		if strings.Contains(l, "mapfile -t SH_ALL") {
			line = l
			break
		}
	}
	if line == "" {
		t.Fatal("could not find the SH_ALL selection line in test-e2e-selfhost.yml")
	}
	if !strings.Contains(line, `-test.list '^Test'`) {
		t.Errorf("the e2ecompiler shards do not select every test:\n  %s\n"+
			"The whole package is self-host tests, so a narrower prefix drops the ones named outside it — "+
			"seven were running nowhere before `^Test` (#8471).", strings.TrimSpace(line))
	}
}
