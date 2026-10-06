// Package e2eharness holds the shared e2e test harness — driver builds,
// tooling discovery, caches — used by both internal/testing/e2e and
// internal/testing/e2ecompiler (#4398 part 3). Extracted verbatim from
// internal/testing/e2e/self_host_extern_string_param_test.go.
package e2eharness

import (
	"os"
	"path/filepath"
	"testing"
)

// MustWrite writes content to dir/name and returns the path.
func MustWrite(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}
