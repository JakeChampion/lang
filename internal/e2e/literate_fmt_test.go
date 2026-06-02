package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `fern -fmt` on a .fern.md reformats fern chunk bodies (whole-program
// and statement-fragment chunks), leaves prose / ref-bearing chunks
// untouched, and is idempotent.
func TestLiterateFmt(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := strings.Join([]string{
		"# Doc",
		"",
		"```fern",
		"<<greet>>=",
		"function   greet( ):i32{return 7;}",
		"```",
		"",
		"```fern",
		"<<setup>>=",
		"var    x=1;",
		"```",
		"",
		"```fern",
		"<<*>>=",
		"<<greet>>",
		"function main(): i32 { return 0; }",
		"```",
		"",
	}, "\n")
	path := filepath.Join(dir, "p.fern.md")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) (string, int) {
		var out bytes.Buffer
		cmd := exec.Command(bin, args...)
		cmd.Stdout = &out
		cmd.Stderr = &out
		_ = cmd.Run()
		return out.String(), cmd.ProcessState.ExitCode()
	}

	got, code := run("-fmt", path)
	if code != 0 {
		t.Fatalf("fmt exit %d:\n%s", code, got)
	}
	// Whole-function chunk reformatted to multi-line, spaces normalized.
	if !strings.Contains(got, "function greet(): i32 {\n  return 7;\n}") {
		t.Errorf("greet chunk not reformatted:\n%s", got)
	}
	// Statement-fragment chunk reformatted via wrapping.
	if !strings.Contains(got, "<<setup>>=\nvar x = 1;") {
		t.Errorf("setup fragment not reformatted:\n%s", got)
	}
	// Prose and the ref-bearing root are preserved.
	if !strings.Contains(got, "# Doc") {
		t.Errorf("prose lost:\n%s", got)
	}
	if !strings.Contains(got, "<<*>>=\n<<greet>>\nfunction main(): i32 { return 0; }") {
		t.Errorf("ref-bearing root should be verbatim:\n%s", got)
	}

	// Idempotent: writing back then re-formatting is a no-op.
	if _, code := run("-fmt", "-w", path); code != 0 {
		t.Fatalf("fmt -w exit %d", code)
	}
	if _, code := run("-fmt", "-d", path); code != 0 {
		t.Errorf("formatted doc should be stable under -fmt -d (exit %d)", code)
	}
}
