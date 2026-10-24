package modload_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/pkg/modload"
)

func TestLoadDetectsIndirectCycle(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"a.fern": `import "./b";
pub function fa(): i32 { return b.fb(); }`,
		"b.fern": `import "./c";
pub function fb(): i32 { return c.fc(); }`,
		"c.fern": `import "./a";
pub function fc(): i32 { return a.fa(); }`,
	})
	_, _, err := modload.Load(filepath.Join(dir, "a.fern"))
	if err == nil {
		t.Fatal("expected a cycle error for the a→b→c→a loop, got nil")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("error should mention cycle; got %v", err)
	}
}

func TestLoadDetectsSelfImportCycle(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"s.fern": `import "./s";
pub function fs(): i32 { return 1; }`,
	})
	_, _, err := modload.Load(filepath.Join(dir, "s.fern"))
	if err == nil {
		t.Fatal("expected a cycle error for a self-importing module, got nil")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("error should mention cycle; got %v", err)
	}
}
