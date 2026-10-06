package modload

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

// A plain `.fern` import that resolves to a single-root `.fern.md`
// tangles the document and loads it as a module; the literate
// provenance is reported for diagnostic remapping.
func TestImportSingleRootLiterate(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "greet.fern.md",
		"# Greeter\n\n```fern\n<<*>>=\npub function greeting(): i32 { return 42; }\n```\n")
	main := write(t, dir, "main.fern",
		"import \"./greet\";\nfunction main(): i32 { return greet.greeting(); }\n")

	_, srcs, lit, err := LoadWithLiterate(main, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	greetKey := filepath.Join(dir, "greet.fern")
	if _, ok := srcs[greetKey]; !ok {
		t.Fatalf("greet.fern not in srcs: %v", keys(srcs))
	}
	lm, ok := lit[greetKey]
	if !ok {
		t.Fatalf("greet.fern not recorded as literate: %v", lit)
	}
	if !strings.HasSuffix(lm.DocPath, "greet.fern.md") {
		t.Errorf("DocPath = %q, want …/greet.fern.md", lm.DocPath)
	}
	if len(lm.LineMap) == 0 {
		t.Error("expected a non-empty tangle line map")
	}
}

// Importing a multi-file (`file=`) document is an error — it has no
// single importable module.
func TestImportMultiFileLiterateErrors(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "multi.fern.md",
		"```fern file=a.fern\npub function f(): i32 { return 1; }\n```\n```fern file=b.fern\npub function g(): i32 { return 2; }\n```\n")
	main := write(t, dir, "main.fern",
		"import \"./multi\";\nfunction main(): i32 { return multi.f(); }\n")

	if _, _, _, err := LoadWithLiterate(main, nil); err == nil {
		t.Fatal("expected an error importing a multi-file document")
	} else if !strings.Contains(err.Error(), "multi-file literate") {
		t.Errorf("error = %q, want it to mention multi-file literate", err)
	}
}

// A plain `.fern` wins over a same-named `.fern.md`.
func TestImportPrefersPlainFern(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "dup.fern", "pub function v(): i32 { return 1; }\n")
	write(t, dir, "dup.fern.md", "```fern\n<<*>>=\npub function v(): i32 { return 999; }\n```\n")
	main := write(t, dir, "main.fern",
		"import \"./dup\";\nfunction main(): i32 { return dup.v(); }\n")

	_, _, lit, err := LoadWithLiterate(main, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, ok := lit[filepath.Join(dir, "dup.fern")]; ok {
		t.Error("dup.fern should load from the plain .fern, not the .fern.md")
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
