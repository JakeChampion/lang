package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/pkg/embed"
)

// writeAssets materialises an asset tree and loads it the way the -embed
// flag does, leaving it installed as the compile-time asset set for the
// duration of the test.
func writeAssets(t *testing.T, files map[string]string) {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	set, err := embed.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	prev := embeddedAssets
	embeddedAssets = set
	t.Cleanup(func() { embeddedAssets = prev })
}

// Without -embed, __fern_asset is a compile error naming the flag, not an
// "undefined identifier" from the checker.
func TestEmbedMissingFlagIsADiagnostic(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	prog := `function main(): i32 {
    let s: string = __fern_asset("a.txt");
    return 0;
}
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	prev := embeddedAssets
	embeddedAssets = nil
	t.Cleanup(func() { embeddedAssets = prev })

	err := runCheck(src, "")
	if err == nil {
		t.Fatal("expected -check to reject __fern_asset with no -embed")
	}
	if !strings.Contains(err.Error(), "-embed") {
		t.Fatalf("error %q should point at the -embed flag", err)
	}
}

// A misspelled asset names the near miss rather than only failing.
func TestEmbedUnknownAssetSuggests(t *testing.T) {
	writeAssets(t, map[string]string{"html/index.html": "x"})
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	prog := `function main(): i32 {
    let s: string = __fern_asset("html/index.htm");
    return 0;
}
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	err := runCheck(src, "")
	if err == nil {
		t.Fatal("expected -check to reject an unknown asset")
	}
	if !strings.Contains(err.Error(), `did you mean "html/index.html"`) {
		t.Fatalf("error %q should suggest the near miss", err)
	}
}

// An embed directory holding no files is legitimate: the loop body just
// never runs. Without a stamped element type this fails to compile with
// "E020: empty array literal needs a type annotation" pointing at the
// `for`, which the user cannot act on.
func TestEmbedEnumerationEmptyBundleCompiles(t *testing.T) {
	writeAssets(t, map[string]string{})
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	prog := `function main(): i32 {
    let n: i32 = 0;
    for a in __fern_assets() {
        n = n + 1;
    }
    return n;
}
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runCheck(src, ""); err != nil {
		t.Fatalf("an empty embed bundle must still type-check: %v", err)
	}
}
