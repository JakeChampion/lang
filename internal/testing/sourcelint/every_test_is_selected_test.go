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
	re := regexp.MustCompile(`grep -vE '([^']+)'`)
	m := re.FindStringSubmatch(src)
	if m == nil {
		t.Fatal("could not find the exclusion alternation in scripts/unit-test-packages — if its shape changed, update this gate with it")
	}
	excluded := regexp.MustCompile(m[1])
	allowed := map[string]string{
		"e2e":         "test-e2e-{arm64,x86_64,wasm,other}",
		"e2ecompiler": "test-e2e-selfhost",
		"fernsmith":   "test-fernsmith",
		"coreutils":   "test-coreutils",
	}
	for pkg, lane := range allowed {
		if !excluded.MatchString("github.com/jakechampion/lang/internal/testing/" + pkg) {
			t.Errorf("internal/testing/%s is no longer excluded from the unit lane, but %s still runs it — it would now run twice", pkg, lane)
		}
	}
	for _, pkg := range []string{"e2eharness", "corpus", "sourcelint", "e2e/helper", "e2ecompiler/helper", "coreutils/helper"} {
		if excluded.MatchString("github.com/jakechampion/lang/internal/testing/" + pkg) {
			t.Errorf("the unit lane excludes %s without a recursive suite", pkg)
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
