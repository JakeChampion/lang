package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A defined-but-unreferenced chunk produces a non-fatal stderr warning
// (compile/check still succeeds) — the unused-chunk lint.
func TestLiterateUnusedChunkWarning(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	doc := "```fern\n<<*>>=\nimport \"core/no_prelude\";\nfunction main(): i32 { return 0; }\n```\n" +
		"```fern\n<<orphan>>=\nleftover\n```\n"
	src := filepath.Join(dir, "p.fern.md")
	if err := os.WriteFile(src, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-check", src)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("-check should succeed (warning is non-fatal): %v\nstderr: %s", err, errb.String())
	}
	if !strings.Contains(errb.String(), "chunk <<orphan>> is defined but never used") {
		t.Errorf("expected unused-chunk warning, got stderr:\n%s", errb.String())
	}
}

// A document with every chunk reachable emits no warning.
func TestLiterateNoUnusedWarningWhenClean(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	doc := "```fern\n<<*>>=\nimport \"core/no_prelude\";\n<<body>>\n```\n" +
		"```fern\n<<body>>=\nfunction main(): i32 { return 0; }\n```\n"
	src := filepath.Join(dir, "p.fern.md")
	if err := os.WriteFile(src, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-check", src)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("-check: %v\n%s", err, errb.String())
	}
	if strings.Contains(errb.String(), "never used") {
		t.Errorf("unexpected unused-chunk warning:\n%s", errb.String())
	}
}
