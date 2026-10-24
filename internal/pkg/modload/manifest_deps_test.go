package modload

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	if !found["lib__three"] || !found["extra__four"] {
		t.Fatalf("dependency decls not loaded/mangled as expected: %v", found)
	}
}

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
	var ie *importError
	if !errors.As(err, &ie) || ie.Code() != "E086" || ie.Position().Line != 1 {
		t.Errorf("want E086 at the import on line 1, got %v", err)
	}
}

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

func TestLoadNoManifestUnchanged(t *testing.T) {
	root := writeTree(t, map[string]string{
		"app/main.fern":     `import "sub/util";` + "\n" + `function main(): i32 { return util.one(); }`,
		"app/sub/util.fern": "pub function one(): i32 { return 1; }",
	})
	if _, _, err := Load(filepath.Join(root, "app", "main.fern")); err != nil {
		t.Fatal(err)
	}
}

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

func TestLoadManifestURLDepFromStore(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("FERN_CACHE_DIR", cache)
	hex64 := strings.Repeat("ab", 32)
	store := filepath.Join(cache, "pkgs", hex64)
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "lib.fern"), []byte("pub function eight(): i32 { return 8; }"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := writeTree(t, map[string]string{
		"app/fern.toml": "[package]\nname = \"app\"\n[dependencies]\nhelper = { url = \"https://example.com/h.tar.gz\", hash = \"sha256:" + hex64 + "\" }\n",
		"app/main.fern": `import "helper";` + "\n" + `function main(): i32 { return helper.eight(); }`,
	})
	prog, _, err := Load(filepath.Join(root, "app", "main.fern"))
	if err != nil {
		t.Fatal(err)
	}
	for _, fn := range prog.Funcs {
		if fn.Name == "lib__eight" {
			return
		}
	}
	t.Fatal("url dependency's lib module was not loaded from the store")
}

func TestLoadManifestURLDepUnfetchedErrors(t *testing.T) {
	t.Setenv("FERN_CACHE_DIR", t.TempDir())
	hex64 := strings.Repeat("cd", 32)
	root := writeTree(t, map[string]string{
		"app/fern.toml": "[package]\nname = \"app\"\n[dependencies]\nhelper = { url = \"https://example.com/h.tar.gz\", hash = \"sha256:" + hex64 + "\" }\n",
		"app/main.fern": `import "helper";` + "\n" + `function main(): i32 { return 0; }`,
	})
	_, _, err := Load(filepath.Join(root, "app", "main.fern"))
	if err == nil || !strings.Contains(err.Error(), "-fetch") {
		t.Fatalf("want an error pointing at `fern -fetch`, got %v", err)
	}
}

func TestLoadVendoredResolution(t *testing.T) {
	root := writeTree(t, map[string]string{
		"app/fern.toml":               "[package]\nname = \"app\"\n[dependencies]\nhelper = { path = \"../gone\" }\n",
		"app/main.fern":               `import "helper";` + "\n" + `function main(): i32 { return helper.ten(); }`,
		"app/vendor/helper/fern.toml": "[package]\nname = \"helper\"\n",
		"app/vendor/helper/lib.fern":  "pub function ten(): i32 { return 10; }",
	})
	prog, _, err := Load(filepath.Join(root, "app", "main.fern"))
	if err != nil {
		t.Fatal(err)
	}
	for _, fn := range prog.Funcs {
		if fn.Name == "lib__ten" {
			return
		}
	}
	t.Fatal("vendored dependency's lib module was not loaded from vendor/")
}

func TestLoadVendoredTransitiveFlat(t *testing.T) {
	root := writeTree(t, map[string]string{
		"app/fern.toml":                "[package]\nname = \"app\"\n[dependencies]\nhelper = { path = \"../gone\" }\n",
		"app/main.fern":                `import "helper";` + "\n" + `function main(): i32 { return helper.eleven(); }`,
		"app/vendor/helper/fern.toml":  "[package]\nname = \"helper\"\n[dependencies]\ntextkit = { path = \"../../../gone2\" }\n",
		"app/vendor/helper/lib.fern":   `import "textkit";` + "\n" + `pub function eleven(): i32 { return textkit.eleven(); }`,
		"app/vendor/textkit/fern.toml": "[package]\nname = \"textkit\"\n",
		"app/vendor/textkit/lib.fern":  "pub function eleven(): i32 { return 11; }",
	})
	if _, _, err := Load(filepath.Join(root, "app", "main.fern")); err != nil {
		t.Fatal(err)
	}
}

func TestLoadVendoredMissingDepErrors(t *testing.T) {
	root := writeTree(t, map[string]string{
		"app/fern.toml":               "[package]\nname = \"app\"\n[dependencies]\nhelper = { path = \"../helper\" }\nother = { path = \"../other\" }\n",
		"app/main.fern":               `import "other";` + "\n" + `function main(): i32 { return 0; }`,
		"app/vendor/helper/fern.toml": "[package]\nname = \"helper\"\n",
		"app/vendor/helper/lib.fern":  "pub function x(): i32 { return 1; }",
	})
	_, _, err := Load(filepath.Join(root, "app", "main.fern"))
	if err == nil || !strings.Contains(err.Error(), "-vendor") {
		t.Fatalf("want an error pointing at `fern -vendor`, got %v", err)
	}
}

func TestLoadWorkspaceMemberDep(t *testing.T) {
	root := writeTree(t, map[string]string{
		"fern.toml":       "[workspace]\nmembers = [\"app\", \"lexer\"]\n",
		"app/fern.toml":   "[package]\nname = \"app\"\n[dependencies]\nlexer = { workspace = true }\n",
		"app/main.fern":   `import "lexer";` + "\n" + `function main(): i32 { return lexer.token(); }`,
		"lexer/fern.toml": "[package]\nname = \"lexer\"\n",
		"lexer/lib.fern":  "pub function token(): i32 { return 42; }",
	})
	prog, _, err := Load(filepath.Join(root, "app", "main.fern"))
	if err != nil {
		t.Fatal(err)
	}
	for _, fn := range prog.Funcs {
		if fn.Name == "lib__token" {
			return
		}
	}
	t.Fatal("workspace member 'lexer' was not resolved by name")
}

func TestLoadWorkspaceMemberByPackageName(t *testing.T) {
	root := writeTree(t, map[string]string{
		"fern.toml":          "[workspace]\nmembers = [\"app\", \"pkgs/lex\"]\n",
		"app/fern.toml":      "[package]\nname = \"app\"\n[dependencies]\nlexer = { workspace = true }\n",
		"app/main.fern":      `import "lexer";` + "\n" + `function main(): i32 { return lexer.token(); }`,
		"pkgs/lex/fern.toml": "[package]\nname = \"lexer\"\n",
		"pkgs/lex/lib.fern":  "pub function token(): i32 { return 7; }",
	})
	if _, _, err := Load(filepath.Join(root, "app", "main.fern")); err != nil {
		t.Fatal(err)
	}
}

func TestLoadWorkspaceMemberMissingErrors(t *testing.T) {
	root := writeTree(t, map[string]string{
		"fern.toml":     "[workspace]\nmembers = [\"app\"]\n",
		"app/fern.toml": "[package]\nname = \"app\"\n[dependencies]\nghost = { workspace = true }\n",
		"app/main.fern": `import "ghost";` + "\n" + `function main(): i32 { return 0; }`,
	})
	_, _, err := Load(filepath.Join(root, "app", "main.fern"))
	if err == nil || !strings.Contains(err.Error(), "workspace") {
		t.Fatalf("want a workspace-member error, got %v", err)
	}
}

func TestLoadWorkspaceIsolation(t *testing.T) {
	root := writeTree(t, map[string]string{
		"fern.toml":       "[workspace]\nmembers = [\"app\", \"lexer\"]\n",
		"app/fern.toml":   "[package]\nname = \"app\"\n",
		"app/main.fern":   `import "lexer";` + "\n" + `function main(): i32 { return 0; }`,
		"lexer/fern.toml": "[package]\nname = \"lexer\"\n",
		"lexer/lib.fern":  "pub function token(): i32 { return 1; }",
	})
	_, _, err := Load(filepath.Join(root, "app", "main.fern"))
	if err == nil {
		t.Fatal("undeclared workspace sibling should not resolve")
	}
}
