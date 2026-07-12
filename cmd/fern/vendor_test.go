package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/modload"
)

func writeVendorTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, src := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// `fern -vendor` flattens the transitive path-dep graph into vendor/,
// after which a load resolves everything from vendor/ (proven by
// deleting the original dependency directories before loading).
func TestRunVendorThenLoadOffline(t *testing.T) {
	root := writeVendorTree(t, map[string]string{
		"app/fern.toml":     "[package]\nname = \"app\"\n[dependencies]\nhelper = { path = \"../helper\" }\n",
		"app/main.fern":     `import "helper";` + "\n" + `function main(): i32 { return helper.twelve(); }`,
		"helper/fern.toml":  "[package]\nname = \"helper\"\n[dependencies]\ntextkit = { path = \"../textkit\" }\n",
		"helper/lib.fern":   `import "textkit";` + "\n" + `pub function twelve(): i32 { return textkit.twelve(); }`,
		"textkit/fern.toml": "[package]\nname = \"textkit\"\n",
		"textkit/lib.fern":  "pub function twelve(): i32 { return 12; }",
	})
	app := filepath.Join(root, "app")
	if err := runVendor(app); err != nil {
		t.Fatal(err)
	}
	// Both transitive packages are present in the flat vendor tree.
	for _, name := range []string{"helper", "textkit"} {
		if _, err := os.Stat(filepath.Join(app, "vendor", name, "lib.fern")); err != nil {
			t.Fatalf("vendor/%s/lib.fern missing: %v", name, err)
		}
	}
	// Remove the originals — the load must now be fully offline.
	if err := os.RemoveAll(filepath.Join(root, "helper")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "textkit")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := modload.Load(filepath.Join(app, "main.fern")); err != nil {
		t.Fatalf("vendored load should be offline: %v", err)
	}
}

// Two distinct packages sharing a name can't be vendored into the flat
// namespace — a hard error, not a silent overwrite.
func TestRunVendorNameCollision(t *testing.T) {
	root := writeVendorTree(t, map[string]string{
		"app/fern.toml":  "[package]\nname = \"app\"\n[dependencies]\na = { path = \"../a\" }\nb = { path = \"../b\" }\n",
		"app/main.fern":  `function main(): i32 { return 0; }`,
		"a/fern.toml":    "[package]\nname = \"a\"\n[dependencies]\ndup = { path = \"../dupa\" }\n",
		"a/lib.fern":     "pub function x(): i32 { return 1; }",
		"b/fern.toml":    "[package]\nname = \"b\"\n[dependencies]\ndup = { path = \"../dupb\" }\n",
		"b/lib.fern":     "pub function y(): i32 { return 2; }",
		"dupa/fern.toml": "[package]\nname = \"clash\"\n",
		"dupa/lib.fern":  "pub function p(): i32 { return 1; }",
		"dupb/fern.toml": "[package]\nname = \"clash\"\n",
		"dupb/lib.fern":  "pub function q(): i32 { return 2; }",
	})
	err := runVendor(filepath.Join(root, "app"))
	if err == nil {
		t.Fatal("expected a name-collision error")
	}
}

// copyPackage takes only source files (fern.toml + .fern), skipping a
// nested vendor/ and non-source files.
func TestVendorCopiesOnlySources(t *testing.T) {
	root := writeVendorTree(t, map[string]string{
		"app/fern.toml":        "[package]\nname = \"app\"\n[dependencies]\nh = { path = \"../h\" }\n",
		"app/main.fern":        `import "h";` + "\n" + `function main(): i32 { return h.one(); }`,
		"h/fern.toml":          "[package]\nname = \"h\"\n",
		"h/lib.fern":           "pub function one(): i32 { return 1; }",
		"h/README.md":          "not a source file",
		"h/vendor/junk/x.fern": "pub function junk(): i32 { return 9; }",
	})
	app := filepath.Join(root, "app")
	if err := runVendor(app); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(app, "vendor", "h", "lib.fern")); err != nil {
		t.Fatalf("lib.fern not vendored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(app, "vendor", "h", "README.md")); !os.IsNotExist(err) {
		t.Error("non-source README.md should not be vendored")
	}
	if _, err := os.Stat(filepath.Join(app, "vendor", "h", "vendor")); !os.IsNotExist(err) {
		t.Error("nested vendor/ should not be copied")
	}
}
