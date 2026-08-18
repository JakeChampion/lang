package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildLangBinForCheck compiles cmd/fern to a temp binary the
// `-check` tests below invoke. Mirrors buildLangBinForInterp;
// duplicated rather than shared because each top-level test file
// in this package owns its harness, and Go's build cache makes
// the repeated `go build` essentially free.
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

// `fern -check FILE.fern` — happy path. A well-typed program
// produces no output and exits 0. Mirrors `tsc --noEmit` /
// `go vet` semantics: silent success is the success signal.
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

// `fern -check FILE.fern` against a program with a type error.
// Exit code is 1, stderr carries a formatted diagnostic that
// mentions the source file and the offending construct.
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
	if !strings.Contains(msg, "return type mismatch") {
		t.Errorf("stderr did not mention return-type mismatch: %q", msg)
	}
}

// `fern -check` succeeds on a library file with no `main`.
// `-interp` requires a `main` (there's nothing to run), but
// type-checking should work on library packages — that's the
// whole point of having a check-only mode.
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

// `fern -check -` — read a program from stdin, type-check it.
// Same shape as `fern -interp -`. No import resolution (modload
// reads from disk), but single-file programs check cleanly.
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

// `fern -check -` with a broken program: exit 1 + diagnostic
// labelled `<stdin>` (no filesystem path to point at).
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

// `fern -check ENTRY.fern` follows imports — a type error in a
// transitive dep is surfaced with the dep's own filename, not
// the entry file's. This is the payoff of the check command:
// running it on a project root finds errors everywhere modload
// can reach.
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

// End-to-end for the whole chain the two halves above only cover in pieces:
// `fern -check` on a program whose IMPORT has a syntax error must name that
// import's path and quote that import's line.
//
// The diagnostic used to come out as
//
//	main.fern:2:15: error[P001]: unexpected token ";"
//	function main(): i32 { return util.thing(); }
//	              ^
//
// — the position measured in util.fern, printed over main.fern's path and
// text, with the caret landing on whatever character shared that column. Both
// pieces were in place (modload stamps the module path, the CLI formatter
// routes by it and holds every module's source) and the join between them was
// a type assertion that could never succeed.
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
