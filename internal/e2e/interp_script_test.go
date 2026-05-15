package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildLangBinForInterp compiles cmd/lang to a temp binary the
// `-interp` script-mode tests below invoke. Each test gets its
// own t.TempDir() but the build is cheap enough (Go's incremental
// build cache makes the second+ invocation a few-ms hit).
func buildLangBinForInterp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "lang")
	build := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/lang")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build lang: %v\n%s", err, out)
	}
	return bin
}

// `lang -interp FILE.lang` — script-mode end-to-end. The
// interpreter runs the lang program through the AST evaluator
// (no codegen, no link, no temp binary) and main()'s return
// value becomes the process exit code, clamped to 0..255.
// Mirrors `python script.py` semantics.
func TestInterpScriptFile(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.lang")
	if err := os.WriteFile(src, []byte(`function fact(n: i32): i32 {
    if (n == 0) { return 1; }
    return n * fact(n - 1);
}
function main(): i32 {
    var f: i32 = fact(5);
    print(f.to_string());
    return f;
}
`), 0o644); err != nil {
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

// `lang -interp -` — pipe a program through stdin. Skips
// modload entirely (no imports available in the stdin form);
// parses the buffer as a single file.
func TestInterpScriptStdin(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cmd := exec.Command(bin, "-interp", "-")
	cmd.Stdin = strings.NewReader(`function main(): i32 {
    print("from stdin");
    return 42;
}`)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	code := cmd.ProcessState.ExitCode()
	if code != 42 {
		t.Errorf("exit = %d, want 42\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "from stdin") {
		t.Errorf("stdout missing payload: %q", out.String())
	}
}

// Union types + match through the script-mode interp. The union
// desugar lives in the checker (PRs #390 / #392), so the AST
// the interpreter sees is the synthesised enum form — no
// special-case path needed in the interpreter for unions.
func TestInterpScriptUnions(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cmd := exec.Command(bin, "-interp", "-")
	cmd.Stdin = strings.NewReader(`struct Add { l: i32, r: i32 }
struct Lit { v: i32 }
type Expr = Add | Lit;

function eval(e: Expr): i32 {
    match (e) {
        Add(a) => { return a.l + a.r; },
        Lit(l) => { return l.v; },
    }
}

function main(): i32 {
    var e: Expr = Add { l: 10, r: 32 };
    return eval(e);
}`)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf("exit = %d, want 42 (union via interp)", code)
	}
}

// A program without `main` exits non-zero with a clear error.
// Catches the case where someone pipes a snippet (function
// helpers only) and expects the interpreter to find an entry
// point.
func TestInterpScriptMissingMain(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cmd := exec.Command(bin, "-interp", "-")
	cmd.Stdin = strings.NewReader(`function helper(): i32 { return 1; }`)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code == 0 {
		t.Errorf("exit = 0, expected non-zero on missing main")
	}
	if !strings.Contains(errb.String(), "no `main`") && !strings.Contains(errb.String(), "no main") {
		t.Errorf("stderr did not mention missing main: %q", errb.String())
	}
}
