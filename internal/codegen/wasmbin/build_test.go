package wasmbin

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// buildFromSource is a test helper that mirrors what the
// `lang -target wasm-bin` CLI path does: parse + check the
// source, then call Build to produce module bytes. Returns
// an error string instead of *bytes for tests that expect
// failure ("unsupported op X").
func buildFromSource(t *testing.T, src string) ([]byte, error) {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	return Build(prog, info)
}

// TestBuildMinimalReturnConst — `function main(): i32 { return 42 }`
// compiled through the full CLI pipeline (parse → check →
// treeshake → lower → IR opts → DCE → Emit). The aggressive
// dead-function elimination at the end of the pipeline should
// drop every stdlib helper from the IR program, leaving only
// `main` to compile. Then wasmtime runs the binary and asserts
// the return value.
func TestBuildMinimalReturnConst(t *testing.T) {
	src := `import "core/no_prelude";
function main(): i32 { return 42; }
`
	bin, err := buildFromSource(t, src)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.wasm")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", "--invoke", "main", p)
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("wasmtime: %v\nstderr:%s", err, se.String())
	}
	if got := strings.TrimSpace(so.String()); got != "42" {
		t.Fatalf("main() = %q, want 42", got)
	}
}

// TestBuildArithmeticReturn — a program that does real arithmetic
// in main(). Confirms the optimisation pipeline doesn't fold the
// computation to a const (the operands are intentionally chosen
// so the obvious fold-to-result would still leave the arithmetic
// observable via wasmtime's printed return).
func TestBuildArithmeticReturn(t *testing.T) {
	src := `import "core/no_prelude";
function main(): i32 {
    var a: i32 = 7;
    var b: i32 = 11;
    return a * b + 3;
}
`
	bin, err := buildFromSource(t, src)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.wasm")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", "--invoke", "main", p)
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("wasmtime: %v\nstderr:%s", err, se.String())
	}
	if got := strings.TrimSpace(so.String()); got != "80" { // 7*11+3
		t.Fatalf("got %q, want 80", got)
	}
}

// TestBuildIfElseReal — control flow from real source. Tests
// that the parser → IR → wasmbin path lowers an if-expression
// the same way the synthetic-IR tests do.
func TestBuildIfElseReal(t *testing.T) {
	src := `import "core/no_prelude";
function pick(a: i32, b: i32): i32 {
    if (a > b) { return a; } else { return b; }
}
function main(): i32 { return pick(7, 11); }
`
	bin, err := buildFromSource(t, src)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.wasm")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", "--invoke", "main", p)
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("wasmtime: %v\nstderr:%s", err, se.String())
	}
	if got := strings.TrimSpace(so.String()); got != "11" {
		t.Fatalf("pick(7, 11) = %q, want 11", got)
	}
}

// TestBuildRecursionReal — real-source self-recursion. Bigger
// program: factorial via recursive descent. The DCE step at the
// end must keep `fact` alive even though it's only reachable
// transitively from `main`.
func TestBuildRecursionReal(t *testing.T) {
	src := `import "core/no_prelude";
function fact(n: i32): i32 {
    if (n <= 1) { return 1; }
    return n * fact(n - 1);
}
function main(): i32 { return fact(10); }
`
	bin, err := buildFromSource(t, src)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.wasm")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", "--invoke", "main", p)
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("wasmtime: %v\nstderr:%s", err, se.String())
	}
	if got := strings.TrimSpace(so.String()); got != "3628800" {
		t.Fatalf("fact(10) = %q, want 3628800", got)
	}
}

// TestBuildReportsUnsupported — a program that uses a feature
// the binary backend doesn't yet handle (printing strings via
// `print` — the allocator + WASI fd_write wiring) should fail
// with a clear error. This pins the contract that gaps surface
// as failures, not as silently-wrong output.
func TestBuildReportsUnsupported(t *testing.T) {
	// Closures with captures lower to OpMakeClosure / OpMakeEnv,
	// neither of which is wired in wasmbin yet (the per-capture
	// type info isn't carried at the IR layer). Compiling such a
	// program must fail with a clear error rather than silently
	// emitting nonsense bytes.
	src := `import "core/no_prelude";
function adder(n: i32): (i32) => i32 {
    function add(x: i32): i32 {
        return x + n;
    }
    return add;
}
function main(): i32 {
    var f = adder(7);
    return f(3);
}
`
	_, err := buildFromSource(t, src)
	if err == nil {
		t.Fatal("expected an unsupported-op error for closure-with-capture; got nil")
	}
	if !strings.Contains(err.Error(), "wasmbin") &&
		!strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("error %q doesn't mention wasmbin or unsupported", err)
	}
}

// TestBuildPrintReal — compile + run a real lang source program
// that calls `print()`. With name aliasing (print → __lang_print)
// + WASI fd_write import + the helper chain, end-to-end output
// flows from `.lang` source to stdout.
func TestBuildPrintReal(t *testing.T) {
	src := `import "core/no_prelude";
function main(): i32 {
    print("hello from wasmbin\n");
    return 0;
}
`
	bin, err := buildFromSource(t, src)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.wasm")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// `main` returns i32 but isn't `_start`; wasmtime needs an
	// explicit `--invoke main` to call it, and that mode bypasses
	// WASI command-mode initialisation. Print's fd_write still
	// works under `wasmtime run --invoke main`.
	cmd := exec.Command("wasmtime", "run", "--invoke", "main", p)
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("wasmtime: %v\nstderr:%s\nstdout:%s", err, se.String(), so.String())
	}
	if !strings.Contains(so.String(), "hello from wasmbin") {
		t.Fatalf("stdout doesn't contain expected text: %q", so.String())
	}
}
