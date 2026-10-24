package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const multiFileDoc = "# Two modules\n" +
	"\n" +
	"```fern file=main.fern entry\n" +
	"import \"./mathx\";\n" +
	"function main(): i32 {\n" +
	"    return mathx.square(6);\n" +
	"}\n" +
	"```\n" +
	"\n" +
	"The helper module is assembled from a chunk:\n" +
	"\n" +
	"```fern file=mathx.fern\n" +
	"<<square>>\n" +
	"```\n" +
	"\n" +
	"```fern\n" +
	"<<square>>=\n" +
	"pub function square(n: i32): i32 { return n * n; }\n" +
	"```\n"

func TestLiterateMultiFileInterp(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "app.fern.md")
	if err := os.WriteFile(src, []byte(multiFileDoc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 36 {
		t.Errorf("exit = %d, want 36 (6²)\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

func TestLiterateMultiFileTangle(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "app.fern.md")
	if err := os.WriteFile(src, []byte(multiFileDoc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-tangle", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("tangle failed: %v\nstderr: %s", err, errb.String())
	}
	got := out.String()
	for _, want := range []string{"// ==> main.fern <==", "// ==> mathx.fern <==", "pub function square", "import \"./mathx\";"} {
		if !strings.Contains(got, want) {
			t.Errorf("tangle output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "<<square>>") {
		t.Errorf("chunk reference left untangled:\n%s", got)
	}
}

func TestLiterateMultiFileDiagnosticRemap(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	doc := "# Buggy\n" +
		"\n" +
		"```fern file=main.fern entry\n" +
		"import \"./mathx\";\n" +
		"function main(): i32 { return mathx.square(6); }\n" +
		"```\n" +
		"\n" +
		"```fern file=mathx.fern\n" +
		"pub function square(n: i32): i32 { return \"oops\"; }\n" +
		"```\n"
	src := filepath.Join(dir, "buggy.fern.md")
	if err := os.WriteFile(src, []byte(doc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-check", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err == nil {
		t.Fatalf("expected -check to fail\nstdout: %s", out.String())
	}
	msg := errb.String()
	if !strings.Contains(msg, "buggy.fern.md:9:") {
		t.Errorf("diagnostic should point at buggy.fern.md line 9, got:\n%s", msg)
	}
	if !strings.Contains(msg, `return "oops"`) {
		t.Errorf("diagnostic should render the document source line, got:\n%s", msg)
	}
}

func TestLiterateMultiFileTopLevelErrorRemapsThroughItsModule(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	doc := "# Buggy\n" +
		"\n" +
		"```fern file=main.fern entry\n" +
		"import \"./helper\";\n" +
		"function main(): i32 { return helper.val(); }\n" +
		"```\n" +
		"\n" +
		"```fern file=helper.fern\n" +
		"pub const BAD: i32 = \"oops\";\n" +
		"pub function val(): i32 { return 1; }\n" +
		"```\n"
	src := filepath.Join(dir, "buggy.fern.md")
	if err := os.WriteFile(src, []byte(doc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-check", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err == nil {
		t.Fatalf("expected -check to fail\nstdout: %s", out.String())
	}
	msg := errb.String()
	if !strings.Contains(msg, "does not match") {
		t.Errorf("diagnostic should describe the type mismatch, got:\n%s", msg)
	}
	if !strings.Contains(msg, "buggy.fern.md:9:") {
		t.Errorf("diagnostic should point at buggy.fern.md line 9, got:\n%s", msg)
	}
	if !strings.Contains(msg, `pub const BAD: i32 = "oops";`) {
		t.Errorf("diagnostic should render the document source line, got:\n%s", msg)
	}
}
