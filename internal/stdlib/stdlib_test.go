package stdlib

import (
	"strings"
	"testing"
)

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

func TestResolveRejectsNonNamespacedPath(t *testing.T) {
	if _, ok := Resolve("./util"); ok {
		t.Error("relative paths should not resolve through stdlib")
	}
	if _, ok := Resolve("/abs/path"); ok {
		t.Error("absolute paths should not resolve through stdlib")
	}
}

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
		{"stdlib/foo", false},
		{"", false},
	}
	for _, tt := range tests {
		got := IsStdlibPath(tt.in)
		if got != tt.want {
			t.Errorf("IsStdlibPath(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
