package corpus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaterializeLiterateCorpus(t *testing.T) {
	root, dst := t.TempDir(), t.TempDir()
	files := map[string]string{
		"sources/single.fern.md":  "# Module\n\n```fern\n<<*>>=\n<<body>>\n```\n\n```fern\n<<body>>=\npub const A: i32 = 1;\n```\n",
		"sources/multi.fern.md":   "```fern file=sub/a.fern\npub const B: i32 = 2;\n```\n\n```fern file=plain.fern\nshadow\n```\n",
		"sources/plain.fern":      "original plain source",
		"sources/plain.fern.md":   "```fern\n<<*>>=\n<<undefined>>\n```\n",
		"sources/display.fern.md": "```fern\nfunction shown() {}\n```\n",
		"sources/fern.json":       "{\"name\":\"fixture\"}",
	}
	for path, data := range files {
		target := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := Materialize(root, dst, []string{"sources"}); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		"sources/single.fern":    "pub const A: i32 = 1;",
		"sources/sub/a.fern":     "pub const B: i32 = 2;",
		"sources/plain.fern":     files["sources/plain.fern"],
		"sources/fern.json":      files["sources/fern.json"],
		"sources/single.fern.md": files["sources/single.fern.md"],
	} {
		got, err := os.ReadFile(filepath.Join(dst, path))
		if err != nil || string(got) != want {
			t.Errorf("%s: got %q, %v; want %q", path, got, err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dst, "sources/display.fern")); !os.IsNotExist(err) {
		t.Fatalf("display-only code became a corpus module: %v", err)
	}
}

func TestMaterializeRejectsBrokenSourceDocument(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "broken.fern.md"), []byte("```fern\n<<*>>=\n<<missing>>\n```\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Materialize(root, t.TempDir(), []string{"."})
	if err == nil || !strings.Contains(err.Error(), "broken.fern.md") || !strings.Contains(err.Error(), "undefined chunk") {
		t.Fatalf("broken source diagnostic: %v", err)
	}
}
