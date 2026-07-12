package modload

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fern.toml path-dependency resolution (docs/PACKAGE-MANAGEMENT-SOTA.md,
// manifest slice). A workspace of two sibling packages: the app declares
// `helper = { path = "../helper" }` and imports it by name — bare
// `import "helper"` reaches the dependency's lib module, and
// `import "helper/extra"` reaches a submodule. The dependency's own
// imports resolve against ITS manifest, and an undeclared bare import
// is an error naming the manifest (resolver-side isolation).

func writeTree(t *testing.T, files map[string]string) string {
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

func TestLoadManifestPathDep(t *testing.T) {
	root := writeTree(t, map[string]string{
		"app/fern.toml": "[package]\nname = \"app\"\n[dependencies]\nhelper = { path = \"../helper\" }\n",
		"app/main.fern": `import "helper";
import "helper/extra";
function main(): i32 { return helper.three() + extra.four(); }`,
		"helper/fern.toml":  "[package]\nname = \"helper\"\n",
		"helper/lib.fern":   "pub function three(): i32 { return 3; }",
		"helper/extra.fern": "pub function four(): i32 { return 4; }",
	})
	prog, _, err := Load(filepath.Join(root, "app", "main.fern"))
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, fn := range prog.Funcs {
		found[fn.Name] = true
	}
	// helper/lib.fern's decls are mangled with the module basename (lib__),
	// the qualified call sites having been rewritten to match.
	if !found["lib__three"] || !found["extra__four"] {
		t.Fatalf("dependency decls not loaded/mangled as expected: %v", found)
	}
}

// The dependency's lib entry honours ITS manifest's `lib` key.
func TestLoadManifestDepCustomLib(t *testing.T) {
	root := writeTree(t, map[string]string{
		"app/fern.toml": "[package]\nname = \"app\"\n[dependencies]\nhelper = { path = \"../helper\" }\n",
		"app/main.fern": `import "helper";
function main(): i32 { return helper.five(); }`,
		"helper/fern.toml": "[package]\nname = \"helper\"\nlib = \"api.fern\"\n",
		"helper/api.fern":  "pub function five(): i32 { return 5; }",
	})
	prog, _, err := Load(filepath.Join(root, "app", "main.fern"))
	if err != nil {
		t.Fatal(err)
	}
	for _, fn := range prog.Funcs {
		if fn.Name == "api__five" {
			return
		}
	}
	t.Fatal("helper's custom lib module (api.fern) was not loaded")
}

// Transitive path deps: helper itself depends on textkit, resolved
// against HELPER's manifest, not the app's.
func TestLoadManifestTransitiveDep(t *testing.T) {
	root := writeTree(t, map[string]string{
		"app/fern.toml": "[package]\nname = \"app\"\n[dependencies]\nhelper = { path = \"../helper\" }\n",
		"app/main.fern": `import "helper";
function main(): i32 { return helper.six(); }`,
		"helper/fern.toml": "[package]\nname = \"helper\"\n[dependencies]\ntextkit = { path = \"../textkit\" }\n",
		"helper/lib.fern": `import "textkit";
pub function six(): i32 { return textkit.six(); }`,
		"textkit/fern.toml": "[package]\nname = \"textkit\"\n",
		"textkit/lib.fern":  "pub function six(): i32 { return 6; }",
	})
	if _, _, err := Load(filepath.Join(root, "app", "main.fern")); err != nil {
		t.Fatal(err)
	}
}

// Isolation: an import that is neither a file nor a declared dependency
// errors, and the error names the governing fern.toml. textkit exists on
// disk but the APP never declared it.
func TestLoadManifestUndeclaredDepErrors(t *testing.T) {
	root := writeTree(t, map[string]string{
		"app/fern.toml": "[package]\nname = \"app\"\n",
		"app/main.fern": `import "textkit";
function main(): i32 { return 0; }`,
		"textkit/fern.toml": "[package]\nname = \"textkit\"\n",
		"textkit/lib.fern":  "pub function six(): i32 { return 6; }",
	})
	_, _, err := Load(filepath.Join(root, "app", "main.fern"))
	if err == nil {
		t.Fatal("expected an undeclared-dependency error")
	}
	if !strings.Contains(err.Error(), "fern.toml") || !strings.Contains(err.Error(), "textkit") {
		t.Errorf("error should name the dep and the manifest: %v", err)
	}
}

// Back-compat inside a manifest package: a bare import that resolves to
// an existing sibling file keeps loading as a plain module path, and
// `./relative` + `std/` imports are untouched by the manifest.
func TestLoadManifestKeepsFileImports(t *testing.T) {
	root := writeTree(t, map[string]string{
		"app/fern.toml":     "[package]\nname = \"app\"\n",
		"app/main.fern":     `import "sub/util";` + "\n" + `import "./local";` + "\n" + `import "std/i32";` + "\n" + `function main(): i32 { return util.one() + local.one(); }`,
		"app/sub/util.fern": "pub function one(): i32 { return 1; }",
		"app/local.fern":    "pub function one(): i32 { return 1; }",
	})
	if _, _, err := Load(filepath.Join(root, "app", "main.fern")); err != nil {
		t.Fatal(err)
	}
}

// A dependency name shadows nothing: when a declared dep name ALSO
// matches a sibling file, the declared dependency wins (the manifest is
// the authority inside its package).
func TestLoadManifestDepWinsOverSiblingFile(t *testing.T) {
	root := writeTree(t, map[string]string{
		"app/fern.toml":    "[package]\nname = \"app\"\n[dependencies]\nhelper = { path = \"../helper\" }\n",
		"app/main.fern":    `import "helper";` + "\n" + `function main(): i32 { return helper.seven(); }`,
		"app/helper.fern":  "pub function seven(): i32 { return 700; }",
		"helper/fern.toml": "[package]\nname = \"helper\"\n",
		"helper/lib.fern":  "pub function seven(): i32 { return 7; }",
	})
	prog, _, err := Load(filepath.Join(root, "app", "main.fern"))
	if err != nil {
		t.Fatal(err)
	}
	for _, fn := range prog.Funcs {
		if fn.Name == "lib__seven" {
			return
		}
	}
	t.Fatal("declared dependency should win over the same-named sibling file")
}

// Manifest-less programs are byte-for-byte unaffected: no fern.toml
// anywhere up the tree, bare imports stay directory-relative.
func TestLoadNoManifestUnchanged(t *testing.T) {
	root := writeTree(t, map[string]string{
		"app/main.fern":     `import "sub/util";` + "\n" + `function main(): i32 { return util.one(); }`,
		"app/sub/util.fern": "pub function one(): i32 { return 1; }",
	})
	if _, _, err := Load(filepath.Join(root, "app", "main.fern")); err != nil {
		t.Fatal(err)
	}
}

// A malformed manifest fails the load with the manifest path + line in
// the error, not a silent fallback to manifest-less behaviour.
func TestLoadManifestParseErrorSurfaces(t *testing.T) {
	root := writeTree(t, map[string]string{
		"app/fern.toml": "[package]\nname = \"app\"\n[dependencies]\nhelper = \"1.2\"\n",
		"app/main.fern": `import "helper";` + "\n" + `function main(): i32 { return 0; }`,
	})
	_, _, err := Load(filepath.Join(root, "app", "main.fern"))
	if err == nil || !strings.Contains(err.Error(), "fern.toml") {
		t.Fatalf("expected a manifest parse error naming fern.toml, got %v", err)
	}
}
