package e2eharness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestImportClosureFollowsParentImports(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "multicall")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for path, src := range map[string]string{
		filepath.Join(sub, "entry.fern"): "import \"../util\";\nfunction main(): i32 { return 0; }\n",
		filepath.Join(dir, "util.fern"):  "import \"./lib/leaf\";\npub function u(): i32 { return 1; }\n",
	} {
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lib", "leaf.fern"), []byte("pub function l(): i32 { return 2; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, p := range SelfHostImportClosure(t, sub, "entry.fern") {
		got[filepath.Base(p)] = true
	}
	for _, want := range []string{"entry.fern", "util.fern", "leaf.fern"} {
		if !got[want] {
			t.Errorf("closure is missing %s — it would be absent from the cache key", want)
		}
	}
}

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
		{"../cat", false},
		{"../lib/gnu", false},
	} {
		if got := isExternalFernImport(c.imp); got != c.external {
			t.Errorf("isExternalFernImport(%q) = %v, want %v", c.imp, got, c.external)
		}
	}
}

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

func TestImportClosureAllowsMissingExternalImport(t *testing.T) {
	dir := writeClosure(t, map[string]string{
		"entry.fern": "import \"std/io\";\nimport \"core/int\";\nfunction main(): i32 { return 0; }\n",
	})
	files := SelfHostImportClosure(t, dir, "entry.fern")
	if len(files) != 1 || !strings.HasSuffix(files[0], "entry.fern") {
		t.Errorf("closure = %v, want just the entry (stdlib imports are outside the key)", files)
	}
}

func TestImportClosureTracksSameLineImports(t *testing.T) {
	dir := writeClosure(t, map[string]string{
		"entry.fern": `import "std/io"; import "./first"; import "./second" as second;
function main(): i32 { return 0; }`,
		"first.fern":  "pub function a(): i32 { return 1; }",
		"second.fern": "pub function b(): i32 { return 2; }",
	})
	files := SelfHostImportClosure(t, dir, "entry.fern")
	if len(files) != 3 {
		t.Errorf("closure = %v, want entry plus both same-line local imports", files)
	}
	before := HashSelfHostSources(t, dir, "entry.fern")
	if err := os.WriteFile(filepath.Join(dir, "second.fern"), []byte("pub function b(): i32 { return 99; }"), 0o644); err != nil {
		t.Fatal(err)
	}
	if after := HashSelfHostSources(t, dir, "entry.fern"); before == after {
		t.Error("editing the second same-line import did not invalidate the binary cache")
	}
}

func TestImportClosureIgnoresCommentAndLiteralImports(t *testing.T) {
	dir := writeClosure(t, map[string]string{
		"entry.fern": `// import "./missing_comment";
import // a comment can separate the keyword and path
"./leaf";
function main(): i32 {
  let s: string = "import \"./missing_string\";";
  let f: string = f"import \"./missing_fstring\";";
  return 0;
}`,
		"leaf.fern": "pub function l(): i32 { return 1; }",
	})
	files, err := selfHostImportClosure(dir, "entry.fern")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || filepath.Base(files[1]) != "leaf.fern" {
		t.Errorf("closure = %v, want entry and leaf, not import-like comment/literal text", files)
	}
}

func TestImportClosureTracksReexportsAndDecodedPaths(t *testing.T) {
	dir := writeClosure(t, map[string]string{
		"entry.fern": `pub use "./mid".{m}; function main(): i32 { return 0; }`,
		"mid.fern":   `import "\x2e/leaf"; pub function m(): i32 { return leaf.l(); }`,
		"leaf.fern":  "pub function l(): i32 { return 2; }",
	})
	files := SelfHostImportClosure(t, dir, "entry.fern")
	if len(files) != 3 {
		t.Errorf("closure = %v, want reexport and its decoded-path dependency", files)
	}
	before := HashSelfHostSources(t, dir, "entry.fern")
	if err := os.WriteFile(filepath.Join(dir, "leaf.fern"), []byte("pub function l(): i32 { return 99; }"), 0o644); err != nil {
		t.Fatal(err)
	}
	if after := HashSelfHostSources(t, dir, "entry.fern"); before == after {
		t.Error("editing a transitively reexported dependency did not invalidate the cache")
	}
}

func TestImportClosureFailsOnMissingSameLineLocalImport(t *testing.T) {
	dir := writeClosure(t, map[string]string{
		"entry.fern": `import "std/io"; import "./gone"; function main(): i32 { return 0; }`,
	})
	_, err := selfHostImportClosure(dir, "entry.fern")
	if err == nil || !strings.Contains(err.Error(), "gone.fern") {
		t.Fatalf("missing second import must fail naming gone.fern, got %v", err)
	}
}

func TestImportClosureFailsOnUnlexableSource(t *testing.T) {
	dir := writeClosure(t, map[string]string{"entry.fern": `import "./unterminated`})
	_, err := selfHostImportClosure(dir, "entry.fern")
	if err == nil {
		t.Fatal("unlexable source must not yield a partial dependency closure")
	}
}
