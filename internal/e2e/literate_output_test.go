package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `fern -tangle -o DIR` on a multi-file document writes one file per
// `file=` module under DIR (creating subdirs), and the ejected tree
// compiles + runs on its own.
func TestLiterateTangleToDir(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "app.fern.md")
	if err := os.WriteFile(src, []byte(multiFileDoc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	outDir := filepath.Join(dir, "out")

	cmd := exec.Command(bin, "-tangle", "-o", outDir, src)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("tangle -o failed: %v\nstderr: %s", err, errb.String())
	}
	for _, name := range []string{"main.fern", "mathx.fern"} {
		if _, err := os.Stat(filepath.Join(outDir, name)); err != nil {
			t.Errorf("expected %s on disk: %v", name, err)
		}
	}
	// The ejected modules import each other normally and run to 6² = 36.
	runCmd := exec.Command(bin, "-interp", filepath.Join(outDir, "main.fern"))
	_ = runCmd.Run()
	if code := runCmd.ProcessState.ExitCode(); code != 36 {
		t.Errorf("ejected tree exit = %d, want 36", code)
	}
}

// `fern -tangle -o FILE` on a single-`<<*>>` document writes the tangled
// source to that file (no banners).
func TestLiterateTangleToFile(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	doc := "```fern\n<<*>>=\nimport \"core/no_prelude\";\nfunction main(): i32 { return 7; }\n```\n"
	src := filepath.Join(dir, "one.fern.md")
	if err := os.WriteFile(src, []byte(doc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	outFile := filepath.Join(dir, "one.fern")

	if err := exec.Command(bin, "-tangle", "-o", outFile, src).Run(); err != nil {
		t.Fatalf("tangle -o file: %v", err)
	}
	got, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read tangled file: %v", err)
	}
	if strings.Contains(string(got), "==>") {
		t.Errorf("single-file tangle should have no banner:\n%s", got)
	}
	run := exec.Command(bin, "-interp", outFile)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 7 {
		t.Errorf("tangled file exit = %d, want 7", code)
	}
}

// `fern -weave -html` emits a self-contained HTML page (to stdout, or
// to -o FILE) with clickable chunk cross-references.
func TestLiterateWeaveHTML(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "app.fern.md")
	if err := os.WriteFile(src, []byte(multiFileDoc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	var out bytes.Buffer
	cmd := exec.Command(bin, "-weave", "-html", src)
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("weave -html: %v", err)
	}
	got := out.String()
	for _, w := range []string{"<!DOCTYPE html>", "<style>", `class="ref"`, "</html>"} {
		if !strings.Contains(got, w) {
			t.Errorf("weave -html output missing %q", w)
		}
	}

	// -o writes the same HTML to a file.
	outFile := filepath.Join(dir, "app.html")
	if err := exec.Command(bin, "-weave", "-html", "-o", outFile, src).Run(); err != nil {
		t.Fatalf("weave -html -o: %v", err)
	}
	b, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read html file: %v", err)
	}
	if !strings.Contains(string(b), "<!DOCTYPE html>") {
		t.Errorf("html file missing doctype:\n%s", b)
	}
}

// `fern -weave -o FILE` writes the woven Markdown to disk.
func TestLiterateWeaveToFile(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "app.fern.md")
	if err := os.WriteFile(src, []byte(multiFileDoc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	outFile := filepath.Join(dir, "app.md")

	if err := exec.Command(bin, "-weave", "-o", outFile, src).Run(); err != nil {
		t.Fatalf("weave -o: %v", err)
	}
	got, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read woven file: %v", err)
	}
	// The file-root label shows the woven document reached disk.
	if !strings.Contains(string(got), "main.fern") {
		t.Errorf("woven file missing file-root label:\n%s", got)
	}
}
