package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const factLiterate = "# Factorial\n" +
	"\n" +
	"The program is assembled from named chunks:\n" +
	"\n" +
	"```fern\n" +
	"<<*>>=\n" +
	"<<imports>>\n" +
	"<<fact>>\n" +
	"<<main>>\n" +
	"```\n" +
	"\n" +
	"It needs the integer helpers in scope:\n" +
	"\n" +
	"```fern\n" +
	"<<imports>>=\n" +
	"import \"std/i32\";\n" +
	"```\n" +
	"\n" +
	"`main` computes 5! and reports it:\n" +
	"\n" +
	"```fern\n" +
	"<<main>>=\n" +
	"function main(): i32 {\n" +
	"    let f: i32 = fact(5);\n" +
	"    print(f.to_string());\n" +
	"    return f;\n" +
	"}\n" +
	"```\n" +
	"\n" +
	"The recursion lives in its own chunk, defined last:\n" +
	"\n" +
	"```fern\n" +
	"<<fact>>=\n" +
	"function fact(n: i32): i32 {\n" +
	"    if (n == 0) { return 1; }\n" +
	"    return n * fact(n - 1);\n" +
	"}\n" +
	"```\n"

func TestLiterateInterpEndToEnd(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "fact.fern.md")
	if err := os.WriteFile(src, []byte(factLiterate), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	code := cmd.ProcessState.ExitCode()
	if code != 120 {
		t.Errorf("exit = %d, want 120 (5!)\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "120") {
		t.Errorf("stdout missing `120`: %q", out.String())
	}
}

func TestLiterateTangleStdout(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "fact.fern.md")
	if err := os.WriteFile(src, []byte(factLiterate), 0o644); err != nil {
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
	if strings.Contains(got, "<<") || strings.Contains(got, "# Factorial") {
		t.Errorf("tangled output still contains literate markup:\n%s", got)
	}
	if i, j := strings.Index(got, "function fact"), strings.Index(got, "function main"); i < 0 || j < 0 || i > j {
		t.Errorf("expected fact defined before main in tangled output:\n%s", got)
	}
}

func TestLiterateWeaveStdout(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "fact.fern.md")
	if err := os.WriteFile(src, []byte(factLiterate), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-weave", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("weave failed: %v\nstderr: %s", err, errb.String())
	}
	got := out.String()
	for _, want := range []string{"# Factorial", "⟨*⟩≡", "⟨fact⟩≡", "⟨fact⟩ is used in ⟨*⟩"} {
		if !strings.Contains(got, want) {
			t.Errorf("woven output missing %q:\n%s", want, got)
		}
	}
}

func TestLiterateDiagnosticPointsAtDocumentLine(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	doc := "# Buggy\n" +
		"\n" +
		"```fern\n" +
		"<<*>>=\n" +
		"<<main>>\n" +
		"```\n" +
		"\n" +
		"```fern\n" +
		"<<main>>=\n" +
		"function main(): i32 {\n" +
		"    let f: i32 = \"oops\";\n" +
		"    return f;\n" +
		"}\n" +
		"```\n"
	src := filepath.Join(dir, "buggy.fern.md")
	if err := os.WriteFile(src, []byte(doc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-check", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	if err == nil {
		t.Fatalf("expected -check to fail on a type error\nstdout: %s", out.String())
	}
	msg := errb.String()
	if !strings.Contains(msg, "buggy.fern.md:11:") {
		t.Errorf("diagnostic should point at buggy.fern.md line 11, got:\n%s", msg)
	}
	if !strings.Contains(msg, `let f: i32 = "oops";`) {
		t.Errorf("diagnostic should render the document source line, got:\n%s", msg)
	}
}
