package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runDoctest(t *testing.T, doc string) (string, int) {
	t.Helper()
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "p.fern.md")
	if err := os.WriteFile(src, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-doctest", src)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	_ = cmd.Run()
	return out.String(), cmd.ProcessState.ExitCode()
}

func TestLiterateDoctestPassAndFail(t *testing.T) {
	doc := "# d\n" +
		"```fern\n<<greet>>=\npub function greet(): string { return \"hi\"; }\n```\n" +
		"```fern test name=ok-case\n<<greet>>\nfunction main(): i32 { if (greet() != \"hi\") { return 1; } return 0; }\n```\n" +
		"```fern test name=fail-case\n<<greet>>\nfunction main(): i32 { return 1; }\n```\n"
	out, code := runDoctest(t, doc)
	if !strings.Contains(out, "1..2") {
		t.Errorf("missing TAP plan:\n%s", out)
	}
	if !strings.Contains(out, "ok 1 - ok-case") {
		t.Errorf("expected ok 1:\n%s", out)
	}
	if !strings.Contains(out, "not ok 2 - fail-case") {
		t.Errorf("expected not ok 2:\n%s", out)
	}
	if code == 0 {
		t.Errorf("expected non-zero exit when an example fails, got 0\n%s", out)
	}
}

func TestLiterateDoctestAllPass(t *testing.T) {
	doc := "```fern test\nfunction main(): i32 { return 0; }\n```\n"
	out, code := runDoctest(t, doc)
	if code != 0 {
		t.Errorf("expected exit 0, got %d\n%s", code, out)
	}
	if !strings.Contains(out, "ok 1") {
		t.Errorf("expected ok 1:\n%s", out)
	}
}

func TestLiterateDoctestDiagnosticRemap(t *testing.T) {
	doc := "# t\n" +
		"```fern test\n" +
		"function main(): i32 { return \"nope\"; }\n" +
		"```\n"
	out, code := runDoctest(t, doc)
	if code == 0 {
		t.Fatalf("expected failure:\n%s", out)
	}
	if !strings.Contains(out, "p.fern.md:3:") {
		t.Errorf("compile error should remap to p.fern.md line 3:\n%s", out)
	}
}

func TestLiterateDoctestExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cmd := exec.Command(bin, "-doctest", "../../../examples/literate/doctest_demo.fern.md")
	out, _ := cmd.CombinedOutput()
	if cmd.ProcessState.ExitCode() != 0 {
		t.Errorf("example doctests should all pass:\n%s", out)
	}
}
