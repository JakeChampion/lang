package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLiterateUnusedChunkWarning(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	doc := "```fern\n<<*>>=\nfunction main(): i32 { return 0; }\n```\n" +
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
	if !strings.Contains(errb.String(), src+":6:1: warning[W003]: chunk <<orphan>> is defined but never used\n") {
		t.Errorf("expected unused-chunk warning, got stderr:\n%s", errb.String())
	}
}

func TestLiterateNoUnusedWarningWhenClean(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	doc := "```fern\n<<*>>=\n<<body>>\n```\n" +
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
