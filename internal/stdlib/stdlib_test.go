package stdlib

import (
	"strings"
	"testing"
)

// Resolve hands back the embedded source for known stdlib modules.
// The Phase-1 test fixtures (_test_empty under both prefixes) are
// the canonical "does the embedded FS wiring work" probe.
func TestResolveStd(t *testing.T) {
	src, ok := Resolve("std/_test_empty")
	if !ok {
		t.Fatal("expected std/_test_empty to resolve")
	}
	if !strings.Contains(src, "stdlib_test_marker") {
		t.Errorf("expected source to contain `stdlib_test_marker`; got %q", src)
	}
}

func TestResolveCore(t *testing.T) {
	src, ok := Resolve("core/_test_empty")
	if !ok {
		t.Fatal("expected core/_test_empty to resolve")
	}
	if !strings.Contains(src, "core_test_marker") {
		t.Errorf("expected source to contain `core_test_marker`; got %q", src)
	}
}

// Auto-append `.fern` extension: callers pass either `std/foo` or
// `std/foo.fern` and both resolve to the same module. Mirrors the
// extension-tolerance of the disk resolver in modload.
func TestResolveAutoAppendsExtension(t *testing.T) {
	src1, ok1 := Resolve("std/_test_empty")
	src2, ok2 := Resolve("std/_test_empty.fern")
	if !ok1 || !ok2 {
		t.Fatalf("both forms should resolve; got ok1=%v ok2=%v", ok1, ok2)
	}
	if src1 != src2 {
		t.Errorf("with and without .fern extension should match")
	}
}

// A path outside the namespaced prefixes returns `(_, false)` —
// `Resolve` doesn't speculatively read from disk or anywhere else.
func TestResolveRejectsNonNamespacedPath(t *testing.T) {
	if _, ok := Resolve("./util"); ok {
		t.Error("relative paths should not resolve through stdlib")
	}
	if _, ok := Resolve("/abs/path"); ok {
		t.Error("absolute paths should not resolve through stdlib")
	}
}

// A namespaced path that doesn't exist in the embedded tree
// returns `(_, false)` — callers (modload) can translate that
// into a clear "unknown stdlib module" diagnostic instead of
// falling back to filesystem resolution.
func TestResolveUnknownModule(t *testing.T) {
	if _, ok := Resolve("std/does_not_exist"); ok {
		t.Error("expected nonexistent stdlib module to miss")
	}
	if _, ok := Resolve("core/also_nonexistent"); ok {
		t.Error("expected nonexistent core module to miss")
	}
}

func TestIsStdlibPath(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"std/foo", true},
		{"core/bar", true},
		{"std/foo.fern", true},
		{"./foo", false},
		{"/abs", false},
		{"stdlib/foo", false}, // not the namespace prefix
		{"", false},
	}
	for _, tt := range tests {
		got := IsStdlibPath(tt.in)
		if got != tt.want {
			t.Errorf("IsStdlibPath(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
