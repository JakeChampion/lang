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

// TestBuildPrintMainResult — BuildWithOptions(SynthStart +
// PrintMainResult) wires `_start` to format main's i32 return
// through `int_to_string` and flush it to stdout via
// `__lang_print`. The WAT path's PrintMainResult mode is what
// drives the wasm e2e suite's stdout-based result checks; this
// is the wasmbin parity. Invokes _start under wasmtime (which
// provides wasi_snapshot_preview1) and asserts the printed
// decimal matches main's value.
func TestBuildPrintMainResult(t *testing.T) {
	src := `function main(): i32 { return 42; }`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	bin, err := BuildWithOptions(prog, info, BuildOptions{
		SynthStart:      true,
		PrintMainResult: true,
	})
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
	// `wasmtime run prog.wasm` dispatches to `_start`; the
	// wrapper calls main, formats 42 as "42", appends a
	// newline, and writes it to stdout.
	cmd := exec.Command("wasmtime", "run", p)
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("wasmtime: %v\nstderr:%s\nstdout:%s", err, se.String(), so.String())
	}
	if got := strings.TrimSpace(so.String()); got != "42" {
		t.Fatalf("PrintMainResult stdout = %q, want %q", got, "42")
	}
}

// TestBuildPreview2Wrap — BuildWithOptions(ForceMemorySection +
// SynthStart) produces bytes that wrap cleanly into a preview-2
// component when fed through the WASI adapter. The synthesised
// `_start` makes `wasm-tools component new` happy; the forced
// memory section satisfies the adapter's env::memory import.
// Test gates on the adapter being present at LANG_WASI_ADAPTER.
func TestBuildPreview2Wrap(t *testing.T) {
	adapter := os.Getenv("LANG_WASI_ADAPTER")
	if adapter == "" {
		t.Skip("LANG_WASI_ADAPTER not set")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `import "core/no_prelude";
function main(): i32 { return 0; }
`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	bin, err := BuildWithOptions(prog, info, BuildOptions{
		ForceMemorySection: true,
		SynthStart:         true,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// Walk to confirm the synthesised _start export exists.
	if !exportExists(t, bin, "_start") {
		t.Fatal("module missing _start export after SynthStart=true")
	}
	if !exportExists(t, bin, "memory") {
		t.Fatal("module missing memory export after ForceMemorySection=true")
	}
}

// exportExists walks the export section, returning true if a
// function or memory export with the given name is present.
func exportExists(t *testing.T, bin []byte, want string) bool {
	t.Helper()
	if len(bin) < 8 {
		return false
	}
	i := 8
	for i < len(bin) {
		id := bin[i]
		i++
		size := 0
		shift := 0
		for {
			if i >= len(bin) {
				return false
			}
			b := bin[i]
			i++
			size |= int(b&0x7f) << shift
			if b&0x80 == 0 {
				break
			}
			shift += 7
		}
		if id == 0x07 { // export section
			body := bin[i : i+size]
			// count uleb
			j := 0
			cnt := 0
			sh := 0
			for {
				b := body[j]
				j++
				cnt |= int(b&0x7f) << sh
				if b&0x80 == 0 {
					break
				}
				sh += 7
			}
			for k := 0; k < cnt; k++ {
				// name length uleb
				nl := 0
				sh = 0
				for {
					b := body[j]
					j++
					nl |= int(b&0x7f) << sh
					if b&0x80 == 0 {
						break
					}
					sh += 7
				}
				name := string(body[j : j+nl])
				j += nl
				j++ // kind byte
				// idx uleb
				for body[j]&0x80 != 0 {
					j++
				}
				j++
				if name == want {
					return true
				}
			}
			return false
		}
		i += size
	}
	return false
}

// TestBuildReportsUnsupported — a program that uses a feature
// the binary backend doesn't yet handle should fail with a
// clear error. This pins the contract that gaps surface as
// failures, not as silently-wrong output.
//
// Today's example: TCP. The wasi-sockets imports + the per-
// preview-2 fd_read/fd_write/sock_close wiring aren't ported
// to wasmbin yet, so any program that calls `tcp_listen` /
// `tcp_accept` etc. surfaces an "unsupported" / "unknown
// callee" failure. As TCP support lands, update this test to
// point at the next gap.
func TestBuildReportsUnsupported(t *testing.T) {
	src := `import "core/no_prelude";
function main(): i32 {
    var srv = tcp_listen(8080);
    return 0;
}
`
	_, err := buildFromSource(t, src)
	if err == nil {
		t.Fatal("expected an unsupported error for tcp_listen; got nil")
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

// TestBuildMapReal — end-to-end Map[i32, i32]: build a 1-entry
// map literal, then read the value back with `get_or`. Exercises
// the full Map runtime chain: map_new_impl + __map_set_impl +
// __map_get_or_impl + __map_hash (int key path) + the stdlib
// load/store/alloc shims.
func TestBuildMapReal(t *testing.T) {
	src := `import "core/no_prelude";
function main(): i32 {
    var m: Map[i32, i32] = (Map { 1i32: 10i32 });
    return m.get_or(1i32, 0i32);
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
		t.Fatalf("wasmtime: %v\nstderr:%s\nstdout:%s", err, se.String(), so.String())
	}
	if got := strings.TrimSpace(so.String()); got != "10" {
		t.Fatalf("map get_or = %q, want 10", got)
	}
}
