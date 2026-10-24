package literate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFileTanglesDocumentsAndPrefersPlainSources(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "example.fern")
	document := plain + ".md"
	source := "pub const ANSWER: i32 = 42;"
	if err := os.WriteFile(document, []byte("# Example\n\n```fern\n<<*>>=\n"+source+"\n```\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{plain, document} {
		got, err := ReadFile(path)
		if err != nil || string(got) != source {
			t.Fatalf("ReadFile(%q) = %q, %v", path, got, err)
		}
	}
	if err := os.WriteFile(plain, []byte("plain source"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadFile(document); err != nil || string(got) != "plain source" {
		t.Fatalf("plain precedence: %q, %v", got, err)
	}
	if err := os.Remove(plain); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(document, []byte("```fern\n<<*>>=\n<<missing>>\n```\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(plain); err == nil || !strings.Contains(err.Error(), "example.fern.md") || !strings.Contains(err.Error(), "undefined chunk") {
		t.Fatalf("document diagnostic: %v", err)
	}
}
