package modload_test

// Cycle detection beyond the direct two-module case.
//
// TestLoadDetectsCycle covers the immediate a↔b back-edge. These pin
// the two shapes it doesn't exercise, both of which depend on the
// loader's on-the-stack walk (loadRecursive's `stack[path]` guard)
// rather than the already-loaded short-circuit:
//
//   - an INDIRECT cycle a→b→c→a, where the back-edge to `a` is only
//     reached three frames deep. A detector that only compared
//     against the immediate parent (rather than the whole active
//     stack) would miss this and recurse forever / stack-overflow.
//   - a SELF import s→s, the degenerate one-node cycle.
//
// Both must surface a "cycle" error from the user-import path. (The
// stdlib path deliberately tolerates cycles — see
// TestLoadAllowsStdlibCyclesViaUserImport — so this is specifically
// the disk-module policy.)

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/modload"
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
