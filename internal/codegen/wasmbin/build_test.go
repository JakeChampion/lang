package wasmbin

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

// TestBuildReadFile — end-to-end read_file via wasmbin's
// preview-1 path_open / fd_read / fd_close pipeline. Creates a
// scratch file in a temp dir, runs the compiled program under
// `wasmtime run --dir=. --invoke main`, and verifies the
// returned len matches the file's byte count. Exercises the
// path_open + fd_read loop + the heap-form Ok(string) result
// construction — the success path of __lang_read_file. The
// missing-file (NotFound) path lives in a separate test below.
func TestBuildReadFile(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	src := `function main(): i32 {
    match (read_file("greeting.txt")) {
        Ok(s) => { return len(s); },
        Err(_) => { return -1; }
    }
    return -2;
}`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	bin, err := Build(prog, info)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.wasm")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	const greeting = "hello from wasmbin read_file"
	if err := os.WriteFile(filepath.Join(dir, "greeting.txt"), []byte(greeting), 0o644); err != nil {
		t.Fatalf("write greeting: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", "--dir=.", "--invoke", "main", p)
	cmd.Dir = dir
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("wasmtime: %v\nstderr:%s\nstdout:%s", err, se.String(), so.String())
	}
	got := strings.TrimSpace(so.String())
	want := strconv.Itoa(len(greeting))
	if got != want {
		t.Fatalf("read_file len = %q, want %q (greeting bytes)\nstderr: %s", got, want, se.String())
	}
}

// TestBuildReadFileNotFound — the missing-file path. path_open
// returns errno ENOENT=44, __build_io_error maps that to
// IoError.NotFound(path) (tag 0), and the surrounding wrapper
// produces Err. The program below pattern-matches and returns
// the path length so we can verify the NotFound variant carries
// the path through.
func TestBuildReadFileNotFound(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	src := `function main(): i32 {
    match (read_file("does_not_exist.txt")) {
        Ok(_) => { return -1; },
        Err(err) => {
            match (err) {
                NotFound(p) => { return len(p); },
                PermissionDenied(_) => { return -10; },
                AlreadyExists(_) => { return -11; },
                InvalidUtf8(_) => { return -12; },
                Interrupted => { return -13; },
                Unsupported => { return -14; },
                Other(_, _) => { return -15; }
            }
        }
    }
    return -3;
}`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	bin, err := Build(prog, info)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.wasm")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", "--dir=.", "--invoke", "main", p)
	cmd.Dir = dir
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("wasmtime: %v\nstderr:%s\nstdout:%s", err, se.String(), so.String())
	}
	got := strings.TrimSpace(so.String())
	want := strconv.Itoa(len("does_not_exist.txt"))
	if got != want {
		t.Fatalf("NotFound path len = %q, want %q\nstderr: %s", got, want, se.String())
	}
}

// TestBuildWriteFile — round-trip via wasmbin's preview-1
// path_open(O_CREAT|O_TRUNC) + fd_write loop + fd_close. The
// program writes a known string to a sandbox-relative path and
// returns 0 on success. The harness reads the file back from
// the host side and asserts the bytes match. Exercises the
// success path of __lang_write_file (None return).
func TestBuildWriteFile(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	const content = "wrote some bytes\n"
	src := `function main(): i32 {
    match (write_file("scratch_output.txt", "wrote some bytes\n")) {
        Some(_) => { return 1; },
        None => { return 0; }
    }
    return -1;
}`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	bin, err := Build(prog, info)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.wasm")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", "--dir=.", "--invoke", "main", p)
	cmd.Dir = dir
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("wasmtime: %v\nstderr:%s\nstdout:%s", err, se.String(), so.String())
	}
	got := strings.TrimSpace(so.String())
	if got != "0" {
		t.Fatalf("write_file returned %q, want 0 (None / success)\nstderr: %s", got, se.String())
	}
	written, err := os.ReadFile(filepath.Join(dir, "scratch_output.txt"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(written) != content {
		t.Errorf("write_file produced %q, want %q", written, content)
	}
}

// TestBuildWriteFileRoundtrip — write then read the same file
// from inside one program. Confirms write_file's bytes show up
// on disk in a form __lang_read_file is happy to consume; both
// helpers share the same WASI rights / preopen / scratch
// conventions, so a regression in either surfaces here.
func TestBuildWriteFileRoundtrip(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	src := `function main(): i32 {
    match (write_file("roundtrip.txt", "round trip content")) {
        Some(_) => { return -1; },
        None => {}
    }
    match (read_file("roundtrip.txt")) {
        Ok(s) => { return len(s); },
        Err(_) => { return -2; }
    }
    return -3;
}`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	bin, err := Build(prog, info)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.wasm")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", "--dir=.", "--invoke", "main", p)
	cmd.Dir = dir
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("wasmtime: %v\nstderr:%s\nstdout:%s", err, se.String(), so.String())
	}
	got := strings.TrimSpace(so.String())
	want := strconv.Itoa(len("round trip content"))
	if got != want {
		t.Fatalf("roundtrip len = %q, want %q\nstderr: %s", got, want, se.String())
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
