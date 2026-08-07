package e2eharness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The build cache keys a driver binary on the contents of its transitive local
// import closure. Two properties have to hold or the cache hands back a stale
// binary — and a stale self-host driver is the worst failure this repo has,
// because a fix looks applied while every test still exercises the old
// compiler.
//
//  1. Every file the driver compiles is IN the key. A local import that does
//     not resolve must not be skipped in silence, which would drop it.
//  2. Changing any file in the closure changes the key.

func writeClosure(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// TestImportClosureCoversTransitiveLocalImports — the closure is the entry plus
// everything it reaches, not just its direct imports, and it excludes local
// `.fern` files nobody imports (the invariance two tests sharing a cache entry
// rely on).
func TestImportClosureCoversTransitiveLocalImports(t *testing.T) {
	dir := writeClosure(t, map[string]string{
		"entry.fern":     "import \"std/io\";\nimport \"./mid\";\nfunction main(): i32 { return 0; }\n",
		"mid.fern":       "import \"./leaf\";\npub function m(): i32 { return 1; }\n",
		"leaf.fern":      "pub function l(): i32 { return 2; }\n",
		"unrelated.fern": "pub function u(): i32 { return 3; }\n",
	})

	got := map[string]bool{}
	for _, p := range SelfHostImportClosure(t, dir, "entry.fern") {
		got[filepath.Base(p)] = true
	}
	for _, want := range []string{"entry.fern", "mid.fern", "leaf.fern"} {
		if !got[want] {
			t.Errorf("closure is missing %s — it would be absent from the cache key", want)
		}
	}
	if got["unrelated.fern"] {
		t.Error("closure includes unrelated.fern, which the entry does not import")
	}
}

// TestImportClosureKeyChangesWhenAnyClosureFileChanges — the property the cache
// exists to have. Asserted at the LEAF, the file furthest from the entry, since
// a key that only tracked direct imports would still pass at depth 1.
func TestImportClosureKeyChangesWhenAnyClosureFileChanges(t *testing.T) {
	files := map[string]string{
		"entry.fern": "import \"./mid\";\nfunction main(): i32 { return 0; }\n",
		"mid.fern":   "import \"./leaf\";\npub function m(): i32 { return 1; }\n",
		"leaf.fern":  "pub function l(): i32 { return 2; }\n",
	}
	dir := writeClosure(t, files)
	before := HashSelfHostSources(t, dir, "entry.fern")

	if err := os.WriteFile(filepath.Join(dir, "leaf.fern"),
		[]byte("pub function l(): i32 { return 99; }\n"), 0o644); err != nil {
		t.Fatalf("rewrite leaf: %v", err)
	}
	after := HashSelfHostSources(t, dir, "entry.fern")

	if before == after {
		t.Error("hash unchanged after editing a transitively-imported source — " +
			"the cache would serve a binary built from the old sources")
	}
}

// TestImportClosureIgnoresUnrelatedFileChanges — the other half: a `.fern` the
// entry does not import must NOT move the key, or two tests that stage
// different extra drivers alongside the same stock one stop sharing a cache
// entry and each pays a cold multi-GB build.
func TestImportClosureIgnoresUnrelatedFileChanges(t *testing.T) {
	dir := writeClosure(t, map[string]string{
		"entry.fern": "function main(): i32 { return 0; }\n",
		"other.fern": "pub function o(): i32 { return 1; }\n",
	})
	before := HashSelfHostSources(t, dir, "entry.fern")

	if err := os.WriteFile(filepath.Join(dir, "other.fern"),
		[]byte("pub function o(): i32 { return 42; }\n"), 0o644); err != nil {
		t.Fatalf("rewrite other: %v", err)
	}
	if after := HashSelfHostSources(t, dir, "entry.fern"); before != after {
		t.Error("hash moved after editing a file the entry does not import — " +
			"drivers staged alongside different siblings stop sharing a cache entry")
	}
}

// TestImportClosureExternalImportClassification — `std/…` and `core/…` are
// deliberately outside the key; `./x` and a bare `x` are local and must
// resolve. Leaving that distinction implicit in "the path happens not to
// exist" makes a MISSING local source look exactly like a stdlib import.
func TestImportClosureExternalImportClassification(t *testing.T) {
	for _, c := range []struct {
		imp      string
		external bool
	}{
		{"std/io", true},
		{"core/int", true},
		{"./lexer", false},
		{"lexer", false},
		{"./sub/lexer", false},
	} {
		if got := isExternalFernImport(c.imp); got != c.external {
			t.Errorf("isExternalFernImport(%q) = %v, want %v", c.imp, got, c.external)
		}
	}
}

// TestImportClosureFailsOnMissingLocalImport — the hazard itself. A local
// import naming a file the project dir does not have must be a hard error, not
// a silent omission from the cache key.
func TestImportClosureFailsOnMissingLocalImport(t *testing.T) {
	dir := writeClosure(t, map[string]string{
		"entry.fern": "import \"./gone\";\nfunction main(): i32 { return 0; }\n",
	})
	_, err := selfHostImportClosure(dir, "entry.fern")
	if err == nil {
		t.Fatal("a local import of a missing file was accepted — it would be omitted " +
			"from the cache key, so the driver would keep hashing the same after that " +
			"source changed")
	}
	if !strings.Contains(err.Error(), "gone.fern") {
		t.Errorf("error should name the missing file, got: %v", err)
	}
}

// TestImportClosureAllowsMissingExternalImport — the flip side: `std/io` never
// resolves under a project dir and must stay a no-op, or every real driver
// fails to hash.
func TestImportClosureAllowsMissingExternalImport(t *testing.T) {
	dir := writeClosure(t, map[string]string{
		"entry.fern": "import \"std/io\";\nimport \"core/int\";\nfunction main(): i32 { return 0; }\n",
	})
	files := SelfHostImportClosure(t, dir, "entry.fern")
	if len(files) != 1 || !strings.HasSuffix(files[0], "entry.fern") {
		t.Errorf("closure = %v, want just the entry (stdlib imports are outside the key)", files)
	}
}
