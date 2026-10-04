package launcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStdlibRootWritesTheEmbeddedTreeOnce(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	dir, err := StdlibRoot()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"std/io.fern", "core/int.fern"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s missing from %s: %v", f, dir, err)
		}
	}
	// A second call reuses the copy rather than writing another.
	if err := os.WriteFile(filepath.Join(dir, "marker"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	again, err := StdlibRoot()
	if err != nil {
		t.Fatal(err)
	}
	if again != dir {
		t.Errorf("second call gave %s, want %s", again, dir)
	}
	if _, err := os.Stat(filepath.Join(again, "marker")); err != nil {
		t.Errorf("second call rewrote the directory: %v", err)
	}
}

func TestCompilerPrefersTheEnvironment(t *testing.T) {
	p := filepath.Join(t.TempDir(), "fern-selfhost")
	if err := os.WriteFile(p, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvCompiler, p)
	got, err := Compiler()
	if err != nil || got != p {
		t.Fatalf("Compiler() = %q, %v; want %q", got, err, p)
	}
	t.Setenv(EnvCompiler, filepath.Join(t.TempDir(), "absent"))
	if _, err := Compiler(); err == nil || !strings.Contains(err.Error(), EnvCompiler) {
		t.Errorf("a missing %s gave %v, want an error naming it", EnvCompiler, err)
	}
}
