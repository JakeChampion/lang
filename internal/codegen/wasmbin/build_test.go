package wasmbin

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
	"github.com/jakechampion/lang/internal/parser"
)

// loadAndCheckModule mirrors the modload-backed pipeline the CLI runs:
// it writes src to a temp module, resolves imports through modload
// (so `import "std/…";` / `import "core/…";` actually load), then
// const-folds, type-checks, and monomorphises. Use it for programs
// that reference stdlib functions now that the auto-prelude is gone
// (parser.Parse alone leaves stdlib imports unresolved).
func loadAndCheckModule(t *testing.T, src string) (*ast.Program, *checker.Info) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	prog, _, err := modload.Load(p)
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	return prog, info
}

// buildFromSource is a test helper that mirrors what the
// `fern -target wasm-bin` CLI path does: parse + check the
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
	src := `
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
	src := `
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
	src := `
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
	src := `
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
// construction — the success path of __fern_read_file. The
// missing-file (NotFound) path lives in a separate test below.
func TestBuildReadFile(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	src := `function main(): i32 {
    match (read_file("greeting.txt")) {
        Ok(s) => { return s.len(); },
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
                NotFound(p) => { return p.len(); },
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
// success path of __fern_write_file (None return).
func TestBuildWriteFile(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	const content = "wrote some bytes\n"
	src := `function main(): i32 {
    match (write_file("scratch_output.txt", "wrote some bytes\n")) {
        Err(_) => { return 1; },
        Ok(_) => { return 0; }
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
// on disk in a form __fern_read_file is happy to consume; both
// helpers share the same WASI rights / preopen / scratch
// conventions, so a regression in either surfaces here.
func TestBuildWriteFileRoundtrip(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	src := `function main(): i32 {
    match (write_file("roundtrip.txt", "round trip content")) {
        Err(_) => { return -1; },
        Ok(_) => {}
    }
    match (read_file("roundtrip.txt")) {
        Ok(s) => { return s.len(); },
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
// `__fern_print`. The WAT path's PrintMainResult mode is what
// drives the wasm e2e suite's stdout-based result checks; this
// is the wasmbin parity. Invokes _start under wasmtime (which
// provides wasi_snapshot_preview1) and asserts the printed
// decimal matches main's value.
func TestBuildPrintMainResult(t *testing.T) {
	// PrintMainResult's _start calls int_to_string (core/int); with
	// the auto-prelude gone the program must import it explicitly.
	src := `
import "core/int";
function main(): i32 { return 42; }`
	prog, info := loadAndCheckModule(t, src)
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
// Test gates on the adapter being present at FERN_WASI_ADAPTER.
func TestBuildPreview2Wrap(t *testing.T) {
	adapter := os.Getenv("FERN_WASI_ADAPTER")
	if adapter == "" {
		t.Skip("FERN_WASI_ADAPTER not set")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
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
// Previously this used `tcp_listen`; TCP has since landed in
// wasmbin (see wasi_tcp.go). The next remaining gap is
// `subprocess` — wasi:cli/exec-process isn't wired into the
// runtime helpers yet, so any program that spawns a child
// surfaces an "unknown callee" / "unsupported" failure. As
// each gap closes, update this test to point at the next.
func TestBuildReportsUnsupported(t *testing.T) {
	src := `
function main(): i32 {
    var r = subprocess("/bin/echo", [], "");
    return r.exit_code;
}
`
	_, err := buildFromSource(t, src)
	if err == nil {
		t.Fatal("expected an unsupported error for subprocess; got nil")
	}
	if !strings.Contains(err.Error(), "wasmbin") &&
		!strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("error %q doesn't mention wasmbin or unsupported", err)
	}
}

// TestBuildTcpHelpersCompile — pin the compile-time wiring of
// the TCP helpers. A program touching every one of tcp_listen /
// tcp_accept / tcp_recv / tcp_send / tcp_close must reach the
// end of Build without surfacing "unsupported op" / "unknown
// callee" — i.e. the call-direct alias map, the runtime-helper
// specs (wasi_tcp.go), and the wasi:sockets + wasi:io import
// specs (wasi.go) are all consistently registered. Pure
// compile check; runtime exercise lives in the e2e suite under
// `wasmtime serve`.
func TestBuildTcpHelpersCompile(t *testing.T) {
	src := `
function main(): i32 {
    var srv: i32 = tcp_listen(8080);
    if (srv < 0) { return -1; }
    var conn: i32 = tcp_accept(srv);
    if (conn < 0) {
        tcp_close(srv);
        return -2;
    }
    var data: string = tcp_recv(conn, 4096i32);
    var sent: i32 = tcp_send(conn, data);
    tcp_close(conn);
    tcp_close(srv);
    return sent;
}
`
	bin, err := buildFromSource(t, src)
	if err != nil {
		t.Fatalf("Build (tcp helpers): %v", err)
	}
	if len(bin) == 0 {
		t.Fatal("Build returned empty module bytes")
	}
	// Wasm magic + version. The full structural validation comes
	// from wasm-tools / wasmtime; checking the prefix here pins
	// that Build at least produced a parseable header.
	if len(bin) < 8 ||
		bin[0] != 0x00 || bin[1] != 0x61 || bin[2] != 0x73 || bin[3] != 0x6d {
		t.Fatalf("output doesn't start with the wasm magic; got % x", bin[:8])
	}
}

// TestBuildHttpHandlerCompiles — pin the compile-time wiring of
// the wasi:http/incoming-handler wrapper. A program that defines
// `function handle(req: HttpRequest, plat: Platform):
// HttpResponse` must reach the end of Build with HttpHandler=true
// without surfacing "unsupported op" / "unknown callee" — i.e.
// the wasi:http import specs (wasi.go), the __http_entry helper
// spec (runtime.go + wasi_http.go), the live-extras pinning of
// `handle` + `__method_HeaderMap_append` (build.go), and the
// export rewrite to `wasi:http/incoming-handler@0.2.0#handle`
// (wasmbin.go) are all consistently registered. Pure compile
// check; runtime exercise under `wasmtime serve` lives in the
// e2e suite (TestWasmPreview2HttpHandler).
func TestBuildHttpHandlerCompiles(t *testing.T) {
	src := `
import "std/http";
import "std/tcp";
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    if (req.path == "/hello") {
        return http.http_response_ok("world");
    }
    return http.http_response_text(404, "not found");
}
`
	prog, info := loadAndCheckModule(t, src)
	bin, err := BuildWithOptions(prog, info, BuildOptions{
		ForceMemorySection: true,
		HttpHandler:        true,
	})
	if err != nil {
		t.Fatalf("Build (HttpHandler): %v", err)
	}
	if len(bin) == 0 {
		t.Fatal("Build returned empty module bytes")
	}
	// Wasm magic.
	if len(bin) < 8 ||
		bin[0] != 0x00 || bin[1] != 0x61 || bin[2] != 0x73 || bin[3] != 0x6d {
		t.Fatalf("output doesn't start with the wasm magic; got % x", bin[:8])
	}
	// The canonical component-model export name must be present so
	// `wasm-tools component new` recognises the handler.
	if !exportExists(t, bin, "wasi:http/incoming-handler@0.2.0#handle") {
		t.Fatal("module missing wasi:http/incoming-handler@0.2.0#handle export")
	}
	if !exportExists(t, bin, "cabi_realloc") {
		t.Fatal("module missing cabi_realloc export")
	}
	if !exportExists(t, bin, "memory") {
		t.Fatal("module missing memory export (needed for the adapter env::memory import)")
	}
}

// TestBuildPrintReal — compile + run a real lang source program
// that calls `print()`. With name aliasing (print → __fern_print)
// + WASI fd_write import + the helper chain, end-to-end output
// flows from `.fern` source to stdout.
func TestBuildPrintReal(t *testing.T) {
	src := `
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
	src := `
import "core/map";
function main(): i32 {
    var m: Map[i32, i32] = (Map { 1i32: 10i32 });
    return m.get_or(1i32, 0i32);
}
`
	prog, info := loadAndCheckModule(t, src)
	bin, err := Build(prog, info)
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

// importExists walks the import section (id 0x02) and returns true
// iff an import with the given (module, name) pair is present.
func importExists(t *testing.T, bin []byte, wantModule, wantName string) bool {
	t.Helper()
	if len(bin) < 8 {
		return false
	}
	readULEB := func(buf []byte, i *int) int {
		n := 0
		sh := 0
		for {
			b := buf[*i]
			*i++
			n |= int(b&0x7f) << sh
			if b&0x80 == 0 {
				return n
			}
			sh += 7
		}
	}
	i := 8
	for i < len(bin) {
		id := bin[i]
		i++
		size := readULEB(bin, &i)
		if id != 0x02 {
			i += size
			continue
		}
		body := bin[i : i+size]
		j := 0
		cnt := readULEB(body, &j)
		for k := 0; k < cnt; k++ {
			ml := readULEB(body, &j)
			mod := string(body[j : j+ml])
			j += ml
			nl := readULEB(body, &j)
			name := string(body[j : j+nl])
			j += nl
			kind := body[j]
			j++
			switch kind {
			case 0x00:
				readULEB(body, &j)
			case 0x01:
				j++
				flags := body[j]
				j++
				readULEB(body, &j)
				if flags&0x01 != 0 {
					readULEB(body, &j)
				}
			case 0x02:
				flags := body[j]
				j++
				readULEB(body, &j)
				if flags&0x01 != 0 {
					readULEB(body, &j)
				}
			case 0x03:
				j++
				j++
			}
			if mod == wantModule && name == wantName {
				return true
			}
		}
		return false
	}
	return false
}

// TestBuildPreview2WASIRenamesProcExit — with Preview2WASI=true,
// a Lang program that calls exit() emits its termination import
// as `wasi:cli/exit@0.2.0::exit` (preview-2) instead of
// `wasi_snapshot_preview1::proc_exit`. Core-wasm signature is
// unchanged ((i32) -> ()), so __fern_exit's call site needs no
// adjustment. Foundation for wiring the wrap.go preview-2
// pipeline into the default driver path.
func TestBuildPreview2WASIRenamesProcExit(t *testing.T) {
	src := `
function main(): i32 {
    exit(0);
    return 0;
}
`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	bin, err := BuildWithOptions(prog, info, BuildOptions{Preview2WASI: true})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if importExists(t, bin, "wasi_snapshot_preview1", "proc_exit") {
		t.Errorf("module still has preview-1 proc_exit import under Preview2WASI=true")
	}
	if !importExists(t, bin, "wasi:cli/exit@0.2.0", "exit") {
		t.Errorf("module missing wasi:cli/exit@0.2.0::exit import under Preview2WASI=true")
	}
}

// TestBuildPreview2ArgsUsesEnvironment — with Preview2WASI=true, a
// program that reads argv via args() imports
// `wasi:cli/environment@0.2.0::get-arguments` instead of the
// preview-1 `args_sizes_get` / `args_get`.
func TestBuildPreview2ArgsUsesEnvironment(t *testing.T) {
	src := `
function main(): i32 {
    return args().len();
}
`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	bin, err := BuildWithOptions(prog, info, BuildOptions{Preview2WASI: true})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if importExists(t, bin, "wasi_snapshot_preview1", "args_get") ||
		importExists(t, bin, "wasi_snapshot_preview1", "args_sizes_get") {
		t.Errorf("module still has preview-1 args imports under Preview2WASI=true")
	}
	if !importExists(t, bin, "wasi:cli/environment@0.2.0", "get-arguments") {
		t.Errorf("module missing wasi:cli/environment::get-arguments import under Preview2WASI=true")
	}
}

// TestBuildPreview2ArgsDefaultUsesPreview1 — the default
// (Preview2WASI=false) path still reads argv via the preview-1
// args_sizes_get / args_get imports.
func TestBuildPreview2ArgsDefaultUsesPreview1(t *testing.T) {
	src := `
function main(): i32 {
    return args().len();
}
`
	bin, err := buildFromSource(t, src)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !importExists(t, bin, "wasi_snapshot_preview1", "args_get") {
		t.Errorf("default build missing preview-1 args_get import")
	}
	if importExists(t, bin, "wasi:cli/environment@0.2.0", "get-arguments") {
		t.Errorf("default build has preview-2 get-arguments import without opt-in")
	}
}

// TestBuildPreview2EnvUsesEnvironment — with Preview2WASI=true, a
// program that reads an env var via env() imports
// `wasi:cli/environment@0.2.0::get-environment` instead of the
// preview-1 `environ_sizes_get` / `environ_get`.
func TestBuildPreview2EnvUsesEnvironment(t *testing.T) {
	src := `
function main(): i32 {
    match (env("PATH")) {
        Some(v) => { return 0; },
        None => { return 1; }
    }
    return 1;
}
`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	bin, err := BuildWithOptions(prog, info, BuildOptions{Preview2WASI: true})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if importExists(t, bin, "wasi_snapshot_preview1", "environ_get") ||
		importExists(t, bin, "wasi_snapshot_preview1", "environ_sizes_get") {
		t.Errorf("module still has preview-1 environ imports under Preview2WASI=true")
	}
	if !importExists(t, bin, "wasi:cli/environment@0.2.0", "get-environment") {
		t.Errorf("module missing wasi:cli/environment::get-environment import under Preview2WASI=true")
	}
}

// TestBuildPreview2EnvDefaultUsesPreview1 — the default
// (Preview2WASI=false) path still reads env vars via the preview-1
// environ imports.
func TestBuildPreview2EnvDefaultUsesPreview1(t *testing.T) {
	src := `
function main(): i32 {
    match (env("PATH")) {
        Some(v) => { return 0; },
        None => { return 1; }
    }
    return 1;
}
`
	bin, err := buildFromSource(t, src)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !importExists(t, bin, "wasi_snapshot_preview1", "environ_get") {
		t.Errorf("default build missing preview-1 environ_get import")
	}
	if importExists(t, bin, "wasi:cli/environment@0.2.0", "get-environment") {
		t.Errorf("default build has preview-2 get-environment import without opt-in")
	}
}

// TestBuildPreview2WASIDefaultLeavesProcExit — the default
// (Preview2WASI=false) path still emits the preview-1
// proc_exit import. Pins the opt-in shape of the migration.
func TestBuildPreview2WASIDefaultLeavesProcExit(t *testing.T) {
	src := `
function main(): i32 {
    exit(0);
    return 0;
}
`
	bin, err := buildFromSource(t, src)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !importExists(t, bin, "wasi_snapshot_preview1", "proc_exit") {
		t.Errorf("default build missing preview-1 proc_exit import")
	}
	if importExists(t, bin, "wasi:cli/exit@0.2.0", "exit") {
		t.Errorf("default build has preview-2 exit import without opt-in")
	}
}

// TestBuildExportSurfacesCoreExport — P6: an `@export("iface","name")` function
// surfaces a core export `iface#name` (the WIT-id alias the world-driven
// composer lifts as the named world export), in addition to the plain-name
// export. docs/WIT-BRING-YOUR-OWN.md.
func TestBuildExportSurfacesCoreExport(t *testing.T) {
	src := `
@export("local:test/math@0.1.0", "add")
function add(a: i32, b: i32): i32 { return a + b; }
function main(): i32 { return add(2, 3); }
`
	prog, info := loadAndCheckModule(t, src)
	bin, err := BuildWithOptions(prog, info, BuildOptions{
		ForceMemorySection: true,
		Preview2WASI:       true,
		SynthCliRun:        true,
	})
	if err != nil {
		t.Fatalf("Build (@export): %v", err)
	}
	if !exportExists(t, bin, "local:test/math@0.1.0#add") {
		t.Fatal("module missing the @export WIT-id core export local:test/math@0.1.0#add")
	}
	// The plain-name export is still present (defined functions export by name).
	if !exportExists(t, bin, "add") {
		t.Fatal("module missing the plain-name export add")
	}
}
