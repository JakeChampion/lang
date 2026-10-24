package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLiterateImportedLibraryRuns(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "greet.fern.md"),
		[]byte("# Greeter\n\n```fern\n<<*>>=\npub function greeting(): i32 { return 42; }\n```\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(main,
		[]byte("import \"./greet\";\nfunction main(): i32 { return greet.greeting(); }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-interp", main)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf("exit = %d, want 42", code)
	}
}

func TestLiterateImportedLibraryDiagnosticRemap(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	doc := "# Greeter\n" +
		"\n" +
		"```fern\n" +
		"<<*>>=\n" +
		"pub function greeting(): i32 {\n" +
		"    return \"oops\";\n" +
		"}\n" +
		"```\n"
	if err := os.WriteFile(filepath.Join(dir, "greet.fern.md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(main,
		[]byte("import \"./greet\";\nfunction main(): i32 { return greet.greeting(); }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-check", main)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err == nil {
		t.Fatal("expected -check to fail on the imported library's type error")
	}
	msg := errb.String()
	if !strings.Contains(msg, "greet.fern.md:6:") {
		t.Errorf("diagnostic should point at greet.fern.md line 6, got:\n%s", msg)
	}
	if !strings.Contains(msg, `return "oops"`) {
		t.Errorf("diagnostic should render the document source line, got:\n%s", msg)
	}
}
