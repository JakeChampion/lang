package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func buildLangBinForCheck(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "fern")
	build := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/fern")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build lang: %v\n%s", err, out)
	}
	return bin
}

func TestCheckCleanProgram(t *testing.T) {
	bin := buildLangBinForCheck(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(`function add(a: i32, b: i32): i32 {
    return a + b;
}
function main(): i32 {
    return add(2, 3);
}
`), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-check", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	if out.Len() != 0 {
		t.Errorf("stdout not empty on success: %q", out.String())
	}
	if errb.Len() != 0 {
		t.Errorf("stderr not empty on success: %q", errb.String())
	}
}

func TestCheckTypeError(t *testing.T) {
	bin := buildLangBinForCheck(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "bad.fern")
	if err := os.WriteFile(src, []byte(`function main(): i32 {
    return "not an int";
}
`), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-check", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code == 0 {
		t.Fatalf("exit = 0, want non-zero\nstdout: %s\nstderr: %s", out.String(), errb.String())
	}
	msg := errb.String()
	if !strings.Contains(msg, "bad.fern") {
		t.Errorf("stderr did not mention source file: %q", msg)
	}
	if !strings.Contains(msg, "return value: expected") {
		t.Errorf("stderr did not mention return-type mismatch: %q", msg)
	}
}

func TestCheckLibraryNoMain(t *testing.T) {
	bin := buildLangBinForCheck(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "lib.fern")
	if err := os.WriteFile(src, []byte(`function helper(n: i32): i32 {
    return n * 2;
}
`), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-check", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("exit = %d, want 0 (libraries should check fine)\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

func TestCheckStdin(t *testing.T) {
	bin := buildLangBinForCheck(t)
	cmd := exec.Command(bin, "-check", "-")
	cmd.Stdin = strings.NewReader(`function f(n: i32): i32 { return n + 1; }`)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

func TestCheckStdinTypeError(t *testing.T) {
	bin := buildLangBinForCheck(t)
	cmd := exec.Command(bin, "-check", "-")
	cmd.Stdin = strings.NewReader(`function f(): i32 { return "x"; }`)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code == 0 {
		t.Fatalf("exit = 0, want non-zero\nstdout: %s\nstderr: %s", out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "<stdin>") {
		t.Errorf("stderr did not label stdin source: %q", errb.String())
	}
}

func TestCheckFollowsImports(t *testing.T) {
	bin := buildLangBinForCheck(t)
	dir := t.TempDir()
	lib := filepath.Join(dir, "lib.fern")
	if err := os.WriteFile(lib, []byte(`pub function broken(): i32 {
    return "still not an int";
}
`), 0o644); err != nil {
		t.Fatalf("write lib: %v", err)
	}
	entry := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(entry, []byte(`import "./lib";
function main(): i32 {
    return lib.broken();
}
`), 0o644); err != nil {
		t.Fatalf("write entry: %v", err)
	}
	cmd := exec.Command(bin, "-check", entry)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code == 0 {
		t.Fatalf("exit = 0, want non-zero (broken import should fail check)\nstdout: %s\nstderr: %s", out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "lib.fern") {
		t.Errorf("stderr did not point at the offending import file: %q", errb.String())
	}
}

func TestCheckAttributesAnImportsSyntaxError(t *testing.T) {
	bin := buildLangBinForCheck(t)
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.fern")
	helper := filepath.Join(dir, "util.fern")
	write := func(path, src string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	write(entry, "import \"./util\";\nfunction main(): i32 { return util.thing(); }\n")
	write(helper, "pub function thing(): i32 {\n    return 1 +;\n}\n")

	cmd := exec.Command(bin, "-check", entry)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code == 0 {
		t.Fatalf("exit = 0, want non-zero for a syntax error in an import")
	}
	got := errb.String()
	if !strings.Contains(got, helper+":2:") {
		t.Errorf("diagnostic does not point at %s:2:\n%s", helper, got)
	}
	if strings.Contains(got, entry+":") {
		t.Errorf("diagnostic blames the entry file, which parses cleanly:\n%s", got)
	}
	if !strings.Contains(got, "return 1 +;") {
		t.Errorf("diagnostic does not quote the offending line from the import:\n%s", got)
	}
}
