package sourcelint

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var ambientEnvRe = regexp.MustCompile(`os\.Environ\(\)`)

var ambientFilterRe = regexp.MustCompile(`range\s+os\.Environ\(\)`)

func TestSourcelintChildEnvIsFiltered(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}
	dir := filepath.Join(root, "internal", "testing", "sourcelint")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	var scanned int
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		scanned++
		for i, line := range strings.Split(string(b), "\n") {
			code, _, _ := strings.Cut(line, "//")
			if !ambientEnvRe.MatchString(code) || ambientFilterRe.MatchString(code) {
				continue
			}
			t.Errorf("%s:%d splices the inherited environment straight into a child:\n\t%s\n"+
				"Use ciEnv(...) instead — it strips GITHUB_* / RUNNER_* / FERN_* so an "+
				"ambient value cannot decide what this test asserts (#6833).",
				e.Name(), i+1, strings.TrimSpace(line))
		}
	}

	if scanned == 0 {
		t.Fatal("scanned no _test.go files in internal/testing/sourcelint")
	}
}
