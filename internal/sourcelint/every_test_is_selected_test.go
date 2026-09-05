package sourcelint

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every meta-gate in this repo asks whether a lane's SELECTOR resolves —
// `make testnames` verifies that a name a workflow names is a real test.
// None asked the inverse: whether every test is selected by something. Two
// families were running nowhere at all when that question was finally put
// (#8470, #8471):
//
//   - internal/e2eharness, 22 tests including the in-process linker parity
//     gates and TestX86StructuredMatchesText, excluded from the derived unit
//     set on the stale grounds that it had "no tests of its own".
//   - seven tests in internal/e2eselfhost whose names did not start with
//     `TestSelfHost`, which is what the shards selected on.
//
// Both are fixed at the source — the exclusion is gone and the shards select
// `^Test` — and these two tests keep them fixed. They are cheap and textual
// on purpose: the expensive general version (parse every workflow, resolve
// every selector, diff against every test in the tree) is worth building, but
// a gate that exists beats one that is planned.

// TestUnitLaneCoversEveryNonSlowPackage pins the exclusion list in
// scripts/unit-test-packages. A package may be excluded only if it has a
// workflow of its own; anything else excluded is a package nothing runs.
func TestUnitLaneCoversEveryNonSlowPackage(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "scripts", "unit-test-packages"))
	if err != nil {
		t.Fatalf("read unit-test-packages: %v", err)
	}
	src := string(b)
	// The exclusions are one grep -vE alternation; read it back rather than
	// trusting the comment above it, which is what went stale.
	re := regexp.MustCompile(`grep -vE '/internal/\(([^)]*)\)`)
	m := re.FindStringSubmatch(src)
	if m == nil {
		t.Fatal("could not find the exclusion alternation in scripts/unit-test-packages — if its shape changed, update this gate with it")
	}
	excluded := strings.Split(m[1], "|")
	// Each of these has a workflow of its own. internal/e2eharness is
	// deliberately NOT here: it is fast and nothing else runs it.
	allowed := map[string]string{
		"e2e":         "test-e2e-{arm64,x86_64,wasm,other}",
		"e2eselfhost": "test-e2e-selfhost",
		"fernsmith":   "test-fernsmith",
	}
	for _, pkg := range excluded {
		pkg = strings.TrimSpace(pkg)
		if pkg == "" {
			continue
		}
		if _, ok := allowed[pkg]; !ok {
			t.Errorf("internal/%s is excluded from the unit lane but has no workflow of its own, so nothing runs its tests. "+
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
			t.Errorf("internal/%s is no longer excluded from the unit lane, but %s still runs it — it would now run twice", pkg, lane)
		}
	}
}

// TestSelfHostShardsSelectEveryTest pins the shard selector. The e2eselfhost
// package is entirely self-host tests, so the shards must list `^Test`: a
// narrower prefix silently drops any test named outside it, which is exactly
// how seven of them came to run nowhere.
func TestSelfHostShardsSelectEveryTest(t *testing.T) {
	path := filepath.Join("..", "..", ".github", "workflows", "test-e2e-selfhost.yml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read test-e2e-selfhost.yml: %v", err)
	}
	src := string(b)
	// SH_ALL is the e2eselfhost binary's list; RES_ALL is the internal/e2e
	// one, which legitimately keeps the prefix because it is filtering
	// self-host tests OUT of a package with its own lanes.
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
		t.Errorf("the e2eselfhost shards do not select every test:\n  %s\n"+
			"The whole package is self-host tests, so a narrower prefix drops the ones named outside it — "+
			"seven were running nowhere before `^Test` (#8471).", strings.TrimSpace(line))
	}
}
