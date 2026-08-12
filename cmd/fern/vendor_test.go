package main

import (
	"os"
	"path/filepath"
	"strings"
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

// A workspace root vendors the UNION of all members' external deps into
// the root vendor/, skipping in-tree workspace-member deps; members then
// resolve those deps out of the root vendor/ (proven by deleting the
// original external dep dir).
func TestRunVendorWorkspaceUnion(t *testing.T) {
	root := writeVendorTree(t, map[string]string{
		"fern.toml":       "[workspace]\nmembers = [\"lexer\", \"app\"]\n",
		"lexer/fern.toml": "[package]\nname = \"lexer\"\n[dependencies]\next = { path = \"../ext\" }\n",
		"lexer/lib.fern":  `import "ext";` + "\n" + `pub function token(): i32 { return ext.val(); }`,
		"app/fern.toml":   "[package]\nname = \"app\"\n[dependencies]\nlexer = { workspace = true }\n",
		"app/main.fern":   `import "lexer";` + "\n" + `function main(): i32 { return lexer.token(); }`,
		"ext/fern.toml":   "[package]\nname = \"ext\"\n",
		"ext/lib.fern":    "pub function val(): i32 { return 41; }",
	})
	if err := runVendor(root); err != nil {
		t.Fatal(err)
	}
	// The external dep is vendored; the in-tree workspace member is not.
	if _, err := os.Stat(filepath.Join(root, "vendor", "ext", "lib.fern")); err != nil {
		t.Fatalf("ext should be vendored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "vendor", "lexer")); !os.IsNotExist(err) {
		t.Error("workspace member 'lexer' should not be vendored (it's in-tree)")
	}
	// Delete the original ext — the member build must now resolve it from
	// the root vendor/.
	if err := os.RemoveAll(filepath.Join(root, "ext")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := modload.Load(filepath.Join(root, "app", "main.fern")); err != nil {
		t.Fatalf("member build should resolve ext from the root vendor/: %v", err)
	}
}

// A versioned (MVS) dependency names no directory of its own, so its source
// has to come from fern.lock — the same place a build reads it from. Before
// this resolved through the lock, `m.DepDir` answered the manifest's OWN
// directory: the package was vendored into its own vendor/ under its own
// name, and the dependency was dropped silently.
func TestRunVendorVersionedDepThroughLock(t *testing.T) {
	root := writeVendorTree(t, map[string]string{
		"app/fern.toml": "[package]\nname = \"app\"\nindex = \"../index.toml\"\n" +
			"[dependencies]\nlibv = \"1.0.0\"\n",
		"app/main.fern":        `import "libv";` + "\n" + `function main(): i32 { return libv.n(); }`,
		"index.toml":           "[libv]\n\"1.0.0\" = { path = \"libv-1.0.0\" }\n",
		"libv-1.0.0/fern.toml": "[package]\nname = \"libv\"\n",
		"libv-1.0.0/lib.fern":  "pub function n(): i32 { return 7; }",
	})
	app := filepath.Join(root, "app")
	if err := runResolve(app); err != nil {
		t.Fatal(err)
	}
	if err := runVendor(app); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(app, "vendor", "libv", "lib.fern")); err != nil {
		t.Fatalf("the locked version should be vendored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(app, "vendor", "app")); !os.IsNotExist(err) {
		t.Error("the package vendored ITSELF — a versioned dep resolved to its own manifest dir")
	}
	// The pinned source is now the only copy the build can reach.
	if err := os.RemoveAll(filepath.Join(root, "libv-1.0.0")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := modload.Load(filepath.Join(app, "main.fern")); err != nil {
		t.Fatalf("vendored load of a versioned dep should be offline: %v", err)
	}
}

// A versioned dependency with no lock at all is refused, naming `-resolve` —
// vendoring never resolves versions on its own.
func TestRunVendorVersionedDepWithoutLock(t *testing.T) {
	root := writeVendorTree(t, map[string]string{
		"app/fern.toml": "[package]\nname = \"app\"\nindex = \"../index.toml\"\n" +
			"[dependencies]\nlibv = \"1.0.0\"\n",
		"app/main.fern": `function main(): i32 { return 0; }`,
	})
	err := runVendor(filepath.Join(root, "app"))
	if err == nil {
		t.Fatal("expected a refusal for a versioned dep with no fern.lock")
	}
	if !strings.Contains(err.Error(), "fern -resolve") {
		t.Errorf("refusal should name `fern -resolve`, got: %v", err)
	}
}
