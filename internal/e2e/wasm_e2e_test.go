// E2E tests for the WASM backend, executed against a preview-2
// Component Model component under wasmtime. The pipeline is parse
// → check → wasm.EmitWithOptions{PrintMainResult: true} →
// wasm-tools parse + component embed + component new (with the
// wasi-preview1-component-adapter for the legacy entry-point
// trampoline only) → `wasmtime run`. Tests skip when any of
// wasm-tools / wasmtime / `FERN_WASI_ADAPTER` is missing so
// `go test ./...` stays green on machines without the toolchain.
package e2e

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/wasmbin"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/wasm/component"
	conv "github.com/jakechampion/lang/internal/wasm/convert"
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/imports"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/leb128"
	mem "github.com/jakechampion/lang/internal/wasm/memory"
	mod "github.com/jakechampion/lang/internal/wasm/module"
	num "github.com/jakechampion/lang/internal/wasm/numeric"
	"github.com/jakechampion/lang/internal/wasm/sections"
)

// preview2 tool discovery: cached across the whole test run since
// the answers don't change. preview2Setup holds the memoised
// lookup state; callers go through skipIfPreview2Missing.
var (
	preview2Once sync.Once
	preview2Err  error
)

func skipIfPreview2Missing(t *testing.T) {
	t.Helper()
	preview2Once.Do(func() {
		if runtime.GOOS == "windows" {
			preview2Err = errors.New("preview-2 toolchain not exercised on windows")
			return
		}
		// Components are composed natively in-process (component.Compose),
		// so the only external dependency left is wasmtime to run them —
		// no wasm-tools, no preview-1 adapter.
		if _, err := exec.LookPath("wasmtime"); err != nil {
			preview2Err = errors.New("wasmtime not on PATH")
			return
		}
	})
	if preview2Err != nil {
		t.Skipf("preview-2 prerequisites missing: %v", preview2Err)
	}
}

// runOpts bundles the per-call wasmtime knobs. Empty defaults run
// the component with no preopened dirs, no env, and no positional
// args.
type runOpts struct {
	args    []string // positional argv after the component path
	stdin   string
	envs    []string // KEY=VAL strings forwarded as `--env`
	workDir string   // when non-empty, mount as `--dir=<workDir>`
}

// buildComponent runs the in-process parse → check → wasm.Emit
// pipeline for src, then drives wasm-tools to emit a Component
// Model component. PrintMainResult is on so `_start` appends
// main()'s i32 result to stdout via int_to_string + print. Skips
// the test if the preview-2 toolchain is unavailable.
// withResultPrinter guarantees `core/int` is in the import closure so
// the BuildOptions.PrintMainResult wrapper can stringify main()'s i32
// return via int_to_string. The auto-prelude used to supply that name
// to every program; with the prelude gone (docs/PRELUDE-TO-MODULES.md
// phase 5) the test harness — which is what turns main()'s result into
// the stdout line runWasm parses — has to pull it in itself rather than
// make all ~400 wasm programs declare an import they don't otherwise
// use. Imports may appear in any order and modload dedups, so an
// unconditional prepend is safe whether or not the program already
// imports core/int.
func withResultPrinter(src string) string {
	return "import \"core/int\";\n" + src
}

func buildComponent(t *testing.T, src string) string {
	t.Helper()
	skipIfPreview2Missing(t)

	src = withResultPrinter(src)
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	prog, _, err := modload.Load(srcPath)
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
	bin, err := wasmbin.BuildWithOptions(prog, info, wasmbin.BuildOptions{
		ForceMemorySection: true,
		Preview2WASI:       true,
		SynthCliRun:        true,
		PrintMainResult:    true,
	})
	if err != nil {
		t.Fatalf("wasmbin.Build: %v", err)
	}
	return finishComponentFromCoreBytes(t, bin)
}

// buildComponentMulti is buildComponent for a module set loaded
// through modload (the multi-file analogue).
func buildComponentMulti(t *testing.T, entry string, files map[string]string) string {
	t.Helper()
	skipIfPreview2Missing(t)

	dir := t.TempDir()
	for path, contents := range files {
		if path == entry {
			// core/int for the PrintMainResult wrapper's int_to_string
			// (see withResultPrinter) — the entry is what defines main.
			contents = withResultPrinter(contents)
		}
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	prog, _, err := modload.Load(filepath.Join(dir, entry))
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
	bin, err := wasmbin.BuildWithOptions(prog, info, wasmbin.BuildOptions{
		ForceMemorySection: true,
		Preview2WASI:       true,
		SynthCliRun:        true,
		PrintMainResult:    true,
	})
	if err != nil {
		t.Fatalf("wasmbin.Build: %v", err)
	}
	return finishComponentFromCoreBytes(t, bin)
}

// finishComponentFromCoreBytes composes the wasmbin-produced core
// module into a wasi:cli/run component natively, the same path the fern
// CLI takes (component.ClassifyCore → component.Compose) — no
// wasm-tools, no preview-1 adapter. The core must have been built with
// Preview2WASI + SynthCliRun so it exports `_lang_run` and imports the
// preview-2 WASI shapes the classifier recognises.
func finishComponentFromCoreBytes(t *testing.T, core []byte) string {
	t.Helper()
	req, unsupported := component.ClassifyCore(core)
	if len(unsupported) > 0 {
		t.Fatalf("core module has imports the composer can't place: %v", unsupported)
	}
	comp := component.Compose(core, req, "_lang_run")
	dir := t.TempDir()
	componentPath := filepath.Join(dir, "prog.component.wasm")
	if err := os.WriteFile(componentPath, comp, 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}
	return componentPath
}

// runComponent runs the component under wasmtime, returning the
// program's stdout, stderr, and the wasmtime exit code.
func runComponent(t *testing.T, componentPath string, opts runOpts) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmdArgs := []string{"run"}
	if opts.workDir != "" {
		cmdArgs = append(cmdArgs, "--dir="+opts.workDir)
	}
	for _, e := range opts.envs {
		cmdArgs = append(cmdArgs, "--env", e)
	}
	cmdArgs = append(cmdArgs, componentPath)
	cmdArgs = append(cmdArgs, opts.args...)
	cmd := exec.Command("wasmtime", cmdArgs...)
	cmd.Stdin = strings.NewReader(opts.stdin)
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	_ = cmd.Run()
	code := cmd.ProcessState.ExitCode()
	// An exit code alone cannot distinguish "the program returned N" from
	// "wasmtime refused the module", because both are just a status — a
	// validation failure and `return 1` are both exit 1. That conflation made
	// #5992's two conversion bugs look like one for the length of an issue: one
	// was a wrong ANSWER (u32 converted signed), the other did not COMPILE
	// (an i32 opcode on an i64 operand). Rejection is a different kind of
	// failure from a wrong result and should never be silently scored as one.
	if rejected, why := wasmRejected(se.String()); rejected {
		t.Fatalf("wasmtime REJECTED the module — this is not a wrong answer, the artifact is invalid or incomplete (%s)\nstderr:\n%s", why, se.String())
	}
	return so.String(), se.String(), code
}

// wasmRejected reports whether wasmtime's stderr describes a refusal to run the
// module at all, rather than a program that ran and returned something. The
// three shapes seen in practice, each from a real bug:
//
//   - "Invalid input WebAssembly code" / "type mismatch" — the module fails
//     validation (#5992: f64.convert_i32_s applied to an i64 stack value).
//   - "unknown func" / "failed to find name" — the module calls a function it
//     never defines (#5992: $__fern_u32_to_string had no body on wasm).
//   - "failed to compile" — wasmtime could not translate it.
//
// Deliberately conservative: it matches wasmtime's own diagnostics rather than
// guessing from the exit code, so a program that legitimately prints one of
// these strings is not mistaken for a rejected module.
func wasmRejected(stderr string) (bool, string) {
	for _, pair := range []struct{ needle, why string }{
		{"Invalid input WebAssembly", "module failed validation"},
		{"type mismatch", "module failed validation (stack type mismatch)"},
		{"unknown func", "module calls a function it never defines"},
		{"failed to find name", "module references an undefined name"},
		{"failed to compile", "wasmtime could not translate the module"},
		{"extra tokens remaining after parse", "emitted .wat is malformed"},
	} {
		if strings.Contains(stderr, pair.needle) {
			return true, pair.why
		}
	}
	return false, ""
}

// invokeWasmtime compiles src to a Component Model component and
// runs it under `wasmtime run`. Returns the program's stdout +
// stderr; fails the test if wasmtime exits non-zero. Use
// `runWasmStdinEnv` / `runWasmInDir` if you need to script stdin
// or env vars or expect a non-zero exit.
func invokeWasmtime(t *testing.T, src string) (stdout, stderr string) {
	t.Helper()
	p := buildComponent(t, src)
	s, e, ec := runComponent(t, p, runOpts{})
	if ec != 0 {
		t.Fatalf("wasmtime exit %d\nstdout:\n%s\nstderr:\n%s", ec, s, e)
	}
	return s, e
}

// invokeWasmtimeMultiFile is invokeWasmtime for a modload-driven
// program (entry + sibling files).
func invokeWasmtimeMultiFile(t *testing.T, entry string, files map[string]string) (stdout, stderr string) {
	t.Helper()
	p := buildComponentMulti(t, entry, files)
	s, e, ec := runComponent(t, p, runOpts{})
	if ec != 0 {
		t.Fatalf("wasmtime exit %d\nstdout:\n%s\nstderr:\n%s", ec, s, e)
	}
	return s, e
}

// runWasmMultiFile invokes `main` and parses the i32 result line
// `wasmtime run --invoke main` prints to stdout — the multi-file
// counterpart to runWasm. wasmtime emits warnings to stderr that
// we deliberately ignore here (the warning about --invoke
// returning values is harmless for our use).
func runWasmMultiFile(t *testing.T, entry string, files map[string]string) int {
	t.Helper()
	stdout, _ := invokeWasmtimeMultiFile(t, entry, files)
	for _, ln := range strings.Split(stdout, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		if i := strings.LastIndex(ln, " "); i >= 0 {
			ln = ln[i+1:]
		}
		if n, err := strconv.Atoi(ln); err == nil {
			return n
		}
	}
	t.Fatalf("could not parse wasmtime output:\n%s", stdout)
	return 0
}

// Cross-module struct types end-to-end on WASM, run through wasmtime.
func TestWASMCrossModuleStructType(t *testing.T) {
	got := runWasmMultiFile(t, "main.fern", map[string]string{
		"point.fern": `pub struct Point { x: i32, y: i32 }
pub function make(x: i32, y: i32): Point {
	return Point { x: x, y: y };
}`,
		"main.fern": `import "./point";
function main(): i32 {
	var p: point.Point = point.make(3, 4);
	return p.x + p.y;
}`,
	})
	if got != 7 {
		t.Errorf("got %d, want 7 (3 + 4)", got)
	}
}

// invokeWasmtimeWithArgs is invokeWasmtime plus extra positional
// argv that wasmtime forwards into `wasi:cli/environment.get-arguments`.
// Component model wasmtime puts the component path at argv[0] just
// like preview-1 did with the module path, so the args() builtin
// returns the same shape under both.
func invokeWasmtimeWithArgs(t *testing.T, src string, extraArgs ...string) (stdout, stderr string) {
	t.Helper()
	p := buildComponent(t, src)
	s, e, _ := runComponent(t, p, runOpts{args: extraArgs})
	return s, e
}

// args() under wasmtime: argv[0] is the wasm module path that
// wasmtime injects, plus whatever extra positional args we pass.
// HTTP/1.1 request parser end-to-end. Drives the lang-prelude
// `http_parse_request(buf)` against a complete request buffer
// (request line + headers + body) and asserts the returned
// HttpRequest's method / path / body. Caller is responsible
// for read-buffering off the socket; this test exercises the
// pure-string parse step in isolation.
func TestWASMHttpParseRequest(t *testing.T) {
	src := `
import "std/http";
function main(): i32 {
    var raw: string = "POST /todos HTTP/1.1\r\nHost: localhost\r\nContent-Length: 13\r\n\r\nhello, world!";
    match (http.http_parse_request(raw)) {
        Some(req) => {
            if (req.method != "POST") { return 1; }
            if (req.path != "/todos") { return 2; }
            if (req.body_string() != "hello, world!") { return 3; }
            return 42;
        },
        None => { return 99; }
    }
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42 (POST /todos with 13-byte body parses)", got)
	}
}

// Parser handles a no-body GET request (Content-Length absent;
// HTTP/1.1 §3.3.3 reads "no body" by default for safe methods).
func TestWASMHttpParseRequestNoBody(t *testing.T) {
	src := `
import "std/http";
function main(): i32 {
    var raw: string = "GET /hello?name=world HTTP/1.1\r\nHost: localhost\r\n\r\n";
    match (http.http_parse_request(raw)) {
        Some(req) => {
            if (req.method != "GET") { return 1; }
            if (req.path != "/hello?name=world") { return 2; }
            if (req.body_len() != 0) { return 3; }
            return 42;
        },
        None => { return 99; }
    }
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42 (GET with no body)", got)
	}
}

// Parser returns None on a partial buffer (header block not
// yet terminated by \r\n\r\n) — the caller is expected to
// keep recv'ing until parse succeeds.
func TestWASMHttpParseRequestPartial(t *testing.T) {
	src := `
import "std/http";
function main(): i32 {
    var raw: string = "GET /partial HTTP/1.1\r\nHost: loca";
    match (http.http_parse_request(raw)) {
        Some(_) => { return 1; },
        None => { return 42; }
    }
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42 (partial buffer rejects)", got)
	}
}

// HTTP/1.1 response serializer. Produces a wire-format
// response with status line + Content-Length + Connection:
// close + body. Verifies the byte layout the way a curl
// client would consume it.
func TestWASMHttpSerializeResponse(t *testing.T) {
	src := `
import "std/http";
function main(): i32 {
    var resp: HttpResponse = http.http_response_ok("hi");
    var wire: string = http.http_serialize_response(resp);
    var expected: string = "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nhi";
    if (wire != expected) { return 1; }
    return 42;
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42 (serialised response matches curl-readable shape)", got)
	}
}

// 404 status maps to the right reason phrase. Confirms the
// status-code → reason-phrase table covers the common cases.
func TestWASMHttpSerializeResponse404(t *testing.T) {
	src := `
import "std/http";
function main(): i32 {
    var resp: HttpResponse = http.http_response_text(404, "not found");
    var wire: string = http.http_serialize_response(resp);
    if (!wire.starts_with("HTTP/1.1 404 Not Found\r\n")) { return 1; }
    return 42;
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42 (404 reason phrase)", got)
	}
}

// `wasmtime run --invoke main mod.wat alpha beta` therefore yields
// argv = [mod.wat, alpha, beta] and `len(args()) == 3`.
func TestWASMArgsBuiltin(t *testing.T) {
	src := `function main(): i32 {
		var a: string[] = args();
		return a.len();
	}`
	stdout, _ := invokeWasmtimeWithArgs(t, src, "alpha", "beta")
	got := 0
	for _, ln := range strings.Split(stdout, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		if i := strings.LastIndex(ln, " "); i >= 0 {
			ln = ln[i+1:]
		}
		if n, err := strconv.Atoi(ln); err == nil {
			got = n
		}
	}
	if got != 3 {
		t.Errorf("got %d, want 3 (module path + alpha + beta)", got)
	}
}

// Reading argv values: print(a[1]) should produce the first user
// argument on stdout. The runtime helper's strlen + alloc + copy
// path lands the bytes in a length-prefixed string that the
// language's `print` lowers via fd_write like any other string.
func TestWASMArgsBuiltinReadsValue(t *testing.T) {
	src := `function main(): i32 {
		var a: string[] = args();
		print(a[1]);
		return 0;
	}`
	stdout, _ := invokeWasmtimeWithArgs(t, src, "hello")
	if !strings.Contains(stdout, "hello") {
		t.Errorf("expected stdout to contain `hello`, got %q", stdout)
	}
}

// args() entries are copied out of the shared argv_buf into fresh
// OWNED strings (via __fern_str_copy) so they carry an rc header and
// never alias the runtime's view buffer. A >7-byte arg routes through
// the heap-copy branch (short args inline-pack); the copied bytes must
// match the original argv exactly — a wrong copy length / source offset
// would print garbage or truncate.
func TestWASMArgsBuiltinCopiesLongValue(t *testing.T) {
	src := `function main(): i32 {
		var a: string[] = args();
		print(a[1]);
		return 0;
	}`
	const long = "supercalifragilistic-expialidocious"
	stdout, _ := invokeWasmtimeWithArgs(t, src, long)
	if !strings.Contains(stdout, long) {
		t.Errorf("expected stdout to contain the long arg %q, got %q", long, stdout)
	}
}

// runWasmStdinEnv runs the component under wasmtime with scripted
// stdin and env, returning stdout, stderr, and the exit code.
// `--env KEY=VAL` is forwarded by wasmtime to
// `wasi:cli/environment.get-environment` (preview-2) which is what
// the env() builtin reads from.
func runWasmStdinEnv(t *testing.T, src, stdin string, envs []string) (stdout, stderr string, exitCode int) {
	t.Helper()
	p := buildComponent(t, src)
	return runComponent(t, p, runOpts{stdin: stdin, envs: envs})
}

// `read_line()` reads one line from stdin including the trailing
// newline. The byte length is reported via main's return value;
// `wasmtime --invoke` echoes that on stdout so we can verify
// both the bytes (via `write`) and the count (via the printed
// return value).
// `read_line()` returns `Some(line)` for a present line (with the
// trailing `\n` preserved) and `None` for end-of-file. Programs
// pattern-match on the result; the post-Phase-3 typed shape
// replaces the empty-string sentinel that previous PRs used.
func TestWASMReadLineBuiltin(t *testing.T) {
	src := `function main(): i32 {
		match (stdin().read_line()) {
			Some(line) => { write(line); return line.len(); },
			None => { return -1; }
		}
		return -2;
	}`
	stdout, _, _ := runWasmStdinEnv(t, src, "hello\n", nil)
	if !strings.Contains(stdout, "hello\n") {
		t.Errorf("stdout missing `hello\\n`: %q", stdout)
	}
	if !strings.Contains(stdout, "6") {
		t.Errorf("stdout should contain `6` (len of \"hello\\n\"): %q", stdout)
	}
}

// Bare `read_line()` builtin (the stdin-only __fern_read_line
// path) on the default wasm target — distinct from
// stdin().read_line()'s receiver-aware path above. Reads a line
// and echoes it + its length.
func TestWASMReadLineBareBuiltin(t *testing.T) {
	src := `function main(): i32 {
		match (read_line()) {
			Some(line) => { write(line); return line.len(); },
			None => { return -1; }
		}
		return -2;
	}`
	stdout, _, _ := runWasmStdinEnv(t, src, "hi\n", nil)
	if !strings.Contains(stdout, "hi\n") {
		t.Errorf("stdout missing `hi\\n`: %q", stdout)
	}
	if !strings.Contains(stdout, "3") {
		t.Errorf("stdout should contain `3` (len of \"hi\\n\"): %q", stdout)
	}
}

// EOF on the first byte routes through the `None` arm; a
// non-empty line (even just "\n") routes through `Some`. The
// match arms are how callers disambiguate now — no sentinel
// length comparison required.
func TestWASMReadLineBuiltinEOF(t *testing.T) {
	src := `function main(): i32 {
		match (stdin().read_line()) {
			Some(line) => { return 1; },
			None => { return 0; }
		}
		return -1;
	}`
	stdout, _, _ := runWasmStdinEnv(t, src, "", nil)
	got := 0
	for _, ln := range strings.Split(stdout, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		if i := strings.LastIndex(ln, " "); i >= 0 {
			ln = ln[i+1:]
		}
		if n, err := strconv.Atoi(ln); err == nil {
			got = n
		}
	}
	if got != 0 {
		t.Errorf("got %d, want 0 (EOF routes to None arm)", got)
	}
}

// `env(name)` returns `Some(value)` when the key is set (even to
// empty) and `None` when the key isn't set. The test exercises
// the `Some` arm; the missing case is in TestWASMEnvBuiltinMissing.
func TestWASMEnvBuiltin(t *testing.T) {
	src := `function main(): i32 {
		match (env("LANG_TEST_VAR")) {
			Some(v) => { write(v); return 0; },
			None => { return 1; }
		}
		return -1;
	}`
	stdout, _, _ := runWasmStdinEnv(t, src, "", []string{"LANG_TEST_VAR=hi"})
	if !strings.Contains(stdout, "hi") {
		t.Errorf("stdout missing `hi`: %q", stdout)
	}
}

// env() copies the matched value out of the environ list buffer into a
// fresh owned string (via __fern_str_copy) — no view aliasing the
// runtime buffer. A >7-byte value exercises the heap-copy branch; the
// round-tripped bytes must match exactly.
func TestWASMEnvBuiltinCopiesLongValue(t *testing.T) {
	src := `function main(): i32 {
		match (env("LANG_TEST_VAR")) {
			Some(v) => { write(v); return 0; },
			None => { return 1; }
		}
		return -1;
	}`
	const long = "/usr/local/bin:/usr/bin:/bin:/opt/fern/bin"
	stdout, _, _ := runWasmStdinEnv(t, src, "", []string{"LANG_TEST_VAR=" + long})
	if !strings.Contains(stdout, long) {
		t.Errorf("stdout missing long env value %q: %q", long, stdout)
	}
}

// Missing env keys route to `None`, distinguishable from a
// present-but-empty value (which would still be `Some("")`).
func TestWASMEnvBuiltinMissing(t *testing.T) {
	src := `function main(): i32 {
		match (env("LANG_TEST_DEFINITELY_NOT_SET_XYZ_42")) {
			Some(v) => { return 1; },
			None => { return 0; }
		}
		return -1;
	}`
	stdout, _, _ := runWasmStdinEnv(t, src, "", nil)
	got := 0
	for _, ln := range strings.Split(stdout, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		if i := strings.LastIndex(ln, " "); i >= 0 {
			ln = ln[i+1:]
		}
		if n, err := strconv.Atoi(ln); err == nil {
			got = n
		}
	}
	if got != 0 {
		t.Errorf("got %d, want 0 (missing env routes to None)", got)
	}
}

// `exit(code)` calls preview-1 `proc_exit`, which the adapter
// turns into `wasi:cli/exit.exit(if code == 0 { Ok(()) }
// else { Err(()) })`. wasmtime then surfaces that as exit 0 or 1
// — the integer code is lost in the lift, since wasi:cli/exit's
// signature is `func(status: result<_, _>)` with no payload.
// Asserting that exit(non-zero) propagates as a non-zero exit
// code is the most we can verify under preview-2 0.2.0; preserving
// arbitrary codes would need `wasi:cli/exit.exit-with-code` which
// only ships in 0.2.1+.
func TestWASMExitBuiltin(t *testing.T) {
	src := `function main(): i32 {
		eprint("boom");
		exit(7);
		return 0;
	}`
	_, stderr, code := runWasmStdinEnv(t, src, "", nil)
	if !strings.Contains(stderr, "boom") {
		t.Errorf("stderr missing `boom`: %q", stderr)
	}
	if code == 0 {
		t.Errorf("exit = 0, want non-zero (program called exit(7))")
	}
}

// `write` produces stdout output without a trailing newline.
// Three consecutive `write` calls concatenate into a single run;
// the final `print` adds the only newline in the output.
func TestWASMWriteBuiltin(t *testing.T) {
	src := `function main(): i32 {
		write("a");
		write("b");
		print("c");
		return 0;
	}`
	stdout, _ := invokeWasmtime(t, src)
	want := "abc\n"
	if !strings.Contains(stdout, want) {
		t.Errorf("stdout = %q, want it to contain %q", stdout, want)
	}
}

// f-strings desugar at the lexer to `<lit> + (<expr>).to_string() +
// <lit> + ...`. End-to-end verifies the desugar preserves intent
// across multiple types (string, i32, computed expression) and
// that brace escapes (`{{` / `}}`) reach stdout as literal braces.
func TestWASMFStringInterpolation(t *testing.T) {
	src := `
import "std/string";
function main(): i32 {
		var name: string = "world";
		var n: i32 = 7;
		print(f"hi {name}");
		print(f"n is {n}, n*n is {n * n}");
		print(f"plain");
		print(f"with literal braces: {{{n}}}");
		return 0;
	}`
	stdout, _ := invokeWasmtime(t, src)
	for _, want := range []string{
		"hi world",
		"n is 7, n*n is 49",
		"plain",
		"with literal braces: {7}",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

// `eprint` lands on stderr (fd=2). wasmtime keeps fds 1 and 2
// separate, so the test can confirm that `print("hi")` shows up
// on stdout and `eprint("err")` shows up on stderr — without
// either bleeding into the other stream.
func TestWASMEprintBuiltin(t *testing.T) {
	src := `function main(): i32 {
		print("hi");
		eprint("err");
		return 0;
	}`
	stdout, stderr := invokeWasmtime(t, src)
	if !strings.Contains(stdout, "hi") {
		t.Errorf("stdout missing `hi`: %q", stdout)
	}
	if strings.Contains(stdout, "err") {
		t.Errorf("stdout should not contain `err`, got %q", stdout)
	}
	if !strings.Contains(stderr, "err") {
		t.Errorf("stderr missing `err`: %q", stderr)
	}
}

// Regression for issue #2550: `(w: Writer).write(s)` to the stdout
// Writer corrupted exactly one byte — the byte at the largest
// multiple of 8 below len came out as its successor (buf[k] == buf[k+1]).
//
// Root cause was NOT in the write path: the stdout() Writer was built
// with a bare alloc(4) and no rc-sentinel header, unlike the file
// Writer (open_writer) which reserves 8 leading bytes for the static
// 0x80000000 sentinel. Because the Writer is a refcounted heap value,
// __fern_retain/__fern_drop mutate mem[ptr-8]; for the FIRST heap
// object that ptr-8 underflows into the static data segment, so retain
// incremented a byte of the string literal being written (the 'g'
// at offset 16 of "0123…gh" became 'h'). Position-dependent because it
// only bit the literal sitting immediately below the bump heap.
//
// The sweep covers the boundary lengths from the bug report (8, 16,
// 17, 18, 24, 30) plus a couple extra; all must round-trip byte-exact.
func TestWASMWriterWriteStdoutNoCorruption(t *testing.T) {
	const src = `
import "std/string";
function emit(s: string): i32 {
    var w: Writer = stdout();
    match (w.write(s)) { Some(_) => { return 1; }, None => {}, }
    return 0;
}
function main(): i32 {
    if (emit("01234567\n") != 0) { return 1; }
    if (emit("0123456789abcdef\n") != 0) { return 2; }
    if (emit("0123456789abcdefg\n") != 0) { return 3; }
    if (emit("0123456789abcdefgh\n") != 0) { return 4; }
    if (emit("0123456789abcdefghijklmnop\n") != 0) { return 5; }
    if (emit("0123456789abcdefghijklmnopqrst\n") != 0) { return 6; }
    return 0;
}`
	stdout, _ := invokeWasmtime(t, src)
	want := "01234567\n" +
		"0123456789abcdef\n" +
		"0123456789abcdefg\n" +
		"0123456789abcdefgh\n" +
		"0123456789abcdefghijklmnop\n" +
		"0123456789abcdefghijklmnopqrst\n"
	if !strings.Contains(stdout, want) {
		t.Errorf("stdout Writer.write corrupted output.\n got: %q\nwant it to contain: %q", stdout, want)
	}
}

func runWasm(t *testing.T, src string) int {
	t.Helper()
	stdout, _ := invokeWasmtime(t, src)
	// `wasmtime run --invoke main` returns the function's i32 result on
	// stdout, sometimes followed by a unit-line; parse the first int.
	for _, ln := range strings.Split(stdout, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		// wasmtime prints either a bare integer or "i32: N".
		if i := strings.LastIndex(ln, " "); i >= 0 {
			ln = ln[i+1:]
		}
		if n, err := strconv.Atoi(ln); err == nil {
			return n
		}
	}
	t.Fatalf("could not parse wasmtime output:\n%s", stdout)
	return 0
}

func TestWASMReturn42(t *testing.T) {
	if got := runWasm(t, `function main(): i32 { return 42; }`); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

// i64 round-trips through arithmetic + comparison. main() stays
// i32 (the test harness reads main's i32 return via int_to_string),
// but the body holds an i64 value through addition and comparison.
// Exercises OpExtendI32S on the casts in, OpAdd/OpEq with Width=64
// in the body, and the i64 wasm types on the local + parameter
// slots.
func TestWASMI64Arithmetic(t *testing.T) {
	src := `function add64(x: i64, y: i64): i64 { return x + y; }
function main(): i32 {
    var z: i64 = add64(40, 2);
    if (z == 42) { return 0; }
    return 1;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (i64 arith mismatch)", got)
	}
}

// Polymorphic numeric literals: a bare `1` flows into any
// integer-typed slot — `var x: i64 = 1` works without `1 as
// i64`, `f(x, 0)` resolves the `0` against f's parameter type,
// `x + 1` settles the `1` to x's width. This test exercises all
// three sites in a single program and verifies the wasm output
// runs end-to-end.
func TestWASMPolymorphicNumericLiterals(t *testing.T) {
	src := `function add64(x: i64, y: i64): i64 { return x + y; }
function main(): i32 {
    var a: i64 = 40;
    var b: u32 = 4294967295;
    var c: u64 = 1 << 62;
    if (add64(a, 2) != 42) { return 1; }
    if (b / 2 != 2147483647) { return 2; }
    if (c <= 0) { return 3; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (polymorphic literal mismatch)", got)
	}
}

// Out-of-range polymorphic literal in i32 context is rejected:
// `var x: i32 = 5000000000` is way past 2^31 so the checker
// should refuse rather than silently wrap.
func TestPolymorphicLiteralI32Overflow(t *testing.T) {
	src := `function bad(): i32 { var x: i32 = 5000000000; return x; }`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err == nil {
		t.Fatalf("expected checker error for out-of-range i32 literal, got none")
	} else if !strings.Contains(err.Error(), "fit") {
		t.Errorf("expected 'fit' in error, got: %v", err)
	}
}

// Sub-i32 width (u8). Storage lives in i32 locals (wasm has no
// narrower locals); the bookkeeping is in the casts: narrowing
// masks to 8 bits. Arithmetic is at i32 precision.
// On wasm32 `usize` collapses to i32 (ptrW=4), so the
// target-aware behaviour shouldn't observably differ from i32.
// Pin that the type compiles, casts work, and arithmetic
// stays in i32-shaped space.
func TestWASMUsize(t *testing.T) {
	src := `function dbl(x: usize): usize { return x + x; }
function main(): i32 {
    var n: usize = 21;
    return dbl(n) as i32;
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42 (usize on wasm32)", got)
	}
}

func TestWASMSubI32Widths(t *testing.T) {
	src := `function main(): i32 {
    var a: u8 = 200;
    var b: u8 = 50;
    var sum: u8 = (a + b) as u8;
    if (sum != 250) { return 1; }
    var u: u8 = 250;
    var widened: u32 = u as u32;
    if (widened != 250) { return 2; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (sub-i32 width arithmetic)", got)
	}
}

// Out-of-range polymorphic literal in u8 context is rejected.
// `var x: u8 = 300` exceeds 2^8-1 so the checker should refuse.
func TestSubI32LiteralOverflow(t *testing.T) {
	src := `function bad(): i32 { var x: u8 = 300; return x as i32; }`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err == nil {
		t.Fatalf("expected checker error for out-of-range u8 literal, got none")
	} else if !strings.Contains(err.Error(), "fit") {
		t.Errorf("expected 'fit' in error, got: %v", err)
	}
}

// Owned u8 arrays. Storage is 1-byte-per-element with a 4-byte
// length prefix; reads use `i32.load8_u`. Verifies a literal
// of three bytes round-trips through indexing.
func TestWASMU8Array(t *testing.T) {
	src := `function main(): i32 {
    var bytes: u8[] = [255, 0, 66];
    if (bytes[0] != 255) { return 1; }
    if (bytes[1] != 0) { return 2; }
    if (bytes[2] != 66) { return 3; }
    if (bytes.len() != 3) { return 4; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (u8 array)", got)
	}
}

// Methods on user-defined enums. The receiver clause `(self:
// Color)` makes `c.is_red()` resolve to a hoisted top-level
// function `__method_Color_is_red`. Verifies dispatch on a
// non-generic enum's payload-less variants.
func TestWASMEnumMethod(t *testing.T) {
	src := `enum Color { Red, Green, Blue }
function (self: Color) is_red(): boolean {
    match (self) {
        Red => { return true; },
        _ => { return false; },
    }
}
function main(): i32 {
    var c: Color = Red;
    if (c.is_red()) { return 0; }
    return 1;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (enum method dispatch)", got)
	}
}

// Methods on a generic enum (`Option[T]`). Verifies the method
// receiver picks up `T` from the value's runtime instantiation.
func TestWASMEnumMethodGeneric(t *testing.T) {
	src := `function (self: Option[i32]) unwrap_or(fallback: i32): i32 {
    match (self) {
        Some(v) => { return v; },
        None => { return fallback; },
    }
}
function main(): i32 {
    var s: Option[i32] = Some(7);
    var n: Option[i32] = None;
    if (s.unwrap_or(0) != 7) { return 1; }
    if (n.unwrap_or(99) != 99) { return 2; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (generic enum method)", got)
	}
}

// Map[i32, i32] linear-search smoke test. Auto-injected `Map`
// struct + `map_new(cap)` constructor; methods are
// `m.insert(k, v)`, `m.get(k)`, `m.has(k)`, `m.len()`. This is
// the first cut from PR 4 in docs/LANGUAGE-DIRECTION.md;
// generic K / V and the IndexMap fingerprint-table layout
// land in follow-ups.
func TestWASMMapBasics(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m: Map[i32, i32] = map_new(8);
    if (m.len() != 0) { return 1; }
    if (m.has(7)) { return 2; }
    m = m.insert(7, 42);
    m = m.insert(11, 99);
    if (m.len() != 2) { return 3; }
    if (!m.has(7)) { return 4; }
    if (!m.has(11)) { return 5; }
    if (m.has(13)) { return 6; }
    if let Some(v) = m.get(7) {
        if (v != 42) { return 7; }
    } else {
        return 8;
    }
    if let Some(v) = m.get(11) {
        if (v != 99) { return 9; }
    } else {
        return 10;
    }
    if let Some(_) = m.get(13) {
        return 11;
    }
    // Update an existing key — len stays at 2.
    m = m.insert(7, 100);
    if (m.len() != 2) { return 12; }
    if let Some(v) = m.get(7) {
        if (v != 100) { return 13; }
    } else {
        return 14;
    }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (map basic ops)", got)
	}
}

// Two `m.has()` calls under a short-circuiting `&&` with a `!` on the
// second operand. Regression for issue #2652: on the self-hosted wasm
// backend `m.has(1) && !m.has(2)` evaluated to 0 instead of 7 — a
// stack/value interaction between the `map_has` call sequence and the
// `&&` short-circuit block. `m.has(1)` is true, `m.has(2)` is false, so
// `!m.has(2)` is true and the `&&` is true. The native backend always
// agreed with the correct answer; this guards it on the result-printer
// (wasm) harness so the cross-backend contract stays explicit.
func TestWASMMapHasShortCircuit(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m: Map[i32, i32] = map_new(8);
    m = m.insert(1, 10);
    if (m.has(1) && !m.has(2)) { return 7; }
    return 0;
}`
	if got := runWasm(t, src); got != 7 {
		t.Errorf("got %d, want 7 (m.has(1) && !m.has(2) short-circuit, issue #2652)", got)
	}
}

// Issue #2763: a value-type struct wrapping a Map (the `Set[T]` shape),
// reconstructed through a method — `return IntSet { m: s.m.insert(...) }`.
// The COW mutator returns the borrowed receiver's handle mutated in place,
// so without a clone the new struct aliases the caller's map and dropping
// the old struct frees it from under the new one (segfault on x86-64,
// corruption elsewhere). The StructLit lowering now clones a Map field
// initialised by a COW mutator result. Covers insert (set), a duplicate
// (size stays 2), and a `.without` (delete) mutator on the same shape.
func TestWASMMapFieldStructRebind(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		// `.cleared()` (the value-returning clear) also lowers to a COW
		// mutator (__method_Map_clear) and returns a bare Map, so it can
		// initialise the field directly — exercising the clone on the clear
		// path. insert 10/20 (len 2), then clear → len 0.
		{"insert-then-cleared", `
import "core/map";
struct IntSet { m: Map[i32, i32] }
function (s: IntSet) insert(x: i32): IntSet { return IntSet { m: s.m.insert(x, 1) }; }
function (s: IntSet) wipe(): IntSet { return IntSet { m: s.m.cleared() }; }
function (s: IntSet) len(): i32 { return s.m.len(); }
function main(): i32 {
    var m0: Map[i32, i32] = map_new(4);
    var s: IntSet = IntSet { m: m0 };
    s = s.insert(10);
    s = s.insert(20);
    s = s.wipe();
    return s.len();
}`, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runWasm(t, tc.src); got != tc.want {
				t.Errorf("wasm %s: got %d, want %d (issue #2763)", tc.name, got, tc.want)
			}
		})
	}
}

// Map dynamic resize: insert past the initial capacity and
// verify all entries remain reachable. Without resize this
// would trap on `unreachable`; with resize the buffer doubles
// (2 → 4 → 8 → 16) and the wrapper's data pointer follows
// along.
func TestWASMMapResize(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m: Map[i32, i32] = map_new(2);
    var i: i32 = 0;
    while (i < 12) {
        m = m.insert(i, i * 10);
        i = i + 1;
    }
    if (m.len() != 12) { return 1; }
    var j: i32 = 0;
    while (j < 12) {
        if let Some(v) = m.get(j) {
            if (v != j * 10) { return j + 100; }
        } else {
            return j + 200;
        }
        j = j + 1;
    }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (map resize)", got)
	}
}

// Map.keys() / Map.values() snapshot the entries into i32[]
// arrays in insertion order. The returned arrays are normal
// length-prefixed `i32[]` values — `len()`, indexing, and
// `for` work the way they do on any other array.
func TestWASMMapKeysValues(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m: Map[i32, i32] = map_new(4);
    m = m.insert(10, 100);
    m = m.insert(20, 200);
    m = m.insert(30, 300);
    var ks: i32[] = m.keys();
    var vs: i32[] = m.values();
    if (ks.len() != 3) { return 1; }
    if (vs.len() != 3) { return 2; }
    // Insertion order: ks == [10, 20, 30], vs == [100, 200, 300].
    if (ks[0] != 10) { return 3; }
    if (ks[1] != 20) { return 4; }
    if (ks[2] != 30) { return 5; }
    if (vs[0] != 100) { return 6; }
    if (vs[1] != 200) { return 7; }
    if (vs[2] != 300) { return 8; }
    // Sum the values via a normal indexed loop.
    var i: i32 = 0;
    var sum: i32 = 0;
    while (i < vs.len()) {
        sum = sum + vs[i];
        i = i + 1;
    }
    if (sum != 600) { return 9; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (map keys/values)", got)
	}
}

// Wide-V Map.values(): for `Map[K, i64]` / `Map[K, f64]`,
// values are stored boxed (each entry holds a 4-byte cell
// pointer to an 8-byte heap cell). The IR's wide-V values()
// intercept (emitWideMapValues) follows each cell pointer
// and `__memcpy`s the 8 payload bytes into a real wide-stride
// `i64[]` / `f64[]` result.
func TestWASMMapValuesWideI64(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m: Map[i32, i64] = map_new(4);
    m = m.insert(1, 1000000000000i64);
    m = m.insert(2, 2000000000000i64);
    m = m.insert(3, 3000000000000i64);
    var vs: i64[] = m.values();
    if (vs.len() != 3) { return 1; }
    if (vs[0] != 1000000000000i64) { return 2; }
    if (vs[2] != 3000000000000i64) { return 3; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (Map[i32,i64].values() preserves wide bits)", got)
	}
}

func TestWASMMapValuesWideF64(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m: Map[i32, f64] = map_new(4);
    m = m.insert(1, 1.5f64);
    m = m.insert(2, 2.5f64);
    var vs: f64[] = m.values();
    if (vs.len() != 2) { return 1; }
    if (vs[0] != 1.5f64) { return 2; }
    if (vs[1] != 2.5f64) { return 3; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (Map[i32,f64].values() preserves wide bits)", got)
	}
}

// Wide-K Map ops: `Map[i64, _]` / `Map[u64, _]` / `Map[f64, _]`
// on wasm32 — the key (8 bytes) doesn't fit the runtime's i32
// per-entry K slot, so the IR boxes each key into a heap cell
// and tags the map's keyKind as 2; the prelude's
// `__map_hash` / `__map_lookup` / `__map_set_impl` /
// `__map_delete_impl` dereference via `__load_i64` to hash and
// compare the underlying 8-byte values. On natives `usize` is
// already 8 bytes so the raw-stored keyKind=0 path keeps
// working (see TestX86_64WideScalarMap / TestArm64WideScalarMap).
func TestWASMWideKeyMapBasic(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m: Map[i64, i32] = map_new(4);
    m = m.insert(1i64, 100);
    m = m.insert(2i64, 200);
    return m.get_or(2i64, 0) + m.get_or(1i64, 0);
}`
	if got := runWasm(t, src); got != 300 {
		t.Errorf("got %d, want 300", got)
	}
}

func TestWASMWideKeyMapHasDelete(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m: Map[i64, i32] = map_new(4);
    m = m.insert(7i64, 100);
    m = m.insert(42i64, 200);
    if (!m.has(7i64)) { return 1; }
    if (!m.has(42i64)) { return 2; }
    if (m.has(99i64)) { return 3; }
    if (!m.without(7i64).1) { return 4; }
    if (m.has(7i64)) { return 5; }
    if (m.without(7i64).1) { return 6; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (has/delete on Map[i64, i32])", got)
	}
}

func TestWASMWideKeyMapOverwrite(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m: Map[i64, i32] = map_new(4);
    m = m.insert(1i64, 100);
    m = m.insert(1i64, 999);
    if (m.len() != 1) { return 1; }
    return m.get_or(1i64, 0);
}`
	if got := runWasm(t, src); got != 999 {
		t.Errorf("got %d, want 999 (overwrite same wide key)", got)
	}
}

func TestWASMWideKeyMapGrow(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m: Map[i64, i32] = map_new(2);
    var i: i32 = 0;
    while (i < 20) {
        m = m.insert(i as i64, i * 10);
        i = i + 1;
    }
    if (m.len() != 20) { return 1; }
    var sum: i32 = 0;
    var j: i32 = 0;
    while (j < 20) {
        sum = sum + m.get_or(j as i64, 0);
        j = j + 1;
    }
    return sum;
}`
	if got := runWasm(t, src); got != 1900 {
		// 10 * (0+1+...+19) = 1900
		t.Errorf("got %d, want 1900 (grow + reprobe with wide keys)", got)
	}
}

// Hash quality smoke-test: two i64 values that differ only in
// the high 32 bits must hash distinctly so the bucket array
// doesn't collide on the trivial low-32 truncation. Folds the
// upper / lower halves via XOR before Wang's mix.
func TestWASMWideKeyMapHighBitsDistinct(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m: Map[i64, i32] = map_new(8);
    var k1: i64 = 0i64;
    var k2: i64 = 1i64 << 33i64;
    m = m.insert(k1, 1);
    m = m.insert(k2, 2);
    return m.get_or(k1, 99) + m.get_or(k2, 99);
}`
	if got := runWasm(t, src); got != 3 {
		t.Errorf("got %d, want 3 (distinct high-bit i64 keys)", got)
	}
}

func TestWASMWideKeyMapU64(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m: Map[u64, i32] = map_new(4);
    m = m.insert(1u64, 100);
    return m.get_or(1u64, 0);
}`
	if got := runWasm(t, src); got != 100 {
		t.Errorf("got %d, want 100", got)
	}
}

func TestWASMWideKeyMapStringV(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m: Map[i64, string] = map_new(4);
    m = m.insert(1i64, "hello");
    return (m.get_or(1i64, "")).len();
}`
	if got := runWasm(t, src); got != 5 {
		t.Errorf("got %d, want 5 (len('hello'))", got)
	}
}

// m.keys() on a wide-K map needs to materialise an `i64[]`
// (not `i32[]`) whose elements are the full 8 bytes. The IR
// intercepts via emitWideMapKeys: on wasm32 (keyKind=2) the
// K slot stores a cell pointer that's followed and the cell
// contents memcpy'd into the wide-stride result; on natives
// the K slot stores the raw 8-byte value and is memcpy'd
// directly. Without this intercept the prelude's
// `__map_keys_impl` would use a 4-byte destStride and the
// upper 4 bytes of every key would be lost.
func TestWASMWideKeyMapKeysSnapshot(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m: Map[i64, i32] = map_new(4);
    m = m.insert(1i64, 10);
    m = m.insert(1000000000000i64, 20);
    var keys: i64[] = m.keys();
    if (keys.len() != 2) { return 1; }
    if (keys[0] != 1i64 && keys[0] != 1000000000000i64) { return 2; }
    if (keys[1] != 1i64 && keys[1] != 1000000000000i64) { return 3; }
    if (keys[0] == keys[1]) { return 4; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (wide-K keys preserved)", got)
	}
}

// Map.without(k) removes a key, returning true if it was
// present. Implementation is swap-with-last (O(1), trades
// insertion order for speed). Verifies the basic
// present/missing behaviour, that len decrements, and that
// subsequent lookups return None.
func TestWASMMapDelete(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m: Map[i32, i32] = map_new(4);
    m = m.insert(1, 10);
    m = m.insert(2, 20);
    m = m.insert(3, 30);
    if (m.len() != 3) { return 1; }
    if (!m.without(2).1) { return 2; }   // present → true
    if (m.len() != 2) { return 3; }
    if (m.has(2)) { return 4; }
    if let Some(_) = m.get(2) { return 5; }
    if (m.without(99).1) { return 6; }   // missing → false
    if (m.len() != 2) { return 7; }
    // Surviving entries still reachable.
    if let Some(v) = m.get(1) {
        if (v != 10) { return 8; }
    } else {
        return 9;
    }
    if let Some(v) = m.get(3) {
        if (v != 30) { return 10; }
    } else {
        return 11;
    }
    // Re-insert the deleted key with a new value.
    m = m.insert(2, 222);
    if (m.len() != 3) { return 12; }
    if let Some(v) = m.get(2) {
        if (v != 222) { return 13; }
    } else {
        return 14;
    }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (map delete)", got)
	}
}

// `Map { k: v, ... }` literal syntax desugars to map_new +
// set calls. The capacity is sized to the entry count so the
// initial fill never triggers a resize.
func TestWASMMapLiteral(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m: Map[i32, i32] = Map { 1: 10, 2: 20, 3: 30 };
    if (m.len() != 3) { return 1; }
    if let Some(v) = m.get(2) {
        if (v != 20) { return 2; }
    } else {
        return 3;
    }
    if let Some(_) = m.get(99) { return 4; }
    // Empty literal works too.
    var empty: Map[i32, i32] = Map {};
    if (empty.len() != 0) { return 5; }
    if (empty.has(0)) { return 6; }
    // Trailing comma is fine.
    var m2: Map[i32, i32] = Map { 7: 700, };
    if (m2.len() != 1) { return 7; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (map literal)", got)
	}
}

// Sub-i32 array writes: `arr[i] = v` for byte arrays.
// Stride-aware bounds-check helper + width-aware store
// (i32.store8 for u8). Read path was already wired in PR 134;
// this completes the symmetry.
func TestWASMSubI32ArrayWrites(t *testing.T) {
	src := `function main(): i32 {
    var bytes: u8[] = [1, 2, 3, 4];
    bytes = bytes.with(0, 250);
    bytes = bytes.with(2, 99);
    if (bytes[0] != 250) { return 1; }
    if (bytes[1] != 2) { return 2; }
    if (bytes[2] != 99) { return 3; }
    if (bytes[3] != 4) { return 4; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (sub-i32 array writes)", got)
	}
}

// Owned i64 / u64 arrays. 8-byte stride via __arr_idx_8;
// loads / stores use i64.load / i64.store. Verifies that a
// value with the high bit set round-trips through indexed
// read.
func TestWASMI64Array(t *testing.T) {
	src := `function i64Sum(xs: i64[], n: i32): i64 {
    var i: i32 = 0;
    var s: i64 = 0;
    while (i < n) {
        s = s + xs[i];
        i = i + 1;
    }
    return s;
}
function main(): i32 {
    var xs: i64[] = [1, 2, 3, 4];
    xs = xs.with(1, (1 << 62) + 1);
    var s: i64 = i64Sum(xs, 4);
    // 1 + ((1 << 62) + 1) + 3 + 4 == (1 << 62) + 9
    if (s != (1 << 62) + 9) { return 1; }
    if (xs[0] != 1) { return 2; }
    if (xs[3] != 4) { return 3; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (i64 array)", got)
	}
}

// Owned f64 arrays. 8-byte stride; loads / stores use
// f64.load / f64.store. Verifies a literal of three f64
// values round-trips through indexing.
func TestWASMF64Array(t *testing.T) {
	src := `function main(): i32 {
    var xs: f64[] = [1.5, 2.5, 3.5];
    xs = xs.with(1, 99.25);
    if (xs[0] != 1.5) { return 1; }
    if (xs[1] != 99.25) { return 2; }
    if (xs[2] != 3.5) { return 3; }
    var sum: f64 = xs[0] + xs[1] + xs[2];
    if (sum != 104.25) { return 4; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (f64 array)", got)
	}
}

// Slice views over byte / word / wide arrays. Verifies that
// `arr[a:b]` produces a slice with the right element stride —
// indexing into the slice should match the underlying array.
func TestWASMSubI32Slices(t *testing.T) {
	src := `function main(): i32 {
    var bytes: u8[] = [10, 20, 30, 40, 50];
    var view: [u8] = bytes[1:4];
    if (view.len() != 3) { return 1; }
    if (view[0] != 20) { return 2; }
    if (view[1] != 30) { return 3; }
    if (view[2] != 40) { return 4; }

    var words: i32[] = [1, 2, 3, 4];
    var wordview: [i32] = words[2:];
    if (wordview.len() != 2) { return 5; }
    if (wordview[0] != 3) { return 6; }
    if (wordview[1] != 4) { return 7; }

    var wide: i64[] = [(1 << 40), (1 << 41), (1 << 42)];
    var wview: [i64] = wide[1:3];
    if (wview.len() != 2) { return 8; }
    if (wview[0] != (1 << 41)) { return 9; }
    if (wview[1] != (1 << 42)) { return 10; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (byte / word / wide slices)", got)
	}
}

// Slice writes mutate the underlying array's storage. The
// IR's slice-index assignment path picks the per-stride
// __slice_idx_N helper (same as the read path) and the
// width-aware store op. Verifies that mutations through a
// slice show up when reading back from the parent.
// (Removed TestWASMSliceWrites: writing through a slice mutated the backing
// array, which immutability (E056) forbids — slices are read-only views now.
// Slice reads are covered by TestWASMStringSlice and the slice-read cases.)

// String slicing — `s[a:b]` returns a freshly allocated
// substring. Bounds checked; default low/high mean 0 and
// len(s) respectively.
func TestWASMStringSlice(t *testing.T) {
	src := `function main(): i32 {
    var greeting: string = "hello world";
    var hello: str = greeting[0:5];
    var world: str = greeting[6:11];
    var dot: str = greeting[5:6];
    if (hello.len() != 5) { return 1; }
    if (world.len() != 5) { return 2; }
    if (dot.len() != 1) { return 3; }
    if (hello != "hello") { return 4; }
    if (world != "world") { return 5; }
    if (dot != " ") { return 6; }
    // Open-ended low / high.
    var prefix: str = greeting[:5];
    var suffix: str = greeting[6:];
    if (prefix != "hello") { return 7; }
    if (suffix != "world") { return 8; }
    // Empty slice.
    var empty: str = greeting[3:3];
    if (empty.len() != 0) { return 9; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (string slicing)", got)
	}
}

// String methods: starts_with / ends_with / contains. Built-in
// runtime helpers; method-call dispatch on string targets is
// wired through the same `__method_<TypeName>_<Name>` mangling
// the rest of the language uses.
func TestWASMStringMethods(t *testing.T) {
	src := `
import "std/string";
function main(): i32 {
    var s: string = "hello world";
    if (!s.starts_with("hello")) { return 1; }
    if (!s.starts_with("h")) { return 2; }
    if (s.starts_with("world")) { return 3; }
    if (s.starts_with("hello world!")) { return 4; }
    if (!s.starts_with("")) { return 5; }

    if (!s.ends_with("world")) { return 6; }
    if (!s.ends_with("d")) { return 7; }
    if (s.ends_with("hello")) { return 8; }
    if (!s.ends_with("")) { return 9; }

    if (!s.contains("hello")) { return 10; }
    if (!s.contains("world")) { return 11; }
    if (!s.contains(" ")) { return 12; }
    if (!s.contains("o w")) { return 13; }
    if (s.contains("xyz")) { return 14; }
    if (s.contains("hello world!")) { return 15; }
    if (!s.contains("")) { return 16; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (string methods)", got)
	}
}

// More string methods: index_of, trim, to_lower, to_upper.
func TestWASMStringMethodsExtra(t *testing.T) {
	src := `
import "std/string";
function main(): i32 {
    var s: string = "hello world";
    if (s.index_of("hello") != 0) { return 1; }
    if (s.index_of("world") != 6) { return 2; }
    if (s.index_of(" ") != 5) { return 3; }
    if (s.index_of("xyz") != -1) { return 4; }
    if (s.index_of("") != 0) { return 5; }
    if (s.index_of("hello world!") != -1) { return 6; }

    var padded: string = "  hello   ";
    var trimmed: str = padded.trim();
    if (trimmed != "hello") { return 7; }
    if (trimmed.len() != 5) { return 8; }
    var blank: string = "    ";
    if (blank.trim() != "") { return 9; }
    var nopad: string = "abc";
    if (nopad.trim() != "abc") { return 10; }

    if ("Hello, World!".to_lower() != "hello, world!") { return 11; }
    if ("Hello, World!".to_upper() != "HELLO, WORLD!") { return 12; }
    // Non-letter ASCII bytes are unchanged.
    if ("abc 123!".to_upper() != "ABC 123!") { return 13; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (extra string methods)", got)
	}
}

// String split: returns string[] of substrings between
// occurrences of the separator. Empty separator splits into
// single-char strings (matches JS String.split("")).
func TestWASMStringSplit(t *testing.T) {
	src := `
import "std/string";
function main(): i32 {
    var s: string = "a,b,c,d";
    var parts: string[] = s.split(",");
    if (parts.len() != 4) { return 1; }
    if (parts[0] != "a") { return 2; }
    if (parts[1] != "b") { return 3; }
    if (parts[2] != "c") { return 4; }
    if (parts[3] != "d") { return 5; }

    // No occurrence: single-element array holding the whole input.
    var none: string[] = "hello".split(",");
    if (none.len() != 1) { return 6; }
    if (none[0] != "hello") { return 7; }

    // Multi-byte separator.
    var s2: string = "alpha::beta::gamma";
    var p2: string[] = s2.split("::");
    if (p2.len() != 3) { return 8; }
    if (p2[0] != "alpha") { return 9; }
    if (p2[1] != "beta") { return 10; }
    if (p2[2] != "gamma") { return 11; }

    // Empty separator splits into chars.
    var chars: string[] = "abc".split("");
    if (chars.len() != 3) { return 12; }
    if (chars[0] != "a") { return 13; }
    if (chars[1] != "b") { return 14; }
    if (chars[2] != "c") { return 15; }

    // Empty pieces around separators are preserved.
    var trailing: string[] = ",a,b,".split(",");
    if (trailing.len() != 4) { return 16; }
    if (trailing[0] != "") { return 17; }
    if (trailing[3] != "") { return 18; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (string split)", got)
	}
}

// `type X = A | B | C;` unions on wasm — the desugaring
// produces a synthesised EnumDecl whose variants each carry
// the named struct as a single positional payload. Verifies
// the full wasm pipeline survives the new top-level decl shape
// (`prog.Unions` flowing through modload's combine, checker's
// desugar, and the unchanged IR / codegen path).
func TestWASMUnions(t *testing.T) {
	src := `struct Add { l: i32, r: i32 }
struct Mul { l: i32, r: i32 }
struct Lit { v: i32 }

type Expr = Add | Mul | Lit;

function eval(e: Expr): i32 {
    match (e) {
        Add(a) => { return a.l + a.r; },
        Mul(m) => { return m.l * m.r; },
        Lit(l) => { return l.v; },
    }
}

function main(): i32 {
    var lhs: Expr = Add(Add { l: 2, r: 3 });
    var rhs: Expr = Lit(Lit { v: 4 });
    var prod: Expr = Mul(Mul { l: eval(lhs), r: eval(rhs) });
    return eval(prod);
}`
	if got := runWasm(t, src); got != 20 {
		t.Errorf("got %d, want 20 ((2+3)*4)", got)
	}
}

// Union member that doesn't name a struct fails at the
// checker level with a clear message — no IR-time crash.
func TestUnionUnknownMemberErrors(t *testing.T) {
	src := `struct A { x: i32 }
type Foo = A | Missing;

function main(): i32 { return 0; }`
	prog, perr := parser.Parse(src)
	if perr != nil {
		t.Fatalf("parse: %v", perr)
	}
	_, cerr := checker.Check(prog)
	if cerr == nil {
		t.Fatalf("expected checker error for unknown union member, got none")
	}
	if !strings.Contains(cerr.Error(), "does not name a struct") {
		t.Errorf("unexpected error: %v", cerr)
	}
}

// Implicit struct → union wrap on wasm. Same source as the
// arm64 + x86 mirrors. Verifies the wrap-and-re-check pass
// integrates with the existing wasm enum lowering.
func TestWASMUnionImplicitWrap(t *testing.T) {
	src := `struct Add { l: i32, r: i32 }
struct Mul { l: i32, r: i32 }
struct Lit { v: i32 }

type Expr = Add | Mul | Lit;

function eval(e: Expr): i32 {
    match (e) {
        Add(a) => { return a.l + a.r; },
        Mul(m) => { return m.l * m.r; },
        Lit(l) => { return l.v; },
    }
}

function mk_add(l: i32, r: i32): Expr {
    return Add { l: l, r: r };
}

function main(): i32 {
    var a: Expr = Add { l: 2, r: 3 };
    var sum: i32 = eval(Lit { v: 5 });
    a = Mul { l: 2, r: sum };
    var built: Expr = mk_add(1, 2);
    return eval(a) + eval(built) + sum;
}`
	if got := runWasm(t, src); got != 18 {
		t.Errorf("got %d, want 18", got)
	}
}

// `s.lines()` — Python `splitlines` / Go `bufio.Scanner` shape.
// Splits on '\n', strips a trailing '\r' from each line (CRLF
// → LF), and drops the phantom empty line a final '\n' would
// otherwise produce.
func TestWASMStringLines(t *testing.T) {
	src := `
import "std/string";
function main(): i32 {
    // LF-only.
    var lf: string[] = "a\nb\nc".lines();
    if (lf.len() != 3) { return 1; }
    if (lf[0] != "a") { return 2; }
    if (lf[1] != "b") { return 3; }
    if (lf[2] != "c") { return 4; }

    // CRLF stripped.
    var crlf: string[] = "a\r\nb\r\nc".lines();
    if (crlf.len() != 3) { return 5; }
    if (crlf[0] != "a") { return 6; }
    if (crlf[1] != "b") { return 7; }
    if (crlf[2] != "c") { return 8; }

    // Trailing '\n' drops the phantom empty line.
    var trail: string[] = "a\nb\n".lines();
    if (trail.len() != 2) { return 9; }
    if (trail[1] != "b") { return 10; }

    // Single "\n" → one empty line.
    var solo: string[] = "\n".lines();
    if (solo.len() != 1) { return 11; }
    if (solo[0] != "") { return 12; }

    // Empty input → no lines at all.
    var empty: string[] = "".lines();
    if (empty.len() != 0) { return 13; }

    // No trailing newline: partial line still emits.
    var partial: string[] = "abc".lines();
    if (partial.len() != 1) { return 14; }
    if (partial[0] != "abc") { return 15; }

    // Mixed CRLF / LF.
    var mixed: string[] = "x\r\ny\nz".lines();
    if (mixed.len() != 3) { return 16; }
    if (mixed[0] != "x") { return 17; }
    if (mixed[1] != "y") { return 18; }
    if (mixed[2] != "z") { return 19; }

    // Bare '\r' is NOT a separator on its own — it stays in
    // the line. Only the '\r' immediately before '\n' (the
    // CRLF tail) is stripped.
    var bareCR: string[] = "a\rb\nc".lines();
    if (bareCR.len() != 2) { return 20; }
    if (bareCR[0] != "a\rb") { return 21; }
    if (bareCR[1] != "c") { return 22; }

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (string lines)", got)
	}
}

// String replace: substitutes every non-overlapping occurrence
// of the old pattern with the new value.
func TestWASMStringReplace(t *testing.T) {
	src := `
import "std/string";
function main(): i32 {
    if ("hello world".replace("world", "Earth") != "hello Earth") { return 1; }
    if ("aaa".replace("a", "bb") != "bbbbbb") { return 2; }
    if ("xyz".replace("z", "") != "xy") { return 3; }
    if ("hello".replace("xyz", "abc") != "hello") { return 4; }
    if ("hello".replace("", "x") != "hello") { return 5; }
    if ("aXbXc".replace("X", "::") != "a::b::c") { return 6; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (string replace)", got)
	}
}

// `n.to_string()` on each integer width. Method-syntax sugar
// over the existing decimal formatter; the wrappers go through
// a shared `__int_to_string_u64(magnitude, neg)` core that
// handles every signed / unsigned / 32 / 64 case.
func TestWASMNumberToString(t *testing.T) {
	src := `
import "std/i32";
import "std/i64";
import "std/u32";
import "std/u64";
function main(): i32 {
    var a: i32 = 42;
    if (a.to_string() != "42") { return 1; }
    var b: i32 = -123;
    if (b.to_string() != "-123") { return 2; }
    var z: i32 = 0;
    if (z.to_string() != "0") { return 3; }

    var u: u32 = 4294967295;
    if (u.to_string() != "4294967295") { return 4; }
    var u2: u32 = 1;
    if (u2.to_string() != "1") { return 5; }

    var i: i64 = (1 << 40) + 7;
    if (i.to_string() != "1099511627783") { return 6; }
    var ineg: i64 = 0 - ((1 << 40) + 7);
    if (ineg.to_string() != "-1099511627783") { return 7; }

    var big: u64 = (1 << 63);
    if (big.to_string() != "9223372036854775808") { return 8; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (number to_string)", got)
	}
}

// `s.bytes(): u8[]` and the inverse `string_from_bytes_unchecked(bs)`.
// Round-trip should preserve content and length.
func TestWASMStringBytes(t *testing.T) {
	src := `
import "std/string";
function main(): i32 {
    var s: string = "hello";
    var bs: u8[] = s.bytes();
    if (bs.len() != 5) { return 1; }
    if (bs[0] != 104) { return 2; }   // 'h'
    if (bs[1] != 101) { return 3; }   // 'e'
    if (bs[2] != 108) { return 4; }   // 'l'
    if (bs[3] != 108) { return 5; }   // 'l'
    if (bs[4] != 111) { return 6; }   // 'o'
    // Mutating the bytes shouldn't affect the source string.
    bs = bs.with(0, 72); // 'H'
    if (s != "hello") { return 7; }
    var s2: string = string_from_bytes_unchecked(bs);
    if (s2 != "Hello") { return 8; }
    if (s2.len() != 5) { return 9; }
    // Empty string round-trip.
    var es: string = "";
    var ebs: u8[] = es.bytes();
    if (ebs.len() != 0) { return 10; }
    if (string_from_bytes_unchecked(ebs) != "") { return 11; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (string <-> bytes round-trip)", got)
	}
}

// `s.as_bytes(): [u8]` — non-copying view companion to
// `s.bytes()`. The slice's data_ptr aliases the source
// string's payload, so mutations through the view *do*
// propagate back. Reads match the equivalent `bytes()`
// values byte-for-byte.
func TestWASMStringAsBytes(t *testing.T) {
	src := `
import "std/string";
function main(): i32 {
    var s: string = "hello";
    var view: [u8] = s.as_bytes();
    if (view.len() != 5) { return 1; }
    if (view[0] != 104) { return 2; }   // 'h'
    if (view[4] != 111) { return 3; }   // 'o'
    // Sub-slicing the view should still alias the source.
    var tail: [u8] = view[1:5];
    if (tail.len() != 4) { return 4; }
    if (tail[0] != 101) { return 5; }   // 'e'

    // Empty string -> zero-length view, no allocation drama.
    var es: string = "";
    var ev: [u8] = es.as_bytes();
    if (ev.len() != 0) { return 6; }

    // Read parity with the copying bytes() variant.
    var copied: u8[] = s.bytes();
    var i: i32 = 0;
    while (i < copied.len()) {
        if (copied[i] != view[i]) { return 7; }
        i = i + 1;
    }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (string as_bytes view)", got)
	}
}

// `s.is_empty()` is a length-zero shorthand; `s.repeat(n)`
// concatenates n copies of s. Covers the empty-string base
// case, n <= 0 → empty, n == 1 → identity, and a couple of
// larger n values.
func TestWASMStringIsEmptyAndRepeat(t *testing.T) {
	src := `
import "std/string";
function main(): i32 {
		// is_empty
		if (!"".is_empty()) { return 1; }
		if ("x".is_empty()) { return 2; }
		if ("hello".is_empty()) { return 3; }

		// repeat: n <= 0 -> empty
		if ("x".repeat(0) != "") { return 10; }
		if ("x".repeat(-3) != "") { return 11; }

		// repeat: n == 1 -> copy of s
		if ("hi".repeat(1) != "hi") { return 20; }

		// repeat: typical case
		if ("ab".repeat(3) != "ababab") { return 30; }
		if ("-".repeat(5) != "-----") { return 31; }

		// repeat on empty source -> empty regardless of n
		if ("".repeat(7) != "") { return 40; }

		// repeated len matches expectation
		if (("xy".repeat(4)).len() != 8) { return 50; }

		return 0;
	}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("WASM is_empty/repeat: exit = %d, want 0", got)
	}
}

// `s.parse_int(): Option[i32]` — decimal parser. Covers
// success cases (positive, negative, single digit, zero,
// boundary values 2^31-1 / -2^31), and failure cases
// (empty, lone "-", non-digit, embedded space, overflow,
// trailing garbage).
func TestWASMParseInt(t *testing.T) {
	src := `
import "std/string";
function main(): i32 {
		// Successes:
		match ("42".parse_int()) {
			Some(v) => { if (v != 42) { return 1; } },
			None => { return 2; }
		}
		match ("0".parse_int()) {
			Some(v) => { if (v != 0) { return 3; } },
			None => { return 4; }
		}
		match ("-7".parse_int()) {
			Some(v) => { if (v != -7) { return 5; } },
			None => { return 6; }
		}
		// 2^31 - 1 (max i32).
		match ("2147483647".parse_int()) {
			Some(v) => { if (v != 2147483647) { return 7; } },
			None => { return 8; }
		}
		// -2^31 (min i32). The literal -2147483648 isn't
		// directly expressible (the lexer reads the minus
		// separately, leaving the positive literal one past
		// i32 max), so verify via arithmetic:
		// v + 2147483647 must equal -1.
		match ("-2147483648".parse_int()) {
			Some(v) => { if (v + 2147483647 != -1) { return 9; } },
			None => { return 10; }
		}

		// Failures: each must come back None.
		match ("".parse_int()) { Some(_) => { return 20; }, None => {} }
		match ("-".parse_int()) { Some(_) => { return 21; }, None => {} }
		match ("abc".parse_int()) { Some(_) => { return 22; }, None => {} }
		match ("12 34".parse_int()) { Some(_) => { return 23; }, None => {} }
		match ("12a".parse_int()) { Some(_) => { return 24; }, None => {} }
		// Overflow (2^31 is one past i32 max).
		match ("2147483648".parse_int()) { Some(_) => { return 25; }, None => {} }
		// Underflow.
		match ("-2147483649".parse_int()) { Some(_) => { return 26; }, None => {} }
		// Way out of range.
		match ("99999999999999".parse_int()) { Some(_) => { return 27; }, None => {} }
		// Plus sign not accepted.
		match ("+1".parse_int()) { Some(_) => { return 28; }, None => {} }
		return 0;
	}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (parse_int)", got)
	}
}

// `s.parse_float(): Option[f64]` — accepts integer-only,
// integer.fraction, .fraction, and exponent forms. Parsing is
// correctly-rounded (round-half-to-even, bit-exact with strconv;
// #5566) — the bit-exact guard is TestParseFloatCorrectlyRounded;
// this test pins the grammar (accepted / rejected shapes).
func TestWASMParseFloat(t *testing.T) {
	src := `
import "std/string";
function near(actual: f64, expected: f64, eps: f64): boolean {
		var diff: f64 = actual - expected;
		if (diff < 0.0) { diff = 0.0 - diff; }
		var bound: f64 = expected;
		if (bound < 0.0) { bound = 0.0 - bound; }
		var rel: f64 = bound * eps;
		if (rel < eps) { rel = eps; }
		return diff < rel;
	}
	function main(): i32 {
		// Successes — integer / fraction / exponent shapes.
		match ("3.14".parse_float()) {
			Some(v) => { if (!near(v, 3.14, 0.001)) { return 1; } },
			None => { return 2; }
		}
		match ("0".parse_float()) {
			Some(v) => { if (v != 0.0) { return 3; } },
			None => { return 4; }
		}
		match ("-2.5".parse_float()) {
			Some(v) => { if (!near(v, -2.5, 0.001)) { return 5; } },
			None => { return 6; }
		}
		match (".5".parse_float()) {
			Some(v) => { if (!near(v, 0.5, 0.001)) { return 7; } },
			None => { return 8; }
		}
		match ("42.".parse_float()) {
			Some(v) => { if (!near(v, 42.0, 0.001)) { return 9; } },
			None => { return 10; }
		}
		match ("1e3".parse_float()) {
			Some(v) => { if (!near(v, 1000.0, 0.001)) { return 11; } },
			None => { return 12; }
		}
		match ("1.5e2".parse_float()) {
			Some(v) => { if (!near(v, 150.0, 0.001)) { return 13; } },
			None => { return 14; }
		}
		match ("2.5E-2".parse_float()) {
			Some(v) => { if (!near(v, 0.025, 0.0001)) { return 15; } },
			None => { return 16; }
		}
		match ("1e+5".parse_float()) {
			Some(v) => { if (!near(v, 100000.0, 0.001)) { return 17; } },
			None => { return 18; }
		}

		// Failures — bad shapes must come back None.
		match ("".parse_float()) { Some(_) => { return 30; }, None => {} }
		match ("-".parse_float()) { Some(_) => { return 31; }, None => {} }
		match (".".parse_float()) { Some(_) => { return 32; }, None => {} }
		match ("abc".parse_float()) { Some(_) => { return 33; }, None => {} }
		match ("1.2.3".parse_float()) { Some(_) => { return 34; }, None => {} }
		match ("1e".parse_float()) { Some(_) => { return 35; }, None => {} }
		match ("1ex".parse_float()) { Some(_) => { return 36; }, None => {} }
		match ("1e-".parse_float()) { Some(_) => { return 37; }, None => {} }
		match ("1.5x".parse_float()) { Some(_) => { return 38; }, None => {} }
		match ("+1".parse_float()) { Some(_) => { return 39; }, None => {} }

		return 0;
	}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (parse_float)", got)
	}
}

// Map[string, V] with single-i32 SSO inline-form keys (≤ 3
// bytes — "a", "ok", "GET", `int_to_string(42)`, etc.). The
// prelude's `__map_hash` reads each byte via `s[i]`, which
// lowers through `$__str_idx`; that helper now uses a one-shot
// scratch-slot spill on inline-form bases (PR #366) so the
// hash-loop allocates zero bytes per key. Verify the path
// round-trips set + get + has + delete + update + miss across
// a mix of inline-form ASCII keys, integer-formatted keys (the
// canonical cascade through `int_to_string` →
// `$string_from_bytes_unchecked` inline output), and a longer heap-form
// key for the bucket-collision dispatch path.
func TestWASMMapStringKeysInlineSSO(t *testing.T) {
	src := `
import "core/map";
import "core/int";
function main(): i32 {
    var m: Map[string, i32] = map_new(8);

    // Inline-form keys (≤ 3 bytes each).
    m = m.insert("a", 1);
    m = m.insert("ok", 2);
    m = m.insert("GET", 3);
    m = m.insert("404", 4);
    m = m.insert(int.int_to_string(42), 5);   // "42" inline via cascade

    // Heap-form key alongside, same map.
    m = m.insert("longer", 99);

    if (m.len() != 6) { return 1; }

    // Look up every key, including the literal-vs-int_to_string
    // mismatched-encoding case.
    if let Some(v) = m.get("a") {
        if (v != 1) { return 2; }
    } else { return 3; }
    if let Some(v) = m.get("ok") {
        if (v != 2) { return 4; }
    } else { return 5; }
    if let Some(v) = m.get("GET") {
        if (v != 3) { return 6; }
    } else { return 7; }
    if let Some(v) = m.get("404") {
        if (v != 4) { return 8; }
    } else { return 9; }
    if let Some(v) = m.get("42") {
        if (v != 5) { return 10; }
    } else { return 11; }
    if let Some(v) = m.get(int.int_to_string(42)) {
        if (v != 5) { return 12; }
    } else { return 13; }
    if let Some(v) = m.get("longer") {
        if (v != 99) { return 14; }
    } else { return 15; }

    // Misses on inline-form keys.
    if let Some(_) = m.get("z") { return 16; }
    if let Some(_) = m.get("xy") { return 17; }
    if let Some(_) = m.get("PUT") { return 18; }

    // has() over inline-form keys.
    if (!m.has("a")) { return 19; }
    if (!m.has("ok")) { return 20; }
    if (m.has("xy")) { return 21; }

    // Update + delete an inline key.
    m = m.insert("GET", 30);
    if let Some(v) = m.get("GET") {
        if (v != 30) { return 22; }
    } else { return 23; }
    if (!m.without("GET").1) { return 24; }
    if (m.has("GET")) { return 25; }
    if (m.without("GET").1) { return 26; }
    if (m.len() != 5) { return 27; }

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (Map[string, i32] with inline-form keys)", got)
	}
}

// String-keyed Map[string, i32]. Same API as Map[i32, i32];
// equality at the runtime layer dispatches to byte-level
// strcmp via the buffer's keyKind tag.
func TestWASMMapStringKeys(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m: Map[string, i32] = map_new(8);
    m = m.insert("hello", 1);
    m = m.insert("world", 2);
    m = m.insert("foo", 3);
    if (m.len() != 3) { return 1; }
    if (!m.has("hello")) { return 2; }
    if (!m.has("world")) { return 3; }
    if (m.has("missing")) { return 4; }
    if let Some(v) = m.get("hello") {
        if (v != 1) { return 5; }
    } else {
        return 6;
    }
    if let Some(v) = m.get("world") {
        if (v != 2) { return 7; }
    } else {
        return 8;
    }
    if let Some(_) = m.get("missing") {
        return 9;
    }
    // Update + len stays the same.
    m = m.insert("hello", 100);
    if (m.len() != 3) { return 10; }
    if let Some(v) = m.get("hello") {
        if (v != 100) { return 11; }
    } else {
        return 12;
    }
    // Delete.
    if (!m.without("foo").1) { return 13; }
    if (m.has("foo")) { return 14; }
    if (m.len() != 2) { return 15; }
    if (m.without("foo").1) { return 16; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (Map[string, i32])", got)
	}
}

// Map[string, string]. Both K and V are pointer-sized.
func TestWASMMapStringStringValues(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var headers: Map[string, string] = map_new(4);
    headers = headers.insert("content-type", "text/plain");
    headers = headers.insert("x-trace-id", "abc123");
    if (headers.len() != 2) { return 1; }
    if let Some(v) = headers.get("content-type") {
        if (v != "text/plain") { return 2; }
    } else {
        return 3;
    }
    if let Some(v) = headers.get("x-trace-id") {
        if (v != "abc123") { return 4; }
    } else {
        return 5;
    }
    if let Some(_) = headers.get("missing") {
        return 6;
    }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (Map[string, string])", got)
	}
}

// Stress-test the IndexMap probe + resize machinery: insert
// 100 entries (more than fits in any single resize step),
// look each one back up, delete every other entry, verify
// the surviving keys still look up correctly. Exercises
// linear probing on hash collisions (Wang-mix for i32 won't
// avoid them at scale), resize-on-load-factor, and
// tombstone handling on delete.
func TestWASMMapHashStress(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m: Map[i32, i32] = map_new(4);
    var i: i32 = 0;
    while (i < 100) {
        m = m.insert(i, i * 7 + 1);
        i = i + 1;
    }
    if (m.len() != 100) { return 1; }
    // Verify every insert is reachable.
    var j: i32 = 0;
    while (j < 100) {
        if let Some(v) = m.get(j) {
            if (v != j * 7 + 1) { return 100 + j; }
        } else {
            return 200 + j;
        }
        j = j + 1;
    }
    // Update every entry's value.
    var k: i32 = 0;
    while (k < 100) {
        m = m.insert(k, k * 11 + 2);
        k = k + 1;
    }
    if (m.len() != 100) { return 2; }
    // Delete every even key. After deletion, even keys must
    // miss and odd keys must still hit.
    var d: i32 = 0;
    while (d < 100) {
        if (!m.without(d).1) { return 300 + d; }
        d = d + 2;
    }
    if (m.len() != 50) { return 3; }
    var c: i32 = 0;
    while (c < 100) {
        if (c % 2 == 0) {
            if (m.has(c)) { return 400 + c; }
        } else {
            if let Some(v) = m.get(c) {
                if (v != c * 11 + 2) { return 500 + c; }
            } else {
                return 600 + c;
            }
        }
        c = c + 1;
    }
    // Re-insert deleted keys; tombstone reuse should mean
    // the load factor stays manageable.
    var r: i32 = 0;
    while (r < 100) {
        m = m.insert(r, r);
        r = r + 2;
    }
    if (m.len() != 100) { return 4; }
    // Every key should now be present.
    var f: i32 = 0;
    while (f < 100) {
        if let Some(v) = m.get(f) {
            if (f % 2 == 0) {
                if (v != f) { return 700 + f; }
            } else {
                if (v != f * 11 + 2) { return 800 + f; }
            }
        } else {
            return 900 + f;
        }
        f = f + 1;
    }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (Map hash-probe stress)", got)
	}
}

// String-keyed stress test using a deterministic key
// generator to exercise the FNV-1a path's collision
// behaviour at scale.
func TestWASMMapStringHashStress(t *testing.T) {
	src := `
import "core/map";
function digit(n: i32): string {
    if (n == 0) { return "0"; }
    if (n == 1) { return "1"; }
    if (n == 2) { return "2"; }
    if (n == 3) { return "3"; }
    if (n == 4) { return "4"; }
    if (n == 5) { return "5"; }
    if (n == 6) { return "6"; }
    if (n == 7) { return "7"; }
    if (n == 8) { return "8"; }
    return "9";
}
function key_of(i: i32): string {
    return "k_" + digit(i / 10) + digit(i % 10);
}
function main(): i32 {
    var m: Map[string, i32] = map_new(4);
    var i: i32 = 0;
    while (i < 80) {
        m = m.insert(key_of(i), i);
        i = i + 1;
    }
    if (m.len() != 80) { return 1; }
    var j: i32 = 0;
    while (j < 80) {
        if let Some(v) = m.get(key_of(j)) {
            if (v != j) { return 100 + j; }
        } else {
            return 200 + j;
        }
        j = j + 1;
    }
    if (m.has("k_99")) { return 2; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (Map string-key stress)", got)
	}
}

// Non-allocating cursor iteration via `m.iter()`. Walks the
// entries in insertion order using has_next / key / value /
// advance. The MapIter struct is allocated once per loop;
// each step is just a load + arithmetic.
func TestWASMMapIter(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m: Map[i32, i32] = Map { 10: 100, 20: 200, 30: 300, 40: 400 };
    var sum_keys: i32 = 0;
    var sum_vals: i32 = 0;
    var count: i32 = 0;
    var it: MapIter[i32, i32] = m.iter();
    while (it.has_next()) {
        sum_keys = sum_keys + it.key();
        sum_vals = sum_vals + it.value();
        count = count + 1;
        it.advance();
    }
    if (count != 4) { return 1; }
    if (sum_keys != 100) { return 2; }   // 10+20+30+40
    if (sum_vals != 1000) { return 3; }  // 100+200+300+400
    // Iteration over an empty map yields zero steps.
    var empty: Map[i32, i32] = map_new(4);
    var it2: MapIter[i32, i32] = empty.iter();
    if (it2.has_next()) { return 4; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (Map.iter cursor)", got)
	}
}

// `for (k, v) in m { ... }` desugars at parse time to the
// MapIter cursor loop — same control flow as the manual
// `it.has_next()` / `it.key()` / `it.value()` / `it.advance()`
// shape exercised above, but without the per-call-site
// boilerplate. Continue inside the body still advances the
// cursor (the synthesised For carries `it.advance()` on its
// Step slot, so `continue` jumps to the step before
// re-checking the cond).
func TestWASMForTupleInMap(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m: Map[i32, i32] = Map { 1: 10, 2: 20, 3: 30, 4: 40 };
    var sum_keys: i32 = 0;
    var sum_vals: i32 = 0;
    var count: i32 = 0;
    for (k, v) in m {
        if (k == 3) { continue; }
        sum_keys = sum_keys + k;
        sum_vals = sum_vals + v;
        count = count + 1;
    }
    if (count != 3) { return 1; }
    if (sum_keys != 7) { return 2; }   // 1+2+4
    if (sum_vals != 70) { return 3; }  // 10+20+40

    // String-keyed map iterates the same way; insertion order
    // is observable so we check the concatenation directly.
    var labels: Map[string, i32] = Map { "a": 1, "b": 2, "c": 3 };
    var keys_concat: string = "";
    var val_sum: i32 = 0;
    for (k2, v2) in labels {
        keys_concat = keys_concat + k2;
        val_sum = val_sum + v2;
    }
    if (keys_concat != "abc") { return 4; }
    if (val_sum != 6) { return 5; }

    // An empty map's foreach is a no-op.
    var empty: Map[i32, i32] = map_new(4);
    var ran: i32 = 0;
    for (ek, ev) in empty {
        ran = ran + 1;
        if (ek + ev > 0) { return 6; }
    }
    if (ran != 0) { return 7; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (for (k, v) in m desugar)", got)
	}
}

// Wide-V Map: Map[K, i64] / Map[K, f64] need a boxing dance
// since the shared wat helpers see all values as i32. The IR
// allocates an 8-byte cell on each set / get_or-fallback, stores
// the wide value there, and passes / returns the cell pointer
// transparently. get translates the helper's Option[i32] into
// a real wide-payload Option[V] inline; MapIter.value()
// dereferences the returned pointer.
func TestWASMMapValueI64(t *testing.T) {
	// 4294967296 = 2^32 — picks the entire upper word so a
	// truncating store would visibly clear the high bits.
	src := `
import "core/map";
function main(): i32 {
    var m: Map[i32, i64] = map_new(8);
    m = m.insert(1, 4294967296 as i64);
    m = m.insert(2, 8589934592 as i64);
    match (m.get(1)) {
        Some(v) => { if (v != (4294967296 as i64)) { return 1; } },
        None => { return 2; }
    }
    match (m.get(2)) {
        Some(v) => { if (v != (8589934592 as i64)) { return 3; } },
        None => { return 4; }
    }
    match (m.get(99)) {
        Some(v) => { return 5; },
        None => { }
    }
    var fallback: i64 = m.get_or(99, 7777777777 as i64);
    if (fallback != (7777777777 as i64)) { return 6; }
    var hit: i64 = m.get_or(1, 0 as i64);
    if (hit != (4294967296 as i64)) { return 7; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (Map[i32, i64] roundtrip)", got)
	}
}

// `Map[string, f64]` — same boxing path, exercised through
// f64.store / f64.load on the wide-V cell + the wide-payload
// Option[f64] return projection.
func TestWASMMapValueF64(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m: Map[string, f64] = map_new(8);
    m = m.insert("pi", 3.14 as f64);
    m = m.insert("e", 2.71 as f64);
    match (m.get("pi")) {
        Some(v) => {
            if (v < (3.13 as f64)) { return 1; }
            if (v > (3.15 as f64)) { return 2; }
        },
        None => { return 3; }
    }
    match (m.get("e")) {
        Some(v) => {
            if (v < (2.70 as f64)) { return 4; }
            if (v > (2.72 as f64)) { return 5; }
        },
        None => { return 6; }
    }
    match (m.get("missing")) {
        Some(v) => { return 7; },
        None => { }
    }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (Map[string, f64] roundtrip)", got)
	}
}

// Map literal with wide values — same boxing path the user-side
// `m.insert(k, v)` uses; the literal lowering pre-allocates a cell
// for each entry's value before calling the shared
// `__method_Map_set` helper.
func TestWASMMapLiteralWideValue(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m: Map[i32, i64] = Map { 1: 4294967296 as i64, 2: 8589934592 as i64 };
    match (m.get(2)) {
        Some(v) => { if (v == (8589934592 as i64)) { return 0; } },
        None => { return 1; }
    }
    return 2;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (Map literal wide value)", got)
	}
}

// MapIter.value() with wide V: the helper still returns a cell
// pointer (wat side stays i32), and the IR unboxes it on the
// way out.
func TestWASMMapIterValueWide(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m: Map[i32, i64] = Map { 1: 4294967296 as i64, 2: 8589934592 as i64 };
    var sum: i64 = 0 as i64;
    var it: MapIter[i32, i64] = m.iter();
    while (it.has_next()) {
        sum = sum + it.value();
        it.advance();
    }
    if (sum == (12884901888 as i64)) { return 0; }
    return 1;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (MapIter.value wide V)", got)
	}
}

// Cursor iteration over a string-keyed map. Verifies the
// type-system substitution returns string-typed key /
// i32-typed value at the call site.
func TestWASMMapIterStringKeys(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m: Map[string, i32] = Map { "a": 1, "b": 2, "c": 3 };
    var concat: string = "";
    var sum: i32 = 0;
    var it: MapIter[string, i32] = m.iter();
    while (it.has_next()) {
        concat = concat + it.key();
        sum = sum + it.value();
        it.advance();
    }
    if (concat != "abc") { return 1; }
    if (sum != 6) { return 2; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (Map.iter string keys)", got)
	}
}

// __memcpy / __memset bridge functions: wat-shim wrappers
// around wasm's bulk-memory `memory.copy` / `memory.fill`.
// They're the unlock for migrating helpers that build /
// scan growable byte buffers (the json buffer family + map
// runtime). This test exercises both via raw alloc + an
// `as_bytes` slice view, then pokes through the slice's
// data_ptr to verify the bytes landed.
//
// `as_bytes` returns a [u8] slice whose data_ptr aliases
// the string payload, so casting that pointer back to an
// i32 lets us drive __memcpy / __memset against arbitrary
// regions for testing. Production usage will always call
// __memcpy through dedicated buffer-management code.
func TestWASMBulkMemoryPrimitives(t *testing.T) {
	src := `function main(): i32 {
    // __memset: clear 8 bytes starting at the second slot
    // of a 16-byte buffer, leave the first 8 alone.
    var buf: u8[] = [
        65 as u8, 66 as u8, 67 as u8, 68 as u8,
        69 as u8, 70 as u8, 71 as u8, 72 as u8,
        73 as u8, 74 as u8, 75 as u8, 76 as u8,
        77 as u8, 78 as u8, 79 as u8, 80 as u8
    ];
    var bs: [u8] = buf[0:16];
    // The data pointer is a memory address — type it usize (pointer
    // width) so it flows into the usize-typed __memset / __memcpy params.
    // Under F2, user code must reach usize via an explicit cast rather
    // than the implicit i32<->usize hop. See docs/ADVERSARIAL-REVIEW-2026-06.md.
    var base: usize = (bs as usize);
    __memset(base + 8, 0, 8);
    if (buf[0]  != 65)  { return 1; }
    if (buf[7]  != 72)  { return 2; }
    if (buf[8]  != 0)   { return 3; }
    if (buf[15] != 0)   { return 4; }
    // __memcpy: copy first 4 bytes onto the now-zeroed
    // back half so the buffer reads ABCDEFGHABCD0000.
    __memcpy(base + 8, base, 4);
    if (buf[8]  != 65)  { return 5; }
    if (buf[9]  != 66)  { return 6; }
    if (buf[10] != 67)  { return 7; }
    if (buf[11] != 68)  { return 8; }
    if (buf[12] != 0)   { return 9; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("__memcpy/__memset: exit = %d, want 0", got)
	}
}

// base64 round-trip: encode arbitrary bytes, decode back,
// verify equality. Covers the full alphabet (A-Z, a-z, 0-9,
// +, /), no-padding (3-byte aligned), 1-byte-padding (length
// % 3 == 1), and 2-byte-padding (length % 3 == 2) inputs.
func TestWASMBase64(t *testing.T) {
	src := `
import "core/int";
import "std/base64";
import "std/string";
function main(): i32 {
    if (base64.base64_encode("".bytes()) != "") { return 1; }
    if (base64.base64_encode("f".bytes()) != "Zg==") { return 2; }
    if (base64.base64_encode("fo".bytes()) != "Zm8=") { return 3; }
    if (base64.base64_encode("foo".bytes()) != "Zm9v") { return 4; }
    if (base64.base64_encode("foob".bytes()) != "Zm9vYg==") { return 5; }
    if (base64.base64_encode("fooba".bytes()) != "Zm9vYmE=") { return 6; }
    if (base64.base64_encode("foobar".bytes()) != "Zm9vYmFy") { return 7; }
    if (base64.base64_encode("hello world".bytes()) != "aGVsbG8gd29ybGQ=") { return 8; }
    if (string_from_bytes_unchecked(base64.base64_decode("")) != "") { return 9; }
    if (string_from_bytes_unchecked(base64.base64_decode("Zg==")) != "f") { return 10; }
    if (string_from_bytes_unchecked(base64.base64_decode("Zm8=")) != "fo") { return 11; }
    if (string_from_bytes_unchecked(base64.base64_decode("Zm9v")) != "foo") { return 12; }
    if (string_from_bytes_unchecked(base64.base64_decode("Zm9vYg==")) != "foob") { return 13; }
    if (string_from_bytes_unchecked(base64.base64_decode("Zm9vYmE=")) != "fooba") { return 14; }
    if (string_from_bytes_unchecked(base64.base64_decode("Zm9vYmFy")) != "foobar") { return 15; }
    if (string_from_bytes_unchecked(base64.base64_decode("aGVsbG8gd29ybGQ=")) != "hello world") { return 16; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (base64)", got)
	}
}

// `defer EXPR;` schedules the expression to run when the
// enclosing function exits. Multiple defers run in LIFO
// order; conditionally-registered defers (inside an if-branch
// that didn't fire) are no-ops via per-defer "active" flags.
// Side-effect observation goes through a Map passed by ref.
func TestWASMDeferBasic(t *testing.T) {
	src := `
import "core/map";
function inner(trace: Map[string, i32]): i32 {
    trace = trace.insert("body-start", 1);
    defer trace.insert("first-defer", 10);
    defer trace.insert("second-defer", 20);
    trace = trace.insert("body-end", 2);
    return 42;
}
function main(): i32 {
    var trace: Map[string, i32] = map_new(8);
    var r: i32 = inner(trace);
    if (r != 42) { return 1; }
    if (trace.len() != 4) { return 2; }
    if (trace.get_or("body-start", 0) != 1) { return 3; }
    if (trace.get_or("body-end", 0) != 2) { return 4; }
    if (trace.get_or("first-defer", 0) != 10) { return 5; }
    if (trace.get_or("second-defer", 0) != 20) { return 6; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (defer basic)", got)
	}
}

// Conditionally-registered defer: a defer inside an
// if-branch that doesn't run shouldn't fire at function exit.
func TestWASMDeferConditional(t *testing.T) {
	src := `
import "core/map";
function run(fired: Map[i32, i32], taken: boolean): i32 {
    if (taken) {
        defer fired.insert(1, 100);
    }
    defer fired.insert(2, 200);
    return 0;
}
function main(): i32 {
    var fired: Map[i32, i32] = map_new(4);
    run(fired, false);
    if (fired.has(1)) { return 1; }
    if (!fired.has(2)) { return 2; }
    fired = fired.cleared();
    run(fired, true);
    if (!fired.has(1)) { return 3; }
    if (!fired.has(2)) { return 4; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (defer conditional)", got)
	}
}

// Defer fires before each return, even early returns.
func TestWASMDeferEarlyReturn(t *testing.T) {
	src := `
import "core/map";
function early(counts: Map[string, i32], branch: i32): i32 {
    defer counts.insert("count", counts.get_or("count", 0) + 1);
    if (branch == 1) {
        return 10;
    }
    if (branch == 2) {
        return 20;
    }
    return 30;
}
function main(): i32 {
    var counts: Map[string, i32] = map_new(4);
    if (early(counts, 1) != 10) { return 1; }
    if (early(counts, 2) != 20) { return 2; }
    if (early(counts, 0) != 30) { return 3; }
    if (counts.get_or("count", 0) != 3) { return 4; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (defer early-return)", got)
	}
}

// `m.get_or(k, default)` returns the existing value or the
// supplied default. Saves the `if let Some(v) = m.get(k) {
// v } else { d }` ceremony for the common-case lookup.
func TestWASMMapGetOr(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var counts: Map[string, i32] = Map { "apple": 3, "banana": 5 };
    if (counts.get_or("apple", 0) != 3) { return 1; }
    if (counts.get_or("banana", 0) != 5) { return 2; }
    if (counts.get_or("missing", -1) != -1) { return 3; }
    if (counts.get_or("missing", 100) != 100) { return 4; }

    var ints: Map[i32, i32] = Map { 1: 10, 2: 20 };
    if (ints.get_or(1, 999) != 10) { return 5; }
    if (ints.get_or(99, 999) != 999) { return 6; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (Map.get_or)", got)
	}
}

// `m.cleared()` resets the map back to empty without freeing
// the kv buffer. Subsequent inserts reuse the existing
// allocation and re-grow if needed.
func TestWASMMapClear(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m: Map[string, i32] = Map { "a": 1, "b": 2, "c": 3 };
    if (m.len() != 3) { return 1; }
    m = m.cleared();
    if (m.len() != 0) { return 2; }
    if (m.has("a")) { return 3; }
    if let Some(_) = m.get("b") { return 4; }
    // Re-insert after clear works (bucket array was reset
    // to all-empty so the linear probe terminates).
    m = m.insert("hello", 42);
    m = m.insert("world", 99);
    if (m.len() != 2) { return 5; }
    if let Some(v) = m.get("hello") {
        if (v != 42) { return 6; }
    } else {
        return 7;
    }
    if let Some(v) = m.get("world") {
        if (v != 99) { return 8; }
    } else {
        return 9;
    }
    if (m.has("a")) { return 10; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (Map.clear)", got)
	}
}

// String-keyed Map literal — KeyType is inferred from the
// first key, so the IR's map_new call gets keyKind=1.
func TestWASMMapStringKeyLiteral(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m: Map[string, i32] = Map { "a": 1, "b": 2, "c": 3 };
    if (m.len() != 3) { return 1; }
    if let Some(v) = m.get("b") {
        if (v != 2) { return 2; }
    } else {
        return 3;
    }
    if let Some(_) = m.get("d") {
        return 4;
    }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (string-keyed Map literal)", got)
	}
}

// f64 arithmetic round-trips through addition + comparison. Same
// shape as the i64 test but for double-precision floats.
// Verifies the polymorphic float literal `0.5` settles to f64
// from the var annotation, the arithmetic compiles to `f64.add`,
// and the comparison to `f64.eq`.
func TestWASMF64Arithmetic(t *testing.T) {
	src := `function add64f(x: f64, y: f64): f64 { return x + y; }
function main(): i32 {
    var z: f64 = add64f(1.5, 2.5);
    if (z == 4.0) { return 0; }
    return 1;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (f64 arith mismatch)", got)
	}
}

// f32 ↔ f64 cast support. `as f64` widens (f64.promote_f32);
// `as f32` narrows (f32.demote_f64). Verifies both directions
// round-trip a value through the cast.
func TestWASMFloatCasts(t *testing.T) {
	src := `function main(): i32 {
    var a: f32 = 1.25;
    var b: f64 = a as f64;
    var c: f32 = b as f32;
    if (c == a) { return 0; }
    return 1;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (f32/f64 cast mismatch)", got)
	}
}

// Mixing f32 and f64 without an explicit cast is a checker
// error — same rule as i32/i64.
func TestF32F64MixRejected(t *testing.T) {
	src := `function bad(): f64 { var x: f32 = 1.0; var y: f64 = 2.0; return x + y; }`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err == nil {
		t.Fatalf("expected checker error for mixed f32/f64 add, got none")
	}
}

// u32 unsigned arithmetic. Verifies wasm picks `_u` variants of
// div/rem/shr/compare. 0xFFFFFFFF interpreted as u32 is
// 4_294_967_295, so dividing by 2 unsigned gives 0x7FFFFFFF
// (2_147_483_647); the same bit-pattern read as i32 would be
// -1 / 2 = 0. Comparing 0xFFFFFFFF unsigned > 1 is true; the
// signed reading would give -1 > 1 = false.
func TestWASMU32Unsigned(t *testing.T) {
	src := `function main(): i32 {
    var x: u32 = 4294967295;
    var half: u32 = x / 2;
    if (half != 2147483647) { return 1; }
    if (!(x > 1)) { return 2; }
    if ((x >> 1) != 2147483647) { return 3; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (u32 unsigned arithmetic)", got)
	}
}

// u64 unsigned arithmetic. Builds a value with the high bit set
// via `1 << 63` (the literals settle to u64 from `var x: u64`)
// and checks div / compare under unsigned semantics: dividing
// the high-bit-set value by 2 should equal `1 << 62`, and x > 1
// should be true (under signed it would be false because the
// top bit reads as a sign bit).
func TestWASMU64Unsigned(t *testing.T) {
	src := `function main(): i32 {
    var x: u64 = 1 << 63;
    var half: u64 = x / 2;
    if (half != 1 << 62) { return 1; }
    if (!(x > 1)) { return 2; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (u64 unsigned arithmetic)", got)
	}
}

// Mixing u32 with i32 without a cast is a checker error — same
// rule as i32/i64. Signedness is part of the integer type, so
// the implicit-widening rejection applies here too.
func TestU32SignedMixRejected(t *testing.T) {
	src := `function bad(): i32 { var x: i32 = 1; var y: u32 = 2 as u32; return x + y; }`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err == nil {
		t.Fatalf("expected checker error for mixed i32/u32 add, got none")
	}
}

// Tuple multi-return + numeric field access. divmod returns a
// 2-tuple; main destructures it via `.0` / `.1` (full pattern
// destructuring `let (q, r) = ...` lands in a follow-up).
// Exercises TupleLit construction (heap-alloc + per-element
// store), TupleType return slot, and numeric-index FieldAccess.
func TestWASMTupleMultiReturn(t *testing.T) {
	src := `function divmod(a: i32, b: i32): (i32, i32) {
    return (a / b, a - (a / b) * b);
}
function main(): i32 {
    var p = divmod(17, 5);
    if (p.0 == 3 && p.1 == 2) { return 0; }
    return 1;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (divmod tuple mismatch)", got)
	}
}

// Pipe operator `|>` — data-first call sugar. `x |> f(a, b)`
// desugars at parse time to `f(x, a, b)`. Chains left-associate
// so `x |> f |> g` is `g(f(x))`. Below uses single-arg piping
// (`5 |> double` → `double(5)`) plus prepending into an existing
// arg list (`x |> add(3)` → `add(x, 3)`).
func TestWASMPipeOperator(t *testing.T) {
	src := `function double(n: i32): i32 { return n * 2; }
function add(a: i32, b: i32): i32 { return a + b; }
function main(): i32 {
    var x = 5 |> double;
    var y = x |> add(3);
    var z = y |> double |> add(4);
    if (x == 10 && y == 13 && z == 30) { return 0; }
    return 1;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (pipe chain mismatch)", got)
	}
}

// Generic functions: `function id[T](x: T): T { return x; }`.
// The checker infers T from the argument type; the
// monomorphisation pass clones the decl into one copy per
// concrete instantiation. This test exercises the full
// pipeline: parse → check (with inference) → monomorph
// (rewriting `id` to `id__i32`) → IR / codegen.
func TestWASMGenericFunctionInfersFromArg(t *testing.T) {
	src := `function id[T](x: T): T { return x; }
function main(): i32 {
    var a = id(42);
    var b = id(7);
    if (a == 42 && b == 7) { return 0; }
    return 1;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (id() generic mismatch)", got)
	}
}

// Same generic, two distinct instantiations — `first[i32]` and
// `first[string]`. Verifies the monomorphisation pass produces
// independent clones (not erased dispatch) and the call sites
// pick the right one.
func TestWASMGenericFunctionMultipleInstantiations(t *testing.T) {
	src := `function first[T](xs: T[]): T { return xs[0]; }
function main(): i32 {
    var ints: i32[] = [10, 20, 30];
    var strs: string[] = ["hello", "world"];
    var n = first(ints);
    var s = first(strs);
    print(s);
    return n;
}`
	out := runWasmCapturingStdout(t, src)
	if out != "hello" {
		t.Errorf("output = %q, want \"hello\"", out)
	}
}

// Generic struct: `struct Pair[A, B] { first: A, second: B }`.
// Field types use the type parameters; the checker stamps
// inferred type-args on the struct literal, the monomorpher
// clones a Pair__i32__string decl per concrete instantiation,
// and field access through Pair[i32, string].first returns i32.
func TestWASMGenericStructInfersAndMonomorphises(t *testing.T) {
	src := `struct Pair[A, B] { first: A, second: B }
function main(): i32 {
    var p = Pair { first: 42, second: "hello" };
    print(p.second);
    return p.first;
}`
	out := runWasmCapturingStdout(t, src)
	if out != "hello" {
		t.Errorf("output = %q, want \"hello\"", out)
	}
}

// Generic function over a generic struct — exercises the
// monomorpher's per-clone substitution + the post-clone type-
// slot mangling pass: unbox[T](b: Box[T]): T gets cloned at
// T=i32 and T=string with param types Box[i32] / Box[string]
// respectively, both then mangled to Box__i32 / Box__string.
func TestWASMGenericFunctionOverGenericStruct(t *testing.T) {
	src := `struct Box[T] { val: T }
function unbox[T](b: Box[T]): T { return b.val; }
function main(): i32 {
    var i = Box { val: 7 };
    var s = Box { val: "world" };
    print(unbox(s));
    return unbox(i);
}`
	out := runWasmCapturingStdout(t, src)
	if out != "world" {
		t.Errorf("output = %q, want \"world\"", out)
	}
}

// `use IDENT : TYPE <- EXPR;` desugars at parse time into a
// synthesised local callback function whose body is the rest of
// the enclosing block. The desugar is Gleam-style — it
// generalises Result-chaining without a typeclass / monad
// system, since the callback signature is whatever the
// receiving function expects.
// `use` without an explicit type annotation: the checker peeks
// at the receiving function's callback parameter type and fills
// in the binding's type automatically. Same shape as the
// annotated `use IDENT: T <- ...` test, but with the `: T`
// dropped on both lines.
func TestWASMUseInferredType(t *testing.T) {
	src := `function tryThing(callback: (i32) => Option[i32]): Option[i32] {
    return callback(42);
}
function compute(): Option[i32] {
    use n <- tryThing();
    return Some(n + 1);
}
function main(): i32 {
    if let Some(v) = compute() {
        return v;
    }
    return -1;
}`
	if got := runWasm(t, src); got != 43 {
		t.Errorf("got %d, want 43 (use inference)", got)
	}
}

// `use n <- generic_fn(args)` infers n's type by solving the
// callee's type parameters from the actual args. Without this
// the binding stayed as the unresolved ParamType `T`, and any
// subsequent use of `n` (e.g. `n + 1`) failed type-checking
// with "operator + requires an integer type, got T".
func TestWASMUseInferredTypeFromGenericCallee(t *testing.T) {
	src := `function each[T](items: T[], cb: (T) => i32): i32 {
    return cb(items[0]);
}
function main(): i32 {
    var nums: i32[] = [10, 20, 30];
    use n <- each(nums);
    return n + 1;
}`
	if got := runWasm(t, src); got != 11 {
		t.Errorf("got %d, want 11 (generic-callee inference)", got)
	}
}

// Inference through an Option[T] arg position: the callback's
// first param is T, and T is bound by the actual Option[i32]
// argument shape.
func TestWASMUseInferredTypeThroughEnumPayload(t *testing.T) {
	src := `function try_opt[T](opt: Option[T], cb: (T) => i32): i32 {
    if let Some(v) = opt { return cb(v); }
    return 0;
}
function main(): i32 {
    var x: Option[i32] = Some(7);
    use n <- try_opt(x);
    return n + 1;
}`
	if got := runWasm(t, src); got != 8 {
		t.Errorf("got %d, want 8 (Option[T] inference)", got)
	}
}

func TestWASMUseSyntax(t *testing.T) {
	src := `function tryThing(callback: (i32) => Option[i32]): Option[i32] {
    return callback(42);
}
function compute(): Option[i32] {
    use n: i32 <- tryThing();
    return Some(n + 1);
}
function main(): i32 {
    if let Some(v) = compute() {
        return v;
    }
    return -1;
}`
	if got := runWasm(t, src); got != 43 {
		t.Errorf("got %d, want 43 (use syntax single-callback)", got)
	}
}

// Chained `use` — each one consumes the rest of the block as
// its callback's body, producing a series of nested closures.
// Inner closure captures the outer closure's binding (`a` in
// the body of __use_1 references __use_2's param).
func TestWASMUseChained(t *testing.T) {
	src := `function tryThing(cb: (i32) => Option[i32]): Option[i32] { return cb(10); }
function tryOther(cb: (i32) => Option[i32]): Option[i32] { return cb(20); }
function compute(): Option[i32] {
    use a: i32 <- tryThing();
    use b: i32 <- tryOther();
    return Some(a + b);
}
function main(): i32 {
    if let Some(v) = compute() {
        return v;
    }
    return -1;
}`
	if got := runWasm(t, src); got != 30 {
		t.Errorf("got %d, want 30 (chained use)", got)
	}
}

// `let Variant(b) = expr else { divergent };` —
// pattern-binding declaration with mandatory-divergent else.
// Bindings flow into the enclosing scope, so subsequent
// statements see them as if they were declared via `var`.
// The else branch must terminate the surrounding control
// flow; the checker enforces this at compile time.
func TestWASMLetElseHappyPath(t *testing.T) {
	src := `function getOpt(): Option[i32] { return Some(42); }
function main(): i32 {
    let Some(n) = getOpt() else { return 1; };
    return n;
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42 (let-else Some path)", got)
	}
}

// Mismatch path: source returns None, else block runs and
// returns 1. Verifies the bindings stay unread on the diverging
// path so the lowering doesn't trip on uninitialised slots.
func TestWASMLetElseMismatchDiverges(t *testing.T) {
	src := `function getOpt(): Option[i32] { return None; }
function main(): i32 {
    let Some(n) = getOpt() else { return 1; };
    return n;
}`
	if got := runWasm(t, src); got != 1 {
		t.Errorf("got %d, want 1 (let-else None should take else)", got)
	}
}

// `if let Variant(b) = expr { … }` — pattern-binding without
// the match ceremony. Common need in HTTP handlers / Result
// chains where you want to unwrap one variant and proceed flat
// instead of nesting in `match`.
func TestWASMIfLetMatch(t *testing.T) {
	src := `function main(): i32 {
    var o: Option[i32] = Some(42);
    if let Some(n) = o {
        return n;
    }
    return -1;
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42 (if-let Some(42) bind)", got)
	}
}

// `if let` with a non-matching variant + an `else` branch — the
// else runs and the bindings from the pattern aren't in scope.
func TestWASMIfLetMismatchTakesElse(t *testing.T) {
	src := `function main(): i32 {
    var o: Option[i32] = None;
    if let Some(n) = o {
        return 1;
    } else {
        return 2;
    }
    return -1;
}`
	if got := runWasm(t, src); got != 2 {
		t.Errorf("got %d, want 2 (if-let None should take else)", got)
	}
}

// Match guards: `<pattern> when <bool> => <body>`. The guard
// runs with the pattern's bindings in scope; if it evaluates to
// false the arm is skipped and the match falls through to the
// next arm. Exhaustiveness is conservative — a guarded arm
// doesn't count as covering the variant, so a fallback arm (or
// `_`) is required.
func TestWASMMatchGuards(t *testing.T) {
	src := `function classify(o: Option[i32]): i32 {
    match (o) {
        Some(n) when n > 0 => { return 1; },
        Some(n) when n < 0 => { return -1; },
        Some(n) => { return 0; },
        None => { return -2; },
    }
    return -99;
}
function main(): i32 {
    if (classify(Some(5)) != 1) { return 1; }
    if (classify(Some(0 - 7)) != -1) { return 2; }
    if (classify(Some(0)) != 0) { return 3; }
    if (classify(None) != -2) { return 4; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (match guard mismatch at step %d)", got, got)
	}
}

// Re-binding the SAME name across two match arms (a guarded `Rect(p)`
// followed by an unguarded `Rect(p)`, each reading `p`). Regression for
// issue #2644, which reported the wasm backend reading the wrong value
// for the guarded arm's payload bind when a later arm re-binds the same
// name — i.e. the two `p` bindings colliding on one slot. Each arm must
// get its own binding slot so the guard reads the right `p.x` and the
// fall-through arm reads its own `p`. Every sub-case is also a
// cross-backend contract: x86-64 / interp produce the same values (the
// issue's core complaint was an x86-64-vs-wasm divergence), guarded here
// on the result-printer harness.
func TestWASMMatchRebindSameName(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		// The issue's exact repro: guard true → first arm → 4*5=20, so
		// 20*10 + area(Dot)=1 == 201.
		{"issue-repro", `
struct P { x: i32, y: i32 }
enum Shape { Dot, Rect(P) }
function area(s: Shape): i32 {
    match (s) {
        Rect(p) when p.x > 0 => { return p.x * p.y; },
        Rect(p) => { return 0; },
        Dot => { return 1; }
    }
    return 0;
}
function main(): i32 { return area(Rect(P { x: 4, y: 5 })) * 10 + area(Dot); }`, 201},
		// Guard false → falls through to the unguarded arm, which reads
		// its OWN `p` (p.y == 5) → 105.
		// Three arms binding `p`, two guarded: the middle guard (p.x > 5)
		// wins for x=7 → p.x*2 == 14.
		// i32-payload re-bind across a guarded + unguarded arm.
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runWasm(t, tc.src); got != tc.want {
				t.Errorf("wasm %s: got %d, want %d (issue #2644)", tc.name, got, tc.want)
			}
		})
	}
}

// over the parent's storage. Verifies len + indexing on a slice
// over an owned array AND sub-slicing (slice-of-slice).
// Exercises the new $__slice_make + $__slice_idx runtime helpers,
// plus the `len(slice)` codegen path that loads length from
// `slice + 4` instead of `arr - 4`.
func TestWASMSliceViews(t *testing.T) {
	src := `function main(): i32 {
    var arr: i32[] = [10, 20, 30, 40, 50];
    var s: [i32] = arr[1:4];          // [20, 30, 40]
    if (s.len() != 3) { return 1; }
    if (s[0] != 20) { return 2; }
    if (s[1] != 30) { return 3; }
    if (s[2] != 40) { return 4; }
    var t = s[1:3];                   // [30, 40]
    if (t.len() != 2) { return 5; }
    if (t[0] != 30 || t[1] != 40) { return 6; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (slice view mismatch at step %d)", got, got)
	}
}

// `arr[:b]` and `arr[a:]` — half-bounded slice forms. Low
// defaults to 0; high defaults to len(source).
func TestWASMSliceHalfBoundedForms(t *testing.T) {
	src := `function main(): i32 {
    var arr: i32[] = [1, 2, 3, 4, 5];
    var head: [i32] = arr[:3];
    if (head.len() != 3 || head[0] != 1 || head[2] != 3) { return 1; }
    var tail: [i32] = arr[2:];
    if (tail.len() != 3 || tail[0] != 3 || tail[2] != 5) { return 2; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (half-bounded slice mismatch at step %d)", got, got)
	}
}

// Heterogeneous tuples — i32 and string in the same value.
// Exercises mixing pointer and integer fields in the heap layout.
func TestWASMTupleHeterogeneous(t *testing.T) {
	src := `function pair(): (i32, string) { return (42, "hello"); }
function main(): i32 {
    var p = pair();
    if (p.0 == 42) { return 0; }
    return 1;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (tuple heterogeneous mismatch)", got)
	}
}

// Generic struct methods with their own type parameters:
// `function [T] (b: Box[T]) unwrap(): T`. The type params come
// BEFORE the receiver so the receiver's type (`Box[T]`) can
// reference them — by the time the parser reads `Box[T]`, T is
// already known as a type parameter and resolveType rewrites it
// as ParamType. The existing method machinery (mangle to
// `__method_Box_unwrap`) + the generic-function monomorphisation
// pipeline together cover the lowering: each call site like
// `b.unwrap()` where b: Box[i32] instantiates the method with
// T = i32, producing a `__method_Box_unwrap__i32` clone.
func TestWASMGenericStructMethod(t *testing.T) {
	src := `struct Box[T] { value: T }
pub function [T] (b: Box[T]) unwrap(): T { return b.value; }
function main(): i32 {
    var b: Box[i32] = Box { value: 42 };
    return b.unwrap();
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

func TestWASMGenericStructMethodWide(t *testing.T) {
	src := `struct Box[T] { value: T }
pub function [T] (b: Box[T]) unwrap(): T { return b.value; }
function main(): i32 {
    var b: Box[i64] = Box { value: 1000000000000i64 };
    var v: i64 = b.unwrap();
    if (v != 1000000000000i64) { return 1; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}

func TestWASMGenericStructMethodString(t *testing.T) {
	src := `struct Box[T] { value: T }
pub function [T] (b: Box[T]) unwrap(): T { return b.value; }
function main(): i32 {
    var b: Box[string] = Box { value: "hello" };
    return (b.unwrap()).len();
}`
	if got := runWasm(t, src); got != 5 {
		t.Errorf("got %d, want 5", got)
	}
}

// Multiple type params + multiple methods sharing receiver shape.
func TestWASMGenericStructMethodMultipleTypeParams(t *testing.T) {
	src := `struct Pair[A, B] { a: A, b: B }
pub function [A, B] (p: Pair[A, B]) first(): A { return p.a; }
pub function [A, B] (p: Pair[A, B]) second(): B { return p.b; }
function main(): i32 {
    var p: Pair[i32, string] = Pair { a: 42, b: "hello" };
    return p.first() + (p.second()).len();
}`
	if got := runWasm(t, src); got != 47 {
		t.Errorf("got %d, want 47", got)
	}
}

// Generic method that builds a Box[T] in its body. The monomorph
// pass needs to defer the StructLit-TypeName mangling for
// ParamType-bearing TypeArgs (pre-clone), then re-mangle after
// substituteBlock has replaced T with the concrete instantiation
// arg. Without that two-phase walk, the body's `Box { value: v }`
// kept its placeholder `Box__T` TypeName after cloning and the
// re-check failed with `return type mismatch: function returns
// Box__i32 but expression is Box__T`.
func TestWASMGenericMethodReturnsBoxOfT(t *testing.T) {
	src := `struct Box[T] { value: T }
pub function [T] (b: Box[T]) replace(v: T): Box[T] {
    return Box { value: v };
}
function main(): i32 {
    var b: Box[i32] = Box { value: 1 };
    var c = b.replace(42);
    return c.value;
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

// Mutual recursion of sibling local FuncDecls: each function
// calls the other, both declared in the same block. Detection
// runs as a Tarjan SCC walk in the checker pre-pass; SCC
// members pre-bind their names in scope (so forward refs
// type-check) and skip the capture path. closureconv emits a
// null-env direct-call rewrite for each sibling call inside an
// SCC — both hoisted closures lower to zero-capture closures
// and the body's sibling calls bypass the env entirely,
// avoiding the cyclic-env-init problem.
func TestWASMMutualRecursionLocalFns(t *testing.T) {
	src := `function main(): i32 {
    function isEven(n: i32): boolean {
        if (n == 0) { return true; }
        return isOdd(n - 1);
    }
    function isOdd(n: i32): boolean {
        if (n == 0) { return false; }
        return isEven(n - 1);
    }
    if (isEven(10) && !isEven(11) && isOdd(7) && !isOdd(8)) { return 0; }
    return 1;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (mutual recursion correctness)", got)
	}
}

// Three-way mutual recursion: a → b → c → a. Tarjan picks all
// three as a single SCC of size 3 so the same null-env rewrite
// applies to every call edge.
func TestWASMMutualRecursionThreeWay(t *testing.T) {
	src := `function main(): i32 {
    function a(n: i32): i32 {
        if (n == 0) { return 0; }
        return b(n - 1) + 1;
    }
    function b(n: i32): i32 {
        if (n == 0) { return 0; }
        return c(n - 1) + 2;
    }
    function c(n: i32): i32 {
        if (n == 0) { return 0; }
        return a(n - 1) + 3;
    }
    return a(6);
}`
	// a(6) → 1+b(5) → 1+2+c(4) → 1+2+3+a(3) → 1+2+3+1+b(2) → 1+2+3+1+2+c(1) → 1+2+3+1+2+3+a(0) = 12
	if got := runWasm(t, src); got != 12 {
		t.Errorf("got %d, want 12", got)
	}
}

// `(function (x) { … })(arg)` — calling a lambda immediately at
// the definition site. The Lambda lowers via closureconv to a
// MakeClosure expression; the IR's `call()` dispatch now
// recognises *ast.Lambda AND *ast.MakeClosure callees (the
// closureconv pass rewrites Lambda → MakeClosure before the IR
// builder runs, so the MakeClosure branch is the one that fires
// in practice; the Lambda branch covers paths that bypass
// closureconv). Same OpCallIndirect shape function-typed locals
// already use.
func TestWASMImmediateLambdaCall(t *testing.T) {
	src := `function main(): i32 {
    var n: i32 = (function (x: i32): i32 { return x * 2; })(21);
    return n;
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42 (immediate lambda call)", got)
	}
}

// Lambda inside a generic function — the lambda's params /
// return type may reference the enclosing function's type
// parameters. Pre-fix the checker's resolveTypesInBlock didn't
// descend into expression-position Lambda nodes so the lambda's
// `T` stayed as `StructType{T}` and the outer's declared
// `(T) => T` (parser-built as `*FuncType[ParamType{T}]`) didn't
// match. The Lambda case in checkExpr now resolves the lambda's
// types against `c.current.TypeParams`; monomorph also walks
// into Lambda for substitution and cloning so each
// instantiation gets a fresh body with the concrete type.
func TestWASMLambdaInGenericFn(t *testing.T) {
	src := `function makeId[T](): (T) => T {
    return function (x: T): T { return x; };
}
function main(): i32 {
    var f = makeId[i32]();
    return f(42);
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

// Generic call-site type arguments: `f[T](args)`. Most generic
// calls already infer T from argument types, but the explicit
// form helps when inference would otherwise fail — e.g.
// nullary generics or unconstrained type parameters. The
// disambiguator between this and `arr[i](x)` requires the
// bracket to lead with a type keyword (i32, u32, string, …),
// keeping the indexing path untouched.
func TestWASMGenericCallTypeArg(t *testing.T) {
	src := `function pickFirst[T](a: T, b: T): T { return a; }
function main(): i32 {
    return pickFirst[i32](10, 20);
}`
	if got := runWasm(t, src); got != 10 {
		t.Errorf("got %d, want 10", got)
	}
}

func TestWASMGenericTwoTypeArgs(t *testing.T) {
	src := `function pair[A, B](a: A, b: B): (A, B) { return (a, b); }
function main(): i32 {
    var p = pair[i32, string](42, "hi");
    if (p.0 != 42) { return 1; }
    if (p.1 != "hi") { return 2; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}

// Match on literal patterns: `match (n) { 0 => …, 1 => …, _ => … }`
// for number / string / bool scrutinees. The checker dispatches
// to `checkLiteralMatch` when the scrutinee isn't an enum; the
// IR lowers as an if-else-if chain (eq-test per arm, wildcard
// fall-through). Exhaustiveness requires a trailing unguarded `_`.
func TestWASMMatchLiteralInt(t *testing.T) {
	src := `function main(): i32 {
    var n: i32 = 1;
    match (n) {
        0 => { return 100; },
        1 => { return 200; },
        2 => { return 300; },
        _ => { return 0; }
    }
    return -1;
}`
	if got := runWasm(t, src); got != 200 {
		t.Errorf("got %d, want 200", got)
	}
}

func TestWASMMatchLiteralExpr(t *testing.T) {
	src := `function classify(n: i32): string {
    return match (n) {
        0 => "zero",
        1 => "one",
        _ => "many",
    };
}
function main(): i32 {
    if (classify(1) != "one") { return 1; }
    if (classify(99) != "many") { return 2; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}

func TestWASMMatchLiteralBool(t *testing.T) {
	src := `function main(): i32 {
    var b: boolean = true;
    return match (b) {
        true => 1,
        false => 0,
        _ => -1,
    };
}`
	if got := runWasm(t, src); got != 1 {
		t.Errorf("got %d, want 1", got)
	}
}

func TestWASMMatchLiteralString(t *testing.T) {
	src := `function main(): i32 {
    var s: string = "world";
    return match (s) {
        "hello" => 1,
        "world" => 2,
        _ => 0,
    };
}`
	if got := runWasm(t, src); got != 2 {
		t.Errorf("got %d, want 2", got)
	}
}

func TestWASMMatchLiteralWithGuard(t *testing.T) {
	src := `function classify(n: i32, big: boolean): i32 {
    return match (n) {
        0 when big => 100,
        0 => 0,
        _ => 999,
    };
}
function main(): i32 {
    if (classify(0, true) != 100) { return 1; }
    if (classify(0, false) != 0) { return 2; }
    if (classify(5, true) != 999) { return 3; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}

// Anonymous function expressions (lambdas). `function (x: T): R
// { body }` in expression position. The parser emits an `*ast.Lambda`
// node; the checker treats it like a local FuncDecl (captures
// analysis runs against the surrounding scope chain); closureconv
// hoists a synthetic top-level `__lambda_<N>` FuncDecl and rewrites
// the Lambda expression to a MakeClosure at its source location.
// End-to-end shape is identical to a named local FuncDecl that's
// immediately returned / passed — the lambda just elides the name.
func TestWASMLambdaBasic(t *testing.T) {
	src := `function main(): i32 {
    var f = function (x: i32): i32 { return x + 1; };
    return f(41);
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

func TestWASMLambdaCaptures(t *testing.T) {
	src := `function main(): i32 {
    var n: i32 = 10;
    var f = function (x: i32): i32 { return x + n; };
    return f(32);
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

func TestWASMLambdaReturned(t *testing.T) {
	src := `function makeAdder(n: i32): (i32) => i32 {
    return function (x: i32): i32 { return x + n; };
}
function main(): i32 {
    var add10 = makeAdder(10);
    return add10(32);
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

func TestWASMLambdaAsArg(t *testing.T) {
	src := `function apply(f: (i32) => i32, x: i32): i32 { return f(x); }
function main(): i32 {
    return apply(function (n: i32): i32 { return n * 2; }, 21);
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

// `var (a, b) = expr;` tuple destructuring — alternative to
// `let (a, b) = expr;` for symmetry with regular `var name = …;`
// declarations. The parser detects the `(` after `var` and routes
// to the same parseTupleDestructure path the `let` form uses.
func TestWASMVarTupleDestructure(t *testing.T) {
	src := `function getit(): (i64, i32) {
    return (1000000000000i64, 42);
}
function main(): i32 {
    var (a, b) = getit();
    if (a != 1000000000000i64) { return 1; }
    if (b != 42) { return 2; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (var tuple destructure)", got)
	}
}

// `else if` chained in an *expression* IfExpr (not just the
// statement form). The recursive IfExpr stands in for the else
// arm, exactly mirroring the statement-level dispatch.
func TestWASMElseIfExpr(t *testing.T) {
	src := `function classify(n: i32): i32 {
    return if (n > 0) { 1 }
        else if (n < 0) { 0 - 1 }
        else { 0 };
}
function main(): i32 {
    if (classify(5) != 1) { return 1; }
    if (classify(0 - 3) != (0 - 1)) { return 2; }
    if (classify(0) != 0) { return 3; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (else-if as expression)", got)
	}
}

// Array / slice of function-typed elements: `((T) => R)[]` and
// `[(T) => R]`. The single-element-parens case in the type
// parser now treats them as a grouping wrapper so the inner
// FuncType resolves cleanly before the trailing `[]` / `[low:hi]`
// suffix. Calling `arr[i](args)` then goes through OpCallIndirect
// the same way function-typed locals do — the IR's `call()`
// dispatch was extended to recognise `*ast.Index` callees whose
// element type is a `*ast.FuncType`.
func TestWASMArrayOfClosures(t *testing.T) {
	src := `function makeAdder(n: i32): (i32) => i32 {
    function add(x: i32): i32 { return x + n; }
    return add;
}
function main(): i32 {
    var arr: ((i32) => i32)[] = [makeAdder(1), makeAdder(10), makeAdder(100)];
    return arr[0](1) + arr[1](2) + arr[2](3);
}`
	// (1 + 1) + (10 + 2) + (100 + 3) = 117
	if got := runWasm(t, src); got != 117 {
		t.Errorf("got %d, want 117 (array of closures)", got)
	}
}

func TestWASMSliceOfClosures(t *testing.T) {
	src := `function makeAdder(n: i32): (i32) => i32 {
    function add(x: i32): i32 { return x + n; }
    return add;
}
function main(): i32 {
    var arr: ((i32) => i32)[] = [makeAdder(1), makeAdder(2), makeAdder(3)];
    var sl: [(i32) => i32] = arr[1:3];
    return sl[0](10) + sl[1](10);
}`
	// (10 + 2) + (10 + 3) = 25
	if got := runWasm(t, src); got != 25 {
		t.Errorf("got %d, want 25 (slice of closures)", got)
	}
}

// Closure created inside a loop body captures the iteration's
// per-iteration `ic` binding — each closure value reads its
// own captured slot, NOT the loop variable's final value. `ic`
// is re-DECLARED (never assigned) each iteration, so it is not
// boxed into a shared cell (#5301 boxes only captured-and-
// assigned locals) and every closure keeps its own binding.
func TestWASMClosureInLoopCapturesIterVar(t *testing.T) {
	src := `function main(): i32 {
    var arr: ((i32) => i32)[] = [];
    var i: i32 = 0;
    while (i < 3) {
        var ic: i32 = i;
        function f(x: i32): i32 { return x + ic; }
        arr = arr.append(f);
        i = i + 1;
    }
    return arr[0](10) + arr[1](10) + arr[2](10);
}`
	// arr[0]: 10+0 = 10
	// arr[1]: 10+1 = 11
	// arr[2]: 10+2 = 12
	// total: 33
	if got := runWasm(t, src); got != 33 {
		t.Errorf("got %d, want 33 (per-iteration capture)", got)
	}
}

// Mixing i32 and i64 with matching signedness auto-widens the
// narrower operand. The checker inserts an implicit CastExpr so
// the IR sees a homogeneous-width binop; user code doesn't need
// `i32 as i64` everywhere when doing pointer-style arithmetic.
// Mixed SIGNEDNESS still requires an explicit cast.
func TestI64ImplicitWideningAccepted(t *testing.T) {
	src := `function f(): i64 { var x: i32 = 1; var y: i64 = 2i64; return x + y; }
function main(): i32 { return f() as i32; }`
	if got := runWasm(t, src); got != 3 {
		t.Errorf("got %d, want 3 (i32 + i64 auto-widens)", got)
	}
}

// Mixed-signedness widths still demand an explicit cast — the
// reinterpretation isn't free of edge cases. Verify the checker
// rejects `i32 + u32` (same width, different signedness) and
// `u32 + i64` (different widths AND signedness).
func TestMixedSignednessRejected(t *testing.T) {
	for _, src := range []string{
		`function bad(): i32 { var x: i32 = 1; var y: u32 = 2 as u32; return x + y; }`,
		`function bad(): i64 { var x: u32 = 1 as u32; var y: i64 = 2i64; return (x as i64) + y; }`,
	} {
		prog, err := parser.Parse(src)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		// First src should error; second one is fine (explicit cast).
		_, err = checker.Check(prog)
		_ = err
	}
	// Targeted error check on the genuinely-bad source.
	src := `function bad(): i32 { var x: i32 = 1; var y: u32 = 2 as u32; return x + y; }`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err == nil {
		t.Fatalf("expected checker error for mixed i32/u32 add, got none")
	} else if !strings.Contains(err.Error(), "use `as`") {
		t.Errorf("expected error mentioning `as`, got: %v", err)
	}
}

func TestWASMFactorial(t *testing.T) {
	src := `function fact(n: i32): i32 {
		if (n == 0) { return 1; }
		return n * fact(n - 1);
	}
	function main(): i32 { return fact(5); }`
	if got := runWasm(t, src); got != 120 {
		t.Errorf("got %d, want 120", got)
	}
}

func TestWASMForLoopWithBreakContinue(t *testing.T) {
	src := `function main(): i32 {
		var sum = 0;
		for (var i = 0; i < 10; i = i + 1) {
			if (i < 5) { continue; }
			if (i == 8) { break; }
			sum = sum + i;
		}
		return sum;
	}`
	// 5 + 6 + 7 = 18 (break before adding 8)
	if got := runWasm(t, src); got != 18 {
		t.Errorf("got %d, want 18", got)
	}
}

// Function values materialise as `{fn_idx, env}` pair-cell
// pointers; the `fn_idx` field stores the function's POSITION
// IN THE FUNCREF TABLE, not its position in `prog.Funcs`. If
// the emitter ever pushed funcIndex instead of tableIndex,
// `call_indirect` would either trap or dispatch to the wrong
// function. This program declares two non-table functions
// before `target` so funcIndex["target"] = 2 but
// tableIndex["target"] = 0; dispatching through
// `apply(target, 4)` must hit `target` (which returns 40).
func TestWASMFunctionValueOrderIndependent(t *testing.T) {
	src := `function unrelated_a(x: i32): i32 { return x + 1; }
	function unrelated_b(x: i32): i32 { return x + 2; }
	function target(x: i32): i32 { return x * 10; }
	function apply(f: (i32) => i32, x: i32): i32 {
		return f(x);
	}
	function main(): i32 { return apply(target, 4); }`
	if got := runWasm(t, src); got != 40 {
		t.Errorf("got %d, want 40", got)
	}
}

// runWasmCapturingStdout returns whatever the program wrote to stdout
// via WASI fd_write, with the trailing wasmtime-emitted i32 result
// line stripped so callers see only the program's own output.
func runWasmCapturingStdout(t *testing.T, src string) string {
	t.Helper()
	stdout, _ := invokeWasmtime(t, src)
	lines := strings.Split(stdout, "\n")
	for len(lines) > 0 {
		last := strings.TrimSpace(lines[len(lines)-1])
		if last == "" {
			lines = lines[:len(lines)-1]
			continue
		}
		if _, err := strconv.Atoi(last); err == nil {
			lines = lines[:len(lines)-1]
			continue
		}
		break
	}
	return strings.Join(lines, "\n")
}

func TestWASMPrintHelloWorld(t *testing.T) {
	src := `function main(): i32 {
		print("Hello, world!");
		return 0;
	}`
	out := runWasmCapturingStdout(t, src)
	if out != "Hello, world!" {
		t.Errorf("output = %q, want \"Hello, world!\"", out)
	}
}

// Float observation through the component pipeline: stdout only
// carries i32 results (via PrintMainResult + int_to_string), and
// `wasi:cli/exit` clamps the exit code to 0/1, so neither channel
// can carry an f32. Float tests instead express the assertion in
// the lang program itself — main returns 1 when the expected
// value matches and 0 otherwise — and we observe the integer.

func TestWASMFloatArithmetic(t *testing.T) {
	src := `function main(): i32 {
		if ((1.5 + 2.5) == 4.0) { return 1; }
		return 0;
	}`
	if got := runWasm(t, src); got != 1 {
		t.Errorf("got %d, want 1 (1.5 + 2.5 == 4.0)", got)
	}
}

func TestWASMFloatMultiplyAndDivide(t *testing.T) {
	src := `function main(): i32 {
		if ((6.0 * 0.5 / 0.25) == 12.0) { return 1; }
		return 0;
	}`
	if got := runWasm(t, src); got != 1 {
		t.Errorf("got %d, want 1 (6.0 * 0.5 / 0.25 == 12.0)", got)
	}
}

func TestWASMFloatNegate(t *testing.T) {
	src := `function f(x: f32): f32 { return -x; }
		function main(): i32 {
			if (f(3.5) == -3.5) { return 1; }
			return 0;
		}`
	if got := runWasm(t, src); got != 1 {
		t.Errorf("got %d, want 1 (-f(3.5) == -3.5)", got)
	}
}

func TestWASMPutcharWritesBytes(t *testing.T) {
	src := `function main(): i32 {
		putchar(72); putchar(73); putchar(10);
		return 0;
	}`
	out := runWasmCapturingStdout(t, src)
	if out != "HI" {
		t.Errorf("output = %q, want \"HI\"", out)
	}
}

// `for x in arr { ... }` desugars to an index loop; verify the
// WASM backend produces a runnable module that iterates correctly.
func TestWASMForEachOverArray(t *testing.T) {
	src := `
		function main(): i32 {
			var sum: i32 = 0;
			for x in [10, 20, 30] {
				sum = sum + x;
			}
			return sum;
		}`
	if got := runWasm(t, src); got != 60 {
		t.Errorf("got %d, want 60 (10+20+30)", got)
	}
}

// break and continue inside a foreach body work as expected on the
// WASM backend.
func TestWASMForEachBreakContinue(t *testing.T) {
	src := `
		function main(): i32 {
			var sum: i32 = 0;
			for x in [1, 2, 3, 4, 5] {
				if (x == 2) { continue; }
				if (x == 5) { break; }
				sum = sum + x;
			}
			return sum;
		}`
	if got := runWasm(t, src); got != 8 {
		t.Errorf("got %d, want 8 (1+3+4)", got)
	}
}

func TestWASMArraySumAndMutation(t *testing.T) {
	src := `function main(): i32 {
		var a: i32[] = [10, 20, 30, 40];
		a = a.with(2, 100);
		return a[0] + a[1] + a[2] + a[3];
	}`
	if got := runWasm(t, src); got != 170 {
		t.Errorf("got %d, want 170 (10+20+100+40)", got)
	}
}

// Nested ArrayLits get distinct scratch locals so the inner literal
// can finish allocating before the outer one assigns its base.
func TestWASMNestedArrayLits(t *testing.T) {
	src := `function main(): i32 {
		var inner: i32[] = [3, 4];
		var outer: i32[] = [1, 2, inner[0]];
		return outer[2] + inner[1];
	}`
	if got := runWasm(t, src); got != 7 {
		t.Errorf("got %d, want 7 (3 + 4)", got)
	}
}

func TestWASMIndirectCallApply(t *testing.T) {
	src := `
		function add(a: i32, b: i32): i32 { return a + b; }
		function apply(f: (i32, i32) => i32, a: i32, b: i32): i32 {
			return f(a, b);
		}
		function main(): i32 { return apply(add, 40, 2); }`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

// #4804: a closure/fn-value whose signature contains a `string`
// parameter must flow through wasm `call_indirect`. Strings use the
// two-slot (data, len) ABI on wasm32, and the indirect-call signature
// seam (addClosureSigType) previously typed each param via valtypeFor,
// which rejected `string` outright — so any `(string, …) => …`
// comparator/callback failed to compile for -target wasm. The seam now
// fans string params through slotValtypes, matching the callee
// definition (paramValtypes). Exercises the motivating `sort_by`-style
// string comparator shape end-to-end.
func TestWASMIndirectCallStringParam(t *testing.T) {
	src := `
		function apply(f: (string, string) => i32, a: string, b: string): i32 {
			return f(a, b);
		}
		function main(): i32 {
			var cmp: (string, string) => i32 = (a: string, b: string) => a.len() + b.len();
			return apply(cmp, "hi", "world");
		}`
	if got := runWasm(t, src); got != 7 {
		t.Errorf("got %d, want 7 (len(\"hi\")+len(\"world\"))", got)
	}
}

// #4804: a single string param through call_indirect (the one-slot-arg
// count still fans to two wasm slots).
func TestWASMIndirectCallSingleStringParam(t *testing.T) {
	src := `
		function apply(f: (string) => i32, a: string): i32 { return f(a); }
		function main(): i32 {
			var g: (string) => i32 = (a: string) => a.len() * 10;
			return apply(g, "hello");
		}`
	if got := runWasm(t, src); got != 50 {
		t.Errorf("got %d, want 50 (len(\"hello\")*10)", got)
	}
}

// #4804: a MIXED (i32, string) signature — the string arg's two-slot
// fan-out must land at the right callee slot offset alongside the
// scalar. Guards the slot-offset arithmetic, not just the valtype set.
func TestWASMIndirectCallMixedScalarStringParam(t *testing.T) {
	src := `
		function apply(f: (i32, string) => i32, a: i32, b: string): i32 { return f(a, b); }
		function main(): i32 {
			var g: (i32, string) => i32 = (a: i32, b: string) => a + b.len();
			return apply(g, 3, "hello");
		}`
	if got := runWasm(t, src); got != 8 {
		t.Errorf("got %d, want 8 (3 + len(\"hello\"))", got)
	}
}

// #4804 motivating case: a `sort_by`-style generic sort driven by a
// string comparator passed as a fn-value, invoked through call_indirect
// in a loop. This is the exact shape #4802's mangler follow-up wanted to
// collapse std/sort's monomorphic string sorts into — blocked on wasm
// until the string-param seam here. Single-file (the import-mangling half
// is #4802); this pins the codegen half runs correctly end-to-end.
func TestWASMStringComparatorSort(t *testing.T) {
	src := `
		function sort_by(xs: string[], cmp: (string, string) => i32): string[] {
			var out: string[] = xs;
			var i: i32 = 0;
			while (i < out.len()) {
				var j: i32 = i + 1;
				while (j < out.len()) {
					if (cmp(out[j], out[i]) < 0) {
						var t: string = out[i];
						out = out.with(i, out[j]);
						out = out.with(j, t);
					}
					j = j + 1;
				}
				i = i + 1;
			}
			return out;
		}
		function by_len(a: string, b: string): i32 { return a.len() - b.len(); }
		function main(): i32 {
			var xs: string[] = ["ccc", "a", "bb"];
			var s: string[] = sort_by(xs, by_len);
			if (s[0] == "a" && s[1] == "bb" && s[2] == "ccc") { return 0; }
			return 1;
		}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (string comparator sort by length)", got)
	}
}

// #4816: the minimal trigger for the string-helper funcidx bug. A `string`
// sourced from an ARRAY ELEMENT flows as an argument to a `call_indirect`
// (closure) call, and `main`'s `string[]` local is reclaimed at scope exit,
// pulling in `__fern_drop_arr_str`. That helper transitively needs
// `__fern_str_dec`, whose body UNCONDITIONALLY frees the buffer via
// `__fern_box_free` — but the drop_arr_str need-case omitted box_free, so
// `helperIdxs["__fern_box_free"]` missed → 0, and str_dec's `call 0`
// resolved to the user function at funcidx 0 (here the 5-slot comparator
// `scmp`), producing "expected i32 but nothing on stack" invalid wasm.
//
// MUST use the BARE-CORE cli/run path (runNativeWasmCli), NOT runWasm: the
// component path's result-printer wrapper (withResultPrinter) pulls in
// __fern_box_free independently, masking the missing dep. Here main returns
// 0 on success; the pre-fix invalid module fails to run → non-zero.
func TestWASMArrayElemStringClosureArg(t *testing.T) {
	src := `
		function scmp(a: string, b: string): i32 { if (a.len() < b.len()) { return 0-1; } return 1; }
		function g(arr: string[], cmp: (string, string) => i32): i32 { return cmp(arr[0], "zz"); }
		function main(): i32 { var xs: string[] = ["a","bb"]; if (g(xs, scmp) == 0-1) { return 0; } return 1; }`
	if got := runNativeWasmCli(t, src); got != 0 {
		t.Errorf("got %d, want 0 (scmp(\"a\",\"zz\")=-1) — #4816 string helper funcidx", got)
	}
}

// #4816: two GENERIC `sort_by[string]` instantiations in one module (asc +
// desc comparators) — the `prop_sort_strings`-shaped case. Before the fix
// this failed to validate the moment the string sorts crossed the generic
// closure-call body with the drop_arr_str helper present. Bare-core path
// (see the note above); asc puts "a" first, desc puts a length-2 string
// first; main returns 0 on success.
func TestWASMGenericStringSortTwoInstantiations(t *testing.T) {
	src := `
		function asc(a: string, b: string): i32 { if (a.len() < b.len()) { return 0-1; } if (a.len() > b.len()) { return 1; } return 0; }
		function desc(a: string, b: string): i32 { if (a.len() > b.len()) { return 0-1; } if (a.len() < b.len()) { return 1; } return 0; }
		function sort_by[T](arr: T[], cmp: (T, T) => i32): T[] {
			var n: i32 = arr.len(); if (n < 2) { return arr; } var out: T[] = arr; var i: i32 = 1;
			while (i < n) { var j: i32 = i; while (j > 0 && cmp(out[j], out[j-1]) < 0) { var t: T = out[j]; out = out.with(j, out[j-1]); out = out.with(j-1, t); j = j - 1; } i = i + 1; }
			return out;
		}
		function main(): i32 {
			var xs: string[] = ["cc","a","bb"];
			var up: string[] = sort_by(xs, asc);
			var dn: string[] = sort_by(xs, desc);
			if (up[0] == "a" && dn[0].len() == 2) { return 0; }
			return 1;
		}`
	if got := runNativeWasmCli(t, src); got != 0 {
		t.Errorf("got %d, want 0 (two sort_by[string] instantiations) — #4816", got)
	}
}

func TestWASMFunctionValueInVar(t *testing.T) {
	src := `
		function dbl(x: i32): i32 { return x * 2; }
		function main(): i32 {
			var f = dbl;
			return f(7);
		}`
	if got := runWasm(t, src); got != 14 {
		t.Errorf("got %d, want 14", got)
	}
}

func TestWASMIfExpr(t *testing.T) {
	src := `function abs(n: i32): i32 { return if (n < 0) { 0 - n } else { n }; }
		function main(): i32 { return abs(-7); }`
	if got := runWasm(t, src); got != 7 {
		t.Errorf("got %d, want 7", got)
	}
}

// Postfix `?` — Option-try operator. `m.get(k)?` evaluates to the
// Some payload on hit; on miss the surrounding function returns
// None early. Surrounding return type must be Option[_]; enforced
// by the checker.
func TestWASMOptionTryHappyPath(t *testing.T) {
	src := `
import "core/map";
function chained(m: Map[i32, i32], k: i32): Option[i32] {
    var v: i32 = m.get(k)?;
    return Some(v + 1);
}
function main(): i32 {
    var m: Map[i32, i32] = Map { 7: 100 };
    match (chained(m, 7)) {
        Some(v) => { return v; },
        None    => { return 1; }
    }
    return 2;
}`
	if got := runWasm(t, src); got != 101 {
		t.Errorf("got %d, want 101 (m.get(7)? + 1)", got)
	}
}

// On a None value, `?` skips the rest of the function and returns
// None. The caller observes the early-return outcome.
func TestWASMOptionTryNoneEarlyReturn(t *testing.T) {
	src := `
import "core/map";
function chained(m: Map[i32, i32], k: i32): Option[i32] {
    var v: i32 = m.get(k)?;
    return Some(v + 1);
}
function main(): i32 {
    var m: Map[i32, i32] = Map { 7: 100 };
    match (chained(m, 99)) {
        Some(v) => { return 1; },
        None    => { return 0; }
    }
    return 2;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (None early-return)", got)
	}
}

// `?` on Result[T, E] — Ok path yields T and falls through.
func TestWASMResultTryHappyPath(t *testing.T) {
	src := `function inner(n: i32): Result[i32, i32] {
    if (n < 0) { return Err(0 - n); }
    return Ok(n * 2);
}
function outer(n: i32): Result[i32, i32] {
    var v: i32 = inner(n)?;
    return Ok(v + 1);
}
function main(): i32 {
    match (outer(5)) {
        Ok(v)  => { return v; },
        Err(e) => { return 0 - e; }
    }
    return 0;
}`
	if got := runWasm(t, src); got != 11 {
		t.Errorf("got %d, want 11 (5 * 2 + 1)", got)
	}
}

// Polymorphic integer literals promote to the float type when
// one side of an arithmetic op is a concrete float. Verifies the
// IR's NumberLit lowering picks OpConstF32 / OpConstF64 (instead
// of OpConstI32) so the runtime computation is float-correct.
func TestWASMPolyIntPromotesToF32Mul(t *testing.T) {
	src := `function main(): i32 {
    var r: f32 = 1.5f32;
    var s: f32 = r * 2;
    if (s == 3.0f32) { return 0; }
    return 1;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (1.5 * 2 = 3.0 as f32)", got)
	}
}

// Same trick on the comparison side: `r <= 0` where r is f32,
// without any `as f32` cast or `0f32` suffix.
func TestWASMPolyIntPromotesToF32Compare(t *testing.T) {
	src := `enum Shape { Circle(f32), Square(f32) }
function classify(s: Shape): i32 {
    match (s) {
        Circle(r) when r <= 0 => { return 1; },
        Circle(_)             => { return 2; },
        Square(_)             => { return 3; }
    }
    return 0;
}
function main(): i32 {
    var c: Shape = Circle(0.5f32);
    return classify(c);
}`
	if got := runWasm(t, src); got != 2 {
		t.Errorf("got %d, want 2 (positive Circle hits non-guarded arm)", got)
	}
}

// f64 promotion path — different OpConstF64 lowering.
func TestWASMPolyIntPromotesToF64(t *testing.T) {
	src := `function main(): i32 {
    var x: f64 = 100.5f64;
    var y: f64 = x - 100;
    if (y == 0.5f64) { return 0; }
    return 1;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (100.5 - 100 = 0.5 as f64)", got)
	}
}

// Typed numeric literal suffixes resolve to concrete types at
// parse time. End-to-end: an `i64` literal value survives the
// full pipeline without an `as i64` cast, and the wasm output
// uses `i64.const`.
func TestWASMNumericLiteralSuffixI64(t *testing.T) {
	src := `function main(): i32 {
    var n: i64 = 1000000i64;
    var m: i64 = n + 23i64;
    if (m == 1000023i64) { return 0; }
    return 1;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (i64 arithmetic via suffix)", got)
	}
}

// f32 suffix on a guard literal removes the `as f32` cast that
// the doc-roadmap flagged as noise.
func TestWASMNumericLiteralSuffixGuardF32(t *testing.T) {
	src := `enum Shape { Circle(f32), Square(f32) }
function classify(s: Shape): i32 {
    match (s) {
        Circle(r) when r <= 0f32 => { return 1; },
        Circle(_)                => { return 2; },
        Square(_)                => { return 3; }
    }
    return 0;
}
function main(): i32 {
    var c: Shape = Circle(0.5f32);
    return classify(c);
}`
	if got := runWasm(t, src); got != 2 {
		t.Errorf("got %d, want 2 (positive-radius Circle hits second arm)", got)
	}
}

// `arr.append(v)` is a generic method on T[] that lowers to the
// per-stride append helper. For 4-byte-stride T (i32, f32, all
// pointer / heap-ref types: string, struct, enum, T[]) it routes
// to `__array_append_string` at codegen — identical wat shape as
// the older `__array_append_*` direct calls, just without users
// having to know the per-T helper name.
// 8-byte int stride: arr.append(v) on i64[] / u64[] routes to the
// wat-side __array_append_i64 helper. The header layout is
// length-prefix (4 bytes) + 8-byte elements, which means
// elements are 4-byte-aligned but not 8-byte-aligned — wasm
// allows unaligned i64.store / i64.load functionally, just with
// a perf hint penalty we accept here.
func TestWASMArrayPushI64(t *testing.T) {
	src := `function main(): i32 {
    var xs: i64[] = [10i64, 20i64];
    xs = xs.append(30i64);
    xs = xs.append(40i64);
    if (xs[0] != 10i64) { return 1; }
    if (xs[3] != 40i64) { return 2; }
    if ((xs.len() as i64) != 4i64) { return 3; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (i64 push round-trip)", got)
	}
}

// 8-byte float stride: arr.append(v) on f64[] mirrors the i64
// path, calling __array_append_f64 with f64.store under the
// hood. Confirms the f64 lang-prelude helper composes the same
// way the i64 sibling does.
func TestWASMArrayPushF64(t *testing.T) {
	src := `function main(): i32 {
    var xs: f64[] = [1.5f64, 2.5f64];
    xs = xs.append(3.5f64);
    xs = xs.append(4.5f64);
    if (xs[3] != 4.5f64) { return 1; }
    if (xs.len() != 4) { return 2; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (f64 push round-trip)", got)
	}
}

// 1-byte stride: u8[].append(v) routes to __array_append_u8.
// Verifies bytes are stored back-to-back (no padding) and read
// back via the array indexer with the right zero-extension.
func TestWASMArrayPushU8(t *testing.T) {
	src := `function main(): i32 {
    var xs: u8[] = [];
    xs = xs.append(10u8);
    xs = xs.append(20u8);
    xs = xs.append(255u8);
    if (xs[0] != 10u8) { return 1; }
    if (xs[2] != 255u8) { return 2; }
    return xs.len();
}`
	if got := runWasm(t, src); got != 3 {
		t.Errorf("got %d, want 3 (3 u8 pushes)", got)
	}
}

// Empty-array start: confirms the oldLen==0 fast-path of the
// helper (skips memory.copy).
func TestWASMArrayPushI64EmptyStart(t *testing.T) {
	src := `function main(): i32 {
    var xs: i64[] = [];
    xs = xs.append(7i64);
    if (xs[0] != 7i64) { return 1; }
    return xs.len();
}`
	if got := runWasm(t, src); got != 1 {
		t.Errorf("got %d, want 1 (single push from empty i64[])", got)
	}
}

func TestWASMArrayPushI32(t *testing.T) {
	src := `function main(): i32 {
    var xs: i32[] = [1, 2];
    xs = xs.append(3);
    xs = xs.append(4);
    if (xs[0] != 1) { return 1; }
    if (xs[3] != 4) { return 2; }
    return xs.len();
}`
	if got := runWasm(t, src); got != 4 {
		t.Errorf("got %d, want 4 (i32[] push)", got)
	}
}

func TestWASMArrayPushEnum(t *testing.T) {
	// JsonValue is a pointer-shaped enum, so push routes to the
	// same 4-byte helper. Confirms enum payloads survive the
	// push round-trip.
	src := `function main(): i32 {
    var xs: JsonValue[] = [];
    xs = xs.append(JString("a"));
    xs = xs.append(JString("bb"));
    return match (xs[1]) {
        JString(s) => s.len(),
        _          => 0 - 1
    };
}`
	if got := runWasm(t, src); got != 2 {
		t.Errorf("got %d, want 2 ((\"bb\").len() after enum push)", got)
	}
}

func TestWASMArrayPushString(t *testing.T) {
	src := `function main(): i32 {
    var xs: string[] = ["a", "b"];
    xs = xs.append("c");
    xs = xs.append("d");
    return xs.len();
}`
	if got := runWasm(t, src); got != 4 {
		t.Errorf("got %d, want 4 (4 elements after pushes)", got)
	}
}

// Verifies pushed values are actually stored in order — i.e. the
// alias to __array_append_string preserves the heap layout.
func TestWASMArrayPushStringValuesPreserved(t *testing.T) {
	src := `function main(): i32 {
    var xs: string[] = [];
    xs = xs.append("hello");
    xs = xs.append("world");
    if (xs[0] != "hello") { return 1; }
    if (xs[1] != "world") { return 2; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (push preserves order)", got)
	}
}

// `match` in expression position: each arm body is one
// expression and the construct evaluates to the unified arm type.
// Replaces a 6-line if-let-else chain with a single line.
func TestWASMMatchExprUnwrapsOption(t *testing.T) {
	src := `function unwrap_or(o: Option[i32], dflt: i32): i32 {
    return match (o) {
        Some(x) => x + 1,
        None    => dflt
    };
}
function main(): i32 {
    return unwrap_or(Some(41), 99);
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42 (Some(41) + 1)", got)
	}
}

// None branch yields the alternative without falling through to
// the Some arm — exhaustiveness on Option[T] is two arms.
func TestWASMMatchExprNoneFallback(t *testing.T) {
	src := `function unwrap_or(o: Option[i32], dflt: i32): i32 {
    return match (o) {
        Some(x) => x + 1,
        None    => dflt
    };
}
function main(): i32 {
    return unwrap_or(None, 99);
}`
	if got := runWasm(t, src); got != 99 {
		t.Errorf("got %d, want 99 (None → dflt)", got)
	}
}

// Wildcard `_` arm covers the unmatched variants and the
// construct still produces a value. Checker-side exhaustiveness
// has a separate test; this exercises runtime selection.
func TestWASMMatchExprWildcardArm(t *testing.T) {
	src := `enum Light { Red, Green, Yellow }
function score(l: Light): i32 {
    return match (l) {
        Red => 1,
        _   => 0
    };
}
function main(): i32 {
    return score(Yellow);
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (wildcard arm)", got)
	}
}

// `match` expression composes inside other expressions —
// confirms it slots into a binary op without parse-time fight.
func TestWASMMatchExprComposesInExpr(t *testing.T) {
	src := `function main(): i32 {
    var o: Option[i32] = Some(10);
    return 1 + match (o) {
        Some(x) => x * 2,
        None    => 0
    };
}`
	if got := runWasm(t, src); got != 21 {
		t.Errorf("got %d, want 21 (1 + 10*2)", got)
	}
}

// Err path: `?` short-circuits, forwarding the source Err
// pointer through the enclosing function unchanged.
func TestWASMResultTryErrPropagates(t *testing.T) {
	src := `function inner(n: i32): Result[i32, i32] {
    if (n < 0) { return Err(0 - n); }
    return Ok(n * 2);
}
function outer(n: i32): Result[i32, i32] {
    var v: i32 = inner(n)?;
    return Ok(v + 1);
}
function main(): i32 {
    match (outer(0 - 7)) {
        Ok(v)  => { return v; },
        Err(e) => { return e; }
    }
    return 0;
}`
	// inner(-7) -> Err(7); outer forwards it; main reads e=7.
	if got := runWasm(t, src); got != 7 {
		t.Errorf("got %d, want 7 (Err(7) propagated)", got)
	}
}

func TestWASMCompoundAssign(t *testing.T) {
	src := `function main(): i32 {
		var x: i32 = 1;
		x += 2;
		x *= 5;
		x -= 1;
		return x;
	}`
	// (1 + 2) * 5 - 1 = 14
	if got := runWasm(t, src); got != 14 {
		t.Errorf("got %d, want 14", got)
	}
}

func TestWASMLenOfString(t *testing.T) {
	src := `function main(): i32 { return ("hello").len(); }`
	if got := runWasm(t, src); got != 5 {
		t.Errorf("got %d, want 5", got)
	}
}

func TestWASMStringIndexAndCompare(t *testing.T) {
	src := `function main(): i32 {
		var s: string = "abc";
		var byte: i32 = s[1] as i32;
		var equal: boolean = "yes" == "yes";
		var different: boolean = "yes" == "no";
		// 'b' = 98; equal=1, different=0 → 98 + 1 - 0 = 99
		var ok: i32 = 0;
		if (equal) { ok = ok + 1; }
		if (different) { ok = ok - 1; }
		return byte + ok;
	}`
	// 'b' (98) + 1 = 99
	if got := runWasm(t, src); got != 99 {
		t.Errorf("got %d, want 99", got)
	}
}

func TestWASMStructBasic(t *testing.T) {
	// Fields are immutable; update via struct-update + rebind.
	src := `struct Point { x: i32, y: i32 }
		function main(): i32 {
			var p: Point = Point { x: 10, y: 32 };
			p = Point { ...p, x: p.x + 5 };
			return p.x + p.y;
		}`
	// (10+5) + 32 = 47
	if got := runWasm(t, src); got != 47 {
		t.Errorf("got %d, want 47", got)
	}
}

// Structs are passed by reference (pointer), but that reference is
// read-only: a function can't mutate its caller's struct in place
// (the old "mutate-through-call" semantic the immutability migration
// removes). The functional form returns the new value and the caller
// rebinds.
func TestWASMStructFunctionalUpdate(t *testing.T) {
	src := `struct Box { v: i32 }
		function bump(b: Box): Box { return Box { ...b, v: b.v + 100 }; }
		function main(): i32 {
			var b: Box = Box { v: 5 };
			b = bump(b);
			return b.v;
		}`
	if got := runWasm(t, src); got != 105 {
		t.Errorf("got %d, want 105", got)
	}
}

func TestWASMStringConcat(t *testing.T) {
	src := `function main(): i32 {
		var a: string = "hello, ";
		var b: string = "world";
		var c: string = a + b;
		return c.len();
	}`
	if got := runWasm(t, src); got != 12 {
		t.Errorf("got %d, want 12 (len of \"hello, world\")", got)
	}
}

func TestWASMStringConcatPreservesContent(t *testing.T) {
	src := `function main(): void {
		print("hello, " + "world");
	}`
	out := runWasmCapturingStdout(t, src)
	if out != "hello, world" {
		t.Errorf("output = %q, want \"hello, world\"", out)
	}
}

// runWasmExpectingTrap compiles src, runs the component, and
// Empty-string sentinel: concat-of-empties, zero-width slices and
// zero-length string_from_bytes_unchecked all return the shared static
// sentinel rather than allocating a fresh 0-byte buffer. The test
// pin is behavioural — `len()` returns 0 for the result, the
// result compares equal to "" via the string-eq runtime, and a
// follow-up concat with a non-empty operand still produces the
// expected bytes. The constant-fold path on the IR side handles
// literal `"" + ""`, so the test forces the runtime path by
// concatenating two empty `args()` slots (the test infra runs
// with no extra argv after the module path, so a[2] / a[3]
// don't exist; we use the runtime-built empty string in a way
// that always exercises the helper). Easier path: build empties
// via `__str_slice(s, 0, 0)` on any non-empty string.
func TestWASMEmptyStringSentinelConcat(t *testing.T) {
	src := `function main(): i32 {
		var s: string = "abcd";
		var a: str = s[0:0];
		var b: str = s[0:0];
		var c: string = a + b;
		return c.len();
	}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (empty + empty)", got)
	}
}

func TestWASMEmptyStringSentinelSlice(t *testing.T) {
	src := `function main(): i32 {
		var s: string = "abcd";
		var empty: str = s[2:2];
		return empty.len();
	}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (zero-width slice)", got)
	}
}

// Empty u8[] sentinel: `__alloc_u8(0)` returns the shared static
// empty-buffer sentinel (`[length=0]` at internString("") offset)
// rather than allocating a fresh 4-byte length-only block. On
// wasm both empty-string and empty-array share the same backing
// memory since they're byte-identical; native targets keep
// distinct symbols.
func TestWASMEmptyU8Sentinel(t *testing.T) {
	src := `function main(): i32 {
        var bs: u8[] = __alloc_u8(0);
        return bs.len();
    }`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (len of __alloc_u8(0))", got)
	}
}

func TestWASMEmptyStringSentinelFromBytes(t *testing.T) {
	src := `function main(): i32 {
		var bs: u8[] = __alloc_u8(0);
		var s: string = string_from_bytes_unchecked(bs);
		return s.len();
	}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (string_from_bytes_unchecked of empty u8[])", got)
	}
}

// Sentinel + non-empty operand still produces the right bytes.
// Catches a regression where the sentinel short-circuit returned a
// pointer the concat path then dereferenced wrong.
func TestWASMEmptyStringSentinelRoundtrip(t *testing.T) {
	src := `function main(): i32 {
		var s: string = "world";
		var empty: str = s[0:0];
		var greeting: string = "hello, " + empty + s;
		return greeting.len();
	}`
	if got := runWasm(t, src); got != 12 {
		t.Errorf("got %d, want 12 (\"hello, \" + \"\" + \"world\")", got)
	}
}

// returns true when wasmtime exited non-zero (the trap surface).
// Used by the array bounds-check tests where the program is
// expected to abort.
func runWasmExpectingTrap(t *testing.T, src string) (stdout, stderr string, ok bool) {
	t.Helper()
	p := buildComponent(t, src)
	s, e, ec := runComponent(t, p, runOpts{})
	return s, e, ec != 0
}

func TestWASMArrayOutOfBoundsTraps(t *testing.T) {
	src := `function main(): i32 {
		var a: i32[] = [1, 2, 3];
		return a[10];
	}`
	stdout, stderr, ok := runWasmExpectingTrap(t, src)
	if !ok {
		t.Errorf("expected wasmtime to trap on a[10], but it succeeded\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
}

func TestWASMNegativeIndexTraps(t *testing.T) {
	src := `function main(): i32 {
		var a: i32[] = [1, 2, 3];
		return a[0 - 1];
	}`
	if _, _, ok := runWasmExpectingTrap(t, src); !ok {
		t.Errorf("expected wasmtime to trap on a[-1], but it succeeded")
	}
}

func TestWASMInBoundsStillWorks(t *testing.T) {
	// Make sure the bounds check doesn't break the happy path.
	src := `function main(): i32 {
		var a: i32[] = [10, 20, 30];
		return a[1];
	}`
	if got := runWasm(t, src); got != 20 {
		t.Errorf("got %d, want 20", got)
	}
}

func TestWASMClosureFactory(t *testing.T) {
	src := `function makeAdder(n: i32): (i32) => i32 {
		function add(x: i32): i32 { return x + n; }
		return add;
	}
	function main(): i32 {
		var f = makeAdder(7);
		return f(35);
	}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

func TestWASMClosureMultipleInstances(t *testing.T) {
	// Two closures over different captured values shouldn't share state.
	src := `function makeAdder(n: i32): (i32) => i32 {
		function add(x: i32): i32 { return x + n; }
		return add;
	}
	function main(): i32 {
		var add5 = makeAdder(5);
		var add10 = makeAdder(10);
		return add5(1) + add10(1);
	}`
	// (5+1) + (10+1) = 17
	if got := runWasm(t, src); got != 17 {
		t.Errorf("got %d, want 17", got)
	}
}

func TestWASMClosureCapturesParamAndVar(t *testing.T) {
	src := `function outer(seed: i32): i32 {
		var bonus: i32 = 100;
		function inner(x: i32): i32 { return x + seed + bonus; }
		return inner(2);
	}
	function main(): i32 { return outer(40); }`
	// 2 + 40 + 100 = 142
	if got := runWasm(t, src); got != 142 {
		t.Errorf("got %d, want 142", got)
	}
}

// Pointer-shaped captures: string, T[], struct, enum heap refs
// fit in the same 4-byte env slot scalars use. The hoisted
// closure body loads them via `i32.load __env+offset` and the
// type-system tracks the reference correctly through the body.

func TestWASMClosureCapturesString(t *testing.T) {
	src := `function outer(s: string): i32 {
    function inner(): i32 { return s.len(); }
    return inner();
}
function main(): i32 { return outer("hello"); }`
	if got := runWasm(t, src); got != 5 {
		t.Errorf("got %d, want 5 (len(\"hello\") via captured string)", got)
	}
}

// Mirror of TestArm64LambdaWithBodyLocals. Anonymous lambdas
// used to compile fine without body-locals but panic with
// "var X has no slot (compiler bug)" the moment the lambda
// declared `var sq = ...` or similar — the checker stored those
// Var nodes against a throwaway synthetic FuncDecl pointer that
// closureconv never re-keyed onto the hoisted lambda.
func TestWASMLambdaWithBodyLocals(t *testing.T) {
	src := `function main(): i32 {
    var greet = "hi";
    var f = function (n: i32): i32 {
        var sq = n * n;
        var tag = greet + "!";
        print(tag);
        return sq;
    };
    return f(6);
}`
	stdout, _ := invokeWasmtime(t, src)
	if !strings.Contains(stdout, "hi!") {
		t.Errorf("stdout missing %q:\n%s", "hi!", stdout)
	}
	if !strings.Contains(stdout, "36") {
		t.Errorf("stdout missing %q:\n%s", "36", stdout)
	}
}

// Mirror of TestArm64LambdaCallsMethodOnCapturedString. Method
// calls inside a lambda body used to fall off the treeshake
// liveness walk because `walkExpr` had no case for
// `*ast.Lambda`; the lambda body was invisible at shake time
// (closureconv hoists it later), so `__method_string_trim`
// got pruned and the wasm module failed to validate /
// reference the missing import.
func TestWASMLambdaCallsMethodOnCapturedString(t *testing.T) {
	src := `
import "std/string";
function main(): i32 {
    var s: string = "  hi  ";
    var f = function (): string { return s.trim().to_owned(); };
    var got = f();
    if (got == "hi") { return 0; }
    return 1;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (s.trim() inside lambda body)", got)
	}
}

func TestWASMClosureCapturesArray(t *testing.T) {
	src := `function outer(xs: i32[]): i32 {
    function inner(i: i32): i32 { return xs[i]; }
    return inner(2);
}
function main(): i32 {
    var xs: i32[] = [10, 20, 30, 40];
    return outer(xs);
}`
	if got := runWasm(t, src); got != 30 {
		t.Errorf("got %d, want 30 (xs[2] via captured array)", got)
	}
}

func TestWASMClosureCapturesStruct(t *testing.T) {
	src := `struct Pt { x: i32, y: i32 }
function outer(p: Pt): i32 {
    function inner(): i32 { return p.x + p.y; }
    return inner();
}
function main(): i32 {
    var p: Pt = Pt { x: 10, y: 32 };
    return outer(p);
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42 (struct field access via captured struct)", got)
	}
}

func TestWASMClosureCapturesEnum(t *testing.T) {
	src := `function outer(o: Option[i32]): i32 {
    function inner(): i32 {
        return match (o) {
            Some(x) => x * 10,
            None    => 0
        };
    }
    return inner();
}
function main(): i32 { return outer(Some(7)); }`
	if got := runWasm(t, src); got != 70 {
		t.Errorf("got %d, want 70 (Some(7).x * 10 via captured enum)", got)
	}
}

// Wide-capture closures: i64 / f64 captures occupy 8-byte slots
// in the env block (vs the 4-byte default). The previous fix
// (#217) opened pointer-shaped captures but kept the env-slot
// width at 4 bytes, which silently truncated wide values. This
// follow-up uses the per-stride store/load path the array-push
// PRs (#218–#220) added.
func TestWASMClosureCapturesI64(t *testing.T) {
	src := `function outer(seed: i64): i64 {
    function inner(): i64 { return seed + 1i64; }
    return inner();
}
function main(): i32 {
    var v: i64 = outer(1000000000000i64);
    if (v != 1000000000001i64) { return 1; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (wide i64 capture preserves all bits)", got)
	}
}

func TestWASMClosureCapturesF64(t *testing.T) {
	src := `function outer(scale: f64): f64 {
    function inner(): f64 { return scale * 2.0f64; }
    return inner();
}
function main(): i32 {
    var r: f64 = outer(3.5f64);
    if (r != 7.0f64) { return 1; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (f64 capture round-trip)", got)
	}
}

// Mixed-stride captures: confirms the running offset accumulator
// handles a sequence of (4-byte, 8-byte, 4-byte) slots without
// stepping on neighbours. The closure reads each capture
// separately to detect any offset-arithmetic mistake.
func TestWASMClosureCapturesMixedWidths(t *testing.T) {
	src := `function outer(a: i32, b: i64, c: i32): i64 {
    function inner(): i64 { return (a as i64) + b + (c as i64); }
    return inner();
}
function main(): i32 {
    var r: i64 = outer(7, 1000000000000i64, 3);
    if (r != 1000000000010i64) { return 1; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (mixed-width capture offsets)", got)
	}
}

// Multiple pointer captures in one closure — exercises offset
// arithmetic for non-zero slot indexes.
func TestWASMClosureCapturesMixedPointers(t *testing.T) {
	src := `function outer(s: string, xs: i32[]): i32 {
    function inner(): i32 { return s.len() + xs.len(); }
    return inner();
}
function main(): i32 {
    var xs: i32[] = [1, 2, 3];
    return outer("hi", xs);
}`
	if got := runWasm(t, src); got != 5 {
		t.Errorf("got %d, want 5 (len(\"hi\")=2 + len([1,2,3])=3)", got)
	}
}

// Closure captures a tuple, then accesses fields by numeric
// index inside the body (`t.0`, `t.1`). The IR's FieldAccess
// lowering needs to dispatch on the receiver's static type to
// pick between the tuple offset path and the struct offset
// path — for captures that meant recognising `*ast.CaptureRef`
// in `targetTupleType`. Without this dispatch the IR fell
// through to the struct path, `fieldOwner` returned "" (no
// matching struct decl), and codegen errored with
// `field access on unresolved struct ""` before any code
// reached the backends.
func TestWASMClosureCapturesTuple(t *testing.T) {
	src := `function build(): () => i64 {
    var t: (i64, i64) = (1000000000000i64, 2000000000000i64);
    function read(): i64 { return t.0 + t.1; }
    return read;
}
function main(): i32 {
    var f = build();
    if (f() != 3000000000000i64) { return 1; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (closure captures (i64, i64))", got)
	}
}

// `f()()` — chained closure call where the inner call's
// result IS the next callee. The IR's call() guard only
// recognised Ident / CaptureRef / FieldAccess callees; a
// `*ast.Call` callee whose return was a closure fell through
// to the `indirect call from non-identifier expression` error.
// Now `call()` peels through the inner Call via
// `callReturnType` and dispatches indirectly when the inner
// result is a *FuncType.
func TestWASMChainedClosureCallResult(t *testing.T) {
	src := `function makeMaker(n: i32): () => () => i32 {
    function level2(): () => i32 {
        function level1(): i32 { return n; }
        return level1;
    }
    return level2;
}
function main(): i32 {
    var m = makeMaker(42);
    return m()();
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42 (f()() chained)", got)
	}
}

// Three-level nested closure where the innermost captures from
// the OUTERMOST scope (skipping the two intermediate functions
// that don't bind the name natively). Pre-fix the checker only
// looked one level up via captureOuter — when level1's body
// referenced `x` (defined in makeChain), the lookup against
// level2's scope failed and the checker errored with
// `undefined identifier "x"`. Now the captureChain walks the
// full nesting stack and records the name as a capture at every
// intermediate level so each closure's env block forwards the
// reference inward to the deepest reader.
func TestWASMTransitiveClosureCapture(t *testing.T) {
	src := `function makeChain(): () => () => () => i32 {
    var x: i32 = 42;
    function level3(): () => () => i32 {
        function level2(): () => i32 {
            function level1(): i32 { return x; }
            return level1;
        }
        return level2;
    }
    return level3;
}
function main(): i32 {
    var f3 = makeChain();
    var f2 = f3();
    var f1 = f2();
    return f1();
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42 (transitive capture through 3 levels)", got)
	}
}

// A nested function captures an outer-scope name that's been
// shadowed by a sibling `var` declaration. With proper lexical
// scoping the body should see the SHADOWED local, not the
// pre-shadow param. Pre-fix shadowrename ran a fresh sub-
// renamer for the nested body so the parent's `n -> n$1`
// rename never reached the body — the body's `Ident{n}` stayed
// raw, the checker recorded `n` (the param) as the capture,
// and the closure returned the original param value (21)
// instead of the shadowed local (42). The pass now walks
// nested bodies under the SAME renamer state and patches each
// FuncDecl's Captures list so the recorded capture names
// match the body's post-rename references.
func TestWASMClosureShadowedParamCapture(t *testing.T) {
	src := `function makeReader(n: i32): () => i32 {
    var n: i32 = n * 2;
    function build(): i32 { return n; }
    return build;
}
function main(): i32 {
    var f = makeReader(21);
    return f();
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42 (closure captures shadowed local)", got)
	}
}

// `(t.N)(args)` where `t.N` is a tuple element holding a closure.
// Same shape as the struct-field case but the field lookup goes
// through `targetTupleType` (numeric selector + tuple's static
// element types) instead of `fieldOwner` + struct decl. Without
// the tuple branch in `call()`'s FieldAccess case the IR errored
// with `indirect call from non-identifier expression`.
func TestWASMCallClosureFromTupleElem(t *testing.T) {
	src := `function makeAdder(n: i32): (i32) => i32 {
    function add(x: i32): i32 { return x + n; }
    return add;
}
function main(): i32 {
    var t: ((i32) => i32, i32) = (makeAdder(10), 32);
    return (t.0)(t.1);
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42 (closure stored in tuple element)", got)
	}
}

// Pattern-binding a closure value via match: `match (o) { Some(f) => f(...) }`.
// The match-arm binding `f` lives in `b.locals` + `b.scratchType`
// (IR-introduced locals), not in `info.Locals` which the original
// `localFuncType` walked. Without the scratchType fallback the
// indirect-call lookup errored with `unknown local "f"`.
func TestWASMCallClosurePatternBound(t *testing.T) {
	src := `function makeAdder(n: i32): (i32) => i32 {
    function add(x: i32): i32 { return x + n; }
    return add;
}
function main(): i32 {
    var o: Option[(i32) => i32] = Some(makeAdder(10));
    match (o) {
        Some(f) => { return f(32); },
        None => { return 0; }
    }
    return -1;
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42 (closure pattern-bound by match)", got)
	}
}

// `(b.f)(args)` where `b.f` is a struct field holding a closure
// value. The callee is a `*ast.FieldAccess`, not an `*ast.Ident`;
// the IR's call() guard used to reject anything non-Ident with
// `indirect call from non-identifier expression`. Now FieldAccess
// callees whose field type resolves to *FuncType go through the
// same OpCallIndirect path closures-in-locals already use.
func TestWASMCallClosureFromStructField(t *testing.T) {
	src := `struct Box { f: (i32) => i32 }
function makeAdder(n: i32): (i32) => i32 {
    function add(x: i32): i32 { return x + n; }
    return add;
}
function main(): i32 {
    var b: Box = Box { f: makeAdder(10) };
    return (b.f)(32);
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42 (closure stored in struct field)", got)
	}
}

// `f().field` / `f().0` where `f` is a closure value (function-
// typed local or param). callReturnType used to consult only
// `info.FuncSigs[id.Name]` — function-typed locals weren't in
// FuncSigs, so the lookup returned nil and `fieldOwner` /
// `targetTupleType` fell through to an unresolved struct,
// erroring at IR-emit with `field access on unresolved struct ""`.
// Now callReturnType also looks up function-typed params,
// info.Locals, and *ast.CaptureRef callees.
func TestWASMClosureResultStructFieldAccess(t *testing.T) {
	src := `struct P { x: i32, y: i32 }
function makeReader(): () => P {
    function build(): P { return P { x: 10, y: 32 }; }
    return build;
}
function main(): i32 {
    var f = makeReader();
    return f().x + f().y;
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

func TestWASMClosureResultTupleAccess(t *testing.T) {
	src := `function makeReader(): () => (i32, i32) {
    function build(): (i32, i32) { return (10, 32); }
    return build;
}
function main(): i32 {
    var f = makeReader();
    return f().0 + f().1;
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

// Sibling local functions: `outer` references `inner`, both
// defined in the same enclosing function. checker's Ident
// lookup used to consult info.FuncSigs before captureOuter,
// which made local FuncDecl names resolve as top-level
// functions instead of as captures — closureconv then left
// the Ident untouched and codegen errored with
// `function "inner" not in table`. captureOuter now wins so
// the sibling gets captured.
func TestWASMSiblingLocalFnCapture(t *testing.T) {
	src := `function makeMaker(): () => () => i32 {
    function inner(): i32 { return 42; }
    function outer(): () => i32 { return inner; }
    return outer;
}
function main(): i32 {
    var mk = makeMaker();
    var f = mk();
    return f();
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

// `len(f())` where `f` is a closure-typed local returning a
// string. The IR's `len()` lowering picks between `OpStrLen`
// (SSO-aware) and the array-shape `[ptr-4]; load` based on
// `exprType(arg)` — the dispatch needs to see `string`, not
// nil. Before this fix `exprType(*ast.Call)` only inspected
// `info.FuncSigs[callee.Name]`, which never matches a function-
// typed local (vars / params don't live in FuncSigs). The
// load fell through to `[ptr-4]` and trapped on inline-form
// strings produced by string-returning closures (the SSO flag
// bit makes `ptr - 4` an out-of-bounds memory address).
func TestWASMLenOfClosureReturningString(t *testing.T) {
	src := `function makeReader(): () => string {
    function build(): string { return "hello"; }
    return build;
}
function main(): i32 {
    var f = makeReader();
    return (f()).len();
}`
	if got := runWasm(t, src); got != 5 {
		t.Errorf("got %d, want 5 (len of closure-returned string)", got)
	}
}

// `if let` / `match` / `let else` / `defer` statements inside a
// closure body, where the scrutinee / source / deferred expr
// references a captured outer-scope name. closureconv's
// rewriteStmt switch was missing cases for these four statement
// kinds — captured refs inside them survived as raw idents and
// IR-emit failed with `unresolved identifier "o" (compiler bug)`.
func TestWASMClosureIfLetCapture(t *testing.T) {
	src := `function makeReader(o: Option[i32]): () => i32 {
    function build(): i32 {
        if let Some(v) = o {
            return v;
        }
        return 0;
    }
    return build;
}
function main(): i32 {
    var f = makeReader(Some(42));
    return f();
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

func TestWASMClosureMatchStmtCapture(t *testing.T) {
	src := `function makeReader(o: Option[i32]): () => i32 {
    function build(): i32 {
        match (o) {
            Some(v) => { return v; },
            None => { return 0; }
        }
        return -1;
    }
    return build;
}
function main(): i32 {
    var f = makeReader(Some(42));
    return f();
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

func TestWASMClosureLetElseCapture(t *testing.T) {
	src := `function makeReader(o: Option[i32]): () => i32 {
    function build(): i32 {
        let Some(v) = o else {
            return 0;
        };
        return v;
    }
    return build;
}
function main(): i32 {
    var f = makeReader(Some(42));
    return f();
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

// FString / MapLit inside a closure body whose captured names
// appear in the desugared `+`-chain or per-entry expressions.
// The closureconv pass used to skip these node kinds entirely;
// captured-name idents inside the f"…" or `Map { … }` survived
// as raw `*ast.Ident` and the IR errored with
// `unresolved identifier "name" (compiler bug)`. The rewrite
// now recurses through FString.Parts + FString.Desugared and
// per-entry MapLit.Entries[i].Key/Value (and TupleLit.Elems
// while we're here — the cases were missing).
func TestWASMClosureFStringCapture(t *testing.T) {
	src := `
import "std/string";
function makeNamer(name: string): () => string {
    function build(): string { return f"hello, {name}!"; }
    return build;
}
function main(): i32 {
    var f = makeNamer("world");
    if (f() != "hello, world!") { return 1; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}

func TestWASMClosureMapLitCapture(t *testing.T) {
	src := `
import "core/map";
function makeMap(k: i32, v: i32): () => Map[i32, i32] {
    function build(): Map[i32, i32] { return Map { k: v }; }
    return build;
}
function main(): i32 {
    var f = makeMap(7, 42);
    var m = f();
    return m.get_or(7, 0);
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42 (Map { capK: capV } inside closure)", got)
	}
}

// Mutable captured variable: a closure body writes back to a
// captured outer-scope variable. The env block lives on the
// heap and is shared by every call to the same closure, so
// successive invocations see the updated value — classic
// counter-closure shape. Pre-fix this errored at IR-emit with
// `ir: assignment target *ast.CaptureRef not yet lowered`. The
// assignment now stores to `[__env + capOffset]` via the type-
// correct payloadStoreOp, matching the read side's load shape.
// Note: captures are by VALUE at make-time. Two separate
// closures over the same outer name would each get their own
// env copy — they don't share the slot.
func TestWASMMutableCapturedVar(t *testing.T) {
	src := `function makeCounter(): () => i32 {
    var count: i32 = 0;
    function tick(): i32 {
        count = count + 1;
        return count;
    }
    return tick;
}
function main(): i32 {
    var c = makeCounter();
    var a: i32 = c();
    var b: i32 = c();
    var d: i32 = c();
    return a + b + d;
}`
	// 1 + 2 + 3 = 6
	if got := runWasm(t, src); got != 6 {
		t.Errorf("got %d, want 6 (counter increments in env)", got)
	}
}

// Wide-payload counterpart: an i64 captured local stores back
// via OpStore Width=64, so the high 32 bits survive the
// round-trip through the env block.
func TestWASMMutableCapturedI64(t *testing.T) {
	src := `function makeCounter(): () => i64 {
    var count: i64 = 0i64;
    function tick(): i64 {
        count = count + 1i64;
        return count;
    }
    return tick;
}
function main(): i32 {
    var c = makeCounter();
    var a: i64 = c();
    var b: i64 = c();
    if (a != 1i64) { return 1; }
    if (b != 2i64) { return 2; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}

// Higher-order closure: a nested function captures a function-
// typed param and calls it from inside its body. The closureconv
// pass rewrote that captured-name reference to a `*ast.CaptureRef`,
// so the call's callee is no longer an Ident — the IR's call()
// path used to reject anything non-Ident with `ir: indirect call
// from non-identifier expression`. Now CaptureRef callees are
// recognised: the captured slot holds the closure pair pointer,
// the IR pushes args, then dispatches indirectly through the
// pair via OpCallIndirect (same path function-typed locals use).
func TestWASMClosureCallsCapturedFn(t *testing.T) {
	src := `function makeAdder(n: i32): (i32) => i32 {
    function add(x: i32): i32 { return x + n; }
    return add;
}
function makeApplier(f: (i32) => i32): (i32) => i32 {
    function apply(x: i32): i32 { return f(x) + 1; }
    return apply;
}
function main(): i32 {
    var a = makeAdder(10);
    var ap = makeApplier(a);
    return ap(5);
}`
	// (5 + 10) + 1 = 16
	if got := runWasm(t, src); got != 16 {
		t.Errorf("got %d, want 16", got)
	}
}

// Closure that recursively calls itself by name from inside its
// own body — `fact(n - 1)` inside `function fact`. The checker
// skips this self-reference in the capture set (to avoid the
// chicken-and-egg of an env containing the closure that needs
// the env) so closureconv has to bridge the renamed top-level
// at the call site. Pre-fix the recursive `fact(...)` survived
// unchanged and codegen tried to call a top-level `fact` that
// no longer existed (the function was renamed to
// `__closure_fact_1`) — wasm-tools rejected with `unknown
// func: failed to find name $fact`.
func TestWASMClosureRecursiveSelfCall(t *testing.T) {
	src := `function makeFact(): (i32) => i32 {
    function fact(n: i32): i32 {
        if (n <= 1) { return 1; }
        return n * fact(n - 1);
    }
    return fact;
}
function main(): i32 {
    var f = makeFact();
    return f(5);
}`
	// 5! = 120
	if got := runWasm(t, src); got != 120 {
		t.Errorf("got %d, want 120 (5!)", got)
	}
}

func TestWASMMethodOnStruct(t *testing.T) {
	src := `struct Point { x: i32, y: i32 }
		function (p: Point) sum(): i32 { return p.x + p.y; }
		function main(): i32 {
			var p: Point = Point { x: 10, y: 32 };
			return p.sum();
		}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

func TestWASMMethodWithExtraArg(t *testing.T) {
	// `b.shifted(7)` rewrites to `__method_Box_shifted(b, 7)`.
	src := `struct Box { v: i32 }
		function (b: Box) shifted(n: i32): i32 { return b.v + n; }
		function main(): i32 {
			var b: Box = Box { v: 5 };
			return b.shifted(37);
		}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

// Sum types end-to-end on WASM: a `pub enum` with a payload-
// carrying variant gets constructed, then matched, with the
// bound payload flowing into the returned value.
func TestWASMEnumMatchPayload(t *testing.T) {
	src := `enum Pair { Two(i32, i32) }
		function main(): i32 {
			var p: Pair = Two(7, 5);
			match (p) {
				Two(a, b) => { return a + b; }
			}
			return -1;
		}`
	if got := runWasm(t, src); got != 12 {
		t.Errorf("got %d, want 12", got)
	}
}

// Wide-payload variants: `Option[i64]` round-trips an i64 value
// through Some(...) construction and a match-arm bind. This
// fails on the pre-wide-payload IR (uniform 4-byte slot) because
// the value gets truncated on store. A passing test confirms the
// IR's payloadLayout / payloadStoreOp / payloadLoadOp handshake
// picks 8-byte slots and i64.store / i64.load for i64 payloads.
//
// The explicit `as i64` cast on the literal is needed today
// because polymorphic-literal inference doesn't yet flow from
// an enum-constructor's destination annotation back into the
// argument expression — `var o: Option[i64] = Some(1)` reads
// the `1` as i32 and rejects the assignment. Separate concern
// from the wide-payload IR work; once contextual literal
// inference covers enum constructors the casts can drop.
func TestWASMEnumMatchPayloadI64(t *testing.T) {
	src := `function main(): i32 {
    var o: Option[i64] = Some(4294967296 as i64);
    match (o) {
        Some(n) => {
            if (n == (4294967296 as i64)) { return 1; }
            return 0;
        },
        None => { return -1; }
    }
    return -1;
}`
	if got := runWasm(t, src); got != 1 {
		t.Errorf("got %d, want 1 (Option[i64] roundtrip)", got)
	}
}

// `Option[f64]` round-trips an f64 through Some + match. f64
// values use the same 8-byte payload slot as i64 but go through
// f64.store / f64.load.
func TestWASMEnumMatchPayloadF64(t *testing.T) {
	src := `function main(): i32 {
    var o: Option[f64] = Some(3.5 as f64);
    match (o) {
        Some(x) => {
            if (x > (3.4 as f64)) { if (x < (3.6 as f64)) { return 1; } }
            return 0;
        },
        None => { return -1; }
    }
    return -1;
}`
	if got := runWasm(t, src); got != 1 {
		t.Errorf("got %d, want 1 (Option[f64] roundtrip)", got)
	}
}

// Bare-literal version of the same — the destination
// annotation `Option[i64]` flows into `Some(...)` via
// settleNumeric's EnumType case, locking the literal to i64
// before assignable runs. No `as i64` cast on the literal.
func TestWASMEnumPayloadInferredI64(t *testing.T) {
	src := `function main(): i32 {
    var o: Option[i64] = Some(4294967296);
    match (o) {
        Some(n) => {
            if (n == 4294967296) { return 1; }
            return 0;
        },
        None => { return -1; }
    }
    return -1;
}`
	if got := runWasm(t, src); got != 1 {
		t.Errorf("got %d, want 1 (Option[i64] inferred)", got)
	}
}

// Same shape with f64 — `var o: Option[f64] = Some(3.5);`
// resolves the literal from the destination type.
func TestWASMEnumPayloadInferredF64(t *testing.T) {
	src := `function main(): i32 {
    var o: Option[f64] = Some(3.5);
    match (o) {
        Some(x) => {
            if (x > 3.4) { if (x < 3.6) { return 1; } }
            return 0;
        },
        None => { return -1; }
    }
    return -1;
}`
	if got := runWasm(t, src); got != 1 {
		t.Errorf("got %d, want 1 (Option[f64] inferred)", got)
	}
}

// Mixed-width payloads: a variant with (i64, i32) lays out
// payload[0] at offset 8 (8-byte aligned) and payload[1] at
// offset 16 — total enum size is 20 bytes (4 tag + 4 align-pad
// + 8 i64 + 4 i32). The match-arm load must mirror the same
// offset table or both bindings come back as garbage.
func TestWASMEnumMatchPayloadWideMixed(t *testing.T) {
	// No `as i64` cast on the literals — the variant's declared
	// payload type drives the polymorphic-literal settle (the
	// non-generic-enum case where vr.payloads[i] is already
	// concrete). Generic Option[i64] still needs the cast since
	// the type-param substitution only fires when an arg
	// provides the concrete type.
	src := `enum Wide { W(i64, i32) }
function main(): i32 {
    var w: Wide = W(8589934592, 7);
    match (w) {
        W(big, small) => {
            if (big == 8589934592) { if (small == 7) { return 1; } }
            return 0;
        }
    }
    return -1;
}`
	if got := runWasm(t, src); got != 1 {
		t.Errorf("got %d, want 1 (mixed-width enum payload)", got)
	}
}

// Same shape via `if let` — the alternate lowering path mirrors
// match's payload-load offsets, so wide payloads must work here
// too.
func TestWASMIfLetPayloadI64(t *testing.T) {
	src := `function main(): i32 {
    var o: Option[i64] = Some(4294967296 as i64);
    if let Some(n) = o {
        if (n == (4294967296 as i64)) { return 1; }
        return 0;
    }
    return -1;
}`
	if got := runWasm(t, src); got != 1 {
		t.Errorf("got %d, want 1 (if-let Option[i64])", got)
	}
}

// And via `let else` — the third payload-load lowering path.
func TestWASMLetElsePayloadI64(t *testing.T) {
	src := `function main(): i32 {
    var o: Option[i64] = Some(4294967296 as i64);
    let Some(n) = o else { return -1; };
    if (n == (4294967296 as i64)) { return 1; }
    return 0;
}`
	if got := runWasm(t, src); got != 1 {
		t.Errorf("got %d, want 1 (let-else Option[i64])", got)
	}
}

// Multi-variant dispatch picks the right arm. The test exercises
// both branches of a 2-arm enum so a regression in tag dispatch
// (off-by-one, branch fall-through, etc.) shows up.
func TestWASMEnumMatchDispatch(t *testing.T) {
	// Use distinct variant names so the test program doesn't
	// collide with the auto-injected Result enum (which owns
	// `Ok` / `Err`).
	srcOk := `enum Status { Good, Bad(string) }
		function status(): Status { return Good; }
		function main(): i32 {
			match (status()) {
				Good => { return 0; },
				Bad(msg) => { return 1; }
			}
			return -1;
		}`
	if got := runWasm(t, srcOk); got != 0 {
		t.Errorf("Good arm: got %d, want 0", got)
	}
	srcErr := `enum Status { Good, Bad(string) }
		function status(): Status { return Bad("boom"); }
		function main(): i32 {
			match (status()) {
				Good => { return 0; },
				Bad(msg) => { return msg.len(); }
			}
			return -1;
		}`
	if got := runWasm(t, srcErr); got != 4 {
		t.Errorf("Bad arm: got %d, want 4 (len of \"boom\")", got)
	}
}

// Generic Option end-to-end on WASM: `Some(42)` constructs
// `Option[i32]`, the match arm extracts the payload, the
// caller's main returns the value. Type erasure means there's
// no per-T monomorphization: the WAT treats every payload as
// i32 regardless of T.
func TestWASMGenericOptionSome(t *testing.T) {
	src := `function find(): Option[i32] { return Some(42); }
		function main(): i32 {
			match (find()) {
				Some(v) => { return v; },
				None => { return -1; }
			}
			return 99;
		}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

// Payload-less variants on generic enums (`None`) flow through
// the assignment relaxation: the constructor leaves type args
// unresolved, the var / return slot supplies them, and the match
// runs the right arm.
func TestWASMGenericOptionNone(t *testing.T) {
	src := `function find(): Option[i32] { return None; }
		function main(): i32 {
			match (find()) {
				Some(v) => { return v; },
				None => { return 7; }
			}
			return 99;
		}`
	if got := runWasm(t, src); got != 7 {
		t.Errorf("got %d, want 7 (None arm)", got)
	}
}

// Result[T, E] with two type params, both routed at runtime.
// The error-arm value is a string carried through the heap as
// any other payload; `len(msg)` exercises the substituted
// payload type.
func TestWASMGenericResult(t *testing.T) {
	// Use the auto-injected `Result[T, E]` rather than re-declaring it.
	src := `function divide(a: i32, b: i32): Result[i32, string] {
			if (b == 0) { return Err("zero"); }
			return Ok(a / b);
		}
		function main(): i32 {
			match (divide(20, 4)) {
				Ok(v) => { return v; },
				Err(msg) => { return msg.len(); }
			}
			return -1;
		}`
	if got := runWasm(t, src); got != 5 {
		t.Errorf("got %d, want 5 (20/4)", got)
	}
}

// Generic enum payload typed with `T = float` survives the
// store / load round trip on WASM. Before the
// VariantCallPayloads side-map landed, `Some(3.14)` failed
// validation because the IR emitted `OpStore` (the variant's
// declared payload was `T`, not `float`) and the operand on
// the stack was f32. The checker now records the substituted
// payload type so codegen picks `OpFStore`.
// random_bytes(n) on WASM goes through `wasi_snapshot_preview1.
// random_get`. Length / non-equality assertions only — no
// content checks since it's a CSPRNG.
func TestWASMRandomBytes(t *testing.T) {
	src := `function main(): i32 {
		var a: string = random_bytes(16);
		var b: string = random_bytes(16);
		if (a.len() != 16) { return 1; }
		if (b.len() != 16) { return 2; }
		if (a == b) { return 3; }
		return 0;
	}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("WASM random_bytes: exit = %d, want 0", got)
	}
}

// random_i32() on WASM (preview1) goes through
// `wasi_snapshot_preview1.random_get` into a 4-byte buffer.
// Cross-backend companion to the interp / x86-64 / arm64
// random_i32 paths (issue #2747). Liveness-and-variance only:
// two draws must differ.
func TestWASMRandomI32(t *testing.T) {
	src := `function main(): i32 {
		var a: i32 = random_i32();
		var b: i32 = random_i32();
		if (a == b) { return 1; }
		return 0;
	}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("WASM random_i32: exit = %d, want 0 (1 = two draws matched)", got)
	}
}

func TestWASMOptionFloatPayload(t *testing.T) {
	src := `function pick(): Option[f32] { return Some(3.14); }
		function main(): i32 {
			match (pick()) {
				Some(v) => { if (v > 3.0) { return 1; } return 2; },
				None => { return 0; }
			}
			return -1;
		}`
	if got := runWasm(t, src); got != 1 {
		t.Errorf("got %d, want 1 (Some(3.14) > 3.0)", got)
	}
}

// Result[f32, string] — Ok carries an f32 payload through the
// heap. Same fix path as Option[f32], but with a
// non-payload-less variant on each arm so the test covers a
// realistic two-typed-parameter shape.
func TestWASMResultFloatOk(t *testing.T) {
	src := `function check(x: f32): Result[f32, string] {
			if (x < 0.0) { return Err("negative"); }
			return Ok(x);
		}
		function main(): i32 {
			match (check(2.5)) {
				Ok(v) => { if (v > 2.0) { return 1; } return 2; },
				Err(msg) => { return 0; }
			}
			return -1;
		}`
	if got := runWasm(t, src); got != 1 {
		t.Errorf("got %d, want 1 (Ok(2.5) > 2.0)", got)
	}
}

// runWasmInDir compiles src, places any seed files into a fresh
// temp dir, then runs the wasm under wasmtime with that dir
// preopened (path_open's preopen_fd=3 in our runtime). Returns
// stdout, stderr, exit code, AND the dir so callers can read
// back files the program created.
func runWasmInDir(t *testing.T, src string, seed map[string]string) (stdout, stderr string, exitCode int, dir string) {
	t.Helper()
	p := buildComponent(t, src)
	dir = t.TempDir()
	for name, content := range seed {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	s, e, ec := runComponent(t, p, runOpts{workDir: dir})
	return s, e, ec, dir
}

// `read_file` returns Ok(content) for a present file. The
// program writes the content back to stdout so we can verify
// both the read path and the type-erased Result unwrap.
func TestWASMReadFileOk(t *testing.T) {
	src := `function main(): i32 {
		match (read_file("greeting.txt")) {
			Ok(s) => { write(s); return 0; },
			Err(_) => { return 1; }
		}
		return -1;
	}`
	stdout, _, _, _ := runWasmInDir(t, src, map[string]string{
		"greeting.txt": "hello, file\n",
	})
	if !strings.Contains(stdout, "hello, file\n") {
		t.Errorf("stdout missing greeting; got %q", stdout)
	}
}

// Missing files surface as `IoError.NotFound(path)`. The path
// we passed must be visible in the variant payload — that's
// the cribbed-from-Roc affordance the design relies on.
func TestWASMReadFileNotFound(t *testing.T) {
	src := `function main(): i32 {
		match (read_file("does_not_exist.txt")) {
			Ok(_) => { return 0; },
			Err(err) => {
				match (err) {
					NotFound(p) => { write(p); return 44; },
					_ => { return 99; }
				}
			}
		}
		return -1;
	}`
	stdout, _, _, _ := runWasmInDir(t, src, nil)
	if !strings.Contains(stdout, "does_not_exist.txt") {
		t.Errorf("stdout should echo the missing path; got %q", stdout)
	}
}

// `write_file` truncates the target and writes `content`. We
// verify by reading the file back from the host side after
// the program returns.
func TestWASMWriteFileOk(t *testing.T) {
	src := `function main(): i32 {
		match (write_file("out.txt", "wrote it\n")) {
			Err(_) => { return 1; },
			Ok(_) => { return 0; }
		}
		return -1;
	}`
	_, _, _, dir := runWasmInDir(t, src, nil)
	got, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "wrote it\n" {
		t.Errorf("got %q, want %q", got, "wrote it\n")
	}
}

// Round-trip: write a file, then read it back, then compare
// lengths via the language's `len`. Exercises both helpers
// in the same program.
func TestWASMReadWriteFileRoundtrip(t *testing.T) {
	src := `function main(): i32 {
		match (write_file("rt.txt", "round trip")) {
			Err(_) => { return 1; },
			Ok(_) => {}
		}
		match (read_file("rt.txt")) {
			Ok(s) => { return s.len(); },
			Err(_) => { return 2; }
		}
		return -1;
	}`
	stdout, _, _, _ := runWasmInDir(t, src, nil)
	if !strings.Contains(stdout, "10") {
		t.Errorf("stdout should report `10` (len of \"round trip\"); got %q", stdout)
	}
}

// stdout() and stderr() Writers route their .write(s) calls
// to the right host stream. The test pipes them separately so
// each can be verified.
func TestWASMStdStreams(t *testing.T) {
	src := `function main(): i32 {
		match (stdout().write("out\n")) { Some(_) => { return 1; }, None => {} }
		match (stderr().write("err\n")) { Some(_) => { return 2; }, None => {} }
		return 0;
	}`
	stdout, stderr := invokeWasmtime(t, src)
	if !strings.Contains(stdout, "out\n") {
		t.Errorf("stdout missing `out`: %q", stdout)
	}
	if !strings.Contains(stderr, "err\n") {
		t.Errorf("stderr missing `err`: %q", stderr)
	}
	if strings.Contains(stdout, "err") {
		t.Errorf("stdout shouldn't contain `err`: %q", stdout)
	}
}

// `open_writer` + `Writer.write` + `Writer.close` produce
// the file on disk, then `open_reader` + `Reader.read_line`
// streams it back two lines at a time. EOF on the third
// `read_line` returns `None`. End-to-end this exercises every
// streaming primitive in 4b in one shot.
func TestWASMStreamingRoundtrip(t *testing.T) {
	src := `function main(): i32 {
		match (open_writer("out.txt")) {
			Ok(w) => {
				match (w.write("line 1\n")) { Some(_) => { return 1; }, None => {} }
				match (w.write("line 2\n")) { Some(_) => { return 2; }, None => {} }
				match (w.close()) { Some(_) => { return 3; }, None => {} }
			},
			Err(_) => { return 4; }
		}
		match (open_reader("out.txt")) {
			Ok(r) => {
				match (r.read_line()) { Some(line) => { write(line); }, None => { return 5; } }
				match (r.read_line()) { Some(line) => { write(line); }, None => { return 6; } }
				match (r.read_line()) { Some(_) => { return 7; }, None => {} }
				match (r.close()) { Some(_) => { return 8; }, None => {} }
				return 0;
			},
			Err(_) => { return 9; }
		}
		return -1;
	}`
	stdout, _, _, _ := runWasmInDir(t, src, nil)
	if !strings.Contains(stdout, "line 1\n") || !strings.Contains(stdout, "line 2\n") {
		t.Errorf("stdout missing both lines; got %q", stdout)
	}
}

// `Reader.read_chunk(size)` reads up to `size` bytes; the
// first call gets up to a full chunk, the next call gets
// the remainder, and a third call returns None.
func TestWASMReaderReadChunk(t *testing.T) {
	src := `function main(): i32 {
		match (open_writer("rc.txt")) {
			Ok(w) => {
				match (w.write("hello world")) { Some(_) => { return 1; }, None => {} }
				match (w.close()) { Some(_) => { return 2; }, None => {} }
			},
			Err(_) => { return 3; }
		}
		match (open_reader("rc.txt")) {
			Ok(r) => {
				match (r.read_chunk(5)) {
					Some(s) => { write(s); write(":"); },
					None => { return 4; }
				}
				match (r.read_chunk(20)) {
					Some(s) => { write(s); },
					None => { return 5; }
				}
				match (r.read_chunk(20)) {
					Some(_) => { return 6; },
					None => { return 0; }
				}
			},
			Err(_) => { return 7; }
		}
		return -1;
	}`
	stdout, _, _, _ := runWasmInDir(t, src, nil)
	if !strings.Contains(stdout, "hello: world") {
		t.Errorf("stdout should contain `hello: world`; got %q", stdout)
	}
}

// open_appender preserves existing content and appends new
// writes at the end. Combined with read_file we can verify
// the file ends up containing both halves.
func TestWASMOpenAppender(t *testing.T) {
	src := `function main(): i32 {
		match (open_writer("ap.txt")) {
			Ok(w) => {
				match (w.write("first")) { Some(_) => { return 1; }, None => {} }
				match (w.close()) { Some(_) => { return 2; }, None => {} }
			},
			Err(_) => { return 3; }
		}
		match (open_appender("ap.txt")) {
			Ok(w) => {
				match (w.write("-second")) { Some(_) => { return 4; }, None => {} }
				match (w.close()) { Some(_) => { return 5; }, None => {} }
			},
			Err(_) => { return 6; }
		}
		match (read_file("ap.txt")) {
			Ok(s) => { write(s); return 0; },
			Err(_) => { return 7; }
		}
		return -1;
	}`
	stdout, _, _, _ := runWasmInDir(t, src, nil)
	if !strings.Contains(stdout, "first-second") {
		t.Errorf("stdout should contain `first-second`; got %q", stdout)
	}
}

// TestWASMTupleDestructure covers the basic let-destructure
// shape — one statement, multiple bindings — plus a divmod-
// style multi-return, a mixed-type tuple, and a 3-element
// destructure for arity > 2.
func TestWASMTupleDestructure(t *testing.T) {
	src := `function divmod(a: i32, b: i32): (i32, i32) {
		return (a / b, a % b);
	}
	function main(): i32 {
		// Basic two-name destructure of a tuple literal.
		let (a, b) = (10, 32);
		if (a != 10) { return 1; }
		if (b != 32) { return 2; }

		// Multi-return: divmod(17, 5) == (3, 2)
		let (q, r) = divmod(17, 5);
		if (q != 3) { return 3; }
		if (r != 2) { return 4; }

		// Mixed element types — i32 + string, both work.
		let (n, s) = (7, "hi");
		if (n != 7) { return 5; }
		if (s != "hi") { return 6; }

		// Three-element destructure: arity > 2 OK.
		let (x, y, z) = (1, 2, 3);
		if (x + y + z != 6) { return 7; }

		return 0;
	}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("WASM tuple destructure: exit = %d, want 0", got)
	}
}

// TestWASMHex covers the round-trip plus some edge cases:
// empty string, ASCII text, every nibble exercised. Decode
// of an odd-length input or a non-hex char terminates
// mid-way; the prefix length reflects what was actually
// decoded so `len()` on the result gives the right answer.
func TestWASMHex(t *testing.T) {
	src := `
import "core/int";
import "std/hex";
import "std/string";
function main(): i32 {
		// empty round-trips to empty
		if ((hex.hex_encode("".bytes())).len() != 0) { return 1; }
		if ((hex.hex_decode("")).len() != 0) { return 2; }

		// "hi" -> "6869"
		if (hex.hex_encode("hi".bytes()) != "6869") { return 3; }
		if (string_from_bytes_unchecked(hex.hex_decode("6869")) != "hi") { return 4; }

		// every nibble: byte 0xab -> "ab"
		if (hex.hex_encode("hello world".bytes()) != "68656c6c6f20776f726c64") { return 5; }
		if (string_from_bytes_unchecked(hex.hex_decode("68656c6c6f20776f726c64")) != "hello world") { return 6; }

		// uppercase hex digits decode the same
		if (string_from_bytes_unchecked(hex.hex_decode("48454C4C4F")) != "HELLO") { return 7; }

		// odd-length tail and non-hex char both halt the decoder.
		// "414" -> "A" (the trailing "4" is incomplete and dropped).
		if (string_from_bytes_unchecked(hex.hex_decode("414")) != "A") { return 8; }
		// "41xx" -> "A" (decoder bails at the first non-hex byte).
		if (string_from_bytes_unchecked(hex.hex_decode("41xx")) != "A") { return 9; }

		return 0;
	}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("WASM hex: exit = %d, want 0", got)
	}
}

// `url_parse(s)` splits an absolute or relative URL into its
// component pieces. Covers a full URL (scheme + host + port +
// path + query + fragment), a path-only relative URL, missing
// optional sections (no port, no query, no fragment), and the
// edge cases where a `:` appears in the path or a fragment
// before a query is malformed-but-best-effort.
func TestWASMUrlParse(t *testing.T) {
	src := `
import "core/int";
import "std/url";
function main(): i32 {
		// Full URL with every section.
		match (url.url_parse("https://example.com:8080/path/sub?q=1&r=2#section")) {
			Some(u) => {
				if (u.scheme != "https") { return 1; }
				if (u.host != "example.com") { return 2; }
				if (u.port != 8080) { return 3; }
				if (u.path != "/path/sub") { return 4; }
				if (u.query != "q=1&r=2") { return 5; }
				if (u.fragment != "section") { return 6; }
			},
			None => { return 7; }
		}

		// No port — port should default to 0.
		match (url.url_parse("http://example.com/foo")) {
			Some(u) => {
				if (u.scheme != "http") { return 10; }
				if (u.host != "example.com") { return 11; }
				if (u.port != 0) { return 12; }
				if (u.path != "/foo") { return 13; }
			},
			None => { return 14; }
		}

		// Path-only / relative URL — no scheme, no host, port
		// stays 0, path is the whole input.
		match (url.url_parse("/just/a/path?q=hi")) {
			Some(u) => {
				if (u.scheme != "") { return 20; }
				if (u.host != "") { return 21; }
				if (u.port != 0) { return 22; }
				if (u.path != "/just/a/path") { return 23; }
				if (u.query != "q=hi") { return 24; }
				if (u.fragment != "") { return 25; }
			},
			None => { return 26; }
		}

		// Fragment without query.
		match (url.url_parse("http://h/path#anchor")) {
			Some(u) => {
				if (u.path != "/path") { return 30; }
				if (u.query != "") { return 31; }
				if (u.fragment != "anchor") { return 32; }
			},
			None => { return 33; }
		}

		// Empty input -> None.
		match (url.url_parse("")) {
			Some(_) => { return 40; },
			None => {}
		}

		// Authority only (no path).
		match (url.url_parse("http://localhost:3000")) {
			Some(u) => {
				if (u.host != "localhost") { return 50; }
				if (u.port != 3000) { return 51; }
				if (u.path != "") { return 52; }
			},
			None => { return 53; }
		}

		return 0;
	}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("WASM url_parse: exit = %d, want 0", got)
	}
}

// `url_encode(s)` / `url_decode(s)` — RFC 3986 percent
// encoding. Unreserved set passes through unchanged; the
// rest gets `%HH` (uppercase). Decoder is forgiving:
// malformed `%` sequences pass through verbatim.
func TestWASMUrlCoder(t *testing.T) {
	src := `
import "core/int";
import "std/url";
function main(): i32 {
		// All-unreserved input round-trips byte-for-byte.
		if (url.url_encode("safe-text_~.0Aa") != "safe-text_~.0Aa") { return 1; }
		if (url.url_decode("safe-text_~.0Aa") != "safe-text_~.0Aa") { return 2; }

		// Spaces become %20.
		if (url.url_encode("hello world") != "hello%20world") { return 3; }
		if (url.url_decode("hello%20world") != "hello world") { return 4; }

		// Round-trip a query-style payload.
		if (url.url_encode("k=v&x=1") != "k%3Dv%26x%3D1") { return 5; }
		if (url.url_decode("k%3Dv%26x%3D1") != "k=v&x=1") { return 6; }

		// Lowercase hex decodes too.
		if (url.url_decode("a%2bb") != "a+b") { return 7; }

		// Malformed percent sequences pass through verbatim
		// (the decoder is forgiving rather than fatal).
		if (url.url_decode("100%") != "100%") { return 8; }
		if (url.url_decode("%xy") != "%xy") { return 9; }
		if (url.url_decode("%2") != "%2") { return 10; }

		// Empty round-trips to empty.
		if (url.url_encode("") != "") { return 11; }
		if (url.url_decode("") != "") { return 12; }

		// Reserved-set chars (RFC 3986 gen-delims +
		// sub-delims) all need encoding.
		if (url.url_encode("/") != "%2F") { return 13; }
		if (url.url_encode("?") != "%3F") { return 14; }
		if (url.url_encode("#") != "%23") { return 15; }
		if (url.url_decode("%2F%3F%23") != "/?#") { return 16; }

		return 0;
	}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("WASM url_encode/decode: exit = %d, want 0", got)
	}
}

// `query_parse(s)` splits a URL-encoded query string into a
// `Map[string, string[]]`. Pairs separated by `&`; within
// each pair, `=` separates key from value. Both halves are
// url_decode'd. Duplicate keys all preserved — values for
// the same key collect into a string[] in insertion order.
// Pair without `=` records single-element empty-string array.
func TestWASMQueryParse(t *testing.T) {
	src := `
import "core/int";
import "core/map";
import "std/url";
function main(): i32 {
		// Standard pairs — each unique key has a 1-element
		// string[] containing the decoded value.
		var m: Map[string, string[]] = url.query_parse("a=1&b=2&c=hello%20world");
		if (m.len() != 3) { return 1; }
		match (m.get("a")) {
			Some(arr) => {
				if (arr.len() != 1) { return 2; }
				if (arr[0] != "1") { return 3; }
			},
			None => { return 4; }
		}
		match (m.get("c")) {
			Some(arr) => {
				if (arr[0] != "hello world") { return 5; }
			},
			None => { return 6; }
		}

		// Encoded key.
		var m2: Map[string, string[]] = url.query_parse("k%3D%26=v");
		match (m2.get("k=&")) {
			Some(arr) => { if (arr[0] != "v") { return 10; } },
			None => { return 11; }
		}

		// Pair without '=' -> single-element empty value.
		var m3: Map[string, string[]] = url.query_parse("flag&x=1");
		if (m3.len() != 2) { return 20; }
		match (m3.get("flag")) {
			Some(arr) => {
				if (arr.len() != 1) { return 21; }
				if (arr[0] != "") { return 22; }
			},
			None => { return 23; }
		}

		// Empty input -> empty map.
		var m4: Map[string, string[]] = url.query_parse("");
		if (m4.len() != 0) { return 30; }

		// Trailing '&' is ignored.
		var m5: Map[string, string[]] = url.query_parse("a=1&");
		if (m5.len() != 1) { return 40; }

		// Duplicate keys: all values preserved in order.
		var m6: Map[string, string[]] = url.query_parse("tag=a&tag=b&tag=c");
		if (m6.len() != 1) { return 50; }
		match (m6.get("tag")) {
			Some(arr) => {
				if (arr.len() != 3) { return 51; }
				if (arr[0] != "a") { return 52; }
				if (arr[1] != "b") { return 53; }
				if (arr[2] != "c") { return 54; }
			},
			None => { return 55; }
		}

		// Mixed unique + duplicates.
		var m7: Map[string, string[]] = url.query_parse("k=1&j=x&k=2");
		if (m7.len() != 2) { return 60; }
		match (m7.get("k")) {
			Some(arr) => {
				if (arr.len() != 2) { return 61; }
				if (arr[0] != "1") { return 62; }
				if (arr[1] != "2") { return 63; }
			},
			None => { return 64; }
		}
		match (m7.get("j")) {
			Some(arr) => {
				if (arr.len() != 1) { return 65; }
				if (arr[0] != "x") { return 66; }
			},
			None => { return 67; }
		}

		return 0;
	}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("WASM query_parse: exit = %d, want 0", got)
	}
}

// `json_encode(v: JsonValue): string` — serialize the
// auto-injected JsonValue tree to canonical JSON text.
// Covers each variant, nested arrays/objects, and the
// escape-encoding path for strings.
func TestWASMJsonEncode(t *testing.T) {
	src := `
import "core/map";
import "std/json";
function main(): i32 {
		// Primitives.
		if (json.json_encode(JNull) != "null") { return 1; }
		if (json.json_encode(JBool(true)) != "true") { return 2; }
		if (json.json_encode(JBool(false)) != "false") { return 3; }
		if (json.json_encode(JNumber("42")) != "42") { return 4; }
		if (json.json_encode(JNumber("3.14")) != "3.14") { return 5; }
		if (json.json_encode(JString("hi")) != "\"hi\"") { return 6; }
		if (json.json_encode(JString("")) != "\"\"") { return 7; }

		// String escapes.
		if (json.json_encode(JString("a\"b")) != "\"a\\\"b\"") { return 10; }
		if (json.json_encode(JString("a\\b")) != "\"a\\\\b\"") { return 11; }
		if (json.json_encode(JString("line\nbreak")) != "\"line\\nbreak\"") { return 12; }
		if (json.json_encode(JString("\t")) != "\"\\t\"") { return 13; }
		if (json.json_encode(JString("\r")) != "\"\\r\"") { return 14; }

		// Empty object — empty array literal needs a type
		// annotation that's awkward at construction site, so
		// just exercise empty objects.
		var emptyMap: Map[string, JsonValue] = map_new(4);
		if (json.json_encode(JObject(emptyMap)) != "{}") { return 20; }

		// Heterogeneous array.
		var a: JsonValue[] = [JNumber("1"), JString("two"), JBool(true), JNull];
		if (json.json_encode(JArray(a)) != "[1,\"two\",true,null]") { return 30; }

		// Object — insertion order preserved (IndexMap).
		var m: Map[string, JsonValue] = map_new(4);
		m = m.insert("name", JString("alice"));
		m = m.insert("age", JNumber("30"));
		m = m.insert("admin", JBool(false));
		if (json.json_encode(JObject(m)) != "{\"name\":\"alice\",\"age\":30,\"admin\":false}") {
			return 40;
		}

		// Nested: object containing an array of numbers.
		var inner: JsonValue[] = [JNumber("1"), JNumber("2"), JNumber("3")];
		var outer: Map[string, JsonValue] = map_new(2);
		outer = outer.insert("nums", JArray(inner));
		if (json.json_encode(JObject(outer)) != "{\"nums\":[1,2,3]}") { return 50; }

		return 0;
	}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("WASM json_encode: exit = %d, want 0", got)
	}
}

// `json_parse(s) -> Option[JsonValue]` — RFC 8259 grammar
// recognizer. Numbers stored verbatim as JNumber's string
// payload; standard JSON escapes decoded; whitespace
// between tokens skipped. Round-trips with json_encode for
// most inputs (number formatting may not be identical, but
// payload bytes match).
func TestWASMJsonParse(t *testing.T) {
	src := `
import "std/json";
function main(): i32 {
		// Primitives.
		match (json.json_parse("null")) {
			Some(v) => { match (v) { JNull => {}, JBool(_) => { return 1; }, JNumber(_) => { return 1; }, JString(_) => { return 1; }, JArray(_) => { return 1; }, JObject(_) => { return 1; } } },
			None => { return 2; }
		}
		match (json.json_parse("true")) {
			Some(v) => { match (v) {
				JBool(b) => { if (!b) { return 3; } },
				JNull => { return 4; }, JNumber(_) => { return 4; }, JString(_) => { return 4; }, JArray(_) => { return 4; }, JObject(_) => { return 4; }
			} },
			None => { return 5; }
		}
		match (json.json_parse("false")) {
			Some(v) => { match (v) {
				JBool(b) => { if (b) { return 6; } },
				JNull => { return 7; }, JNumber(_) => { return 7; }, JString(_) => { return 7; }, JArray(_) => { return 7; }, JObject(_) => { return 7; }
			} },
			None => { return 8; }
		}
		match (json.json_parse("42")) {
			Some(v) => { match (v) {
				JNumber(n) => { if (n != "42") { return 10; } },
				JNull => { return 11; }, JBool(_) => { return 11; }, JString(_) => { return 11; }, JArray(_) => { return 11; }, JObject(_) => { return 11; }
			} },
			None => { return 12; }
		}
		match (json.json_parse("-3.14")) {
			Some(v) => { match (v) {
				JNumber(n) => { if (n != "-3.14") { return 13; } },
				JNull => { return 14; }, JBool(_) => { return 14; }, JString(_) => { return 14; }, JArray(_) => { return 14; }, JObject(_) => { return 14; }
			} },
			None => { return 15; }
		}
		match (json.json_parse("\"hi\"")) {
			Some(v) => { match (v) {
				JString(s) => { if (s != "hi") { return 20; } },
				JNull => { return 21; }, JBool(_) => { return 21; }, JNumber(_) => { return 21; }, JArray(_) => { return 21; }, JObject(_) => { return 21; }
			} },
			None => { return 22; }
		}
		// String with escapes.
		match (json.json_parse("\"a\\nb\\\"c\"")) {
			Some(v) => { match (v) {
				JString(s) => { if (s != "a\nb\"c") { return 30; } },
				JNull => { return 31; }, JBool(_) => { return 31; }, JNumber(_) => { return 31; }, JArray(_) => { return 31; }, JObject(_) => { return 31; }
			} },
			None => { return 32; }
		}
		// Empty array.
		match (json.json_parse("[]")) {
			Some(v) => { match (v) {
				JArray(arr) => { if (arr.len() != 0) { return 40; } },
				JNull => { return 41; }, JBool(_) => { return 41; }, JNumber(_) => { return 41; }, JString(_) => { return 41; }, JObject(_) => { return 41; }
			} },
			None => { return 42; }
		}
		// Heterogeneous array.
		match (json.json_parse("[1,\"two\",true,null]")) {
			Some(v) => { match (v) {
				JArray(arr) => {
					if (arr.len() != 4) { return 50; }
					match (arr[0]) { JNumber(n) => { if (n != "1") { return 51; } }, JNull => { return 52; }, JBool(_) => { return 52; }, JString(_) => { return 52; }, JArray(_) => { return 52; }, JObject(_) => { return 52; } }
					match (arr[1]) { JString(s) => { if (s != "two") { return 53; } }, JNull => { return 54; }, JBool(_) => { return 54; }, JNumber(_) => { return 54; }, JArray(_) => { return 54; }, JObject(_) => { return 54; } }
				},
				JNull => { return 55; }, JBool(_) => { return 55; }, JNumber(_) => { return 55; }, JString(_) => { return 55; }, JObject(_) => { return 55; }
			} },
			None => { return 56; }
		}
		// Empty object.
		match (json.json_parse("{}")) {
			Some(v) => { match (v) {
				JObject(m) => { if (m.len() != 0) { return 60; } },
				JNull => { return 61; }, JBool(_) => { return 61; }, JNumber(_) => { return 61; }, JString(_) => { return 61; }, JArray(_) => { return 61; }
			} },
			None => { return 62; }
		}
		// Object with string keys, mixed values.
		match (json.json_parse("{\"a\":1,\"b\":\"two\"}")) {
			Some(v) => { match (v) {
				JObject(m) => {
					if (m.len() != 2) { return 70; }
					match (m.get("a")) {
						Some(av) => { match (av) { JNumber(n) => { if (n != "1") { return 71; } }, JNull => { return 72; }, JBool(_) => { return 72; }, JString(_) => { return 72; }, JArray(_) => { return 72; }, JObject(_) => { return 72; } } },
						None => { return 73; }
					}
				},
				JNull => { return 74; }, JBool(_) => { return 74; }, JNumber(_) => { return 74; }, JString(_) => { return 74; }, JArray(_) => { return 74; }
			} },
			None => { return 75; }
		}
		// Whitespace tolerance.
		match (json.json_parse("  [ 1 , 2 ] ")) {
			Some(v) => { match (v) {
				JArray(arr) => { if (arr.len() != 2) { return 80; } },
				JNull => { return 81; }, JBool(_) => { return 81; }, JNumber(_) => { return 81; }, JString(_) => { return 81; }, JObject(_) => { return 81; }
			} },
			None => { return 82; }
		}
		// Garbage -> None.
		match (json.json_parse("xyz")) { Some(_) => { return 90; }, None => {} }
		match (json.json_parse("[1,]")) { Some(_) => { return 91; }, None => {} }
		match (json.json_parse("{")) { Some(_) => { return 92; }, None => {} }
		match (json.json_parse("")) { Some(_) => { return 93; }, None => {} }
		return 0;
	}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("WASM json_parse: exit = %d, want 0", got)
	}
}

// json_parse surrogate-pair handling: `"😀"`
// (U+1F600 GRINNING FACE) decodes to a 4-byte UTF-8
// sequence (F0 9F 98 80). Lone surrogates fall back to
// U+FFFD REPLACEMENT CHARACTER (EF BF BD), matching Go's
// encoding/json + most strict UTF-8 emitters.
func TestWASMJsonParseSurrogatePairs(t *testing.T) {
	src := "" +
		"import \"std/json\";\n" +
		"function main(): i32 {\n" +
		"    // Astral pair: \\uD83D\\uDE00 = U+1F600 = F0 9F 98 80.\n" +
		"    match (json.json_parse(\"\\\"\\\\uD83D\\\\uDE00\\\"\")) {\n" +
		"        Some(v) => { match (v) {\n" +
		"            JString(s) => {\n" +
		"                if (s.len() != 4) { return 1; }\n" +
		"                if ((s[0] as i32) != 240) { return 2; }\n" +
		"                if ((s[1] as i32) != 159) { return 3; }\n" +
		"                if ((s[2] as i32) != 152) { return 4; }\n" +
		"                if ((s[3] as i32) != 128) { return 5; }\n" +
		"            },\n" +
		"            JNull => { return 6; }, JBool(_) => { return 6; }, JNumber(_) => { return 6; }, JArray(_) => { return 6; }, JObject(_) => { return 6; }\n" +
		"        } },\n" +
		"        None => { return 7; }\n" +
		"    }\n" +
		"    // Lone high surrogate: emits U+FFFD = EF BF BD.\n" +
		"    match (json.json_parse(\"\\\"\\\\uD800x\\\"\")) {\n" +
		"        Some(v) => { match (v) {\n" +
		"            JString(s) => {\n" +
		"                if (s.len() != 4) { return 8; }\n" +
		"                if ((s[0] as i32) != 239) { return 9; }\n" +
		"                if ((s[1] as i32) != 191) { return 10; }\n" +
		"                if ((s[2] as i32) != 189) { return 11; }\n" +
		"                if ((s[3] as i32) != 120) { return 12; }   // 'x'\n" +
		"            },\n" +
		"            JNull => { return 13; }, JBool(_) => { return 13; }, JNumber(_) => { return 13; }, JArray(_) => { return 13; }, JObject(_) => { return 13; }\n" +
		"        } },\n" +
		"        None => { return 14; }\n" +
		"    }\n" +
		"    // Lone low surrogate: emits U+FFFD too.\n" +
		"    match (json.json_parse(\"\\\"\\\\uDC00\\\"\")) {\n" +
		"        Some(v) => { match (v) {\n" +
		"            JString(s) => {\n" +
		"                if (s.len() != 3) { return 15; }\n" +
		"                if ((s[0] as i32) != 239) { return 16; }\n" +
		"                if ((s[1] as i32) != 191) { return 17; }\n" +
		"                if ((s[2] as i32) != 189) { return 18; }\n" +
		"            },\n" +
		"            JNull => { return 19; }, JBool(_) => { return 19; }, JNumber(_) => { return 19; }, JArray(_) => { return 19; }, JObject(_) => { return 19; }\n" +
		"        } },\n" +
		"        None => { return 20; }\n" +
		"    }\n" +
		"    return 0;\n" +
		"}"
	if got := runWasm(t, src); got != 0 {
		t.Errorf("json_parse surrogate pairs: exit = %d, want 0", got)
	}
}

// `f32.to_string()` / `f64.to_string()` — decimal text
// formatting. Not bit-exact (truncate-to-N digits), so
// assertions match what the simple algorithm produces:
// integer values show no decimal point, fractions trim
// trailing zeros, special values get canonical names.
func TestWASMFloatToString(t *testing.T) {
	src := `
import "std/float";
function main(): i32 {
		// Integer values lose the decimal point entirely.
		var a: f32 = 0.0;
		if (a.to_string() != "0") { return 1; }
		var b: f32 = 42.0;
		if (b.to_string() != "42") { return 2; }
		var c: f32 = -7.0;
		if (c.to_string() != "-7") { return 3; }

		// Common fractional values.
		var d: f32 = 0.5;
		if (d.to_string() != "0.5") { return 10; }
		var e: f32 = -0.25;
		if (e.to_string() != "-0.25") { return 11; }

		// f64 keeps more precision (15 fractional digits).
		var f: f64 = 0.5;
		if (f.to_string() != "0.5") { return 20; }
		var g: f64 = 1.5;
		if (g.to_string() != "1.5") { return 21; }

		// Round-trip through parse_float (f64 domain) for tolerance check.
		match ("3.14".parse_float()) {
			Some(x) => {
				// x.to_string() should produce something that
				// parses back to ≈ 3.14.
				match (x.to_string().parse_float()) {
					Some(y) => {
						var diff: f64 = y - 3.14;
						if (diff < 0.0) { diff = 0.0 - diff; }
						if (diff > 0.001) { return 30; }
					},
					None => { return 31; }
				}
			},
			None => { return 32; }
		}

		return 0;
	}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("WASM float to_string: exit = %d, want 0", got)
	}
}

// Tail-call optimisation. wasm now wires `ir.TailCallOptimize`
// (backported from PR #274's x86-64 first-consumer wire-up
// and the arm64 sibling backport). Single assertion: a
// 100,000-deep self-tail-recursive `sum_to` returns the
// right value through `wasmtime --invoke main`. Without
// TCO this would exceed wasmtime's default stack depth long
// before completing.
func TestWASMTailCall(t *testing.T) {
	src := `function sum_to(n: i32, acc: i32): i32 {
    if (n == 0) { return acc; }
    return sum_to(n - 1, acc + n);
}
function main(): i32 {
    return sum_to(100000, 0);
}`
	// 100,000 * 100,001 / 2 = 5,000,050,000 → i32 (mod 2^32) = 705,082,704.
	if got := runWasm(t, src); got != 705082704 {
		t.Errorf("sum_to(100000, 0) → %d, want 705082704", got)
	}
}

// Wasm counterpart to TestArm64NoPreludeStdlibImports / its
// x86-64 sibling — proves the no-prelude path through the
// prelude-to-modules stack works on wasm32 too. See the arm64
// version for the rationale and per-case explanations. Programs
// return 0 on success; runWasm parses the i32 main returned out
// of the PrintMainResult-emitted stdout line.
//
// The PrintMainResult wrapper picks the mangled `int__int_to_string`
// name (versus bare `int_to_string` under auto-prelude) so this
// test exercises both the no-prelude load path AND the wat
// emitter's runtime-name lookup.
func TestWASMNoPreludeStdlibImports(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
	}{
		{"i32_string_cycle", `
import "std/i32";
function main(): i32 {
    var s: string = (42).to_string_padded(6);
    if (s == "000042") { return 0; }
    return 1;
}`},
		{"array_method_chain", `
import "std/array";
function main(): i32 {
    var xs: i32[] = [0 - 3, 4, 0 - 1];
    var ys = xs.abs_each();
    if (ys[0] + ys[1] + ys[2] == 8) { return 0; }
    return 1;
}`},
		{"qualified_int_call", `
import "core/int";
function main(): i32 {
    var s: string = int.int_to_string_radix(255, 16);
    if (s == "ff") { return 0; }
    return 1;
}`},
		{"mixed_stdlib", `
import "std/i32";
import "std/string";
import "std/array";
function main(): i32 {
    var s: string = (0 - 42).to_string();
    if (s != "-42") { return 1; }
    var strs: string[] = ["b", "a", "c"];
    var joined: string = strs.join(",");
    if (joined != "b,a,c") { return 2; }
    return 0;
}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := runWasm(t, c.src); got != 0 {
				t.Errorf("got exit %d, want 0", got)
			}
		})
	}
}

// TestWASMLeb128Uleb covers std/wasm/leb128's unsigned encoders.
// Vectors are drawn from the LEB128 reference and the wasm spec's
// numeric encoding examples: small values (0, 127), the first
// two-byte step (128), Wikipedia's 624485 worked example, and the
// u32 / u64 widths' maxima which exercise the 5-byte and 10-byte
// upper bounds. The seed-append case verifies the encoder preserves
// existing buffer contents — wasm section bodies are accumulated
// across many calls, so a non-mutating "return-the-extended-buffer"
// shape only works if append really is append.
func TestWASMLeb128Uleb(t *testing.T) {
	src := `import "std/wasm/leb128";
function main(): i32 {
    var empty: u8[] = [];

    // 0 -> [0x00]
    var b1: u8[] = leb128.uleb_u32(empty, 0u32);
    if (b1.len() != 1) { return 1; }
    if (b1[0] != 0u8) { return 2; }

    // 127 -> [0x7F]
    var b2: u8[] = leb128.uleb_u32(empty, 127u32);
    if (b2.len() != 1) { return 10; }
    if (b2[0] != 127u8) { return 11; }

    // 128 -> [0x80, 0x01]
    var b3: u8[] = leb128.uleb_u32(empty, 128u32);
    if (b3.len() != 2) { return 20; }
    if (b3[0] != 128u8) { return 21; }
    if (b3[1] != 1u8) { return 22; }

    // 624485 -> [0xE5, 0x8E, 0x26]
    var b4: u8[] = leb128.uleb_u32(empty, 624485u32);
    if (b4.len() != 3) { return 30; }
    if (b4[0] != 229u8) { return 31; }
    if (b4[1] != 142u8) { return 32; }
    if (b4[2] != 38u8) { return 33; }

    // u32 max -> [0xFF, 0xFF, 0xFF, 0xFF, 0x0F]
    var b5: u8[] = leb128.uleb_u32(empty, 4294967295u32);
    if (b5.len() != 5) { return 40; }
    if (b5[0] != 255u8) { return 41; }
    if (b5[1] != 255u8) { return 42; }
    if (b5[2] != 255u8) { return 43; }
    if (b5[3] != 255u8) { return 44; }
    if (b5[4] != 15u8) { return 45; }

    // Append preserves seed bytes.
    var seed: u8[] = [10u8, 20u8];
    var b6: u8[] = leb128.uleb_u32(seed, 128u32);
    if (b6.len() != 4) { return 50; }
    if (b6[0] != 10u8) { return 51; }
    if (b6[1] != 20u8) { return 52; }
    if (b6[2] != 128u8) { return 53; }
    if (b6[3] != 1u8) { return 54; }

    // u64 path with a value above 2^32: 8589934592 (= 2^33).
    // Encoding: [0x80, 0x80, 0x80, 0x80, 0x20].
    var b7: u8[] = leb128.uleb_u64(empty, 8589934592u64);
    if (b7.len() != 5) { return 60; }
    if (b7[0] != 128u8) { return 61; }
    if (b7[1] != 128u8) { return 62; }
    if (b7[2] != 128u8) { return 63; }
    if (b7[3] != 128u8) { return 64; }
    if (b7[4] != 32u8) { return 65; }

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("uleb encoder: exit = %d, want 0", got)
	}
}

// TestWASMLeb128Sleb covers std/wasm/leb128's signed encoders.
// Vectors hit the four terminator regimes — small positive,
// small negative, "needs an extra zero byte because bit-6 set"
// (64 and -65), and multi-byte negative — and a wide i64 value
// to confirm the i64 path's arithmetic-right-shift sign extension
// terminates correctly.
func TestWASMLeb128Sleb(t *testing.T) {
	src := `import "std/wasm/leb128";
function main(): i32 {
    var empty: u8[] = [];

    // 0 -> [0x00]
    var b1: u8[] = leb128.sleb_i32(empty, 0);
    if (b1.len() != 1) { return 1; }
    if (b1[0] != 0u8) { return 2; }

    // -1 -> [0x7F]   (the canonical "all-ones" sleb terminator)
    var b2: u8[] = leb128.sleb_i32(empty, 0 - 1);
    if (b2.len() != 1) { return 10; }
    if (b2[0] != 127u8) { return 11; }

    // 63 -> [0x3F]   (largest single-byte positive)
    var b3: u8[] = leb128.sleb_i32(empty, 63);
    if (b3.len() != 1) { return 20; }
    if (b3[0] != 63u8) { return 21; }

    // 64 -> [0xC0, 0x00]   (bit-6 set forces a continuation byte)
    var b4: u8[] = leb128.sleb_i32(empty, 64);
    if (b4.len() != 2) { return 30; }
    if (b4[0] != 192u8) { return 31; }
    if (b4[1] != 0u8) { return 32; }

    // -64 -> [0x40]   (smallest single-byte negative)
    var b5: u8[] = leb128.sleb_i32(empty, 0 - 64);
    if (b5.len() != 1) { return 40; }
    if (b5[0] != 64u8) { return 41; }

    // -65 -> [0xBF, 0x7F]
    var b6: u8[] = leb128.sleb_i32(empty, 0 - 65);
    if (b6.len() != 2) { return 50; }
    if (b6[0] != 191u8) { return 51; }
    if (b6[1] != 127u8) { return 52; }

    // -123456 -> [0xC0, 0xBB, 0x78]   (multi-byte negative)
    var b7: u8[] = leb128.sleb_i32(empty, 0 - 123456);
    if (b7.len() != 3) { return 60; }
    if (b7[0] != 192u8) { return 61; }
    if (b7[1] != 187u8) { return 62; }
    if (b7[2] != 120u8) { return 63; }

    // i64 wide value: 8589934592 (= 2^33).
    // Encoding: [0x80, 0x80, 0x80, 0x80, 0x20].
    var b8: u8[] = leb128.sleb_i64(empty, 8589934592i64);
    if (b8.len() != 5) { return 70; }
    if (b8[0] != 128u8) { return 71; }
    if (b8[4] != 32u8) { return 75; }

    // i64 negative wide value: -8589934592.
    // Encoding: [0x80, 0x80, 0x80, 0x80, 0x60].
    var b9: u8[] = leb128.sleb_i64(empty, 0i64 - 8589934592i64);
    if (b9.len() != 5) { return 80; }
    if (b9[0] != 128u8) { return 81; }
    if (b9[4] != 96u8) { return 85; }

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("sleb encoder: exit = %d, want 0", got)
	}
}

// TestWASMLeb128Size verifies uleb_size_* agree with the
// post-encoding length. Useful for callers that need the length
// before producing the bytes (e.g. wasm section headers, which
// prefix the body with its own ULEB-encoded length).
func TestWASMLeb128Size(t *testing.T) {
	src := `import "std/wasm/leb128";
function main(): i32 {
    if (leb128.uleb_size_u32(0u32) != 1) { return 1; }
    if (leb128.uleb_size_u32(127u32) != 1) { return 2; }
    if (leb128.uleb_size_u32(128u32) != 2) { return 3; }
    if (leb128.uleb_size_u32(16383u32) != 2) { return 4; }
    if (leb128.uleb_size_u32(16384u32) != 3) { return 5; }
    if (leb128.uleb_size_u32(4294967295u32) != 5) { return 6; }

    if (leb128.uleb_size_u64(0u64) != 1) { return 10; }
    if (leb128.uleb_size_u64(8589934592u64) != 5) { return 11; }

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("size helpers: exit = %d, want 0", got)
	}
}

// TestWASMEncodePreamble covers the module-header + raw-byte
// writers — the foundation everything else in std/wasm/encode is
// built on. Vectors are taken straight from the wasm Core 1.0
// binary spec: magic `\0asm` plus version 1 as fixed-width u32
// LE, and the standard "byte order is least-significant first"
// for put_u32_le.
func TestWASMEncodePreamble(t *testing.T) {
	src := `import "std/wasm/encode";
function main(): i32 {
    var empty: u8[] = [];

    // Module preamble: \0asm 0x01000000.
    var hdr: u8[] = encode.put_module_header(empty);
    if (hdr.len() != 8) { return 1; }
    if (hdr[0] != 0u8) { return 2; }
    if (hdr[1] != 97u8) { return 3; }    // 'a'
    if (hdr[2] != 115u8) { return 4; }   // 's'
    if (hdr[3] != 109u8) { return 5; }   // 'm'
    if (hdr[4] != 1u8) { return 6; }     // version LE
    if (hdr[5] != 0u8) { return 7; }
    if (hdr[6] != 0u8) { return 8; }
    if (hdr[7] != 0u8) { return 9; }

    // put_u32_le(0x12345678) — verifies byte order.
    var le: u8[] = encode.put_u32_le(empty, 305419896u32);
    if (le.len() != 4) { return 20; }
    if (le[0] != 120u8) { return 21; }   // 0x78
    if (le[1] != 86u8) { return 22; }    // 0x56
    if (le[2] != 52u8) { return 23; }    // 0x34
    if (le[3] != 18u8) { return 24; }    // 0x12

    // put_u32_le on a seeded buffer appends, not replaces.
    var seed: u8[] = [255u8];
    var le2: u8[] = encode.put_u32_le(seed, 1u32);
    if (le2.len() != 5) { return 30; }
    if (le2[0] != 255u8) { return 31; }
    if (le2[1] != 1u8) { return 32; }
    if (le2[2] != 0u8) { return 33; }
    if (le2[3] != 0u8) { return 34; }
    if (le2[4] != 0u8) { return 35; }

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("preamble: exit = %d, want 0", got)
	}
}

// TestWASMEncodeNameAndSection covers put_name (uleb-prefixed
// UTF-8) and put_section (id + uleb size + body). The
// "uleb-prefixed bytes" shape shows up in three different places
// in the wasm binary: section bodies, names, and vector
// elements; this test exercises the two callers we have today.
func TestWASMEncodeNameAndSection(t *testing.T) {
	src := `import "std/wasm/encode";
function main(): i32 {
    var empty: u8[] = [];

    // Empty name: uleb(0) only.
    var n0: u8[] = encode.put_name(empty, "");
    if (n0.len() != 1) { return 1; }
    if (n0[0] != 0u8) { return 2; }

    // "hi" -> [0x02, 'h', 'i']
    var n1: u8[] = encode.put_name(empty, "hi");
    if (n1.len() != 3) { return 10; }
    if (n1[0] != 2u8) { return 11; }
    if (n1[1] != 104u8) { return 12; }   // 'h'
    if (n1[2] != 105u8) { return 13; }   // 'i'

    // Empty section body: id + uleb(0) = 2 bytes.
    var s0: u8[] = encode.put_section(empty, encode.section_type(), empty);
    if (s0.len() != 2) { return 20; }
    if (s0[0] != 1u8) { return 21; }     // section_type id
    if (s0[1] != 0u8) { return 22; }     // size 0

    // Non-empty section body: id + uleb(2) + body.
    var body: u8[] = [170u8, 187u8];     // 0xAA 0xBB
    var s1: u8[] = encode.put_section(empty, encode.section_function(), body);
    if (s1.len() != 4) { return 30; }
    if (s1[0] != 3u8) { return 31; }     // section_function id
    if (s1[1] != 2u8) { return 32; }     // size 2
    if (s1[2] != 170u8) { return 33; }
    if (s1[3] != 187u8) { return 34; }

    // section_* IDs match the wasm spec table.
    if (encode.section_type() != 1u8) { return 40; }
    if (encode.section_import() != 2u8) { return 41; }
    if (encode.section_function() != 3u8) { return 42; }
    if (encode.section_memory() != 5u8) { return 43; }
    if (encode.section_export() != 7u8) { return 44; }
    if (encode.section_code() != 10u8) { return 45; }
    if (encode.section_data() != 11u8) { return 46; }

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("name + section: exit = %d, want 0", got)
	}
}

// TestWASMEncodeFuncType + minimal-module compose-up: build a
// complete wasm module containing one function type (i32 -> i32)
// and compare every byte against the reference encoding. This is
// the smallest test that exercises the full chain — preamble,
// section wrapping, vector-of-functype, individual functype, and
// valtype constants — together.
//
// Reference bytes (16 total):
//
//	00 61 73 6D 01 00 00 00          \0asm v1
//	01 06                            section: type, size 6
//	01                                  vec(functype) count = 1
//	60 01 7F 01 7F                       functype: i32 -> i32
func TestWASMEncodeMinimalModule(t *testing.T) {
	src := `import "std/wasm/encode";
import "std/wasm/leb128";
function main(): i32 {
    var bytes: u8[] = [];
    bytes = encode.put_module_header(bytes);

    // Build the type section body: vec(functype) count=1, then
    // one functype "(i32) -> (i32)".
    var type_body: u8[] = [];
    type_body = leb128.uleb_u32(type_body, 1u32);
    var params: u8[] = [encode.valtype_i32()];
    var results: u8[] = [encode.valtype_i32()];
    type_body = encode.put_func_type(type_body, params, results);

    bytes = encode.put_section(bytes, encode.section_type(), type_body);

    // Expected: 16 bytes.
    if (bytes.len() != 16) { return 1; }

    // Preamble.
    if (bytes[0] != 0u8) { return 10; }
    if (bytes[1] != 97u8) { return 11; }
    if (bytes[2] != 115u8) { return 12; }
    if (bytes[3] != 109u8) { return 13; }
    if (bytes[4] != 1u8) { return 14; }
    if (bytes[5] != 0u8) { return 15; }
    if (bytes[6] != 0u8) { return 16; }
    if (bytes[7] != 0u8) { return 17; }

    // Section header: id=1 (type), size=6.
    if (bytes[8] != 1u8) { return 20; }
    if (bytes[9] != 6u8) { return 21; }

    // Body: count=1, then one functype.
    if (bytes[10] != 1u8) { return 30; }     // vec count
    if (bytes[11] != 96u8) { return 31; }    // 0x60 functype tag
    if (bytes[12] != 1u8) { return 32; }     // param count
    if (bytes[13] != 127u8) { return 33; }   // 0x7F i32
    if (bytes[14] != 1u8) { return 34; }     // result count
    if (bytes[15] != 127u8) { return 35; }   // 0x7F i32

    // A two-param / no-result functype: 0x60 02 7F 7E 00.
    var ft2: u8[] = [];
    var ps: u8[] = [encode.valtype_i32(), encode.valtype_i64()];
    var rs: u8[] = [];
    ft2 = encode.put_func_type(ft2, ps, rs);
    if (ft2.len() != 5) { return 40; }
    if (ft2[0] != 96u8) { return 41; }
    if (ft2[1] != 2u8) { return 42; }
    if (ft2[2] != 127u8) { return 43; }      // i32
    if (ft2[3] != 126u8) { return 44; }      // i64
    if (ft2[4] != 0u8) { return 45; }        // empty result vec

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("minimal module: exit = %d, want 0", got)
	}
}

// TestWASMInstConsts covers the four const-instruction encoders.
// Vectors hit the leb terminator regimes for i32 / i64 (single-byte
// positive, single-byte negative via the canonical -1, multi-byte
// boundary at 128 / -65) plus a fixed-width-LE check on f32_const
// and f64_const.
func TestWASMInstConsts(t *testing.T) {
	src := `import "std/wasm/inst";
function main(): i32 {
    var empty: u8[] = [];

    // i32.const 0 -> 0x41 0x00
    var c0: u8[] = inst.inst_i32_const(empty, 0);
    if (c0.len() != 2) { return 1; }
    if (c0[0] != 65u8) { return 2; }
    if (c0[1] != 0u8) { return 3; }

    // i32.const 63 -> 0x41 0x3F (largest single-byte sleb positive
    // — bit-6 clear so no continuation needed).
    var c63: u8[] = inst.inst_i32_const(empty, 63);
    if (c63.len() != 2) { return 10; }
    if (c63[0] != 65u8) { return 11; }
    if (c63[1] != 63u8) { return 12; }

    // i32.const 127 -> 0x41 0xFF 0x00. 127 has bit-6 set, so the
    // sleb form needs a continuation byte; otherwise [0x7F] would
    // decode to -1. This is the trap wasm hex dumps stumble into.
    var c127: u8[] = inst.inst_i32_const(empty, 127);
    if (c127.len() != 3) { return 15; }
    if (c127[0] != 65u8) { return 16; }
    if (c127[1] != 255u8) { return 17; }
    if (c127[2] != 0u8) { return 18; }

    // i32.const -1 -> 0x41 0x7F (sleb 0x7F is the all-ones term)
    var cn1: u8[] = inst.inst_i32_const(empty, 0 - 1);
    if (cn1.len() != 2) { return 20; }
    if (cn1[1] != 127u8) { return 22; }

    // i32.const 128 -> 0x41 0x80 0x01 (sleb boundary)
    var c128: u8[] = inst.inst_i32_const(empty, 128);
    if (c128.len() != 3) { return 30; }
    if (c128[0] != 65u8) { return 31; }
    if (c128[1] != 128u8) { return 32; }
    if (c128[2] != 1u8) { return 33; }

    // i64.const 42 -> 0x42 0x2A
    var ci64: u8[] = inst.inst_i64_const(empty, 42i64);
    if (ci64.len() != 2) { return 40; }
    if (ci64[0] != 66u8) { return 41; }
    if (ci64[1] != 42u8) { return 42; }

    // f32.const with bit pattern 0x3F800000 (= 1.0): 0x43 followed
    // by four LE bytes [0x00, 0x00, 0x80, 0x3F].
    var f1: u8[] = inst.inst_f32_const(empty, 1065353216u32);
    if (f1.len() != 5) { return 50; }
    if (f1[0] != 67u8) { return 51; }
    if (f1[1] != 0u8) { return 52; }
    if (f1[2] != 0u8) { return 53; }
    if (f1[3] != 128u8) { return 54; }
    if (f1[4] != 63u8) { return 55; }

    // f64.const with bit pattern 0x3FF0000000000000 (= 1.0): 0x44
    // followed by eight LE bytes ending in 0x3F.
    var f2: u8[] = inst.inst_f64_const(empty, 4607182418800017408u64);
    if (f2.len() != 9) { return 60; }
    if (f2[0] != 68u8) { return 61; }
    if (f2[1] != 0u8) { return 62; }
    if (f2[7] != 240u8) { return 67; }  // 0xF0
    if (f2[8] != 63u8) { return 68; }   // 0x3F

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("inst consts: exit = %d, want 0", got)
	}
}

// TestWASMInstVariable covers local.get / set / tee and
// global.get / set. The single-byte and multi-byte uleb cases
// (idx 0 vs idx 130) verify the immediate is encoded correctly
// after the opcode byte.
func TestWASMInstVariable(t *testing.T) {
	src := `import "std/wasm/inst";
function main(): i32 {
    var empty: u8[] = [];

    // local.get 0 -> 0x20 0x00
    var lg0: u8[] = inst.inst_local_get(empty, 0u32);
    if (lg0.len() != 2) { return 1; }
    if (lg0[0] != 32u8) { return 2; }
    if (lg0[1] != 0u8) { return 3; }

    // local.set 5 -> 0x21 0x05
    var ls5: u8[] = inst.inst_local_set(empty, 5u32);
    if (ls5.len() != 2) { return 10; }
    if (ls5[0] != 33u8) { return 11; }
    if (ls5[1] != 5u8) { return 12; }

    // local.tee 1 -> 0x22 0x01
    var lt1: u8[] = inst.inst_local_tee(empty, 1u32);
    if (lt1.len() != 2) { return 20; }
    if (lt1[0] != 34u8) { return 21; }
    if (lt1[1] != 1u8) { return 22; }

    // local.get 130 -> 0x20 0x82 0x01 (uleb of 130)
    var lg130: u8[] = inst.inst_local_get(empty, 130u32);
    if (lg130.len() != 3) { return 30; }
    if (lg130[0] != 32u8) { return 31; }
    if (lg130[1] != 130u8) { return 32; }
    if (lg130[2] != 1u8) { return 33; }

    // global.get 7 -> 0x23 0x07
    var gg7: u8[] = inst.inst_global_get(empty, 7u32);
    if (gg7.len() != 2) { return 40; }
    if (gg7[0] != 35u8) { return 41; }
    if (gg7[1] != 7u8) { return 42; }

    // global.set 0 -> 0x24 0x00
    var gs0: u8[] = inst.inst_global_set(empty, 0u32);
    if (gs0.len() != 2) { return 50; }
    if (gs0[0] != 36u8) { return 51; }
    if (gs0[1] != 0u8) { return 52; }

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("inst variable: exit = %d, want 0", got)
	}
}

// TestWASMInstControl covers the control-flow opcodes plus
// drop/select. End-to-end vectors include an empty-blocktype
// block, an i32-result block, an if/else/end sequence, a br_if
// to a label, and a call_indirect with two leb immediates.
func TestWASMInstControl(t *testing.T) {
	src := `import "std/wasm/inst";
import "std/wasm/encode";
function main(): i32 {
    var empty: u8[] = [];

    // Single-byte opcodes.
    if ((inst.inst_unreachable(empty)).len() != 1) { return 1; }
    if (inst.inst_unreachable(empty)[0] != 0u8) { return 2; }
    if (inst.inst_nop(empty)[0] != 1u8) { return 3; }
    if (inst.inst_else(empty)[0] != 5u8) { return 4; }
    if (inst.inst_end(empty)[0] != 11u8) { return 5; }
    if (inst.inst_return(empty)[0] != 15u8) { return 6; }
    if (inst.inst_drop(empty)[0] != 26u8) { return 7; }
    if (inst.inst_select(empty)[0] != 27u8) { return 8; }

    // block bt=empty -> 0x02 0x40
    var bk: u8[] = inst.inst_block_start(empty, inst.blocktype_empty());
    if (bk.len() != 2) { return 10; }
    if (bk[0] != 2u8) { return 11; }
    if (bk[1] != 64u8) { return 12; }

    // loop bt=i32 -> 0x03 0x7F
    var lp: u8[] = inst.inst_loop_start(empty, encode.valtype_i32());
    if (lp.len() != 2) { return 20; }
    if (lp[0] != 3u8) { return 21; }
    if (lp[1] != 127u8) { return 22; }

    // if bt=empty -> 0x04 0x40
    var ifs: u8[] = inst.inst_if_start(empty, inst.blocktype_empty());
    if (ifs[0] != 4u8) { return 30; }
    if (ifs[1] != 64u8) { return 31; }

    // br 0 -> 0x0C 0x00
    var br0: u8[] = inst.inst_br(empty, 0u32);
    if (br0[0] != 12u8) { return 40; }
    if (br0[1] != 0u8) { return 41; }

    // br_if 3 -> 0x0D 0x03
    var bri: u8[] = inst.inst_br_if(empty, 3u32);
    if (bri[0] != 13u8) { return 50; }
    if (bri[1] != 3u8) { return 51; }

    // call 7 -> 0x10 0x07
    var c7: u8[] = inst.inst_call(empty, 7u32);
    if (c7[0] != 16u8) { return 60; }
    if (c7[1] != 7u8) { return 61; }

    // call_indirect typeidx=2 tableidx=0 -> 0x11 0x02 0x00
    var ci: u8[] = inst.inst_call_indirect(empty, 2u32, 0u32);
    if (ci.len() != 3) { return 70; }
    if (ci[0] != 17u8) { return 71; }
    if (ci[1] != 2u8) { return 72; }
    if (ci[2] != 0u8) { return 73; }

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("inst control: exit = %d, want 0", got)
	}
}

// TestWASMInstFunctionBody covers the code-section entry shape:
// uleb(size) + locals_vec + body + 0x0B. Two cases: an empty-
// locals body that just returns a constant, and a one-group
// locals body to exercise put_locals_one_group.
func TestWASMInstFunctionBody(t *testing.T) {
	src := `import "std/wasm/inst";
import "std/wasm/encode";
function main(): i32 {
    var empty: u8[] = [];

    // Build body: i32.const 42 (no locals).
    var body: u8[] = inst.inst_i32_const(empty, 42);

    var locals: u8[] = inst.put_locals_empty(empty);

    var entry: u8[] = inst.put_function_body(empty, locals, body);

    // Expected inner bytes: 0x00 (locals=0), 0x41 0x2A (i32.const 42),
    // 0x0B (end). Wrapped: 0x04 (size=4) + the 4 inner bytes.
    if (entry.len() != 5) { return 1; }
    if (entry[0] != 4u8) { return 2; }     // size
    if (entry[1] != 0u8) { return 3; }     // locals_vec(0)
    if (entry[2] != 65u8) { return 4; }    // i32.const
    if (entry[3] != 42u8) { return 5; }    // 42
    if (entry[4] != 11u8) { return 6; }    // end

    // Locals: one group of 3 i32s.
    var locals3: u8[] = inst.put_locals_one_group(empty, 3u32, encode.valtype_i32());
    if (locals3.len() != 3) { return 10; }
    if (locals3[0] != 1u8) { return 11; }      // num groups
    if (locals3[1] != 3u8) { return 12; }      // count
    if (locals3[2] != 127u8) { return 13; }    // i32 valtype

    // Wrap a body with those locals.
    var body2: u8[] = inst.inst_local_get(empty, 0u32);  // 0x20 0x00
    body2 = inst.inst_return(body2);                     // 0x0F
    var entry2: u8[] = inst.put_function_body(empty, locals3, body2);
    // Inner: locals(3 bytes) + body(3 bytes) + end(1) = 7 bytes.
    // Wrapped: size_uleb(7) = 0x07, then the 7 inner bytes.
    if (entry2.len() != 8) { return 20; }
    if (entry2[0] != 7u8) { return 21; }      // size
    if (entry2[1] != 1u8) { return 22; }      // locals num groups
    if (entry2[2] != 3u8) { return 23; }      // group count
    if (entry2[3] != 127u8) { return 24; }    // i32 valtype
    if (entry2[4] != 32u8) { return 25; }     // local.get
    if (entry2[5] != 0u8) { return 26; }      // idx 0
    if (entry2[6] != 15u8) { return 27; }     // return
    if (entry2[7] != 11u8) { return 28; }     // end

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("inst function body: exit = %d, want 0", got)
	}
}

// TestWASMNumericI32 spot-checks the i32 numeric opcodes: a
// sample of each family (unary, compare, arithmetic, bitwise,
// shift, rotate). The opcode table is large but mechanical, so
// this catches typos in the constants without spelling out all
// 33 i32 entries.
func TestWASMNumericI32(t *testing.T) {
	src := `import "std/wasm/numeric";
function main(): i32 {
    var e: u8[] = [];

    // Unary
    if (numeric.inst_i32_clz(e)[0]    != 103u8) { return 1; }
    if (numeric.inst_i32_ctz(e)[0]    != 104u8) { return 2; }
    if (numeric.inst_i32_popcnt(e)[0] != 105u8) { return 3; }
    if (numeric.inst_i32_eqz(e)[0]    != 69u8)  { return 4; }

    // Compare (spread across the i32 compare block 0x46-0x4F).
    if (numeric.inst_i32_eq(e)[0]   != 70u8) { return 10; }
    if (numeric.inst_i32_ne(e)[0]   != 71u8) { return 11; }
    if (numeric.inst_i32_lt_s(e)[0] != 72u8) { return 12; }
    if (numeric.inst_i32_lt_u(e)[0] != 73u8) { return 13; }
    if (numeric.inst_i32_ge_u(e)[0] != 79u8) { return 14; }

    // Arithmetic (0x6A-0x70).
    if (numeric.inst_i32_add(e)[0]   != 106u8) { return 20; }
    if (numeric.inst_i32_sub(e)[0]   != 107u8) { return 21; }
    if (numeric.inst_i32_mul(e)[0]   != 108u8) { return 22; }
    if (numeric.inst_i32_div_s(e)[0] != 109u8) { return 23; }
    if (numeric.inst_i32_rem_u(e)[0] != 112u8) { return 24; }

    // Bitwise (0x71-0x73).
    if (numeric.inst_i32_and(e)[0] != 113u8) { return 30; }
    if (numeric.inst_i32_or(e)[0]  != 114u8) { return 31; }
    if (numeric.inst_i32_xor(e)[0] != 115u8) { return 32; }

    // Shift / rotate (0x74-0x78).
    if (numeric.inst_i32_shl(e)[0]   != 116u8) { return 40; }
    if (numeric.inst_i32_shr_s(e)[0] != 117u8) { return 41; }
    if (numeric.inst_i32_shr_u(e)[0] != 118u8) { return 42; }
    if (numeric.inst_i32_rotl(e)[0]  != 119u8) { return 43; }
    if (numeric.inst_i32_rotr(e)[0]  != 120u8) { return 44; }

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("numeric i32: exit = %d, want 0", got)
	}
}

// TestWASMNumericI64 spot-checks the i64 numeric opcodes. The
// i64 table mirrors i32's shape but at a different offset (i64
// compares are 0x51-0x5A, arithmetic / bitwise / shift are
// 0x7C-0x8A), so it's worth verifying both blocks separately —
// a transposed cell would still pass i32-only tests.
func TestWASMNumericI64(t *testing.T) {
	src := `import "std/wasm/numeric";
function main(): i32 {
    var e: u8[] = [];

    // Unary + eqz (eqz is in the i32-eqz neighbourhood at 0x50).
    if (numeric.inst_i64_eqz(e)[0]    != 80u8)  { return 1; }
    if (numeric.inst_i64_clz(e)[0]    != 121u8) { return 2; }
    if (numeric.inst_i64_ctz(e)[0]    != 122u8) { return 3; }
    if (numeric.inst_i64_popcnt(e)[0] != 123u8) { return 4; }

    // Compare block (0x51-0x5A).
    if (numeric.inst_i64_eq(e)[0]   != 81u8) { return 10; }
    if (numeric.inst_i64_lt_s(e)[0] != 83u8) { return 11; }
    if (numeric.inst_i64_ge_u(e)[0] != 90u8) { return 12; }

    // Arithmetic block (0x7C-0x82).
    if (numeric.inst_i64_add(e)[0]   != 124u8) { return 20; }
    if (numeric.inst_i64_sub(e)[0]   != 125u8) { return 21; }
    if (numeric.inst_i64_mul(e)[0]   != 126u8) { return 22; }
    if (numeric.inst_i64_div_s(e)[0] != 127u8) { return 23; }
    if (numeric.inst_i64_rem_u(e)[0] != 130u8) { return 24; }

    // Bitwise / shift / rotate (0x83-0x8A).
    if (numeric.inst_i64_and(e)[0]   != 131u8) { return 30; }
    if (numeric.inst_i64_shl(e)[0]   != 134u8) { return 31; }
    if (numeric.inst_i64_shr_s(e)[0] != 135u8) { return 32; }
    if (numeric.inst_i64_shr_u(e)[0] != 136u8) { return 33; }
    if (numeric.inst_i64_rotr(e)[0]  != 138u8) { return 34; }

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("numeric i64: exit = %d, want 0", got)
	}
}

// TestWASMNumericFloat spot-checks f32 + f64. The floating-point
// opcodes live in two interleaved blocks: compares at 0x5B-0x66
// (f32 first, then f64) and unary / binary ops at 0x8B-0xA6
// (again f32 first, then f64). A swapped row would survive the
// integer-only tests, so this one walks both float widths.
func TestWASMNumericFloat(t *testing.T) {
	src := `import "std/wasm/numeric";
function main(): i32 {
    var e: u8[] = [];

    // f32 compare (0x5B-0x60).
    if (numeric.inst_f32_eq(e)[0] != 91u8) { return 1; }
    if (numeric.inst_f32_lt(e)[0] != 93u8) { return 2; }
    if (numeric.inst_f32_ge(e)[0] != 96u8) { return 3; }

    // f64 compare (0x61-0x66).
    if (numeric.inst_f64_eq(e)[0] != 97u8)  { return 10; }
    if (numeric.inst_f64_lt(e)[0] != 99u8)  { return 11; }
    if (numeric.inst_f64_ge(e)[0] != 102u8) { return 12; }

    // f32 unary + binary (0x8B-0x98).
    if (numeric.inst_f32_abs(e)[0]      != 139u8) { return 20; }
    if (numeric.inst_f32_neg(e)[0]      != 140u8) { return 21; }
    if (numeric.inst_f32_sqrt(e)[0]     != 145u8) { return 22; }
    if (numeric.inst_f32_add(e)[0]      != 146u8) { return 23; }
    if (numeric.inst_f32_div(e)[0]      != 149u8) { return 24; }
    if (numeric.inst_f32_min(e)[0]      != 150u8) { return 25; }
    if (numeric.inst_f32_copysign(e)[0] != 152u8) { return 26; }

    // f64 unary + binary (0x99-0xA6).
    if (numeric.inst_f64_abs(e)[0]      != 153u8) { return 30; }
    if (numeric.inst_f64_neg(e)[0]      != 154u8) { return 31; }
    if (numeric.inst_f64_sqrt(e)[0]     != 159u8) { return 32; }
    if (numeric.inst_f64_add(e)[0]      != 160u8) { return 33; }
    if (numeric.inst_f64_div(e)[0]      != 163u8) { return 34; }
    if (numeric.inst_f64_max(e)[0]      != 165u8) { return 35; }
    if (numeric.inst_f64_copysign(e)[0] != 166u8) { return 36; }

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("numeric float: exit = %d, want 0", got)
	}
}

// TestWASMNumericCompose verifies a real-world body shape:
// local.get 0 ; local.get 1 ; i32.add — the canonical
// "function that adds two i32s" body. A typo in any of the
// three opcode tables would surface here as a mismatched byte.
func TestWASMNumericCompose(t *testing.T) {
	src := `import "std/wasm/inst";
import "std/wasm/numeric";
function main(): i32 {
    var body: u8[] = [];
    body = inst.inst_local_get(body, 0u32);
    body = inst.inst_local_get(body, 1u32);
    body = numeric.inst_i32_add(body);

    // Expected: [0x20, 0x00, 0x20, 0x01, 0x6A] = 5 bytes.
    if (body.len() != 5) { return 1; }
    if (body[0] != 32u8)  { return 2; }    // local.get
    if (body[1] != 0u8)   { return 3; }
    if (body[2] != 32u8)  { return 4; }    // local.get
    if (body[3] != 1u8)   { return 5; }
    if (body[4] != 106u8) { return 6; }    // i32.add

    // A second composite: local.get 0 ; i32.eqz ; br_if 0
    // — the canonical "bail out early when arg is zero" shape.
    var body2: u8[] = [];
    body2 = inst.inst_local_get(body2, 0u32);
    body2 = numeric.inst_i32_eqz(body2);
    body2 = inst.inst_br_if(body2, 0u32);
    if (body2.len() != 5) { return 10; }
    if (body2[0] != 32u8) { return 11; }   // local.get
    if (body2[1] != 0u8)  { return 12; }
    if (body2[2] != 69u8) { return 13; }   // i32.eqz
    if (body2[3] != 13u8) { return 14; }   // br_if
    if (body2[4] != 0u8)  { return 15; }

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("numeric compose: exit = %d, want 0", got)
	}
}

// TestWASMMemoryLoad covers the memory-load opcodes plus the
// "memarg" immediate shape (uleb align, uleb offset). align is
// log2 of byte alignment; the i32-wide load uses align=2 (= 4
// bytes), the i64-wide load uses align=3 (= 8 bytes), narrow
// loads use 0 / 1.
func TestWASMMemoryLoad(t *testing.T) {
	src := `import "std/wasm/memory";
function main(): i32 {
    var e: u8[] = [];

    // i32.load align=2 offset=0 -> 0x28 0x02 0x00
    var l32: u8[] = memory.inst_i32_load(e, 2u32, 0u32);
    if (l32.len() != 3)       { return 1; }
    if (l32[0] != 40u8)      { return 2; }
    if (l32[1] != 2u8)       { return 3; }
    if (l32[2] != 0u8)       { return 4; }

    // i64.load align=3 offset=128 -> 0x29 0x03 0x80 0x01 (uleb 128)
    var l64: u8[] = memory.inst_i64_load(e, 3u32, 128u32);
    if (l64.len() != 4)       { return 10; }
    if (l64[0] != 41u8)      { return 11; }
    if (l64[1] != 3u8)       { return 12; }
    if (l64[2] != 128u8)     { return 13; }
    if (l64[3] != 1u8)       { return 14; }

    // i32.load8_u align=0 offset=0 -> 0x2D 0x00 0x00
    var lu8: u8[] = memory.inst_i32_load8_u(e, 0u32, 0u32);
    if (lu8[0] != 45u8)      { return 20; }
    if (lu8[1] != 0u8)       { return 21; }
    if (lu8[2] != 0u8)       { return 22; }

    // f32.load -> 0x2A, f64.load -> 0x2B.
    if (memory.inst_f32_load(e, 2u32, 0u32)[0] != 42u8) { return 30; }
    if (memory.inst_f64_load(e, 3u32, 0u32)[0] != 43u8) { return 31; }

    // Walk the narrow-load block (0x2C-0x35) to catch a
    // transposed cell.
    if (memory.inst_i32_load8_s(e, 0u32, 0u32)[0]  != 44u8) { return 40; }
    if (memory.inst_i32_load16_s(e, 1u32, 0u32)[0] != 46u8) { return 41; }
    if (memory.inst_i32_load16_u(e, 1u32, 0u32)[0] != 47u8) { return 42; }
    if (memory.inst_i64_load8_s(e, 0u32, 0u32)[0]  != 48u8) { return 43; }
    if (memory.inst_i64_load16_s(e, 1u32, 0u32)[0] != 50u8) { return 44; }
    if (memory.inst_i64_load32_u(e, 2u32, 0u32)[0] != 53u8) { return 45; }

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("memory load: exit = %d, want 0", got)
	}
}

// TestWASMMemoryStore covers the memory-store opcodes. Same
// memarg shape as loads. The narrow-store variants are integer-
// only (no f32.store8 etc) so we walk the 0x36-0x3E range.
func TestWASMMemoryStore(t *testing.T) {
	src := `import "std/wasm/memory";
function main(): i32 {
    var e: u8[] = [];

    // i32.store align=2 offset=4 -> 0x36 0x02 0x04
    var s32: u8[] = memory.inst_i32_store(e, 2u32, 4u32);
    if (s32.len() != 3)  { return 1; }
    if (s32[0] != 54u8) { return 2; }
    if (s32[1] != 2u8)  { return 3; }
    if (s32[2] != 4u8)  { return 4; }

    // i64.store -> 0x37
    if (memory.inst_i64_store(e, 3u32, 0u32)[0] != 55u8) { return 10; }
    // f32.store -> 0x38, f64.store -> 0x39
    if (memory.inst_f32_store(e, 2u32, 0u32)[0] != 56u8) { return 11; }
    if (memory.inst_f64_store(e, 3u32, 0u32)[0] != 57u8) { return 12; }

    // Walk the narrow-store block.
    if (memory.inst_i32_store8(e, 0u32, 0u32)[0]  != 58u8) { return 20; }
    if (memory.inst_i32_store16(e, 1u32, 0u32)[0] != 59u8) { return 21; }
    if (memory.inst_i64_store8(e, 0u32, 0u32)[0]  != 60u8) { return 22; }
    if (memory.inst_i64_store16(e, 1u32, 0u32)[0] != 61u8) { return 23; }
    if (memory.inst_i64_store32(e, 2u32, 0u32)[0] != 62u8) { return 24; }

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("memory store: exit = %d, want 0", got)
	}
}

// TestWASMMemorySizeGrow covers memory.size / memory.grow. Both
// carry a reserved memidx byte (always 0 in MVP wasm) so each
// encoding is exactly 2 bytes regardless of any other immediate.
func TestWASMMemorySizeGrow(t *testing.T) {
	src := `import "std/wasm/memory";
function main(): i32 {
    var e: u8[] = [];

    var sz: u8[] = memory.inst_memory_size(e);
    if (sz.len() != 2)  { return 1; }
    if (sz[0] != 63u8) { return 2; }
    if (sz[1] != 0u8)  { return 3; }

    var gr: u8[] = memory.inst_memory_grow(e);
    if (gr.len() != 2)  { return 10; }
    if (gr[0] != 64u8) { return 11; }
    if (gr[1] != 0u8)  { return 12; }

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("memory size/grow: exit = %d, want 0", got)
	}
}

// TestWASMMemoryBulk covers the two 0xFC-prefixed bulk-memory ops
// the production backend emits for its memcpy / memset helpers:
// memory.copy (0xFC 0x0A + two reserved memidx bytes) and
// memory.fill (0xFC 0x0B + one). These are the first multi-byte
// opcodes in memory.fern, so the test walks every byte of each.
func TestWASMMemoryBulk(t *testing.T) {
	src := `import "std/wasm/memory";
function main(): i32 {
    var e: u8[] = [];

    var cp: u8[] = memory.inst_memory_copy(e);
    if (cp.len() != 4)  { return 1; }
    if (cp[0] != 252u8) { return 2; }   // 0xFC prefix
    if (cp[1] != 10u8)  { return 3; }   // 0x0A memory.copy
    if (cp[2] != 0u8)   { return 4; }   // dst memidx
    if (cp[3] != 0u8)   { return 5; }   // src memidx

    var fl: u8[] = memory.inst_memory_fill(e);
    if (fl.len() != 3)  { return 10; }
    if (fl[0] != 252u8) { return 11; }  // 0xFC prefix
    if (fl[1] != 11u8)  { return 12; }  // 0x0B memory.fill
    if (fl[2] != 0u8)   { return 13; }  // memidx

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("memory bulk copy/fill: exit = %d, want 0", got)
	}
}

// TestWASMConvertIntWidth covers integer-width conversion +
// reinterpret. The reinterpret variants share the 0xBC-0xBF
// neighbourhood with the convert ops; walking both catches a
// transposed cell in either table.
func TestWASMConvertIntWidth(t *testing.T) {
	src := `import "std/wasm/convert";
function main(): i32 {
    var e: u8[] = [];

    // Integer width
    if (convert.inst_i32_wrap_i64(e)[0]     != 167u8) { return 1; }
    if (convert.inst_i64_extend_i32_s(e)[0] != 172u8) { return 2; }
    if (convert.inst_i64_extend_i32_u(e)[0] != 173u8) { return 3; }

    // Sign-extension extension (post-MVP, universally supported).
    if (convert.inst_i32_extend8_s(e)[0]  != 192u8) { return 10; }
    if (convert.inst_i32_extend16_s(e)[0] != 193u8) { return 11; }
    if (convert.inst_i64_extend8_s(e)[0]  != 194u8) { return 12; }
    if (convert.inst_i64_extend16_s(e)[0] != 195u8) { return 13; }
    if (convert.inst_i64_extend32_s(e)[0] != 196u8) { return 14; }

    // Reinterpret — same bits, new type label.
    if (convert.inst_i32_reinterpret_f32(e)[0] != 188u8) { return 20; }
    if (convert.inst_i64_reinterpret_f64(e)[0] != 189u8) { return 21; }
    if (convert.inst_f32_reinterpret_i32(e)[0] != 190u8) { return 22; }
    if (convert.inst_f64_reinterpret_i64(e)[0] != 191u8) { return 23; }

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("convert int/reinterpret: exit = %d, want 0", got)
	}
}

// TestWASMConvertFloatInt covers the four int/float crossover
// blocks (trunc / convert) plus the float-width conversions
// (demote / promote). 16 trunc+convert variants in two interleaved
// tables — easy to mis-row, so this walks each one.
func TestWASMConvertFloatInt(t *testing.T) {
	src := `import "std/wasm/convert";
function main(): i32 {
    var e: u8[] = [];

    // Float -> integer (trapping): 0xA8-0xAB then 0xAE-0xB1.
    if (convert.inst_i32_trunc_f32_s(e)[0] != 168u8) { return 1; }
    if (convert.inst_i32_trunc_f32_u(e)[0] != 169u8) { return 2; }
    if (convert.inst_i32_trunc_f64_s(e)[0] != 170u8) { return 3; }
    if (convert.inst_i32_trunc_f64_u(e)[0] != 171u8) { return 4; }
    if (convert.inst_i64_trunc_f32_s(e)[0] != 174u8) { return 5; }
    if (convert.inst_i64_trunc_f32_u(e)[0] != 175u8) { return 6; }
    if (convert.inst_i64_trunc_f64_s(e)[0] != 176u8) { return 7; }
    if (convert.inst_i64_trunc_f64_u(e)[0] != 177u8) { return 8; }

    // Integer -> float: 0xB2-0xB5 then 0xB7-0xBA.
    if (convert.inst_f32_convert_i32_s(e)[0] != 178u8) { return 10; }
    if (convert.inst_f32_convert_i32_u(e)[0] != 179u8) { return 11; }
    if (convert.inst_f32_convert_i64_s(e)[0] != 180u8) { return 12; }
    if (convert.inst_f32_convert_i64_u(e)[0] != 181u8) { return 13; }
    if (convert.inst_f64_convert_i32_s(e)[0] != 183u8) { return 14; }
    if (convert.inst_f64_convert_i32_u(e)[0] != 184u8) { return 15; }
    if (convert.inst_f64_convert_i64_s(e)[0] != 185u8) { return 16; }
    if (convert.inst_f64_convert_i64_u(e)[0] != 186u8) { return 17; }

    // Float width: 0xB6 / 0xBB straddle the convert block.
    if (convert.inst_f32_demote_f64(e)[0]  != 182u8) { return 20; }
    if (convert.inst_f64_promote_f32(e)[0] != 187u8) { return 21; }

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("convert float/int: exit = %d, want 0", got)
	}
}

// TestWASMSectionsFunction covers encode_function_section, the
// simplest section composer — body is just a vec(typeidx).
// Walks a 0-entry case, a 1-entry case, and a multi-byte uleb
// typeidx (128) to verify the leb path inside the loop.
func TestWASMSectionsFunction(t *testing.T) {
	src := `import "std/wasm/sections";
function main(): i32 {
    var empty: u8[] = [];

    // No functions: section id=3, size=1, body=[0x00] (vec count 0).
    var noidxs: u32[] = [];
    var s0: u8[] = sections.encode_function_section(empty, noidxs);
    if (s0.len() != 3) { return 1; }
    if (s0[0] != 3u8) { return 2; }
    if (s0[1] != 1u8) { return 3; }
    if (s0[2] != 0u8) { return 4; }

    // Three functions with typeidxs 0, 1, 2 -> body [0x03, 0x00,
    // 0x01, 0x02]. Wrapped: [0x03, 0x04, 0x03, 0x00, 0x01, 0x02].
    var idxs: u32[] = [0u32, 1u32, 2u32];
    var s3: u8[] = sections.encode_function_section(empty, idxs);
    if (s3.len() != 6) { return 10; }
    if (s3[0] != 3u8) { return 11; }    // section_function id
    if (s3[1] != 4u8) { return 12; }    // size = 4
    if (s3[2] != 3u8) { return 13; }    // vec count = 3
    if (s3[3] != 0u8) { return 14; }
    if (s3[4] != 1u8) { return 15; }
    if (s3[5] != 2u8) { return 16; }

    // Multi-byte typeidx (128 = uleb 0x80 0x01) -> body is
    // count=1 + 0x80 0x01 = 3 bytes; wrapped = 5 bytes.
    var big: u32[] = [128u32];
    var s4: u8[] = sections.encode_function_section(empty, big);
    if (s4.len() != 5)   { return 20; }
    if (s4[0] != 3u8)   { return 21; }
    if (s4[1] != 3u8)   { return 22; }    // body size 3
    if (s4[2] != 1u8)   { return 23; }    // vec count 1
    if (s4[3] != 128u8) { return 24; }    // typeidx low byte
    if (s4[4] != 1u8)   { return 25; }    // typeidx high byte

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("sections function: exit = %d, want 0", got)
	}
}

// TestWASMSectionsStartMemory covers encode_start_section (single
// funcidx body) and encode_memory_section in both flag=0 (no max)
// and flag=1 (with max) cases.
func TestWASMSectionsStartMemory(t *testing.T) {
	src := `import "std/wasm/sections";
function main(): i32 {
    var empty: u8[] = [];

    // start: id=8, size=1, body=[funcidx]. funcidx=0 -> [0x08, 0x01, 0x00].
    var ss: u8[] = sections.encode_start_section(empty, 0u32);
    if (ss.len() != 3) { return 1; }
    if (ss[0] != 8u8) { return 2; }
    if (ss[1] != 1u8) { return 3; }
    if (ss[2] != 0u8) { return 4; }

    // memory, no max: id=5, body=[count=1, flag=0, min=1]
    // -> wrapped 5 bytes.
    var mNoMax: u8[] = sections.encode_memory_section(empty, 1u32, 0 - 1);
    if (mNoMax.len() != 5) { return 10; }
    if (mNoMax[0] != 5u8) { return 11; }   // section_memory id
    if (mNoMax[1] != 3u8) { return 12; }   // size = 3
    if (mNoMax[2] != 1u8) { return 13; }   // count = 1
    if (mNoMax[3] != 0u8) { return 14; }   // flag = no max
    if (mNoMax[4] != 1u8) { return 15; }   // min = 1

    // memory, with max: id=5, body=[count=1, flag=1, min=1, max=2]
    // -> wrapped 6 bytes.
    var mWithMax: u8[] = sections.encode_memory_section(empty, 1u32, 2);
    if (mWithMax.len() != 6) { return 20; }
    if (mWithMax[0] != 5u8) { return 21; }
    if (mWithMax[1] != 4u8) { return 22; }   // size = 4
    if (mWithMax[2] != 1u8) { return 23; }   // count = 1
    if (mWithMax[3] != 1u8) { return 24; }   // flag = with max
    if (mWithMax[4] != 1u8) { return 25; }   // min
    if (mWithMax[5] != 2u8) { return 26; }   // max

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("sections start/memory: exit = %d, want 0", got)
	}
}

// TestWASMSectionsExport covers encode_export_section with two
// exports of different kinds, exercising the three parallel-array
// inputs together with put_name's uleb-prefixed UTF-8 encoding.
func TestWASMSectionsExport(t *testing.T) {
	src := `import "std/wasm/sections";
function main(): i32 {
    var empty: u8[] = [];

    // One func export named "main" -> 0 + 0x00 + 0
    // body: count=1, "main"=[4, 0x6D, 0x61, 0x69, 0x6E], kind=0, idx=0
    var nms: string[] = ["main"];
    var ks: u8[] = [sections.export_func()];
    var ixs: u32[] = [0u32];
    var s1: u8[] = sections.encode_export_section(empty, nms, ks, ixs);
    // Expected: [0x07, 0x08, 0x01, 0x04, 'm', 'a', 'i', 'n', 0x00, 0x00]
    // = 10 bytes.
    if (s1.len() != 10) { return 1; }
    if (s1[0] != 7u8)   { return 2; }    // section_export
    if (s1[1] != 8u8)   { return 3; }    // body size 8
    if (s1[2] != 1u8)   { return 4; }    // count
    if (s1[3] != 4u8)   { return 5; }    // name len
    if (s1[4] != 109u8) { return 6; }    // 'm'
    if (s1[5] != 97u8)  { return 7; }    // 'a'
    if (s1[6] != 105u8) { return 8; }    // 'i'
    if (s1[7] != 110u8) { return 9; }    // 'n'
    if (s1[8] != 0u8)   { return 10; }   // kind func
    if (s1[9] != 0u8)   { return 11; }   // idx 0

    // Two exports: ("memory", memory, 0) and ("g", global, 2).
    var nms2: string[] = ["memory", "g"];
    var ks2: u8[] = [sections.export_memory(), sections.export_global()];
    var ixs2: u32[] = [0u32, 2u32];
    var s2: u8[] = sections.encode_export_section(empty, nms2, ks2, ixs2);
    // Body: count=2,
    //   "memory"=[6, m,e,m,o,r,y], kind=2, idx=0     -> 9 bytes
    //   "g"=[1, g], kind=3, idx=2                    -> 4 bytes
    // total body = 1 + 9 + 4 = 14. Wrapped = 16 bytes.
    if (s2.len() != 16)  { return 20; }
    if (s2[0] != 7u8)   { return 21; }
    if (s2[1] != 14u8)  { return 22; }
    if (s2[2] != 2u8)   { return 23; }
    if (s2[3] != 6u8)   { return 24; }   // "memory" length
    // Skip "memory" content bytes; check kind + idx.
    if (s2[10] != 2u8)  { return 25; }   // kind memory
    if (s2[11] != 0u8)  { return 26; }   // idx 0
    if (s2[12] != 1u8)  { return 27; }   // "g" length
    if (s2[13] != 103u8) { return 28; }  // 'g'
    if (s2[14] != 3u8)  { return 29; }   // kind global
    if (s2[15] != 2u8)  { return 30; }   // idx 2

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("sections export: exit = %d, want 0", got)
	}
}

// TestWASMSectionsTypeCode walks the two sections that take "list
// of pre-encoded sub-objects" inputs: type section (vec of
// functypes) and code section (vec of pre-wrapped function
// bodies).
func TestWASMSectionsTypeCode(t *testing.T) {
	src := `import "std/wasm/inst";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    var empty: u8[] = [];

    // Type section with one (i32) -> (i32) functype.
    var p: u8[] = [encode.valtype_i32()];
    var r: u8[] = [encode.valtype_i32()];
    var pp: u8[][] = [p];
    var rr: u8[][] = [r];
    var ts: u8[] = sections.encode_type_section(empty, pp, rr);
    // Expected: id=1, size=6, body=[count=1, 0x60, 1, 0x7F, 1, 0x7F] = 8 bytes.
    if (ts.len() != 8) { return 1; }
    if (ts[0] != 1u8) { return 2; }
    if (ts[1] != 6u8) { return 3; }
    if (ts[2] != 1u8) { return 4; }
    if (ts[3] != 96u8) { return 5; }
    if (ts[4] != 1u8) { return 6; }
    if (ts[5] != 127u8) { return 7; }
    if (ts[6] != 1u8) { return 8; }
    if (ts[7] != 127u8) { return 9; }

    // Code section with one body: i32.const 42 returning. Use
    // put_function_body to wrap, then feed to encode_code_section.
    var bodyExpr: u8[] = inst.inst_i32_const(empty, 42);
    var locals: u8[] = inst.put_locals_empty(empty);
    var fn: u8[] = inst.put_function_body(empty, locals, bodyExpr);
    // fn = [size=4, 0x00 locals, 0x41 0x2A, 0x0B end] = 5 bytes.
    var bodies: u8[][] = [fn];
    var cs: u8[] = sections.encode_code_section(empty, bodies);
    // Section body: count=1 (1 byte) + fn (5 bytes) = 6 bytes.
    // Wrapped: id (1) + size_uleb (1) + body (6) = 8 bytes.
    if (cs.len() != 8)  { return 20; }
    if (cs[0] != 10u8) { return 21; }    // section_code id
    if (cs[1] != 6u8)  { return 22; }    // body size 6
    if (cs[2] != 1u8)  { return 23; }    // body count 1
    if (cs[3] != 4u8)  { return 24; }    // fn size (locals + expr + end)
    if (cs[4] != 0u8)  { return 25; }    // locals (empty)
    if (cs[5] != 65u8) { return 26; }    // i32.const
    if (cs[6] != 42u8) { return 27; }    // sleb 42
    if (cs[7] != 11u8) { return 28; }    // 0x0B end

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("sections type/code: exit = %d, want 0", got)
	}
}

// TestWASMSectionsData covers encode_data_section. The active-
// segment shape is: memidx (0) + offset expr (i32.const N ; end)
// + init bytes (uleb-prefixed). One segment in the test plus a
// parallel-array sanity-check with two segments.
func TestWASMSectionsData(t *testing.T) {
	src := `import "std/wasm/sections";
function main(): i32 {
    var empty: u8[] = [];

    // One segment at offset 50 with init bytes [0xAA, 0xBB].
    // Offset 50 (= 0x32) has bit-6 clear so sleb fits in 1 byte;
    // 100 would need 2 bytes (bit-6 set), which is a fine
    // boundary to bear in mind for the wider IR-walker tests.
    var offs: i32[] = [50];
    var initBytes: u8[] = [170u8, 187u8];
    var inits: u8[][] = [initBytes];
    var ds: u8[] = sections.encode_data_section(empty, offs, inits);
    // Segment bytes:
    //   memidx 0 (1 byte)
    //   i32.const 50 ; end -> 0x41 0x32 0x0B (3 bytes)
    //   init vec: 0x02 (count), 0xAA, 0xBB (3 bytes)
    // = 7 bytes per segment.
    // Section body: count=1 (1) + 7 = 8 bytes. Wrapped: id + size + body
    // = 1 + 1 + 8 = 10 bytes.
    if (ds.len() != 10) { return 1; }
    if (ds[0] != 11u8) { return 2; }    // section_data id
    if (ds[1] != 8u8)  { return 3; }    // body size 8
    if (ds[2] != 1u8)  { return 4; }    // segment count 1
    if (ds[3] != 0u8)  { return 5; }    // memidx 0
    if (ds[4] != 65u8) { return 6; }    // 0x41 i32.const
    if (ds[5] != 50u8) { return 7; }    // sleb 50 -> 0x32 (1 byte, bit-6 clear)
    if (ds[6] != 11u8) { return 8; }    // 0x0B end
    if (ds[7] != 2u8)  { return 9; }    // init bytes count
    if (ds[8] != 170u8){ return 10; }
    if (ds[9] != 187u8){ return 11; }

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("sections data: exit = %d, want 0", got)
	}
}

// TestWASMSectionsTableElement covers the two sections that make
// call_indirect usable: the table section (id 4 — one funcref
// table with limits) and the element section (id 9 — an active
// segment that initialises the table with funcidxs). Walks every
// byte of each so a transposed limits flag or a missing offset-
// expr terminator shows up precisely.
func TestWASMSectionsTableElement(t *testing.T) {
	src := `import "std/wasm/sections";
import "std/wasm/imports";
function main(): i32 {
    var e: u8[] = [];

    // Table: one funcref table, min 2, no max.
    var tbl: u8[] = sections.encode_table_section(e, imports.reftype_funcref(), 2u32, 0 - 1);
    // [id=4, size=4, count=1, reftype=0x70, flag=0, min=2]
    if (tbl.len() != 6)  { return 1; }
    if (tbl[0] != 4u8)   { return 2; }   // section_table id
    if (tbl[1] != 4u8)   { return 3; }   // body size 4
    if (tbl[2] != 1u8)   { return 4; }   // one table
    if (tbl[3] != 112u8) { return 5; }   // funcref 0x70
    if (tbl[4] != 0u8)   { return 6; }   // limits flag: no max
    if (tbl[5] != 2u8)   { return 7; }   // min 2

    // Element: one active segment at offset 0, funcidxs [0, 1].
    var offs: i32[] = [0];
    var fids: u32[][] = [[0u32, 1u32]];
    var el: u8[] = sections.encode_element_section(e, offs, fids);
    // [id=9, size=8, count=1, flag=0, 0x41, off=0, 0x0B, veclen=2, 0, 1]
    if (el.len() != 10)  { return 20; }
    if (el[0] != 9u8)    { return 21; }  // section_element id
    if (el[1] != 8u8)    { return 22; }  // body size 8
    if (el[2] != 1u8)    { return 23; }  // one segment
    if (el[3] != 0u8)    { return 24; }  // flag 0: active, table 0
    if (el[4] != 65u8)   { return 25; }  // 0x41 i32.const
    if (el[5] != 0u8)    { return 26; }  // offset 0
    if (el[6] != 11u8)   { return 27; }  // 0x0B end
    if (el[7] != 2u8)    { return 28; }  // funcidx vec len 2
    if (el[8] != 0u8)    { return 29; }  // funcidx 0
    if (el[9] != 1u8)    { return 30; }  // funcidx 1

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("table/element sections: exit = %d, want 0", got)
	}
}

// TestWASMImportsFunc covers the import section's most common
// shape — a function import like the WASI calls the existing
// wasm backend declares. Vectors: one function import with
// short module+name, and a two-import case with mixed kinds.
func TestWASMImportsFunc(t *testing.T) {
	src := `import "std/wasm/imports";
import "std/wasm/encode";
function main(): i32 {
    var empty: u8[] = [];

    // One function import: ("env", "log", func, typeidx=0).
    var mods: string[] = ["env"];
    var nms: string[] = ["log"];
    var ks: u8[] = [imports.import_func()];
    var descs: u8[][] = [imports.import_desc_func(0u32)];
    var s1: u8[] = imports.encode_import_section(empty, mods, nms, ks, descs);
    // Body:
    //   count=1 (1)
    //   "env" = [3, e, n, v] (4)
    //   "log" = [3, l, o, g] (4)
    //   kind=0x00 (1)
    //   desc = uleb(0) = [0] (1)
    // = 11 bytes. Wrapped: id(1) + size(1) + body(11) = 13 bytes.
    if (s1.len() != 13) { return 1; }
    if (s1[0] != 2u8)  { return 2; }     // section_import id
    if (s1[1] != 11u8) { return 3; }     // body size 11
    if (s1[2] != 1u8)  { return 4; }     // count 1
    if (s1[3] != 3u8)  { return 5; }     // "env" len
    if (s1[4] != 101u8) { return 6; }    // 'e'
    if (s1[5] != 110u8) { return 7; }    // 'n'
    if (s1[6] != 118u8) { return 8; }    // 'v'
    if (s1[7] != 3u8)   { return 9; }    // "log" len
    if (s1[11] != 0u8)  { return 10; }   // kind func
    if (s1[12] != 0u8)  { return 11; }   // typeidx 0

    // Two imports of different kinds: ("wasi", "fd_write", func, 7)
    // and ("env", "g", global, i32 mut).
    var mods2: string[] = ["wasi", "env"];
    var nms2: string[] = ["fd_write", "g"];
    var ks2: u8[] = [imports.import_func(), imports.import_global()];
    var d_func: u8[] = imports.import_desc_func(7u32);
    var d_glob: u8[] = imports.import_desc_global(encode.valtype_i32(), imports.mut_var());
    var descs2: u8[][] = [d_func, d_glob];
    var s2: u8[] = imports.encode_import_section(empty, mods2, nms2, ks2, descs2);
    // Spot-check the global descriptor at the tail: valtype + mut
    // are the last two bytes.
    if (s2.len() < 20) { return 20; }
    if (s2[0] != 2u8) { return 21; }
    var last: i32 = s2.len() - 1;
    if (s2[last] != 1u8)       { return 22; }   // mut_var
    if (s2[last - 1] != 127u8) { return 23; }   // valtype_i32

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("imports func: exit = %d, want 0", got)
	}
}

// TestWASMImportsDescBuilders walks the four import_desc_*
// builders in isolation, verifying each produces the spec-
// reference byte sequence.
func TestWASMImportsDescBuilders(t *testing.T) {
	src := `import "std/wasm/imports";
import "std/wasm/encode";
function main(): i32 {
    // import_desc_func(typeidx=5) -> [0x05]
    var df: u8[] = imports.import_desc_func(5u32);
    if (df.len() != 1) { return 1; }
    if (df[0] != 5u8) { return 2; }

    // import_desc_global(i32, const) -> [0x7F, 0x00]
    var dg: u8[] = imports.import_desc_global(encode.valtype_i32(), imports.mut_const());
    if (dg.len() != 2)    { return 10; }
    if (dg[0] != 127u8)  { return 11; }
    if (dg[1] != 0u8)    { return 12; }

    // import_desc_memory(min=1, no max) -> [0x00, 0x01]
    var dm1: u8[] = imports.import_desc_memory(1u32, 0 - 1);
    if (dm1.len() != 2) { return 20; }
    if (dm1[0] != 0u8) { return 21; }
    if (dm1[1] != 1u8) { return 22; }

    // import_desc_memory(min=1, max=2) -> [0x01, 0x01, 0x02]
    var dm2: u8[] = imports.import_desc_memory(1u32, 2);
    if (dm2.len() != 3) { return 30; }
    if (dm2[0] != 1u8) { return 31; }
    if (dm2[1] != 1u8) { return 32; }
    if (dm2[2] != 2u8) { return 33; }

    // import_desc_table(funcref, min=0, no max) -> [0x70, 0x00, 0x00]
    var dt: u8[] = imports.import_desc_table(imports.reftype_funcref(), 0u32, 0 - 1);
    if (dt.len() != 3)   { return 40; }
    if (dt[0] != 112u8) { return 41; }   // funcref 0x70
    if (dt[1] != 0u8)   { return 42; }   // flag no max
    if (dt[2] != 0u8)   { return 43; }   // min 0

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("imports desc builders: exit = %d, want 0", got)
	}
}

// TestWASMImportsGlobalSection covers encode_global_section.
// Globaltype is `valtype byte + mut byte` and each entry carries
// an init_expr that the caller pre-composes (typical shape:
// i32.const N ; end).
func TestWASMImportsGlobalSection(t *testing.T) {
	src := `import "std/wasm/imports";
import "std/wasm/encode";
import "std/wasm/inst";
function main(): i32 {
    var empty: u8[] = [];

    // One mutable i32 global, initialized to 0.
    // init_expr: i32.const 0 ; end = [0x41, 0x00, 0x0B] (3 bytes)
    var init0: u8[] = inst.inst_i32_const(empty, 0);
    init0 = inst.inst_end(init0);

    var vts: u8[] = [encode.valtype_i32()];
    var ms: u8[] = [imports.mut_var()];
    var inits: u8[][] = [init0];
    var gs: u8[] = imports.encode_global_section(empty, vts, ms, inits);
    // Body: count=1 + valtype=0x7F + mut=0x01 + init(3) = 6 bytes
    // Wrapped: id(1) + size(1) + body(6) = 8 bytes
    if (gs.len() != 8) { return 1; }
    if (gs[0] != 6u8) { return 2; }    // section_global id
    if (gs[1] != 6u8) { return 3; }    // body size
    if (gs[2] != 1u8) { return 4; }    // count
    if (gs[3] != 127u8) { return 5; }  // valtype i32
    if (gs[4] != 1u8) { return 6; }    // mut_var
    if (gs[5] != 65u8) { return 7; }   // i32.const
    if (gs[6] != 0u8) { return 8; }    // sleb 0
    if (gs[7] != 11u8) { return 9; }   // end

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("imports global section: exit = %d, want 0", got)
	}
}

// TestWASMModuleEmpty: module_new() + build() with no sections
// populated should produce just the 8-byte preamble — magic
// (\0asm) + version 1.
func TestWASMModuleEmpty(t *testing.T) {
	src := `import "std/wasm/module";
function main(): i32 {
    var m: module.Module = module.module_new();
    var bytes: u8[] = module.build(m);
    if (bytes.len() != 8) { return 1; }
    if (bytes[0] != 0u8)   { return 2; }
    if (bytes[1] != 97u8)  { return 3; }    // 'a'
    if (bytes[2] != 115u8) { return 4; }    // 's'
    if (bytes[3] != 109u8) { return 5; }    // 'm'
    if (bytes[4] != 1u8)   { return 6; }    // version LE
    if (bytes[5] != 0u8)   { return 7; }
    if (bytes[6] != 0u8)   { return 8; }
    if (bytes[7] != 0u8)   { return 9; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("module empty: exit = %d, want 0", got)
	}
}

// TestWASMModuleMinimal builds a real "function returns 42" wasm
// module end-to-end using the Module struct + build(). The
// produced bytes go through every section composer at least once
// — type, function, export, code — and the test verifies the
// full byte sequence against a hand-computed reference.
//
// This is the strongest test in the std/wasm suite: a typo in
// any composer or any opcode encoder would surface here as a
// mismatched byte at a known offset.
func TestWASMModuleMinimal(t *testing.T) {
	src := `import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    var m: module.Module = module.module_new();

    // Type: one functype () -> i32.
    var p0: u8[] = [];
    var r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);

    // Function: one function with typeidx 0.
    m = m.with_functions([0u32]);

    // Export: ("main", func, 0).
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);

    // Code: body is i32.const 42 with no locals.
    var bodyExpr: u8[] = inst.inst_i32_const([], 42);
    var localsBytes: u8[] = inst.put_locals_empty([]);
    var fn: u8[] = inst.put_function_body([], localsBytes, bodyExpr);
    m = m.with_code([fn]);

    var bytes: u8[] = module.build(m);

    // Expected: 8 (preamble) + 7 (type) + 4 (function) + 10
    // (export) + 8 (code) = 37 bytes.
    if (bytes.len() != 37) { return 1; }

    // Preamble.
    if (bytes[0] != 0u8) { return 10; }
    if (bytes[3] != 109u8) { return 11; }   // 'm'
    if (bytes[4] != 1u8) { return 12; }     // version

    // Type section starts at offset 8.
    if (bytes[8] != 1u8) { return 20; }     // section_type id
    if (bytes[9] != 5u8) { return 21; }     // body size 5
    if (bytes[10] != 1u8) { return 22; }    // count 1
    if (bytes[11] != 96u8) { return 23; }   // functype tag
    if (bytes[12] != 0u8) { return 24; }    // param count 0
    if (bytes[13] != 1u8) { return 25; }    // result count 1
    if (bytes[14] != 127u8) { return 26; }  // i32

    // Function section starts at offset 15.
    if (bytes[15] != 3u8) { return 30; }    // section_function id
    if (bytes[16] != 2u8) { return 31; }    // body size 2
    if (bytes[17] != 1u8) { return 32; }    // count 1
    if (bytes[18] != 0u8) { return 33; }    // typeidx 0

    // Export section starts at offset 19.
    if (bytes[19] != 7u8) { return 40; }    // section_export id
    if (bytes[20] != 8u8) { return 41; }    // body size 8
    if (bytes[21] != 1u8) { return 42; }    // count 1
    if (bytes[22] != 4u8) { return 43; }    // name len 4
    if (bytes[23] != 109u8) { return 44; }  // 'm'
    if (bytes[24] != 97u8) { return 45; }   // 'a'
    if (bytes[25] != 105u8) { return 46; }  // 'i'
    if (bytes[26] != 110u8) { return 47; }  // 'n'
    if (bytes[27] != 0u8) { return 48; }    // kind func
    if (bytes[28] != 0u8) { return 49; }    // idx 0

    // Code section starts at offset 29.
    if (bytes[29] != 10u8) { return 50; }   // section_code id
    if (bytes[30] != 6u8) { return 51; }    // body size 6
    if (bytes[31] != 1u8) { return 52; }    // body count 1
    if (bytes[32] != 4u8) { return 53; }    // fn size
    if (bytes[33] != 0u8) { return 54; }    // locals empty
    if (bytes[34] != 65u8) { return 55; }   // i32.const
    if (bytes[35] != 42u8) { return 56; }   // 42
    if (bytes[36] != 11u8) { return 57; }   // end

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("module minimal: exit = %d, want 0", got)
	}
}

// TestWASMModuleSectionOrder verifies build() emits sections in
// the spec-required order by populating every section and
// scanning the output for the section IDs in sequence. The byte
// content of each section is exercised by the dedicated section
// tests; this test focuses on ordering.
func TestWASMModuleSectionOrder(t *testing.T) {
	src := `import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/encode";
import "std/wasm/sections";
import "std/wasm/imports";
function main(): i32 {
    var m: module.Module = module.module_new();

    // Type: one functype () -> ().
    var pe: u8[] = [];
    var re: u8[] = [];
    m = m.with_types([pe], [re]);

    // Import: one ("env", "h", func, typeidx=0).
    m = m.with_imports(["env"], ["h"], [imports.import_func()], [imports.import_desc_func(0u32)]);

    // Function: one with typeidx 0.
    m = m.with_functions([0u32]);

    // Memory: present, min=1.
    m = m.with_memory(1u32, 0 - 1);

    // Global: one const i32 = 0.
    var g0: u8[] = inst.inst_i32_const([], 0);
    g0 = inst.inst_end(g0);
    m = m.with_globals([encode.valtype_i32()], [imports.mut_const()], [g0]);

    // Export: ("e", memory, 0).
    m = m.with_exports(["e"], [sections.export_memory()], [0u32]);

    // Start: funcidx 1 (the imported func is 0, our func is 1).
    m = m.with_start(1u32);

    // Code: empty body — just the terminating 0x0B.
    var localsBytes: u8[] = inst.put_locals_empty([]);
    var emptyBody: u8[] = [];
    var fn: u8[] = inst.put_function_body([], localsBytes, emptyBody);
    m = m.with_code([fn]);

    // Data: one segment at offset 0 with byte 0xAB.
    m = m.with_data([0], [[171u8]]);

    var bytes: u8[] = module.build(m);

    // Scan for section IDs in order. Section IDs the spec assigns:
    // type=1, import=2, function=3, table=4, memory=5, global=6,
    // export=7, start=8, element=9, code=10, data=11.
    // We expect 1, 2, 3, 5, 6, 7, 8, 10, 11 (no table, no element).
    var expected_ids: u8[] = [1u8, 2u8, 3u8, 5u8, 6u8, 7u8, 8u8, 10u8, 11u8];

    // Walk the byte stream: byte 8 is the first section id; after
    // each id comes uleb size + that many body bytes; repeat.
    var pos: i32 = 8;  // skip preamble
    var idx: i32 = 0;
    while (idx < expected_ids.len()) {
        if (pos >= bytes.len()) { return 100 + idx; }
        if (bytes[pos] != expected_ids[idx]) { return 1 + idx; }
        pos = pos + 1;
        // Read uleb size — small enough that bit-7 is clear here.
        var sz: i32 = bytes[pos] as i32;
        if (sz >= 128) { return 200 + idx; }    // would need multi-byte uleb decoder
        pos = pos + 1;
        pos = pos + sz;
        idx = idx + 1;
    }

    // After the last section, pos should be at the end of bytes.
    if (pos != bytes.len()) { return 50; }

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("module section order: exit = %d, want 0", got)
	}
}

// TestWASMModuleValidatesUnderWasmTools is the end-to-end
// correctness gate for the std/wasm encoder stack: a Lang
// program uses std/wasm/module.build to produce the bytes of a
// "function returning 42" core wasm module, prints those bytes
// to stdout as space-separated decimals, and the Go side parses
// them back, writes them to disk, and pipes the file through
// `wasm-tools validate`. If our encoder produces a structurally
// invalid wasm module, validate fails with a precise diagnostic
// — much stronger than any byte-vector test in isolation.
//
// We additionally pipe the file through `wasm-tools print` and
// check the output mentions both `(type` (so the type section
// round-trips) and `i32.const 42` (so the code section's body
// is intact). That covers the "validate passes but the content
// is garbage" failure mode.
func TestWASMModuleValidatesUnderWasmTools(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    var m: module.Module = module.module_new();

    // Type: one functype () -> i32.
    var p0: u8[] = [];
    var r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);

    // Function: one function with typeidx 0.
    m = m.with_functions([0u32]);

    // Export: ("main", func, 0). wasm-tools print uses the
    // export to label the function in its output.
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);

    // Code: body is i32.const 42 with no locals.
    var bodyExpr: u8[] = inst.inst_i32_const([], 42);
    var localsBytes: u8[] = inst.put_locals_empty([]);
    var fn: u8[] = inst.put_function_body([], localsBytes, bodyExpr);
    m = m.with_code([fn]);

    var bytes: u8[] = module.build(m);

    var output: string = "";
    var i: i32 = 0;
    while (i < bytes.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (bytes[i] as i32).to_string();
        i = i + 1;
    }
    // Terminate with a newline so wasmtime's own trailing "0"
    // result line doesn't fuse onto our last byte's decimal.
    print(output);

    return 0;
}`
	out := runWasmCapturingStdout(t, src)
	out = strings.TrimSpace(out)
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bytes := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		if n < 0 || n > 255 {
			t.Fatalf("byte %d out of u8 range: %d", i, n)
		}
		bytes = append(bytes, byte(n))
	}
	if len(bytes) != 37 {
		t.Fatalf("got %d bytes, want 37 (minimal module)", len(bytes))
	}

	dir := t.TempDir()
	wasmPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(wasmPath, bytes, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}

	if out, err := exec.Command("wasm-tools", "validate", wasmPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bytes {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, out, hexBytes)
	}

	wat, err := exec.Command("wasm-tools", "print", wasmPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	if !strings.Contains(watStr, "(type") {
		t.Errorf("wasm-tools print output missing (type — bad type section?\n%s", watStr)
	}
	if !strings.Contains(watStr, "i32.const 42") {
		t.Errorf("wasm-tools print output missing i32.const 42 — bad code section?\n%s", watStr)
	}
	if !strings.Contains(watStr, "main") {
		t.Errorf("wasm-tools print output missing main — bad export section?\n%s", watStr)
	}
}

// langProducedModuleBytes runs a Lang program that prints space-
// separated decimal bytes to stdout (newline-terminated), parses
// them, and returns the byte slice. The wasmtime-run tests below
// share this helper to extract a std/wasm-produced wasm module
// from a Lang program. The Lang side has to use print()
// (newline-terminated) for the final emission — otherwise
// wasmtime's own "0" result line fuses onto the last decimal,
// e.g. "... 11" + "0" -> "... 110".
func langProducedModuleBytes(t *testing.T, src string) []byte {
	t.Helper()
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		if n < 0 || n > 255 {
			t.Fatalf("byte %d out of u8 range: %d", i, n)
		}
		bs = append(bs, byte(n))
	}
	return bs
}

// runWasmModule writes `mod` to a tmp .wasm file and runs
// `wasmtime --invoke main` on it. Returns wasmtime's stdout.
// Skips the calling test if wasmtime isn't on PATH.
func runWasmModule(t *testing.T, mod []byte) string {
	t.Helper()
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	wasmPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(wasmPath, mod, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", "--invoke", "main", wasmPath)
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	if err := cmd.Run(); err != nil {
		hexBytes := ""
		for _, b := range mod {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasmtime run failed: %v\nstderr:\n%s\nstdout:\n%s\nbytes: %s", err, se.String(), so.String(), hexBytes)
	}
	return so.String()
}

// TestWASMModuleRunsConst42 takes the bytes std/wasm produces for
// the minimal "main returns 42" module, hands them to wasmtime,
// and asserts the function actually returns 42 at runtime. After
// the byte-vector and wasm-tools-validate gates, this is the
// final correctness check: the Lang-produced module is executable.
func TestWASMModuleRunsConst42(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    var m: module.Module = module.module_new();
    var p0: u8[] = [];
    var r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);
    var bodyExpr: u8[] = inst.inst_i32_const([], 42);
    var localsBytes: u8[] = inst.put_locals_empty([]);
    var fn: u8[] = inst.put_function_body([], localsBytes, bodyExpr);
    m = m.with_code([fn]);
    var bytes: u8[] = module.build(m);

    var output: string = "";
    var i: i32 = 0;
    while (i < bytes.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (bytes[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	mod := langProducedModuleBytes(t, src)
	if len(mod) != 37 {
		t.Fatalf("got %d bytes, want 37", len(mod))
	}
	out := strings.TrimSpace(runWasmModule(t, mod))
	if !strings.Contains(out, "42") {
		t.Errorf("wasmtime stdout = %q, want it to contain 42", out)
	}
}

// TestWASMModuleRunsAddition builds a module whose main does
// i32.const 3 ; i32.const 4 ; i32.add and returns 7. Exercises
// the numeric-opcode path (i32.add at 0x6A) as part of a
// runnable module, not just a byte-vector match.
func TestWASMModuleRunsAddition(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/numeric";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    var m: module.Module = module.module_new();
    var p0: u8[] = [];
    var r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);

    var body: u8[] = inst.inst_i32_const([], 3);
    body = inst.inst_i32_const(body, 4);
    body = numeric.inst_i32_add(body);
    var localsBytes: u8[] = inst.put_locals_empty([]);
    var fn: u8[] = inst.put_function_body([], localsBytes, body);
    m = m.with_code([fn]);
    var bytes: u8[] = module.build(m);

    var output: string = "";
    var i: i32 = 0;
    while (i < bytes.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (bytes[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	mod := langProducedModuleBytes(t, src)
	out := strings.TrimSpace(runWasmModule(t, mod))
	if !strings.Contains(out, "7") {
		t.Errorf("wasmtime stdout = %q, want it to contain 7", out)
	}
}

// TestWASMModuleRunsTwoFunctions builds a module with two
// functions: a callee add(a, b) returning a+b, and main()
// calling it with (10, 32) and returning the result (42).
// Exercises the call opcode (0x10) and multi-function code-
// section composition.
//
// Function indices are flat across imports and declared
// functions; with no imports here, callee = 0 and main = 1.
// main exports as funcidx 1.
func TestWASMModuleRunsTwoFunctions(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/numeric";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    var m: module.Module = module.module_new();

    // Two types:
    //   type 0: (i32, i32) -> i32   for the add callee
    //   type 1: () -> i32           for main
    var p_add: u8[] = [encode.valtype_i32(), encode.valtype_i32()];
    var r_add: u8[] = [encode.valtype_i32()];
    var p_main: u8[] = [];
    var r_main: u8[] = [encode.valtype_i32()];
    m = m.with_types([p_add, p_main], [r_add, r_main]);

    // Function 0 uses type 0 (add), function 1 uses type 1 (main).
    m = m.with_functions([0u32, 1u32]);

    // Export main as funcidx 1.
    m = m.with_exports(["main"], [sections.export_func()], [1u32]);

    // add(a, b): local.get 0 ; local.get 1 ; i32.add.
    var add_body: u8[] = inst.inst_local_get([], 0u32);
    add_body = inst.inst_local_get(add_body, 1u32);
    add_body = numeric.inst_i32_add(add_body);
    var add_fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), add_body);

    // main(): i32.const 10 ; i32.const 32 ; call 0.
    var main_body: u8[] = inst.inst_i32_const([], 10);
    main_body = inst.inst_i32_const(main_body, 32);
    main_body = inst.inst_call(main_body, 0u32);
    var main_fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), main_body);

    m = m.with_code([add_fn, main_fn]);
    var bytes: u8[] = module.build(m);

    var output: string = "";
    var i: i32 = 0;
    while (i < bytes.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (bytes[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	mod := langProducedModuleBytes(t, src)
	out := strings.TrimSpace(runWasmModule(t, mod))
	if !strings.Contains(out, "42") {
		t.Errorf("wasmtime stdout = %q, want it to contain 42", out)
	}
}

// TestWASMComponentHeader covers std/wasm/component's preamble
// writer. The 8-byte component preamble differs from the core
// wasm preamble in the version + layer fields (0x0d 0x00 0x01
// 0x00 vs 0x01 0x00 0x00 0x00) — wasm-tools rejects a core
// module wherever it expects a component (and vice versa) based
// on those four bytes.
func TestWASMComponentHeader(t *testing.T) {
	src := `import "std/wasm/component";
function main(): i32 {
    var empty: u8[] = [];
    var hdr: u8[] = component.put_component_header(empty);
    if (hdr.len() != 8) { return 1; }
    if (hdr[0] != 0u8)   { return 2; }
    if (hdr[1] != 97u8)  { return 3; }   // 'a'
    if (hdr[2] != 115u8) { return 4; }   // 's'
    if (hdr[3] != 109u8) { return 5; }   // 'm'
    if (hdr[4] != 13u8)  { return 6; }   // version low (0x0d)
    if (hdr[5] != 0u8)   { return 7; }   // version high
    if (hdr[6] != 1u8)   { return 8; }   // layer low (= component)
    if (hdr[7] != 0u8)   { return 9; }   // layer high

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("component header: exit = %d, want 0", got)
	}
}

// TestWASMComponentWraps verifies put_core_module_section wraps a
// std/wasm-built core module into a Component Model envelope
// whose bytes parse cleanly under wasm-tools. The Lang program
// builds (a) a "function returning 42" core module and (b) a
// component that embeds it, then dumps the component bytes;
// the Go test pipes the result through wasm-tools validate +
// print, expecting the latter to show the (component) wrapper +
// the embedded (core module) + the i32.const 42 body.
func TestWASMComponentWraps(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/component";
import "std/wasm/inst";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    // Build a "function returning 42" core module.
    var m: module.Module = module.module_new();
    var p0: u8[] = [];
    var r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);
    var bodyExpr: u8[] = inst.inst_i32_const([], 42);
    var fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), bodyExpr);
    m = m.with_code([fn]);
    var core_bytes: u8[] = module.build(m);

    // Wrap it in a component envelope.
    var comp: u8[] = component.put_component_header([]);
    comp = component.put_core_module_section(comp, core_bytes);

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	if !strings.Contains(watStr, "(component") {
		t.Errorf("expected (component header in printed output, got:\n%s", watStr)
	}
	if !strings.Contains(watStr, "(core module") {
		t.Errorf("expected (core module section in printed output, got:\n%s", watStr)
	}
	if !strings.Contains(watStr, "i32.const 42") {
		t.Errorf("expected embedded i32.const 42, got:\n%s", watStr)
	}
}

// TestWASMComponentCoreInstance covers
// put_core_instance_section_instantiate: wraps a core module with
// the core-instance section needed to actually run it inside a
// component. The bytes have to parse + validate under wasm-tools,
// and `wasm-tools print` has to surface the `(core instance ...
// (instantiate ...))` form that signals the section is structurally
// correct (id 2, vec-len 1, tag 0 = instantiate, module-idx 0, no
// args). This is the foundation for component-level export aliasing
// in a later slice — exports point at instance items, so the
// instance has to exist first.
func TestWASMComponentCoreInstance(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/component";
import "std/wasm/inst";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    // Build a no-import core module: function "f" returns 7.
    var m: module.Module = module.module_new();
    var p0: u8[] = [];
    var r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["f"], [sections.export_func()], [0u32]);
    var bodyExpr: u8[] = inst.inst_i32_const([], 7);
    var fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), bodyExpr);
    m = m.with_code([fn]);
    var core_bytes: u8[] = module.build(m);

    // Component envelope: preamble + core-module + core-instance.
    var comp: u8[] = component.put_component_header([]);
    comp = component.put_core_module_section(comp, core_bytes);
    comp = component.put_core_instance_section_instantiate(comp, 0u32);

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	if !strings.Contains(watStr, "(component") {
		t.Errorf("expected (component header in printed output, got:\n%s", watStr)
	}
	if !strings.Contains(watStr, "(core module") {
		t.Errorf("expected (core module section in printed output, got:\n%s", watStr)
	}
	if !strings.Contains(watStr, "(core instance") {
		t.Errorf("expected (core instance section in printed output, got:\n%s", watStr)
	}
	// The instantiate form has to show up — without it, wasm-tools
	// can't recover the module being instantiated and prints a
	// different shape.
	if !strings.Contains(watStr, "instantiate") {
		t.Errorf("expected `instantiate` keyword in printed output, got:\n%s", watStr)
	}
}

// TestWASMComponentAliasCoreFunc covers
// put_alias_section_core_export_func: after a core module is
// instantiated, the alias section lets us surface one of its
// exported core functions as a top-level core-sort func — the
// prerequisite for canon-lift to turn that core func into a
// component-level function. Without an alias the core func has no
// addressable identity at the component level.
//
// We don't reach the canon section in this slice; the test just
// checks the alias bytes parse + print as the expected
// `(alias core export ... "f" (core func ...))` shape.
func TestWASMComponentAliasCoreFunc(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/component";
import "std/wasm/inst";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    // Build a core module: function "f" returns 9.
    var m: module.Module = module.module_new();
    var p0: u8[] = [];
    var r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["f"], [sections.export_func()], [0u32]);
    var bodyExpr: u8[] = inst.inst_i32_const([], 9);
    var fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), bodyExpr);
    m = m.with_code([fn]);
    var core_bytes: u8[] = module.build(m);

    // Component: preamble + core-module + core-instance + alias.
    var comp: u8[] = component.put_component_header([]);
    comp = component.put_core_module_section(comp, core_bytes);
    comp = component.put_core_instance_section_instantiate(comp, 0u32);
    comp = component.put_alias_section_core_export_func(comp, 0u32, "f");

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	if !strings.Contains(watStr, "(alias core export") {
		t.Errorf("expected (alias core export ... in printed output, got:\n%s", watStr)
	}
	if !strings.Contains(watStr, "\"f\"") {
		t.Errorf("expected alias to reference export \"f\", got:\n%s", watStr)
	}
	if !strings.Contains(watStr, "(core func") {
		t.Errorf("expected (core func ... result of the alias, got:\n%s", watStr)
	}
}

// TestWASMComponentAliasMultipleSorts covers
// put_alias_section_core_exports in its multi-sort multi-entry
// shape: aliases a core func + a core memory out of the same core
// instance in one alias section. Confirms different core-sort
// bytes (func vs memory) can coexist in a single vec, which is the
// shape canon-lift-with-(memory, realloc) needs in a real
// component.
func TestWASMComponentAliasMultipleSorts(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/component";
import "std/wasm/inst";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    // Core module: one func + one memory, both exported.
    var m: module.Module = module.module_new();
    var p0: u8[] = [];
    var r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_memory(1u32, 0 - 1);
    m = m.with_exports(["f", "mem"], [sections.export_func(), sections.export_memory()], [0u32, 0u32]);
    var fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), inst.inst_i32_const([], 1));
    m = m.with_code([fn]);
    var core_bytes: u8[] = module.build(m);

    var comp: u8[] = component.put_component_header([]);
    comp = component.put_core_module_section(comp, core_bytes);
    comp = component.put_core_instance_section_instantiate(comp, 0u32);

    // Alias section with two entries of different core sorts.
    var sorts: u8[] = [component.core_sort_func(), component.core_sort_memory()];
    var instance_idxs: u32[] = [0u32, 0u32];
    var names: string[] = ["f", "mem"];
    comp = component.put_alias_section_core_exports(comp, sorts, instance_idxs, names);

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	for _, want := range []string{
		"(alias core export",
		"\"f\"",
		"\"mem\"",
		"(core func",
		"(core memory",
	} {
		if !strings.Contains(watStr, want) {
			t.Errorf("expected %q in printed component, got:\n%s", want, watStr)
		}
	}
}

// TestWASMComponentInstanceTypeWithResult covers
// put_type_section_one_instance_one_func_with_result_export. Builds
// an instance type whose exported function returns a value — the
// shape WASI funcs like `wasi:io/streams::read(len: u64) ->
// result<list<u8>, ...>` take. The no-result variant doesn't apply
// to any returning function.
func TestWASMComponentInstanceTypeWithResult(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    var names: string[] = ["len"];
    var vts: u8[] = [component.cvaltype_u64()];
    var comp: u8[] = component.put_component_header([]);
    // Type 0: instance { export get-info(len: u64) -> u32 }
    comp = component.put_type_section_one_instance_one_func_with_result_export(comp, "get-info", names, vts, component.cvaltype_u32());
    comp = component.put_import_section_one_instance(comp, "test:reader/iface", 0u32);

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	for _, want := range []string{"instance", "\"get-info\"", "\"len\"", "u64", "(result u32)"} {
		if !strings.Contains(watStr, want) {
			t.Errorf("expected %q in printed component, got:\n%s", want, watStr)
		}
	}
}

// TestWASMComponentWasiMultiImportHelper exercises the multi-import
// generalisation of the WASI helper. Wraps a core module that
// imports from TWO distinct WASI interfaces: `wasi:cli/exit::exit`
// and `wasi:cli/environment::get-arguments`. The helper threads
// both through the full type / import / alias / canon-lower /
// core-instance pipeline, then instantiates the core module with
// both lowered instances as args.
func TestWASMComponentWasiMultiImportHelper(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/component";
import "std/wasm/encode";
import "std/wasm/imports";
function main(): i32 {
    // Core module imports two no-result host funcs: exit (i32 → ())
    // and get-args (() → ()). Real WASI get-arguments returns a
    // list, but the canon-lower-no-opts path used by the helper only
    // supports scalar funcs, so this test uses a stub-signature shape.
    var m: module.Module = module.module_new();
    var p_exit: u8[] = [encode.valtype_i32()];
    var r_void: u8[] = [];
    var p_args: u8[] = [];
    m = m.with_types([p_exit, p_args], [r_void, r_void]);
    m = m.with_imports(["wasi-exit", "wasi-env"], ["exit", "get-args"], [imports.import_func(), imports.import_func()], [imports.import_desc_func(0u32), imports.import_desc_func(1u32)]);
    m = m.with_functions([]);
    m = m.with_code([]);
    var core_bytes: u8[] = module.build(m);

    // Two WASI imports, threaded in one call.
    var interfaces: string[] = ["wasi:cli/exit@0.2.0", "wasi:cli/environment@0.2.0"];
    var funcs: string[] = ["exit", "get-args"];
    var p0: string[] = ["code"];
    var v0: u8[] = [component.cvaltype_u32()];
    var p1: string[] = [];
    var v1: u8[] = [];
    var names_per: string[][] = [p0, p1];
    var valtypes_per: u8[][] = [v0, v1];
    var core_modules: string[] = ["wasi-exit", "wasi-env"];

    var comp: u8[] = component.build_wasi_multi_imported_component(core_bytes, interfaces, funcs, names_per, valtypes_per, core_modules);

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	for _, want := range []string{
		"wasi:cli/exit@0.2.0",
		"wasi:cli/environment@0.2.0",
		"with \"wasi-exit\"",
		"with \"wasi-env\"",
	} {
		if !strings.Contains(watStr, want) {
			t.Errorf("expected %q in printed component, got:\n%s", want, watStr)
		}
	}
}

// TestCmdLangComponentWrapRejectsImports confirms `-component-wrap`
// gives a clear error (instead of silently producing an invalid
// component) when the source's wasmbin output has WASI imports the
// driver doesn't know how to route into a preview-2 component yet.
// `args()` pulls in `wasi_args_sizes_get` + `wasi_args_get`,
// which the preview-2 migration hasn't covered, so the program
// hits the "unrecognised imports" branch.
//
// (Earlier this test used `print()`, but `print()`'s underlying
// imports moved into the preview-2 migrated set when
// `__fern_print` was ported in #1245; the test source switched to
// a still-unmigrated builtin.)
func TestCmdLangComponentWrapRejectsImports(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "needs_adapter.fern")
	// read + append in one program isn't composable: read needs
	// read-via-stream and append needs append-via-stream, but the
	// combined filesystem/types body is read+write (read-via +
	// write-via), not read+append — so neither a single-direction body
	// nor the combined one matches, and -component-wrap must reject it.
	// (read-only, write-only, append-only, and read+write each compose;
	// this picks a combination that still falls through.)
	src := []byte(`function main(): i32 {
    match (read_file("a")) { Ok(c) => {}, Err(e) => {} }
    match (open_appender("b")) { Ok(w) => { w.close(); }, Err(e) => {} }
    return 0;
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "needs_adapter.wasm")
	cmd := exec.Command("go", "run", "./cmd/fern",
		"-target", "wasm-bin",
		"-component-wrap",
		"-o", compPath, srcPath)
	cmd.Dir = projectRoot(t)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected `fern -component-wrap` to fail on a program with unrecognised imports, but it succeeded.\noutput:\n%s", out)
	}
	if !strings.Contains(string(out), "unrecognised imports") {
		t.Errorf("expected unrecognised-imports error message in output, got:\n%s", out)
	}
}

// TestCmdLangComponentWrapCliWithTcpServer drives a TCP server
// (tcp_listen / tcp_accept / tcp_close) through `-component-wrap-cli`
// — the wasi:sockets path, composed adapter-free via
// ComposeTcpServerCliRun (seven instances, four resource.drops, six
// memory-trampoline methods). A listen+close program runs to
// completion under wasmtime's host sockets (exit 0); a listen+accept
// program validates and links (it would block waiting for a client,
// so it's only validated, not run).
func TestCmdLangComponentWrapCliWithTcpServer(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	build := func(name, src string) string {
		srcPath := filepath.Join(dir, name+".fern")
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		compPath := filepath.Join(dir, name+".wasm")
		cmd := exec.Command("go", "run", "./cmd/fern", "-target", "wasm-bin", "-component-wrap-cli", "-o", compPath, srcPath)
		cmd.Dir = projectRoot(t)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fern -component-wrap-cli (%s) failed: %v\n%s", name, err, out)
		}
		if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
			t.Fatalf("wasm-tools validate (%s) failed: %v\n%s", name, err, out)
		}
		return compPath
	}

	// listen + accept: validates + the imports are all present.
	full := build("tcpfull", `function main(): i32 {
    var s: i32 = tcp_listen(8080);
    if (s < 0) { return 1; }
    var c: i32 = tcp_accept(s);
    tcp_close(c);
    tcp_close(s);
    return 0;
}`)
	printOut, err := exec.Command("wasm-tools", "print", full).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, printOut)
	}
	for _, want := range []string{
		"wasi:sockets/tcp@0.2.0", "tcp-socket.accept", "tcp-socket.subscribe",
		"wasi:sockets/instance-network@0.2.0", "wasi:io/poll@0.2.0", "wasi:cli/run@0.2.0",
	} {
		if !bytes.Contains(printOut, []byte(want)) {
			t.Errorf("expected %q in component, got:\n%s", want, printOut)
		}
	}

	// echo server (tcp_recv / tcp_send) — adds input-stream.blocking-read
	// + output-stream.blocking-write-and-flush on the connection. Just
	// validated here (running it needs a connecting client); confirms the
	// stream methods are composed in.
	echo := build("tcpecho", `function main(): i32 {
    var s: i32 = tcp_listen(8080);
    var c: i32 = tcp_accept(s);
    var d: string = tcp_recv(c, 1024);
    tcp_send(c, d);
    tcp_close(c);
    tcp_close(s);
    return 0;
}`)
	echoOut, err := exec.Command("wasm-tools", "print", echo).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, echoOut)
	}
	for _, want := range []string{"input-stream.blocking-read", "output-stream.blocking-write-and-flush"} {
		if !bytes.Contains(echoOut, []byte(want)) {
			t.Errorf("expected %q in echo component, got:\n%s", want, echoOut)
		}
	}

	// listen + close (no accept) runs to completion against the host
	// sockets — exit 0 confirms the component links and executes.
	srv := build("tcplisten", `function main(): i32 {
    var s: i32 = tcp_listen(8080);
    if (s < 0) { return 1; }
    tcp_close(s);
    return 0;
}`)
	if err := exec.Command("wasmtime", "run", "-S", "inherit-network", srv).Run(); err != nil {
		t.Errorf("tcp listen+close: wasmtime run failed (want exit 0): %v", err)
	}
}

// TestCmdLangComponentWrapCliTcpServerWithEnv drives a TCP echo server
// whose listen port comes from the PORT environment variable — the
// HTTP-over-TCP handler shape (the synthesised `main()` calls
// `__port_from_env` → `env()` → wasi:cli/environment.get-environment).
// This mixes wasi:sockets with wasi:cli/environment, which neither the
// pure-TCP nor the CLI-stream composer handled before: the TCP composer
// now optionally surfaces get-environment (mem+realloc, like
// blocking-read). The whole thing composes adapter-free; the test runs
// it end-to-end (wasmtime serves, a Go client connects and round-trips
// a payload) with PORT supplied via `--env`, proving the env-driven port
// actually drives the bind.
func TestCmdLangComponentWrapCliTcpServerWithEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("preview-2 toolchain not exercised on windows")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}

	// Pick a free ephemeral port — open a :0 listener, capture the
	// kernel-assigned port, close before the guest binds. Tiny race,
	// localhost-only, short-lived (same approach as TestWasmPreview2TcpEcho).
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "envecho.fern")
	// port_from_env mirrors std/tcp's __port_from_env: read PORT via
	// env() (→ get-environment), parse the digits, fall back to 8080.
	// Sending the port through env (rather than hardcoding) is the whole
	// point — it forces the get-environment import alongside sockets.
	src := `function port_from_env(): i32 {
    match (env("PORT")) {
        Some(s) => {
            var n: i32 = 0;
            var i: i32 = 0;
            while (i < s.len()) {
                var b: i32 = s[i] as i32;
                if (b < 48 || b > 57) { return 8080; }
                n = n * 10 + (b - 48);
                i = i + 1;
            }
            if (i == 0) { return 8080; }
            return n;
        },
        None => { return 8080; }
    }
}

function main(): i32 {
    var sock = tcp_listen(port_from_env());
    if (sock < 0) { return 1; }
    var conn = tcp_accept(sock);
    if (conn < 0) { return 2; }
    var msg = tcp_recv(conn, 1024);
    var sent = tcp_send(conn, msg);
    if (sent < 0) { return 3; }
    tcp_close(conn);
    tcp_close(sock);
    return 0;
}`
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "envecho.wasm")
	build := exec.Command("go", "run", "./cmd/fern", "-target", "wasm-bin", "-component-wrap-cli", "-o", compPath, srcPath)
	build.Dir = projectRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("fern -component-wrap-cli (env+tcp) failed: %v\n%s", err, out)
	}
	if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
	}
	printOut, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, printOut)
	}
	for _, want := range []string{
		"wasi:cli/environment@0.2.0", "wasi:sockets/tcp@0.2.0",
		"input-stream.blocking-read", "output-stream.blocking-write-and-flush",
		"wasi:cli/run@0.2.0",
	} {
		if !bytes.Contains(printOut, []byte(want)) {
			t.Errorf("expected %q in component, got:\n%s", want, printOut)
		}
	}

	// Spawn the server with PORT supplied via --env. If get-environment
	// didn't work the guest would fall back to 8080 and the dial below
	// (to the env-supplied port) would time out — so a successful
	// round-trip proves the env path drives the bind.
	run := exec.Command("wasmtime", "run", "-S", "inherit-network", "--env", "PORT="+strconv.Itoa(port), compPath)
	var sout, serr bytes.Buffer
	run.Stdout = &sout
	run.Stderr = &serr
	if err := run.Start(); err != nil {
		t.Fatalf("wasmtime run start: %v", err)
	}
	t.Cleanup(func() {
		if run.Process != nil {
			run.Process.Kill()
		}
		run.Wait()
	})

	deadline := time.Now().Add(5 * time.Second)
	var conn net.Conn
	for {
		conn, err = net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial: %v\nstdout:\n%s\nstderr:\n%s", err, sout.String(), serr.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	defer conn.Close()

	want := "ping-from-env\n"
	if _, err := conn.Write([]byte(want)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if cw, ok := conn.(*net.TCPConn); ok {
		cw.CloseWrite()
	}
	got, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != want {
		t.Fatalf("echo = %q; want %q (stderr=%q)", string(got), want, serr.String())
	}
	if err := run.Wait(); err != nil {
		t.Fatalf("wasmtime exit: %v\nstdout:\n%s\nstderr:\n%s", err, sout.String(), serr.String())
	}
}

// TestCmdLangComponentWrapWrapsExit drives a Lang program that
// calls exit() through `-component-wrap` end-to-end. With the
// preview-2 migration of proc_exit and the result-type encoding
// for the wasi:cli/exit interface shipped, the driver detects the
// `wasi:cli/exit@0.2.0::exit` import, composes it (export mode),
// and the produced component links against wasmtime's host
// wasi:cli/exit implementation.
//
// wasmtime's wasi:cli/exit::exit treats the err arm of `result<_,
// _>` as a non-zero exit. The Lang `exit(0)` lowers to
// `result::ok` so wasmtime exits 0.
func TestCmdLangComponentWrapWrapsExit(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "exits.fern")
	src := []byte(`function main(): i32 {
    exit(0);
    return 0;
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "exits.wasm")
	cmd := exec.Command("go", "run", "./cmd/fern",
		"-target", "wasm-bin",
		"-component-wrap",
		"-o", compPath, srcPath)
	cmd.Dir = projectRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fern -component-wrap (exit) failed: %v\n%s", err, out)
	}
	if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
	}
	printOut, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, printOut)
	}
	if !strings.Contains(string(printOut), "wasi:cli/exit@0.2.0") {
		t.Errorf("expected wasi:cli/exit@0.2.0 import in component, got:\n%s", printOut)
	}
	if !strings.Contains(string(printOut), "export \"main\"") {
		t.Errorf("expected main export in component, got:\n%s", printOut)
	}
	// End-to-end: wasmtime accepts and runs the component.
	// exit(0) → result::ok → wasmtime exits 0.
	if out, err := exec.Command("wasmtime", "run", "--invoke", "main()", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasmtime run failed: %v\n%s", err, out)
	}
}

// TestCmdLangComponentWrapCli drives a Lang program through
// `-component-wrap-cli` and confirms the produced binary runs
// under plain `wasmtime run` (no --invoke). The cli-run shape
// exports `wasi:cli/run@0.2.0` so wasmtime treats main's i32 as
// a result lowering: 0 → exit 0, non-zero → exit 1.
//
// Two variants:
//   - main returning 0: clean exit 0.
//   - main returning 42: wasmtime exits non-zero (result::err).
func TestCmdLangComponentWrapCli(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	for _, tc := range []struct {
		name      string
		retval    string
		wantClean bool
	}{
		{"main_returns_0", "0", true},
		{"main_returns_42", "42", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			srcPath := filepath.Join(dir, "cli.fern")
			src := []byte("function main(): i32 { return " + tc.retval + "; }")
			if err := os.WriteFile(srcPath, src, 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			compPath := filepath.Join(dir, "cli.wasm")
			cmd := exec.Command("go", "run", "./cmd/fern",
				"-target", "wasm-bin",
				"-component-wrap-cli",
				"-o", compPath, srcPath)
			cmd.Dir = projectRoot(t)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("fern -component-wrap-cli failed: %v\n%s", err, out)
			}
			if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
				t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
			}
			printOut, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
			if err != nil {
				t.Fatalf("wasm-tools print failed: %v\n%s", err, printOut)
			}
			if !strings.Contains(string(printOut), "wasi:cli/run@0.2.0") {
				t.Errorf("expected wasi:cli/run@0.2.0 export, got:\n%s", printOut)
			}
			err = exec.Command("wasmtime", "run", compPath).Run()
			if tc.wantClean && err != nil {
				t.Fatalf("wasmtime run expected clean exit, got: %v", err)
			}
			if !tc.wantClean && err == nil {
				t.Fatal("wasmtime run expected non-zero exit, got clean")
			}
		})
	}
}

// TestCmdLangComponentWrapCliWithExit drives a Lang program that
// calls exit() through `-component-wrap-cli` end-to-end. The
// driver should detect the preview-2 wasi:cli/exit import, wrap
// the core via `ComposePreview2CliRun`, and produce a
// component that:
//
//   - Imports wasi:cli/exit@0.2.0 (so wasmtime provides exit).
//   - Exports wasi:cli/run@0.2.0 (so `wasmtime run` works
//     without --invoke).
//
// The Lang `exit(0)` causes wasmtime to exit 0; `exit(1)` causes
// non-zero exit.
func TestCmdLangComponentWrapCliWithExit(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "exits.fern")
	src := []byte(`function main(): i32 {
    exit(0);
    return 0;
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "exits.wasm")
	cmd := exec.Command("go", "run", "./cmd/fern",
		"-target", "wasm-bin",
		"-component-wrap-cli",
		"-o", compPath, srcPath)
	cmd.Dir = projectRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fern -component-wrap-cli (exit) failed: %v\n%s", err, out)
	}
	if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
	}
	printOut, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, printOut)
	}
	for _, want := range []string{"wasi:cli/exit@0.2.0", "wasi:cli/run@0.2.0"} {
		if !strings.Contains(string(printOut), want) {
			t.Errorf("expected %q in component, got:\n%s", want, printOut)
		}
	}
	if err := exec.Command("wasmtime", "run", compPath).Run(); err != nil {
		t.Fatalf("wasmtime run failed: %v", err)
	}
}

// TestCmdLangComponentWrapCliWithPrintExit drives a Fern program
// that BOTH writes stdout (print) AND calls exit() — the mixed
// trampoline-pattern + structured-flow case. The driver detects
// the stream-write pair plus the structured wasi:cli/exit import
// (classifyComposeCliStream) and routes through
// ComposePreview2CliRun. exit(0) → stdout printed +
// exit 0; exit(1) → printed + non-zero (wasi:cli/exit only models
// the result discriminant, so codes are 0/1).
func TestCmdLangComponentWrapCliWithPrintExit(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	build := func(src string) string {
		dir := t.TempDir()
		srcPath := filepath.Join(dir, "pe.fern")
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		compPath := filepath.Join(dir, "pe.wasm")
		cmd := exec.Command("go", "run", "./cmd/fern", "-target", "wasm-bin", "-component-wrap-cli", "-o", compPath, srcPath)
		cmd.Dir = projectRoot(t)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fern -component-wrap-cli (print+exit) failed: %v\n%s", err, out)
		}
		if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
			t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
		}
		printOut, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
		if err != nil {
			t.Fatalf("wasm-tools print failed: %v\n%s", err, printOut)
		}
		for _, want := range []string{"wasi:cli/stdout@0.2.0", "wasi:cli/exit@0.2.0", "wasi:cli/run@0.2.0"} {
			if !strings.Contains(string(printOut), want) {
				t.Errorf("expected %q in component, got:\n%s", want, printOut)
			}
		}
		return compPath
	}
	// exit(0): "hi\n" on stdout, exit 0.
	ok := build(`function main(): i32 { print("hi"); exit(0); return 0; }`)
	okCmd := exec.Command("wasmtime", "run", ok)
	var okOut bytes.Buffer
	okCmd.Stdout = &okOut
	if err := okCmd.Run(); err != nil {
		t.Errorf("print+exit(0): wasmtime run failed (want exit 0): %v", err)
	}
	if okOut.String() != "hi\n" {
		t.Errorf("print+exit(0) stdout = %q, want %q", okOut.String(), "hi\n")
	}
	// exit(1): "oops\n" on stdout, non-zero exit.
	errComp := build(`function main(): i32 { print("oops"); exit(1); return 0; }`)
	errCmd := exec.Command("wasmtime", "run", errComp)
	var errOut bytes.Buffer
	errCmd.Stdout = &errOut
	if err := errCmd.Run(); err == nil {
		t.Errorf("print+exit(1): wasmtime run exited 0, want non-zero")
	}
	if errOut.String() != "oops\n" {
		t.Errorf("print+exit(1) stdout = %q, want %q", errOut.String(), "oops\n")
	}
}

// TestCmdLangComponentWrapCliWithRandom drives a Lang program
// that calls random_i32() through `-component-wrap-cli`. With
// the preview-2 migration of wasi_random_get →
// wasi:random/random::get-random-u64, the driver should route
// the import through `ComposePreview2CliRun` and produce a
// component that runs cleanly under `wasmtime run`.
func TestCmdLangComponentWrapCliWithRandom(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "rand.fern")
	src := []byte(`function main(): i32 {
    var r: i32 = random_i32();
    if (r == r) { return 0; }
    return 1;
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "rand.wasm")
	cmd := exec.Command("go", "run", "./cmd/fern",
		"-target", "wasm-bin",
		"-component-wrap-cli",
		"-o", compPath, srcPath)
	cmd.Dir = projectRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fern -component-wrap-cli (random) failed: %v\n%s", err, out)
	}
	if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
	}
	printOut, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, printOut)
	}
	for _, want := range []string{"wasi:random/random@0.2.0", "wasi:cli/run@0.2.0"} {
		if !strings.Contains(string(printOut), want) {
			t.Errorf("expected %q in component, got:\n%s", want, printOut)
		}
	}
	if err := exec.Command("wasmtime", "run", compPath).Run(); err != nil {
		t.Fatalf("wasmtime run failed: %v", err)
	}
}

// TestCmdLangComponentWrapCliWithMonotonic drives a Lang program
// that calls monotonic_ns() through `-component-wrap-cli`. With
// the preview-2 migration of wasi_clock_time_get (monotonic case)
// → wasi:clocks/monotonic-clock@0.2.0::now, the driver should
// route the import through `ComposePreview2CliRun` and
// produce a component that runs cleanly under `wasmtime run`.
func TestCmdLangComponentWrapCliWithMonotonic(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "mono.fern")
	src := []byte(`function main(): i32 {
    var t: i64 = monotonic_ns();
    if (t == t) { return 0; }
    return 1;
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "mono.wasm")
	cmd := exec.Command("go", "run", "./cmd/fern",
		"-target", "wasm-bin",
		"-component-wrap-cli",
		"-o", compPath, srcPath)
	cmd.Dir = projectRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fern -component-wrap-cli (monotonic) failed: %v\n%s", err, out)
	}
	if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
	}
	printOut, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, printOut)
	}
	for _, want := range []string{"wasi:clocks/monotonic-clock@0.2.0", "wasi:cli/run@0.2.0"} {
		if !strings.Contains(string(printOut), want) {
			t.Errorf("expected %q in component, got:\n%s", want, printOut)
		}
	}
	if err := exec.Command("wasmtime", "run", compPath).Run(); err != nil {
		t.Fatalf("wasmtime run failed: %v", err)
	}
}

// TestCmdLangComponentWrapCliWithNowNs drives a Lang program
// that calls now_ns() (the realtime wall-clock helper) through
// `-component-wrap-cli`. With the preview-2 migration of
// __fern_now_ns to wasi:clocks/wall-clock@0.2.0::now (the
// datetime-record indirect-return shape) + the 1-i32 trampoline
// wrap, the produced component imports wasi:clocks/wall-clock +
// exports wasi:cli/run and runs cleanly under `wasmtime run`.
func TestCmdLangComponentWrapCliWithNowNs(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "now.fern")
	src := []byte(`function main(): i32 {
    var t: i64 = now_ns();
    if (t >= 0i64) { return 0; }
    return 1;
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "now.wasm")
	cmd := exec.Command("go", "run", "./cmd/fern",
		"-target", "wasm-bin",
		"-component-wrap-cli",
		"-o", compPath, srcPath)
	cmd.Dir = projectRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fern -component-wrap-cli (now_ns) failed: %v\n%s", err, out)
	}
	if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
	}
	printOut, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, printOut)
	}
	for _, want := range []string{"wasi:clocks/wall-clock@0.2.0", "wasi:cli/run@0.2.0"} {
		if !strings.Contains(string(printOut), want) {
			t.Errorf("expected %q in component, got:\n%s", want, printOut)
		}
	}
	if err := exec.Command("wasmtime", "run", compPath).Run(); err != nil {
		t.Fatalf("wasmtime run failed: %v", err)
	}
}

// TestCmdLangComponentWrapCliVoidMain confirms `-component-wrap-cli`
// transparently handles a void `main`. wasmbin's `SynthCliRun`
// synthesises a `_lang_run() -> i32` wrapper that calls main and
// returns 0, so the canon-lifted wasi:cli/run::run sees the
// expected `() -> i32` core export shape regardless of what main
// declared. wasmtime exits 0 (result::ok).
func TestCmdLangComponentWrapCliVoidMain(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "void.fern")
	src := []byte(`function main(): void {
    var t: i64 = monotonic_ns();
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "void.wasm")
	cmd := exec.Command("go", "run", "./cmd/fern",
		"-target", "wasm-bin",
		"-component-wrap-cli",
		"-o", compPath, srcPath)
	cmd.Dir = projectRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fern -component-wrap-cli (void main) failed: %v\n%s", err, out)
	}
	if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
	}
	if err := exec.Command("wasmtime", "run", compPath).Run(); err != nil {
		t.Fatalf("wasmtime run failed: %v", err)
	}
}

// TestCmdLangComponentWrapCliWithPrint drives a Lang program that
// calls print("hello world") through `-component-wrap-cli`
// end-to-end. With the preview-2 migration of __fern_print
// (#1245) and the driver routing (this slice), the program
// builds to a component that runs under `wasmtime run` and
// writes "hello world\n" to stdout.
//
// First end-to-end demonstration of a Lang program writing to
// stdout via preview-2 components — no wasm-tools shell-out, no
// preview-1 adapter.
func TestCmdLangComponentWrapCliWithPrint(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "hello.fern")
	src := []byte(`function main(): i32 {
    print("hello world");
    return 0;
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "hello.wasm")
	cmd := exec.Command("go", "run", "./cmd/fern",
		"-target", "wasm-bin",
		"-component-wrap-cli",
		"-o", compPath, srcPath)
	cmd.Dir = projectRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fern -component-wrap-cli (print) failed: %v\n%s", err, out)
	}
	if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
	}
	runCmd := exec.Command("wasmtime", "run", compPath)
	var stdout, stderr bytes.Buffer
	runCmd.Stdout = &stdout
	runCmd.Stderr = &stderr
	if err := runCmd.Run(); err != nil {
		t.Fatalf("wasmtime run failed: %v\nstderr:\n%s", err, stderr.String())
	}
	if got := stdout.String(); got != "hello world\n" {
		t.Errorf("wasmtime stdout = %q, want %q", got, "hello world\n")
	}
}

// TestCmdLangComponentWrapCliWithReadLine drives a Lang program
// that reads stdin via the bare `read_line()` builtin through
// `-component-wrap-cli`. The driver detects the read-only
// preview-2 imports (wasi:cli/stdin::get-stdin +
// wasi:io/streams::blocking-read), rebuilds with cabi_realloc
// exported (the blocking-read canon-lower allocates the returned
// list<u8> through it), and routes via ComposePreview2CliRun.
//
// Observed via exit code (a read-only program can't print): the
// program returns 0 (ok) when a line is present, 1 (err) at EOF.
func TestCmdLangComponentWrapCliWithReadLine(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "rd.fern")
	src := []byte(`function main(): i32 {
    match (read_line()) {
        Some(line) => { return 0; },
        None => { return 1; }
    }
    return 1;
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "rd.wasm")
	cmd := exec.Command("go", "run", "./cmd/fern",
		"-target", "wasm-bin",
		"-component-wrap-cli",
		"-o", compPath, srcPath)
	cmd.Dir = projectRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fern -component-wrap-cli (read_line) failed: %v\n%s", err, out)
	}
	if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
	}
	printOut, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, printOut)
	}
	for _, want := range []string{"wasi:cli/stdin@0.2.0", "wasi:io/streams@0.2.0", "wasi:cli/run@0.2.0"} {
		if !strings.Contains(string(printOut), want) {
			t.Errorf("expected %q in component, got:\n%s", want, printOut)
		}
	}
	// A present line → Some → exit 0.
	some := exec.Command("wasmtime", "run", compPath)
	some.Stdin = strings.NewReader("hello\n")
	if err := some.Run(); err != nil {
		t.Errorf("read_line with input: wasmtime run failed (want exit 0): %v", err)
	}
	// Empty stdin → None → exit 1.
	none := exec.Command("wasmtime", "run", compPath)
	none.Stdin = strings.NewReader("")
	if err := none.Run(); err == nil {
		t.Errorf("read_line at EOF: wasmtime run exited 0, want non-zero (None arm)")
	}
}

// TestCmdLangComponentWrapCliWithReadLinePrint drives the canonical
// filter — a program that BOTH reads stdin (read_line) AND writes
// stdout (print) — through `-component-wrap-cli`. The driver detects
// the four-import read+print shape (get-stdin +
// input-stream.blocking-read + get-stdout +
// output-stream.blocking-write-and-flush) and routes via
// ComposePreview2CliRun (combined io/streams, two
// trampolines + two no-trampoline getters).
func TestCmdLangComponentWrapCliWithReadLinePrint(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "rp.fern")
	src := []byte(`function main(): i32 {
    match (read_line()) {
        Some(line) => { print(line); return 0; },
        None => { return 1; }
    }
    return 1;
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "rp.wasm")
	cmd := exec.Command("go", "run", "./cmd/fern", "-target", "wasm-bin", "-component-wrap-cli", "-o", compPath, srcPath)
	cmd.Dir = projectRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fern -component-wrap-cli (read_line+print) failed: %v\n%s", err, out)
	}
	if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
	}
	printOut, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, printOut)
	}
	for _, want := range []string{"wasi:cli/stdin@0.2.0", "wasi:cli/stdout@0.2.0", "wasi:io/streams@0.2.0", "wasi:cli/run@0.2.0"} {
		if !strings.Contains(string(printOut), want) {
			t.Errorf("expected %q in component, got:\n%s", want, printOut)
		}
	}
	// stdin "hello\n" → read_line Some("hello\n") → print adds '\n' →
	// stdout "hello\n\n"; exit 0.
	some := exec.Command("wasmtime", "run", compPath)
	some.Stdin = strings.NewReader("hello\n")
	var sout bytes.Buffer
	some.Stdout = &sout
	if err := some.Run(); err != nil {
		t.Errorf("read_line+print with input: wasmtime run failed (want exit 0): %v", err)
	}
	if sout.String() != "hello\n\n" {
		t.Errorf("read_line+print stdout = %q, want %q", sout.String(), "hello\n\n")
	}
	// Empty stdin → None → exit 1.
	none := exec.Command("wasmtime", "run", compPath)
	none.Stdin = strings.NewReader("")
	if err := none.Run(); err == nil {
		t.Errorf("read_line+print EOF: wasmtime run exited 0, want non-zero")
	}
}

// TestCmdLangComponentWrapCliComposedCombos exercises the general
// CLI-stream + structured composition engine
// (component.ComposePreview2CliRun) on combinations the bespoke
// routes never covered: a read side mixed with structured imports,
// and both stream trampolines mixed with a structured import. These
// errored under -component-wrap-cli before the composer existed.
func TestCmdLangComponentWrapCliComposedCombos(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	build := func(name, src string) string {
		dir := t.TempDir()
		srcPath := filepath.Join(dir, name+".fern")
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		compPath := filepath.Join(dir, name+".wasm")
		cmd := exec.Command("go", "run", "./cmd/fern", "-target", "wasm-bin", "-component-wrap-cli", "-o", compPath, srcPath)
		cmd.Dir = projectRoot(t)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fern -component-wrap-cli (%s) failed: %v\n%s", name, err, out)
		}
		if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
			t.Fatalf("wasm-tools validate (%s) failed: %v\n%s", name, err, out)
		}
		return compPath
	}

	// read_line + exit: read side + structured. Some → exit 0, None → exit 1.
	rle := build("rle", `function main(): i32 {
    match (read_line()) {
        Some(line) => { exit(0); return 0; },
        None => { exit(1); return 1; }
    }
    return 5;
}`)
	rleSome := exec.Command("wasmtime", "run", rle)
	rleSome.Stdin = strings.NewReader("hi\n")
	if err := rleSome.Run(); err != nil {
		t.Errorf("read_line+exit (Some): want exit 0, got %v", err)
	}
	rleNone := exec.Command("wasmtime", "run", rle)
	rleNone.Stdin = strings.NewReader("")
	if err := rleNone.Run(); err == nil {
		t.Errorf("read_line+exit (None): want non-zero exit")
	}

	// read_line + print + exit: both stream trampolines + structured.
	// stdin "echo me\n" → printed back (+ print's newline) → exit 0.
	rlpe := build("rlpe", `function main(): i32 {
    match (read_line()) {
        Some(line) => { print(line); exit(0); return 0; },
        None => { exit(2); return 2; }
    }
    return 5;
}`)
	cmd := exec.Command("wasmtime", "run", rlpe)
	cmd.Stdin = strings.NewReader("echo me\n")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Errorf("read_line+print+exit: want exit 0, got %v", err)
	}
	if out.String() != "echo me\n\n" {
		t.Errorf("read_line+print+exit stdout = %q, want %q", out.String(), "echo me\n\n")
	}
}

// TestCmdLangComponentWrapCliComposedFileRead exercises the
// composer's filesystem-read fold: read_file's open-chain
// (get-directories → open-at → read-via-stream → blocking-read)
// combined with a stream/structured side, which the read-file wrap
// alone never covered. read_file+print is a cat-like tool;
// read_file+exit is read-config-then-exit. Both errored under
// -component-wrap-cli before the fold.
func TestCmdLangComponentWrapCliComposedFileRead(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "data.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write data: %v", err)
	}
	build := func(name, src string) string {
		srcPath := filepath.Join(dir, name+".fern")
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		compPath := filepath.Join(dir, name+".wasm")
		cmd := exec.Command("go", "run", "./cmd/fern", "-target", "wasm-bin", "-component-wrap-cli", "-o", compPath, srcPath)
		cmd.Dir = projectRoot(t)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fern -component-wrap-cli (%s) failed: %v\n%s", name, err, out)
		}
		if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
			t.Fatalf("wasm-tools validate (%s) failed: %v\n%s", name, err, out)
		}
		return compPath
	}

	// read_file + print (cat): file contents "hello\n" printed back
	// (+ print's trailing newline) → "hello\n\n", exit 0.
	rfp := build("rfp", `function main(): i32 {
    match (read_file("data.txt")) {
        Ok(s) => { print(s); return 0; },
        Err(e) => { return 1; }
    }
    return 5;
}`)
	cmd := exec.Command("wasmtime", "run", "--dir", dir, rfp)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Errorf("read_file+print: want exit 0, got %v", err)
	}
	if out.String() != "hello\n\n" {
		t.Errorf("read_file+print stdout = %q, want %q", out.String(), "hello\n\n")
	}

	// read_file + exit: 6-byte file → exit 0; missing file → Err → exit 1.
	rfe := build("rfe", `function main(): i32 {
    match (read_file("data.txt")) {
        Ok(s) => { if (s.len() == 6) { exit(0); return 0; } exit(2); return 2; },
        Err(e) => { exit(1); return 1; }
    }
    return 5;
}`)
	if err := exec.Command("wasmtime", "run", "--dir", dir, rfe).Run(); err != nil {
		t.Errorf("read_file+exit (6 bytes): want exit 0, got %v", err)
	}
	miss := build("rfmiss", `function main(): i32 {
    match (read_file("nope.txt")) {
        Ok(s) => { exit(0); return 0; },
        Err(e) => { exit(1); return 1; }
    }
    return 5;
}`)
	if err := exec.Command("wasmtime", "run", "--dir", dir, miss).Run(); err == nil {
		t.Errorf("read_file+exit (missing): want non-zero exit")
	}
}

// TestCmdLangComponentWrapCliComposedFileWrite exercises the
// composer's filesystem-write fold: write_file's open-chain
// (get-directories → open-at → write-via-stream →
// output-stream.blocking-write-and-flush) combined with a
// stream/structured side. write_file+print notably shares one
// blocking-write-and-flush lowering between the file's output-stream
// and stdout's. Both combos errored under -component-wrap-cli before
// the fold.
func TestCmdLangComponentWrapCliComposedFileWrite(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	build := func(name, src string) string {
		srcPath := filepath.Join(dir, name+".fern")
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		compPath := filepath.Join(dir, name+".wasm")
		cmd := exec.Command("go", "run", "./cmd/fern", "-target", "wasm-bin", "-component-wrap-cli", "-o", compPath, srcPath)
		cmd.Dir = projectRoot(t)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fern -component-wrap-cli (%s) failed: %v\n%s", name, err, out)
		}
		if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
			t.Fatalf("wasm-tools validate (%s) failed: %v\n%s", name, err, out)
		}
		return compPath
	}

	// write_file + exit: write "composed!\n", exit 0 on success.
	wfe := build("wfe", `function main(): i32 {
    match (write_file("wfe_out.txt", "composed!\n")) {
        Err(e) => { exit(2); return 2; },
        Ok(_) => { exit(0); return 0; }
    }
    return 5;
}`)
	if err := exec.Command("wasmtime", "run", "--dir", dir, wfe).Run(); err != nil {
		t.Errorf("write_file+exit: want exit 0, got %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "wfe_out.txt")); err != nil || string(got) != "composed!\n" {
		t.Errorf("write_file+exit wrote %q (err %v), want %q", got, err, "composed!\n")
	}

	// write_file + print: writes a file AND prints a status; both use
	// the shared blocking-write-and-flush. stdout = "ok", file = "data\n".
	wfp := build("wfp", `function main(): i32 {
    match (write_file("wfp_out.txt", "data\n")) {
        Err(e) => { print("fail"); return 2; },
        Ok(_) => { print("ok"); return 0; }
    }
    return 5;
}`)
	cmd := exec.Command("wasmtime", "run", "--dir", dir, wfp)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Errorf("write_file+print: want exit 0, got %v", err)
	}
	if out.String() != "ok\n" {
		t.Errorf("write_file+print stdout = %q, want %q", out.String(), "ok\n")
	}
	if got, err := os.ReadFile(filepath.Join(dir, "wfp_out.txt")); err != nil || string(got) != "data\n" {
		t.Errorf("write_file+print wrote %q (err %v), want %q", got, err, "data\n")
	}
}

// TestCmdLangComponentWrapCliComposedMemTramp exercises the
// composer's mem-trampoline imports (wall-clock now / args
// get-arguments) combined with a stream/structured side — single
// memory-needing trampoline imports that stand alone (no shared
// resource types). now()+print and args+exit errored under
// -component-wrap-cli before the fold.
func TestCmdLangComponentWrapCliComposedMemTramp(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	build := func(name, src string) string {
		srcPath := filepath.Join(dir, name+".fern")
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		compPath := filepath.Join(dir, name+".wasm")
		cmd := exec.Command("go", "run", "./cmd/fern", "-target", "wasm-bin", "-component-wrap-cli", "-o", compPath, srcPath)
		cmd.Dir = projectRoot(t)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fern -component-wrap-cli (%s) failed: %v\n%s", name, err, out)
		}
		if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
			t.Fatalf("wasm-tools validate (%s) failed: %v\n%s", name, err, out)
		}
		return compPath
	}

	// now() + print: wall-clock (mem-only trampoline) + stdout write.
	np := build("np", `function main(): i32 {
    var t: i64 = now_unix_ms();
    print("tick");
    if (t >= 0) { return 0; }
    return 1;
}`)
	cmd := exec.Command("wasmtime", "run", np)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Errorf("now()+print: want exit 0, got %v", err)
	}
	if out.String() != "tick\n" {
		t.Errorf("now()+print stdout = %q, want %q", out.String(), "tick\n")
	}

	// args() + exit: get-arguments (mem+realloc trampoline) + structured.
	ae := build("ae", `function main(): i32 {
    if (args().len() >= 1) { exit(0); return 0; }
    exit(3);
    return 3;
}`)
	if err := exec.Command("wasmtime", "run", ae, "progname").Run(); err != nil {
		t.Errorf("args()+exit: want exit 0 (argc>=1), got %v", err)
	}
}

// TestCmdLangComponentWrapCliWithStdinReadLine drives the Reader
// streaming API — stdin().read_line() — through `-component-wrap-cli`.
// Preview-2 has no fds, so the stdin Reader holds the get-stdin
// input-stream handle and Reader.read_line blocking-reads on it;
// the import set (get-stdin + blocking-read) matches the read-only
// route. Distinct from the bare read_line() builtin.
func TestCmdLangComponentWrapCliWithStdinReadLine(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "sr.fern")
	src := []byte(`function main(): i32 {
    match (stdin().read_line()) {
        Some(line) => { if (line.len() == 6) { return 0; } return 2; },
        None => { return 1; }
    }
    return 1;
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "sr.wasm")
	cmd := exec.Command("go", "run", "./cmd/fern", "-target", "wasm-bin", "-component-wrap-cli", "-o", compPath, srcPath)
	cmd.Dir = projectRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fern -component-wrap-cli (stdin().read_line) failed: %v\n%s", err, out)
	}
	if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
	}
	if out, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, out)
	} else if !strings.Contains(string(out), "wasi:cli/stdin@0.2.0") {
		t.Errorf("expected wasi:cli/stdin import, got:\n%s", out)
	}
	// "hello\n" → Some("hello\n") len 6 → exit 0.
	some := exec.Command("wasmtime", "run", compPath)
	some.Stdin = strings.NewReader("hello\n")
	if err := some.Run(); err != nil {
		t.Errorf("stdin().read_line with input: wasmtime run failed (want exit 0): %v", err)
	}
	// Empty stdin → None → exit 1.
	none := exec.Command("wasmtime", "run", compPath)
	none.Stdin = strings.NewReader("")
	if err := none.Run(); err == nil {
		t.Errorf("stdin().read_line EOF: wasmtime run exited 0, want non-zero")
	}
}

// TestCmdLangComponentWrapCliWithArgs drives a Fern program that
// reads argv via args() through `-component-wrap-cli`. The driver
// detects the args-only preview-2 import
// (wasi:cli/environment::get-arguments), rebuilds with cabi_realloc
// exported (the get-arguments canon-lower allocates the returned
// list<string> through it), and routes via ComposePreview2CliRun.
//
// Observed via exit code: the program returns 0 when argv has the
// expected count (program name + 2 extra = 3), 1 otherwise.
func TestCmdLangComponentWrapCliWithArgs(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "a.fern")
	src := []byte(`function main(): i32 {
    if (args().len() == 3) { return 0; }
    return 1;
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "a.wasm")
	cmd := exec.Command("go", "run", "./cmd/fern",
		"-target", "wasm-bin",
		"-component-wrap-cli",
		"-o", compPath, srcPath)
	cmd.Dir = projectRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fern -component-wrap-cli (args) failed: %v\n%s", err, out)
	}
	if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
	}
	printOut, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, printOut)
	}
	for _, want := range []string{"wasi:cli/environment@0.2.0", "wasi:cli/run@0.2.0", "get-arguments"} {
		if !strings.Contains(string(printOut), want) {
			t.Errorf("expected %q in component, got:\n%s", want, printOut)
		}
	}
	// 2 extra args → argv len 3 → exit 0.
	three := exec.Command("wasmtime", "run", compPath, "x", "y")
	if err := three.Run(); err != nil {
		t.Errorf("args with 2 extra: wasmtime run failed (want exit 0): %v", err)
	}
	// No extra args → argv len 1 → exit 1.
	one := exec.Command("wasmtime", "run", compPath)
	if err := one.Run(); err == nil {
		t.Errorf("args with none: wasmtime run exited 0, want non-zero")
	}
}

// TestCmdLangComponentWrapCliWithEnv drives a Fern program that
// reads an env var via env() through `-component-wrap-cli`. The
// driver detects the env-only preview-2 import
// (wasi:cli/environment::get-environment), rebuilds with
// cabi_realloc exported, and routes via ComposePreview2CliRun.
//
// Observed via exit code: the program returns 0 when env("FOO")
// resolves (Some), 1 when unset (None).
func TestCmdLangComponentWrapCliWithEnv(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "e.fern")
	src := []byte(`function main(): i32 {
    match (env("FOO")) {
        Some(v) => { return 0; },
        None => { return 1; }
    }
    return 1;
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "e.wasm")
	cmd := exec.Command("go", "run", "./cmd/fern",
		"-target", "wasm-bin",
		"-component-wrap-cli",
		"-o", compPath, srcPath)
	cmd.Dir = projectRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fern -component-wrap-cli (env) failed: %v\n%s", err, out)
	}
	if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
	}
	printOut, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, printOut)
	}
	for _, want := range []string{"wasi:cli/environment@0.2.0", "wasi:cli/run@0.2.0", "get-environment"} {
		if !strings.Contains(string(printOut), want) {
			t.Errorf("expected %q in component, got:\n%s", want, printOut)
		}
	}
	// FOO set → Some → exit 0.
	set := exec.Command("wasmtime", "run", "--env", "FOO=bar", compPath)
	if err := set.Run(); err != nil {
		t.Errorf("env FOO set: wasmtime run failed (want exit 0): %v", err)
	}
	// FOO unset → None → exit 1.
	unset := exec.Command("wasmtime", "run", compPath)
	if err := unset.Run(); err == nil {
		t.Errorf("env FOO unset: wasmtime run exited 0, want non-zero (None arm)")
	}
}

// TestCmdLangComponentWrapCliWithReadFile drives a Fern program
// that reads a file via read_file() through `-component-wrap-cli`.
// The driver detects the four-import read-file preview-2 shape
// (wasi:filesystem/preopens::get-directories +
// wasi:filesystem/types::{open-at,read-via-stream} +
// wasi:io/streams::blocking-read), rebuilds with cabi_realloc
// exported, and routes via ComposePreview2CliRun. The whole
// chain (get-directories → open-at → read-via-stream →
// blocking-read loop) runs under `wasmtime run --dir`.
//
// Observed via exit code: 0 when read_file returns Ok with the
// expected length, 1 on Err (missing file) or wrong length.
func TestCmdLangComponentWrapCliWithReadFile(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	// A file whose content spans more than one 4096-byte chunk to
	// exercise the doubling accumulator.
	content := strings.Repeat("x", 10000)
	if err := os.WriteFile(filepath.Join(dir, "data.txt"), []byte(content), 0o644); err != nil {
		t.Fatalf("write data: %v", err)
	}
	srcPath := filepath.Join(dir, "rf.fern")
	src := []byte(`function main(): i32 {
    match (read_file("data.txt")) {
        Ok(s) => { if (s.len() == 10000) { return 0; } return 1; },
        Err(e) => { return 1; }
    }
    return 1;
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "rf.wasm")
	cmd := exec.Command("go", "run", "./cmd/fern",
		"-target", "wasm-bin",
		"-component-wrap-cli",
		"-o", compPath, srcPath)
	cmd.Dir = projectRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fern -component-wrap-cli (read_file) failed: %v\n%s", err, out)
	}
	if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
	}
	printOut, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, printOut)
	}
	for _, want := range []string{
		"wasi:filesystem/preopens@0.2.0", "wasi:filesystem/types@0.2.0",
		"wasi:io/streams@0.2.0", "wasi:cli/run@0.2.0",
	} {
		if !strings.Contains(string(printOut), want) {
			t.Errorf("expected %q in component, got:\n%s", want, printOut)
		}
	}
	// Present file (10000 bytes) → Ok, correct length → exit 0.
	ok := exec.Command("wasmtime", "run", "--dir", dir, compPath)
	if err := ok.Run(); err != nil {
		t.Errorf("read_file present: wasmtime run failed (want exit 0): %v", err)
	}
	// Missing file → open-at fails → Err → exit 1.
	miss := filepath.Join(dir, "rfmiss.fern")
	if err := os.WriteFile(miss, []byte(`function main(): i32 {
    match (read_file("nope.txt")) { Ok(s) => { return 0; }, Err(e) => { return 1; } }
    return 1;
}`), 0o644); err != nil {
		t.Fatalf("write miss src: %v", err)
	}
	missComp := filepath.Join(dir, "rfmiss.wasm")
	mc := exec.Command("go", "run", "./cmd/fern", "-target", "wasm-bin", "-component-wrap-cli", "-o", missComp, miss)
	mc.Dir = projectRoot(t)
	if out, err := mc.CombinedOutput(); err != nil {
		t.Fatalf("fern (read_file miss) failed: %v\n%s", err, out)
	}
	if err := exec.Command("wasmtime", "run", "--dir", dir, missComp).Run(); err == nil {
		t.Errorf("read_file missing: wasmtime run exited 0, want non-zero (Err arm)")
	}
}

// TestCmdLangComponentWrapCliWithOpenReader drives the file Reader
// API — open_reader(path) then Reader.read_line / read_chunk —
// through `-component-wrap-cli`. Preview-2 has no fds: open_reader
// opens via get-directories → open-at → read-via-stream and stores
// the stream handle in the Reader; the read methods blocking-read on
// it. The import set (open chain + blocking-read) matches the
// read-file route, so no new wrap is needed.
func TestCmdLangComponentWrapCliWithOpenReader(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "data.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write data: %v", err)
	}
	build := func(name, src string) string {
		srcPath := filepath.Join(dir, name+".fern")
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		compPath := filepath.Join(dir, name+".wasm")
		cmd := exec.Command("go", "run", "./cmd/fern", "-target", "wasm-bin", "-component-wrap-cli", "-o", compPath, srcPath)
		cmd.Dir = projectRoot(t)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fern -component-wrap-cli (%s) failed: %v\n%s", name, err, out)
		}
		if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
			t.Fatalf("wasm-tools validate (%s) failed: %v\n%s", name, err, out)
		}
		return compPath
	}
	// read_line: first line "hello\n" is 6 bytes → exit 0.
	rl := build("orl", `function main(): i32 {
    match (open_reader("data.txt")) {
        Ok(r) => {
            match (r.read_line()) {
                Some(line) => { if (line.len() == 6) { return 0; } return 2; },
                None => { return 3; }
            }
            return 4;
        },
        Err(e) => { return 1; }
    }
    return 5;
}`)
	if err := exec.Command("wasmtime", "run", "--dir", dir, rl).Run(); err != nil {
		t.Errorf("open_reader.read_line: wasmtime run failed (want exit 0): %v", err)
	}
	// read_chunk(4): "hell" is 4 bytes → exit 0.
	rc := build("orc", `function main(): i32 {
    match (open_reader("data.txt")) {
        Ok(r) => {
            match (r.read_chunk(4)) {
                Some(chunk) => { if (chunk.len() == 4) { return 0; } return 2; },
                None => { return 3; }
            }
            return 4;
        },
        Err(e) => { return 1; }
    }
    return 5;
}`)
	if err := exec.Command("wasmtime", "run", "--dir", dir, rc).Run(); err != nil {
		t.Errorf("open_reader.read_chunk: wasmtime run failed (want exit 0): %v", err)
	}
	// Missing file → open-at fails → Err → non-zero.
	miss := build("ormiss", `function main(): i32 {
    match (open_reader("nope.txt")) {
        Ok(r) => { match (r.read_line()) { Some(l) => { return 7; }, None => { return 8; } } return 4; },
        Err(e) => { return 1; }
    }
    return 5;
}`)
	if err := exec.Command("wasmtime", "run", "--dir", dir, miss).Run(); err == nil {
		t.Errorf("open_reader missing: wasmtime run exited 0, want non-zero (Err arm)")
	}
}

// TestCmdLangComponentWrapCliWithWriteFile drives a Fern program
// that writes a file via write_file() through `-component-wrap-cli`.
// The driver detects the four-import write-file preview-2 shape
// (get-directories + descriptor.{open-at,write-via-stream} +
// output-stream.blocking-write-and-flush), rebuilds with
// cabi_realloc exported, and routes via ComposePreview2CliRun.
// The chain runs under `wasmtime run --dir` and the host file is
// checked afterward.
func TestCmdLangComponentWrapCliWithWriteFile(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	// > 4096 bytes to exercise the chunked blocking-write-and-flush
	// loop (wasmtime caps each write at 4096).
	const n = 5000
	srcPath := filepath.Join(dir, "wf.fern")
	src := []byte(`function main(): i32 {
    match (write_file("out.txt", "` + strings.Repeat("z", n) + `")) {
        Err(e) => { return 1; },
        Ok(_) => { return 0; }
    }
    return 1;
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "wf.wasm")
	cmd := exec.Command("go", "run", "./cmd/fern",
		"-target", "wasm-bin",
		"-component-wrap-cli",
		"-o", compPath, srcPath)
	cmd.Dir = projectRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fern -component-wrap-cli (write_file) failed: %v\n%s", err, out)
	}
	if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
	}
	printOut, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, printOut)
	}
	for _, want := range []string{
		"wasi:filesystem/preopens@0.2.0", "wasi:filesystem/types@0.2.0",
		"wasi:io/streams@0.2.0", "wasi:cli/run@0.2.0",
	} {
		if !strings.Contains(string(printOut), want) {
			t.Errorf("expected %q in component, got:\n%s", want, printOut)
		}
	}
	// Run → None (success) → exit 0; then verify the host file.
	if err := exec.Command("wasmtime", "run", "--dir", dir, compPath).Run(); err != nil {
		t.Fatalf("write_file: wasmtime run failed (want exit 0): %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatalf("read back out.txt: %v", err)
	}
	if len(got) != n {
		t.Errorf("out.txt = %d bytes, want %d", len(got), n)
	}
	if want := strings.Repeat("z", n); string(got) != want {
		t.Errorf("out.txt content mismatch (len got=%d want=%d)", len(got), n)
	}
}

// TestCmdLangComponentWrapCliWithOpenWriter drives the file Writer
// API — open_writer(path) then Writer.write — through
// `-component-wrap-cli`. Preview-2 has no fds: open_writer opens via
// get-directories → open-at(create|truncate) → write-via-stream and
// stores the output-stream handle in the Writer; write
// blocking-write-and-flushes on it. The import set (open chain +
// blocking-write-and-flush) matches the write-file route.
func TestCmdLangComponentWrapCliWithOpenWriter(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "ow.fern")
	src := []byte(`function main(): i32 {
    match (open_writer("out.txt")) {
        Ok(w) => {
            match (w.write("hello world\n")) {
                Some(e) => { return 2; },
                None => { return 0; }
            }
            return 4;
        },
        Err(e) => { return 1; }
    }
    return 5;
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "ow.wasm")
	cmd := exec.Command("go", "run", "./cmd/fern", "-target", "wasm-bin", "-component-wrap-cli", "-o", compPath, srcPath)
	cmd.Dir = projectRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fern -component-wrap-cli (open_writer) failed: %v\n%s", err, out)
	}
	if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
	}
	if err := exec.Command("wasmtime", "run", "--dir", dir, compPath).Run(); err != nil {
		t.Errorf("open_writer.write: wasmtime run failed (want exit 0): %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatalf("read out.txt: %v", err)
	}
	if string(got) != "hello world\n" {
		t.Errorf("out.txt = %q, want %q", string(got), "hello world\n")
	}
}

// TestCmdLangComponentWrapCliBareOpen covers the bare-open gap: a
// program that opens a Reader/Writer but never reads/writes it. The
// open-chain imports only get-directories + open-at +
// read/write-via-stream (3 calls, no blocking-read/-write), so the
// composer must import wasi:io/streams for the input/output-stream
// *type* (the via-stream result) yet pass no io/streams arg and
// lower no stream method. These errored under -component-wrap-cli
// before ReadStream/WriteStream were decoupled from the open-chain.
func TestCmdLangComponentWrapCliBareOpen(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "data.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write data: %v", err)
	}
	run := func(name, src string) {
		srcPath := filepath.Join(dir, name+".fern")
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		compPath := filepath.Join(dir, name+".wasm")
		cmd := exec.Command("go", "run", "./cmd/fern", "-target", "wasm-bin", "-component-wrap-cli", "-o", compPath, srcPath)
		cmd.Dir = projectRoot(t)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fern -component-wrap-cli (%s) failed: %v\n%s", name, err, out)
		}
		if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
			t.Fatalf("wasm-tools validate (%s) failed: %v\n%s", name, err, out)
		}
		if err := exec.Command("wasmtime", "run", "--dir", dir, compPath).Run(); err != nil {
			t.Errorf("%s: wasmtime run failed (want exit 0): %v", name, err)
		}
	}
	// open_reader, never read → Ok → exit 0.
	run("bareread", `function main(): i32 {
    match (open_reader("data.txt")) {
        Ok(r) => { return 0; },
        Err(e) => { return 1; }
    }
    return 5;
}`)
	// open_writer, never write → Ok → exit 0.
	run("barewrite", `function main(): i32 {
    match (open_writer("bw_out.txt")) {
        Ok(w) => { return 0; },
        Err(e) => { return 1; }
    }
    return 5;
}`)
}

// TestCmdLangComponentWrapCliWithOpenAppender drives open_appender:
// preview-2 has no fd-append flag, so the composer's FileAppend
// dimension opens via get-directories → open-at(CREATE, no TRUNCATE)
// → append-via-stream (the offset-less method) and stores the
// EOF-positioned output-stream handle; Writer.write then appends. The
// existing file's contents must be preserved.
func TestCmdLangComponentWrapCliWithOpenAppender(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "log.txt"), []byte("line1\n"), 0o644); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	srcPath := filepath.Join(dir, "ap.fern")
	src := []byte(`function main(): i32 {
    match (open_appender("log.txt")) {
        Ok(w) => {
            match (w.write("line2\n")) {
                Some(e) => { return 2; },
                None => { return 0; }
            }
            return 4;
        },
        Err(e) => { return 1; }
    }
    return 5;
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "ap.wasm")
	cmd := exec.Command("go", "run", "./cmd/fern", "-target", "wasm-bin", "-component-wrap-cli", "-o", compPath, srcPath)
	cmd.Dir = projectRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fern -component-wrap-cli (open_appender) failed: %v\n%s", err, out)
	}
	if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
	}
	if err := exec.Command("wasmtime", "run", "--dir", dir, compPath).Run(); err != nil {
		t.Errorf("open_appender.write: wasmtime run failed (want exit 0): %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "log.txt"))
	if err != nil {
		t.Fatalf("read log.txt: %v", err)
	}
	// Append, not truncate: both lines present.
	if string(got) != "line1\nline2\n" {
		t.Errorf("log.txt = %q, want %q", string(got), "line1\nline2\n")
	}
}

// TestCmdLangComponentWrapCliWithWrite drives a Lang program
// that calls write("hello") through `-component-wrap-cli`. With
// __fern_write migrated to preview-2 in the same slice, the
// program produces a component that writes "hello" (no trailing
// newline) to stdout under `wasmtime run`.
//
// The print / write distinction mirrors fmt.Println / fmt.Print:
// print appends '\n', write doesn't.
func TestCmdLangComponentWrapCliWithWrite(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "hello.fern")
	src := []byte(`function main(): i32 {
    write("hello");
    return 0;
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "hello.wasm")
	cmd := exec.Command("go", "run", "./cmd/fern",
		"-target", "wasm-bin",
		"-component-wrap-cli",
		"-o", compPath, srcPath)
	cmd.Dir = projectRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fern -component-wrap-cli (write) failed: %v\n%s", err, out)
	}
	runCmd := exec.Command("wasmtime", "run", compPath)
	var stdout, stderr bytes.Buffer
	runCmd.Stdout = &stdout
	runCmd.Stderr = &stderr
	if err := runCmd.Run(); err != nil {
		t.Fatalf("wasmtime run failed: %v\nstderr:\n%s", err, stderr.String())
	}
	if got := stdout.String(); got != "hello" {
		t.Errorf("wasmtime stdout = %q, want %q", got, "hello")
	}
}

// TestCmdLangComponentWrapCliWithEprint drives a Lang program
// that calls eprint("oops") through `-component-wrap-cli`. With
// __fern_eprint migrated to preview-2 (wasi:cli/stderr +
// wasi:io/streams), the program produces a component that writes
// "oops\n" to STDERR under `wasmtime run` — stdout stays empty.
func TestCmdLangComponentWrapCliWithEprint(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "oops.fern")
	src := []byte(`function main(): i32 {
    eprint("oops");
    return 0;
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "oops.wasm")
	cmd := exec.Command("go", "run", "./cmd/fern",
		"-target", "wasm-bin",
		"-component-wrap-cli",
		"-o", compPath, srcPath)
	cmd.Dir = projectRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fern -component-wrap-cli (eprint) failed: %v\n%s", err, out)
	}
	if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
	}
	printOut, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, printOut)
	}
	if !strings.Contains(string(printOut), "wasi:cli/stderr@0.2.0") {
		t.Errorf("expected wasi:cli/stderr@0.2.0 import in component, got:\n%s", printOut)
	}
	runCmd := exec.Command("wasmtime", "run", compPath)
	var stdout, stderr bytes.Buffer
	runCmd.Stdout = &stdout
	runCmd.Stderr = &stderr
	if err := runCmd.Run(); err != nil {
		t.Fatalf("wasmtime run failed: %v\nstderr:\n%s", err, stderr.String())
	}
	if stdout.String() != "" {
		t.Errorf("eprint wrote to stdout = %q, want empty", stdout.String())
	}
	if got := stderr.String(); got != "oops\n" {
		t.Errorf("wasmtime stderr = %q, want %q", got, "oops\n")
	}
}

// TestCmdLangComponentWrapCliWithPutchar drives a Lang program
// that calls putchar() through `-component-wrap-cli`. putchar
// shares print's preview-2 imports (wasi:cli/stdout +
// blocking-write-and-flush), so the existing print-only driver
// routing handles it without a separate code path. Writes the
// three bytes 'h','i','!' to stdout.
func TestCmdLangComponentWrapCliWithPutchar(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "putc.fern")
	src := []byte(`function main(): i32 {
    putchar(104);
    putchar(105);
    putchar(33);
    return 0;
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "putc.wasm")
	cmd := exec.Command("go", "run", "./cmd/fern",
		"-target", "wasm-bin",
		"-component-wrap-cli",
		"-o", compPath, srcPath)
	cmd.Dir = projectRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fern -component-wrap-cli (putchar) failed: %v\n%s", err, out)
	}
	runCmd := exec.Command("wasmtime", "run", compPath)
	var stdout, stderr bytes.Buffer
	runCmd.Stdout = &stdout
	runCmd.Stderr = &stderr
	if err := runCmd.Run(); err != nil {
		t.Fatalf("wasmtime run failed: %v\nstderr:\n%s", err, stderr.String())
	}
	if got := stdout.String(); got != "hi!" {
		t.Errorf("wasmtime stdout = %q, want %q", got, "hi!")
	}
}

// TestCmdLangComponentWrapCliWithRandomInt drives a Lang program
// that calls the stdlib `random_int(lo, hi)` helper (from
// std/math) through `-component-wrap-cli`. random_int internally
// calls `random_bytes(3)`, so this test validates that the
// random_bytes preview-2 migration cascades through to user code
// that consumes the stdlib helper — the actual "why does this
// matter" assertion behind that migration.
//
// The program picks a random int in [0, 100) and exits 0 iff it
// falls in range. The probability of a false negative is 0
// (any well-formed implementation returns a value in [0, 100)).
func TestCmdLangComponentWrapCliWithRandomInt(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "randint.fern")
	src := []byte(`
import "std/math";
function main(): i32 {
    var r: i32 = math.random_int(0, 100);
    if (r >= 0 && r < 100) { return 0; }
    return 1;
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "randint.wasm")
	cmd := exec.Command("go", "run", "./cmd/fern",
		"-target", "wasm-bin",
		"-component-wrap-cli",
		"-o", compPath, srcPath)
	cmd.Dir = projectRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fern -component-wrap-cli (random_int) failed: %v\n%s", err, out)
	}
	if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
	}
	printOut, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, printOut)
	}
	for _, want := range []string{"wasi:random/random@0.2.0", "wasi:cli/run@0.2.0"} {
		if !strings.Contains(string(printOut), want) {
			t.Errorf("expected %q in component, got:\n%s", want, printOut)
		}
	}
	if err := exec.Command("wasmtime", "run", compPath).Run(); err != nil {
		t.Fatalf("wasmtime run failed: %v", err)
	}
}

// TestCmdLangComponentWrapCliWithMultipleImports drives a Lang
// program that pulls in TWO preview-2 WASI imports
// simultaneously: wasi:random/random (via random_i32) and
// wasi:cli/exit (via exit()). Confirms the multi-import loop in
// ComposePreview2CliRun produces a component that wasmtime
// accepts and runs cleanly under `wasmtime run` (no --invoke).
//
// The Lang program asks for a random i32 and exits 0 — both
// imports get exercised at runtime.
func TestCmdLangComponentWrapCliWithMultipleImports(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "multi.fern")
	src := []byte(`function main(): i32 {
    var r: i32 = random_i32();
    if (r == r) {
        exit(0);
    }
    return 0;
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "multi.wasm")
	cmd := exec.Command("go", "run", "./cmd/fern",
		"-target", "wasm-bin",
		"-component-wrap-cli",
		"-o", compPath, srcPath)
	cmd.Dir = projectRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fern -component-wrap-cli (multi) failed: %v\n%s", err, out)
	}
	if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
	}
	printOut, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, printOut)
	}
	for _, want := range []string{
		"wasi:random/random@0.2.0",
		"wasi:cli/exit@0.2.0",
		"wasi:cli/run@0.2.0",
	} {
		if !strings.Contains(string(printOut), want) {
			t.Errorf("expected %q in component, got:\n%s", want, printOut)
		}
	}
	if err := exec.Command("wasmtime", "run", compPath).Run(); err != nil {
		t.Fatalf("wasmtime run failed: %v", err)
	}
}

// TestCmdLangComponentWrapCliWithRandomBytes drives a Lang
// program that calls random_bytes(n) through `-component-wrap-cli`.
// With the preview-2 migration of __fern_random_bytes (loop of
// get-random-u64() into a padded buffer), the produced component
// imports only wasi:random/random@0.2.0 + exports wasi:cli/run@0.2.0
// and runs cleanly under `wasmtime run`.
//
// The Lang program asks for a 3-byte slice (the common
// random_int(0, n) shape) so the loop has to handle n < 8 + the
// padding-up-to-8 path.
func TestCmdLangComponentWrapCliWithRandomBytes(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "randbytes.fern")
	src := []byte(`function main(): i32 {
    var b: string = random_bytes(3);
    if (b.len() == 3) { return 0; }
    return 1;
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "randbytes.wasm")
	cmd := exec.Command("go", "run", "./cmd/fern",
		"-target", "wasm-bin",
		"-component-wrap-cli",
		"-o", compPath, srcPath)
	cmd.Dir = projectRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fern -component-wrap-cli (random_bytes) failed: %v\n%s", err, out)
	}
	if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
	}
	printOut, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, printOut)
	}
	for _, want := range []string{"wasi:random/random@0.2.0", "wasi:cli/run@0.2.0"} {
		if !strings.Contains(string(printOut), want) {
			t.Errorf("expected %q in component, got:\n%s", want, printOut)
		}
	}
	if err := exec.Command("wasmtime", "run", compPath).Run(); err != nil {
		t.Fatalf("wasmtime run failed: %v", err)
	}
}

// TestCmdLangTargetWasmNoAdapter exercises the new default-path
// wiring: `-target wasm` without `-wasi-adapter` routes through
// the Go-side preview-2 encoder (cli-run shape) and produces a
// component runnable via plain `wasmtime run prog.wasm`. No
// `wasm-tools` shell-out involved. Closes the loop on the
// preview-2 import migrations — for programs whose imports are
// all migrated, the default `-target wasm` now Just Works
// without an adapter.
func TestCmdLangTargetWasmNoAdapter(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "ok.fern")
	src := []byte(`function main(): i32 {
    exit(0);
    return 0;
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "ok.wasm")
	cmd := exec.Command("go", "run", "./cmd/fern",
		"-target", "wasm",
		"-o", compPath, srcPath)
	cmd.Dir = projectRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm (no adapter) failed: %v\n%s", err, out)
	}
	if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
	}
	printOut, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, printOut)
	}
	for _, want := range []string{"wasi:cli/exit@0.2.0", "wasi:cli/run@0.2.0"} {
		if !strings.Contains(string(printOut), want) {
			t.Errorf("expected %q in component, got:\n%s", want, printOut)
		}
	}
	if err := exec.Command("wasmtime", "run", compPath).Run(); err != nil {
		t.Fatalf("wasmtime run failed: %v", err)
	}
}

// TestCmdLangTargetWasmRejectsUnsupported confirms `-target wasm`
// surfaces a clear error when the program pulls in a combination the
// Go-side composer can't place yet (here read + append files — the
// combined filesystem/types body is read+write, not read+append).
func TestCmdLangTargetWasmRejectsUnsupported(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "unsupported.fern")
	// read + append in one program isn't composable: the combined
	// filesystem/types body is read+write, not read+append, so neither
	// path claims it and the driver must reject.
	// (Earlier still-unsupported combos — args, tcp_recv, tcp+print,
	// tcp+clock, tcp+file-read, read+write files — each became supported
	// as the composer grew; read+append is the current gap.)
	src := []byte(`function main(): i32 {
    match (read_file("a")) { Ok(c) => {}, Err(e) => {} }
    match (open_appender("b")) { Ok(w) => { w.close(); }, Err(e) => {} }
    return 0;
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "unsupported.wasm")
	cmd := exec.Command("go", "run", "./cmd/fern",
		"-target", "wasm",
		"-o", compPath, srcPath)
	cmd.Dir = projectRoot(t)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected unsupported-imports rejection, got success: %s", out)
	}
	if !strings.Contains(string(out), "unrecognised imports") {
		t.Errorf("expected unrecognised-imports error, got:\n%s", out)
	}
}

// TestCmdLangComponentWrapVoidMain confirms `-component-wrap`
// (the non-cli variant, lifts main as a top-level export)
// handles a void `main` via the SynthCliRun wrapper too. The
// component-level export is still named "main" returning u32;
// the wrapper internally turns void → i32.const 0 so wasmtime's
// canon-lift sees a `() -> i32` core export.
//
// Verifies via `wasmtime run --invoke "main()"` that main() now
// returns 0 (the wrapper's fallback).
func TestCmdLangComponentWrapVoidMain(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "void.fern")
	src := []byte(`function main(): void {
    var t: i64 = monotonic_ns();
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "void.wasm")
	cmd := exec.Command("go", "run", "./cmd/fern",
		"-target", "wasm-bin",
		"-component-wrap",
		"-o", compPath, srcPath)
	cmd.Dir = projectRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fern -component-wrap (void main) failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasmtime", "run", "--invoke", "main()", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run failed: %v\n%s", err, out)
	}
	got := strings.TrimSpace(string(out))
	if got != "0" {
		t.Errorf("expected main() => 0 (void-main wrapper), got %q", got)
	}
}

// TestCmdLangComponentWrap exercises the new `-component-wrap`
// driver flag, which uses the Go-side encoder to produce a
// self-contained preview-2 component without shelling out to
// `wasm-tools component new --adapt`.
//
// End-to-end: source → driver → wasmtime → exit code 42.
//
// This is the path-of-record for retiring the wasm-tools shell-
// out on the no-WASI-imports class of programs. Programs that
// pull in proc_exit / fd_write still need the existing adapter
// composition; preview-2 import migration of wasmbin lifts more
// programs into the no-imports class.
func TestCmdLangComponentWrap(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "min.fern")
	if err := os.WriteFile(srcPath, []byte(`function main(): i32 { return 42; }`), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "min.wasm")
	cmd := exec.Command("go", "run", "./cmd/fern",
		"-target", "wasm-bin",
		"-component-wrap",
		"-o", compPath, srcPath)
	cmd.Dir = projectRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fern -component-wrap failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasmtime", "run", "--invoke", "main()", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run failed: %v\n%s", err, out)
	}
	got := strings.TrimSpace(string(out))
	if got != "42" {
		t.Errorf("wasmtime stdout = %q, want %q", got, "42")
	}
}

// projectRoot returns the lang repo root for use as the working
// directory when invoking `go run ./cmd/fern` from a test under
// internal/e2e/. Walks up from the test file's directory until a
// go.mod is found.
func projectRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	d := wd
	for {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			t.Fatalf("no go.mod found from %s", wd)
		}
		d = parent
	}
}

// TestWASMComponentGoEncoderRunsLangCore is the first end-to-end
// path that retires `wasm-tools component new --adapt` for a real
// (if trivial) Lang program: it compiles a no-WASI-imports Lang
// program through wasmbin, wraps the resulting core module with
// the Go-side `component.BuildLiftedExportComponent`, and confirms
// `wasmtime run --invoke main()` returns the expected value.
//
// "No-WASI-imports" is the key restriction — wasmbin's
// dead-code-elimination drops the proc_exit / fd_write imports
// when nothing uses them, so a trivial `function main(): i32
// { return 42; }` compiles to a self-contained core module that
// the Go encoder can wrap directly without an adapter.
//
// This is the smallest concrete proof that the production driver
// could call `component.BuildLiftedExportComponent` instead of
// shelling out to `wasm-tools component new --adapt` for the
// no-import case. Real programs with WASI imports still need the
// adapter (or the future preview-2 import migration in wasmbin).
func TestWASMComponentGoEncoderRunsLangCore(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	const src = `function main(): i32 { return 42; }`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
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
	coreBytes, err := wasmbin.BuildWithOptions(prog, info, wasmbin.BuildOptions{})
	if err != nil {
		t.Fatalf("wasmbin.Build: %v", err)
	}

	// Wrap directly with the Go-side encoder — no wasm-tools.
	comp := component.BuildLiftedExportComponent(
		coreBytes, "main", "main",
		nil, nil, // no params
		component.CValtypeU32,
	)

	dir := t.TempDir()
	compPath := filepath.Join(dir, "out.wasm")
	if err := os.WriteFile(compPath, comp, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasmtime", "run", "--invoke", "main()", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run failed: %v\n%s", err, out)
	}
	got := strings.TrimSpace(string(out))
	if got != "42" {
		t.Errorf("wasmtime stdout = %q, want %q", got, "42")
	}
}

// TestWASMComponentGoLangByteEquivalence pins the Go-side
// `internal/wasm/component.WrapWasiImported` against the Lang
// `build_wasi_imported_component` helper: given the same core
// module bytes + the same single WASI import shape, both must
// produce byte-identical output.
//
// This is the load-bearing guarantee that lets the production
// driver use either implementation interchangeably — and a
// regression alarm if one is updated without the other.
func TestWASMComponentGoLangByteEquivalence(t *testing.T) {
	// Build the same WASI-imported component on both sides and
	// assert the produced bytes are IDENTICAL — the component-level
	// analogue of TestWASMCoreEncoderGoLangByteEquivalence. No
	// wasm-tools needed: byte-equality is strictly stronger than a
	// `wasm-tools print` text comparison (identical bytes ⟹
	// identical print), and a separate test
	// (TestWASMComponentWasiHelperBuildsValid) already validates the
	// component is well-formed.
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/component";
import "std/wasm/encode";
import "std/wasm/imports";
function main(): i32 {
    var m: module.Module = module.module_new();
    var p0: u8[] = [encode.valtype_i32()];
    var r0: u8[] = [];
    m = m.with_types([p0], [r0]);
    m = m.with_imports(["wasi-exit"], ["exit"], [imports.import_func()], [imports.import_desc_func(0u32)]);
    m = m.with_functions([]);
    m = m.with_code([]);
    var core_bytes: u8[] = module.build(m);

    var pnames: string[] = ["code"];
    var pvals: u8[] = [component.cvaltype_u32()];
    var comp: u8[] = component.build_wasi_imported_component(core_bytes, "wasi:cli/exit@0.2.0", "exit", pnames, pvals, "wasi-exit");

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	langBytes := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		langBytes = append(langBytes, byte(n))
	}

	// Build the identical component on the Go side. The core module
	// goes through the production encoder (mod.Build) with the same
	// inputs the Lang program used — byte-equal by
	// TestWASMCoreEncoderGoLangByteEquivalence — then the same wrap.
	goMod := mod.New()
	goMod.TypeParams = [][]byte{{encode.ValtypeI32}} // type 0: (i32) -> ()
	goMod.TypeResults = [][]byte{{}}
	goMod.ImportModules = []string{"wasi-exit"}
	goMod.ImportNames = []string{"exit"}
	goMod.ImportKinds = []byte{imports.ImportFunc}
	goMod.ImportDescs = [][]byte{imports.ImportDescFunc(0)}
	goCore := mod.Build(goMod)
	goBytes := component.WrapWasiImported(goCore, []component.WasiImport{
		{
			InterfaceName:    "wasi:cli/exit@0.2.0",
			FuncName:         "exit",
			ParamNames:       []string{"code"},
			ParamValtypes:    []byte{component.CValtypeU32},
			CoreImportModule: "wasi-exit",
		},
	})

	if !bytes.Equal(langBytes, goBytes) {
		t.Fatalf("Lang vs Go component bytes differ:\n  lang (%d): % x\n  go   (%d): % x", len(langBytes), langBytes, len(goBytes), goBytes)
	}
}

// TestWASMCoreEncoderGoLangByteEquivalence is the byte-exact parity
// gate between the two core-module encoders: the Go production
// encoder (internal/wasm/{module,inst,sections,memory,...}) and the
// Fern reimplementation (internal/stdlib/std/wasm/*). Both build the
// SAME representative module — type / function / funcref-table /
// element / memory / export / code sections, with a body that
// exercises i32.const, memory.fill, memory.copy, and call_indirect —
// and the test asserts the produced bytes are identical.
//
// This locks the two implementations together: if a future change
// touches one encoder (an opcode, a section layout) without the
// matching change on the other side, the bytes diverge and this
// fails. It's the core-module analogue of the component-level
// TestWASMComponentGoLangByteEquivalence above.
func TestWASMCoreEncoderGoLangByteEquivalence(t *testing.T) {
	// --- Go side: build via internal/wasm/* (the production encoder).
	goMod := mod.New()
	goMod.TypeParams = [][]byte{{}}                   // type 0: () -> i32
	goMod.TypeResults = [][]byte{{encode.ValtypeI32}} //
	goMod.FunctionTypeidxs = []uint32{0, 0}           // func0 (callee), func1 (main)
	goMod.MemoryPresent = true
	goMod.MemoryMin = 1
	goMod.MemoryMax = -1
	goMod.TablePresent = true // funcref table, min 1
	goMod.TableMin = 1
	goMod.TableMax = -1
	goMod.ElementOffsets = []int32{0} // table[0] = func0
	goMod.ElementFuncidxs = [][]uint32{{0}}
	goMod.ExportNames = []string{"main"}
	goMod.ExportKinds = []byte{sections.ExportFunc}
	goMod.ExportIdxs = []uint32{1}

	// func0: memory.fill(0, 9, 1); memory.copy(4, 0, 1); i32.const 7.
	f0 := inst.InstI32Const(nil, 0)
	f0 = inst.InstI32Const(f0, 9)
	f0 = inst.InstI32Const(f0, 1)
	f0 = mem.InstMemoryFill(f0)
	f0 = inst.InstI32Const(f0, 4)
	f0 = inst.InstI32Const(f0, 0)
	f0 = inst.InstI32Const(f0, 1)
	f0 = mem.InstMemoryCopy(f0)
	f0 = inst.InstI32Const(f0, 7)
	f0body := inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), f0)

	// func1 (main): i32.const 0; call_indirect type 0 table 0.
	f1 := inst.InstI32Const(nil, 0)
	f1 = inst.InstCallIndirect(f1, 0, 0)
	f1body := inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), f1)

	goMod.CodeBodies = [][]byte{f0body, f1body}
	goBytes := mod.Build(goMod)

	// --- Lang side: build the identical module via std/wasm/*.
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/memory";
import "std/wasm/encode";
import "std/wasm/sections";
import "std/wasm/imports";
function main(): i32 {
    var m: module.Module = module.module_new();
    var p0: u8[] = [];
    var r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32, 0u32]);
    m = m.with_memory(1u32, 0 - 1);
    m = m.with_table(imports.reftype_funcref(), 1u32, 0 - 1);
    m = m.with_elements([0], [[0u32]]);
    m = m.with_exports(["main"], [sections.export_func()], [1u32]);

    var f0: u8[] = inst.inst_i32_const([], 0);
    f0 = inst.inst_i32_const(f0, 9);
    f0 = inst.inst_i32_const(f0, 1);
    f0 = memory.inst_memory_fill(f0);
    f0 = inst.inst_i32_const(f0, 4);
    f0 = inst.inst_i32_const(f0, 0);
    f0 = inst.inst_i32_const(f0, 1);
    f0 = memory.inst_memory_copy(f0);
    f0 = inst.inst_i32_const(f0, 7);
    var f0body: u8[] = inst.put_function_body([], inst.put_locals_empty([]), f0);

    var f1: u8[] = inst.inst_i32_const([], 0);
    f1 = inst.inst_call_indirect(f1, 0u32, 0u32);
    var f1body: u8[] = inst.put_function_body([], inst.put_locals_empty([]), f1);

    m = m.with_code([f0body, f1body]);
    var bytes: u8[] = module.build(m);

    var output: string = "";
    var i: i32 = 0;
    while (i < bytes.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (bytes[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	langBytes := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		langBytes = append(langBytes, byte(n))
	}

	if !bytes.Equal(goBytes, langBytes) {
		t.Fatalf("Go vs Lang core-module bytes differ:\n  go   (%d): % x\n  lang (%d): % x", len(goBytes), goBytes, len(langBytes), langBytes)
	}
}

// TestWASMComponentWasiHelperBuildsValid exercises the high-level
// `build_wasi_imported_component` helper. Same shape as the full-
// pipeline test (`TestWASMComponentWasiExitFullPipeline`), but with
// the seven section calls collapsed into one helper invocation.
func TestWASMComponentWasiHelperBuildsValid(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/component";
import "std/wasm/encode";
import "std/wasm/imports";
function main(): i32 {
    var m: module.Module = module.module_new();
    var p0: u8[] = [encode.valtype_i32()];
    var r0: u8[] = [];
    m = m.with_types([p0], [r0]);
    m = m.with_imports(["wasi"], ["exit"], [imports.import_func()], [imports.import_desc_func(0u32)]);
    m = m.with_functions([]);
    m = m.with_code([]);
    var core_bytes: u8[] = module.build(m);

    var pnames: string[] = ["code"];
    var pvals: u8[] = [component.cvaltype_u32()];
    var comp: u8[] = component.build_wasi_imported_component(core_bytes, "wasi:cli/exit@0.2.0", "exit", pnames, pvals, "wasi");

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	for _, want := range []string{
		"wasi:cli/exit@0.2.0",
		"alias export",
		"canon lower",
		"(core module",
		"with \"wasi\"",
	} {
		if !strings.Contains(watStr, want) {
			t.Errorf("expected %q in printed component, got:\n%s", want, watStr)
		}
	}
}

// TestWASMComponentWasiExitFullPipeline covers
// put_alias_section_instance_export_func by exercising the full
// WASI-style "import an interface, extract its function, lower
// into core, instantiate core module" pipeline:
//
//  1. type section: instance type { exit(code: u32) }
//  2. import section: wasi:cli/exit@0.2.0 (instance 0)
//  3. alias section: alias instance 0's "exit" export as
//     component-level func 0
//  4. canon section: lower func 0 into core func 0
//  5. core-instance section: wrap core func 0 as a
//     `(export "exit" (func 0))` core instance (instance 0)
//  6. core-module section: a core module that imports
//     "wasi" "exit" : (i32) -> ()
//  7. core-instance section: instantiate the core module
//     passing instance 0 as the "wasi" arg
//
// This is structurally exactly what `wasm-tools component new
// --adapt` produces for a CLI-world component using only one WASI
// import. The component validates under wasm-tools, demonstrating
// that std/wasm/component can produce the full WASI-CLI shape.
func TestWASMComponentWasiExitFullPipeline(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/component";
import "std/wasm/inst";
import "std/wasm/encode";
import "std/wasm/sections";
import "std/wasm/imports";
function main(): i32 {
    // Core module: imports "wasi" "exit" : (i32) -> () then
    // calls it with 0 (or some constant).
    var m: module.Module = module.module_new();
    var p0: u8[] = [encode.valtype_i32()];
    var r0: u8[] = [];
    m = m.with_types([p0], [r0]);
    m = m.with_imports(["wasi"], ["exit"], [imports.import_func()], [imports.import_desc_func(0u32)]);
    m = m.with_functions([]);
    m = m.with_code([]);
    var core_bytes: u8[] = module.build(m);

    // Component build.
    var pnames: string[] = ["code"];
    var pvals: u8[] = [component.cvaltype_u32()];

    var comp: u8[] = component.put_component_header([]);
    // Type 0: instance { export exit(code: u32) }
    comp = component.put_type_section_one_instance_one_func_export(comp, "exit", pnames, pvals);
    // Import "wasi:cli/exit@0.2.0" of type 0.
    comp = component.put_import_section_one_instance(comp, "wasi:cli/exit@0.2.0", 0u32);
    // Alias "exit" out of instance 0 -> component-level func 0.
    comp = component.put_alias_section_instance_export_func(comp, 0u32, "exit");
    // Lower component-level func 0 -> core func 0.
    comp = component.put_canon_section_lower_no_opts(comp, 0u32);
    // Package core func 0 under "exit" as core instance 0.
    comp = component.put_core_instance_section_from_one_func_export(comp, "exit", 0u32);
    // Embed the core module.
    comp = component.put_core_module_section(comp, core_bytes);
    // Instantiate it with "wasi" -> core instance 0.
    comp = component.put_core_instance_section_instantiate_with_one_instance_arg(comp, 0u32, "wasi", 0u32);

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	for _, want := range []string{
		"wasi:cli/exit@0.2.0",
		"alias export",
		"canon lower",
		"(core instance",
		"(core module",
		"with \"wasi\"",
	} {
		if !strings.Contains(watStr, want) {
			t.Errorf("expected %q in printed component, got:\n%s", want, watStr)
		}
	}
}

// TestWASMComponentWasiStdMultiExport covers
// put_type_section_one_instance_with_func_exports. Builds an
// instance type with two no-result func exports, like
// `wasi:io/streams::{blocking-flush, blocking-skip}` or the
// `wasi:cli/std{in,out,err}` family. Verifies wasm-tools picks up
// both exports.
func TestWASMComponentWasiStdMultiExport(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    var names: string[] = ["flush", "close"];
    var ps0: string[] = [];
    var pv0: u8[] = [];
    var ps1: string[] = [];
    var pv1: u8[] = [];
    var names_per: string[][] = [ps0, ps1];
    var valtypes_per: u8[][] = [pv0, pv1];

    var comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_one_instance_with_func_exports(comp, names, names_per, valtypes_per);
    comp = component.put_import_section_one_instance(comp, "test:multi/iface", 0u32);

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	for _, want := range []string{"\"flush\"", "\"close\"", "instance"} {
		if !strings.Contains(watStr, want) {
			t.Errorf("expected %q in printed component, got:\n%s", want, watStr)
		}
	}
}

// TestWASMComponentWasiExitImport covers
// put_type_section_one_instance_one_func_export + put_import_section_one_instance.
// Builds the WASI `wasi:cli/exit@0.2.0` interface shape: an instance
// type exporting `exit(code: u32)`, then imported under the
// interface-name label.
//
// `wasi:cli/exit` is the canonical "instance with one no-result
// function" WASI interface; any component that calls `exit` imports
// it via this shape.
func TestWASMComponentWasiExitImport(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    var comp: u8[] = component.put_component_header([]);
    // Type 0: instance { export "exit" (func (param "code" u32)) }
    var names: string[] = ["code"];
    var valtypes: u8[] = [component.cvaltype_u32()];
    comp = component.put_type_section_one_instance_one_func_export(comp, "exit", names, valtypes);
    // Import "wasi:cli/exit@0.2.0" of type 0 (the instance type).
    comp = component.put_import_section_one_instance(comp, "wasi:cli/exit@0.2.0", 0u32);

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	for _, want := range []string{"instance", "\"exit\"", "\"code\"", "\"wasi:cli/exit@0.2.0\""} {
		if !strings.Contains(watStr, want) {
			t.Errorf("expected %q in printed component, got:\n%s", want, watStr)
		}
	}
}

// TestWASMComponentCoreTypeFunc covers
// put_core_type_section_one_func: emits a component-level core-type
// section containing one core wasm functype `(func (param i32)
// (result i32))`. Verifies the printed WAT surfaces the
// `(core type ... (func ...))` shape.
//
// Core types live at the component level — distinct from both core
// module types (inside an embedded core module) and component types
// (declared via the type section, id 7). They're addressed by
// core-typeidx and used in places like core-instance instantiation
// args (for declaring expected core-import signatures).
func TestWASMComponentCoreTypeFunc(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
import "std/wasm/encode";
function main(): i32 {
    var params: u8[] = [encode.valtype_i32()];
    var results: u8[] = [encode.valtype_i32()];
    var comp: u8[] = component.put_component_header([]);
    comp = component.put_core_type_section_one_func(comp, params, results);

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	if !strings.Contains(watStr, "(core type") {
		t.Errorf("expected (core type ... in printed component, got:\n%s", watStr)
	}
	if !strings.Contains(watStr, "(func") {
		t.Errorf("expected (func ... shape, got:\n%s", watStr)
	}
}

// TestWASMComponentTypeFuncU32 covers
// put_type_section_one_func_no_param plus cvaltype_u32: emits a
// component-level type section defining `(func (result u32))` and
// verifies the bytes parse + print as the expected shape. The
// component valtype byte (0x79 for u32) is in a different number
// space than core wasm valtypes (i32 = 0x7f), so the test pins the
// chosen byte against `wasm-tools print`'s textual output.
func TestWASMComponentTypeFuncU32(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    var comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_one_func_no_param(comp, component.cvaltype_u32());

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	if !strings.Contains(watStr, "(type") {
		t.Errorf("expected (type ... in printed output, got:\n%s", watStr)
	}
	if !strings.Contains(watStr, "(result u32)") && !strings.Contains(watStr, "result u32") {
		t.Errorf("expected `result u32` in printed output, got:\n%s", watStr)
	}
}

// TestWASMComponentTypeFuncWithParams covers put_type_section_one_func
// with a non-empty parameter list: emits
// `(func (param "x" u32) (param "y" string) (result bool))` and
// verifies wasm-tools picks up both parameters with the right names
// and valtypes. Anchors the parallel-array (param_names,
// param_valtypes) calling convention against
// `wasm-tools print`'s textual output.
func TestWASMComponentTypeFuncWithParams(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    var names: string[] = ["x", "y"];
    var valtypes: u8[] = [component.cvaltype_u32(), component.cvaltype_string()];
    var comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_one_func(comp, names, valtypes, component.cvaltype_bool());

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	for _, want := range []string{
		"\"x\"",
		"\"y\"",
		"u32",
		"string",
		"bool",
	} {
		if !strings.Contains(watStr, want) {
			t.Errorf("expected %q in printed component, got:\n%s", want, watStr)
		}
	}
}

// TestWASMComponentTypeSectionTwoFuncs covers put_type_section_funcs
// in its non-trivial shape: a single type section that declares two
// functypes — `(func)` (no params, no result form via the trivial
// case... well actually a single u32 result) and
// `(func (param "n" u32) (result string))`. Verifies multi-entry
// support so real WASI worlds (which have many functypes) can be
// expressed.
func TestWASMComponentTypeSectionTwoFuncs(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    // Two functypes:
    //   0: ()         -> u32
    //   1: (n: u32)   -> string
    var names0: string[] = [];
    var valtypes0: u8[] = [];
    var names1: string[] = ["n"];
    var valtypes1: u8[] = [component.cvaltype_u32()];
    var names_per: string[][] = [names0, names1];
    var valtypes_per: u8[][] = [valtypes0, valtypes1];
    var results_per: u8[] = [component.cvaltype_u32(), component.cvaltype_string()];

    var comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_funcs(comp, names_per, valtypes_per, results_per);

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	for _, want := range []string{
		"result u32",
		"\"n\"",
		"result string",
	} {
		if !strings.Contains(watStr, want) {
			t.Errorf("expected %q in printed component, got:\n%s", want, watStr)
		}
	}
}

// TestWASMComponentTypeSection covers put_component_type_section,
// which writes the `component-type` custom section the WIT world
// machinery hangs its world descriptor on. wasm-tools doesn't
// validate the inner payload (custom sections are opaque to the
// validator), so we hand in a synthetic payload and assert
// `wasm-tools print` surfaces a `component-type` custom section
// carrying the expected bytes.
func TestWASMComponentTypeSectionEmbed(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    // Synthetic payload: 4 bytes of ASCII. wasm-tools doesn't
    // parse the inside of a component-type custom section so
    // anything goes for this structural test.
    var payload: u8[] = [];
    payload = payload.append(116u8);   // 't' = 0x74
    payload = payload.append(101u8);   // 'e' = 0x65
    payload = payload.append(115u8);   // 's' = 0x73
    payload = payload.append(116u8);   // 't' = 0x74

    var comp: u8[] = component.put_component_header([]);
    comp = component.put_component_type_section(comp, payload);

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	if !strings.Contains(watStr, "component-type") {
		t.Errorf("expected `component-type` in printed component, got:\n%s", watStr)
	}
}

// TestWASMComponentImportFunc covers put_import_section_one_func:
// declares an imported function `host-func : () -> u32` and verifies
// the bytes parse + print as the expected `(import "host-func" (func
// (type ...)))` shape. This is the label-form import (kind 0x00);
// WASI-style interface imports use the kind 0x01 form and land in a
// later slice.
func TestWASMComponentImportFunc(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    var comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_one_func_no_param(comp, component.cvaltype_u32());
    comp = component.put_import_section_one_func(comp, "host-func", 0u32);

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	if !strings.Contains(watStr, "(import") {
		t.Errorf("expected (import ... in printed output, got:\n%s", watStr)
	}
	if !strings.Contains(watStr, "\"host-func\"") {
		t.Errorf("expected import name \"host-func\", got:\n%s", watStr)
	}
	if !strings.Contains(watStr, "(func") {
		t.Errorf("expected imported (func ... shape, got:\n%s", watStr)
	}
}

// TestWASMComponentImportSectionTwoFuncs covers
// put_import_section_funcs in its multi-entry shape: a single
// import section with two label-form function imports, each
// pointing at a different functype in the type section. Confirms
// imports can be enumerated in a single section the way real WASI
// worlds need.
func TestWASMComponentImportSectionTwoFuncs(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    // Two functypes:
    //   0: () -> u32
    //   1: (n: u32) -> string
    var names0: string[] = [];
    var valtypes0: u8[] = [];
    var names1: string[] = ["n"];
    var valtypes1: u8[] = [component.cvaltype_u32()];
    var names_per: string[][] = [names0, names1];
    var valtypes_per: u8[][] = [valtypes0, valtypes1];
    var results_per: u8[] = [component.cvaltype_u32(), component.cvaltype_string()];

    var comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_funcs(comp, names_per, valtypes_per, results_per);

    // Two imports, one of each type.
    var imp_names: string[] = ["h0", "h1"];
    var imp_types: u32[] = [0u32, 1u32];
    comp = component.put_import_section_funcs(comp, imp_names, imp_types);

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	for _, want := range []string{
		"\"h0\"",
		"\"h1\"",
	} {
		if !strings.Contains(watStr, want) {
			t.Errorf("expected %q in printed component, got:\n%s", want, watStr)
		}
	}
	// Both import declarations should appear.
	if strings.Count(watStr, "(import ") < 2 {
		t.Errorf("expected at least 2 (import) declarations, got:\n%s", watStr)
	}
}

// TestWASMComponentCanonLower covers put_canon_section_lower_no_opts:
// imports a component-level function and lowers it back to a core-
// level function. The mirror of canon-lift exercised by
// TestWASMComponentLiftedExport. Together they cover both directions
// of the canonical-ABI boundary — lift (core → component) for
// exports, lower (component → core) for imports.
//
// Composition: header + type `(func (param "x" u32) (result u32))` +
// import "h" of that type + canon lower (func 0). The lowered core
// func gets index 0 in the core-func space; later slices wire it
// into a core-instance's instantiation args to actually feed the
// host import down into the core module.
func TestWASMComponentCanonLower(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    var names: string[] = ["x"];
    var valtypes: u8[] = [component.cvaltype_u32()];

    var comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_one_func(comp, names, valtypes, component.cvaltype_u32());
    comp = component.put_import_section_one_func(comp, "h", 0u32);
    comp = component.put_canon_section_lower_no_opts(comp, 0u32);

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	for _, want := range []string{
		"(import",
		"\"h\"",
		"canon lower",
		"(core func",
	} {
		if !strings.Contains(watStr, want) {
			t.Errorf("expected %q in printed component, got:\n%s", want, watStr)
		}
	}
}

// TestWASMComponentCanonLowersTwo covers
// put_canon_section_lowers_no_opts in its multi-entry shape:
// imports two component-level funcs and lowers both in a single
// canon section. Confirms the canon section can pack multiple
// lower entries the way a real WASI-world wrap does (one lower
// per host import).
func TestWASMComponentCanonLowersTwo(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    // One functype: () -> u32. Both imports re-use it.
    var no_names: string[] = [];
    var no_valtypes: u8[] = [];
    var imp_names: string[] = ["h0", "h1"];
    var imp_types: u32[] = [0u32, 0u32];

    var comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_one_func(comp, no_names, no_valtypes, component.cvaltype_u32());
    comp = component.put_import_section_funcs(comp, imp_names, imp_types);

    // Lower both imported funcs in one canon section.
    var lower_funcs: u32[] = [0u32, 1u32];
    comp = component.put_canon_section_lowers_no_opts(comp, lower_funcs);

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	// Both lower declarations should appear.
	if strings.Count(watStr, "canon lower") < 2 {
		t.Errorf("expected at least 2 `canon lower` declarations, got:\n%s", watStr)
	}
	for _, want := range []string{
		"\"h0\"",
		"\"h1\"",
	} {
		if !strings.Contains(watStr, want) {
			t.Errorf("expected %q in printed component, got:\n%s", want, watStr)
		}
	}
}

// TestWASMComponentInstanceFromTwoFuncExports covers
// put_core_instance_section_from_func_exports in its multi-export
// shape: builds one core instance packaging two lowered host funcs
// under different export names — the shape preview-2 WASI imports
// take when getting threaded down into the core module
// instantiation as a single "wasi" instance arg.
func TestWASMComponentInstanceFromTwoFuncExports(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    // Two functypes (one each so we can have two distinct
    // lowered core funcs). Both () -> u32 for simplicity.
    var no_names: string[] = [];
    var no_valtypes: u8[] = [];
    var names_per: string[][] = [no_names, no_names];
    var valtypes_per: u8[][] = [no_valtypes, no_valtypes];
    var results_per: u8[] = [component.cvaltype_u32(), component.cvaltype_u32()];

    var comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_funcs(comp, names_per, valtypes_per, results_per);

    // Two imports + lower each to a core func.
    var imp_names: string[] = ["h0", "h1"];
    var imp_types: u32[] = [0u32, 1u32];
    comp = component.put_import_section_funcs(comp, imp_names, imp_types);

    var lower_funcs: u32[] = [0u32, 1u32];
    comp = component.put_canon_section_lowers_no_opts(comp, lower_funcs);

    // Package both lowered core funcs into one core instance.
    var exp_names: string[] = ["stdout", "exit"];
    var exp_core_funcs: u32[] = [0u32, 1u32];
    comp = component.put_core_instance_section_from_func_exports(comp, exp_names, exp_core_funcs);

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	for _, want := range []string{
		"(core instance",
		"\"stdout\"",
		"\"exit\"",
	} {
		if !strings.Contains(watStr, want) {
			t.Errorf("expected %q in printed component, got:\n%s", want, watStr)
		}
	}
}

// TestWASMComponentInstantiateWithTwoInstanceArgs covers
// put_core_instance_section_instantiate_with_instance_args in its
// multi-arg shape. Builds a core module that imports two functions
// from two different instance names ("host_a" and "host_b"),
// packages a single host instance per name, then instantiates the
// core module passing both instances as args. Confirms the
// multi-arg form encodes correctly.
func TestWASMComponentInstantiateWithTwoInstanceArgs(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/component";
import "std/wasm/inst";
import "std/wasm/encode";
import "std/wasm/sections";
import "std/wasm/imports";
function main(): i32 {
    // Core module: imports host_a.f and host_b.f, both () -> i32.
    var m: module.Module = module.module_new();
    var p0: u8[] = [];
    var r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_imports(["host_a", "host_b"], ["f", "f"], [imports.import_func(), imports.import_func()], [imports.import_desc_func(0u32), imports.import_desc_func(0u32)]);
    // No funcs of our own.
    m = m.with_functions([]);
    m = m.with_code([]);
    var core_bytes: u8[] = module.build(m);

    // Component: 1 type, 2 imports, 2 lowers, 2 single-export
    // instances, then instantiate the core with both.
    var no_names: string[] = [];
    var no_valtypes: u8[] = [];
    var comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_one_func(comp, no_names, no_valtypes, component.cvaltype_u32());

    var imp_names: string[] = ["a", "b"];
    var imp_types: u32[] = [0u32, 0u32];
    comp = component.put_import_section_funcs(comp, imp_names, imp_types);

    var lower_funcs: u32[] = [0u32, 1u32];
    comp = component.put_canon_section_lowers_no_opts(comp, lower_funcs);

    // Two instances, each wrapping one lowered core func under name "f".
    comp = component.put_core_instance_section_from_one_func_export(comp, "f", 0u32);
    comp = component.put_core_instance_section_from_one_func_export(comp, "f", 1u32);

    // Core module section + instantiate-with-two-args section.
    comp = component.put_core_module_section(comp, core_bytes);
    var arg_names: string[] = ["host_a", "host_b"];
    var arg_instance_idxs: u32[] = [0u32, 1u32];
    comp = component.put_core_instance_section_instantiate_with_instance_args(comp, 0u32, arg_names, arg_instance_idxs);

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	for _, want := range []string{
		"with \"host_a\"",
		"with \"host_b\"",
	} {
		if !strings.Contains(watStr, want) {
			t.Errorf("expected %q in printed component, got:\n%s", want, watStr)
		}
	}
}

// TestWASMComponentImportLoweredIntoCoreModule covers the
// other half of the canonical-ABI boundary: a host-imported function
// is lowered into a core function and threaded down as a core
// module's import. Exercises:
//   - put_canon_section_lower_no_opts
//   - put_core_instance_section_from_one_func_export
//   - put_core_instance_section_instantiate_with_one_instance_arg
//
// The component's recipe:
//
//	type 0    : (func (result u32))
//	import 0  : "h" : (func 0)
//	canon lower (func 0) -> core func 0
//	core instance 0 = (export "f" (core func 0))   ; package the lower'd fn
//	core module 0   : imports (host.f : () -> i32), exports "main"
//	                  that just calls the import
//	core instance 1 = (instantiate $m (with "host" (instance 0)))
//
// wasmtime won't *run* this (the host import isn't satisfied), but
// `wasm-tools validate` accepts it and `wasm-tools print` shows
// every wiring step, proving the two new core-instance composers
// produce spec-conformant bytes.
func TestWASMComponentImportLoweredIntoCoreModule(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/component";
import "std/wasm/inst";
import "std/wasm/encode";
import "std/wasm/sections";
import "std/wasm/imports";
function main(): i32 {
    // Core module: imports host.f : () -> i32, exports main that
    // returns whatever the import returns.
    var m: module.Module = module.module_new();
    var p0: u8[] = [];
    var r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    // Type 0 = () -> i32. Import "host"."f" of type 0.
    m = m.with_imports(["host"], ["f"], [imports.import_func()], [imports.import_desc_func(0u32)]);
    // Function 0 is the imported func (after imports). Function 1
    // is our own; type 0 too. Function 1 = "main", body: call 0.
    m = m.with_functions([0u32]);
    m = m.with_exports(["main"], [sections.export_func()], [1u32]);  // imports shift the export idx by 1
    var bodyExpr: u8[] = inst.inst_call([], 0u32);
    var fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), bodyExpr);
    m = m.with_code([fn]);
    var core_bytes: u8[] = module.build(m);

    // Component envelope.
    var names: string[] = [];
    var valtypes: u8[] = [];
    var comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_one_func(comp, names, valtypes, component.cvaltype_u32());
    comp = component.put_import_section_one_func(comp, "h", 0u32);
    comp = component.put_canon_section_lower_no_opts(comp, 0u32);
    comp = component.put_core_instance_section_from_one_func_export(comp, "f", 0u32);
    comp = component.put_core_module_section(comp, core_bytes);
    comp = component.put_core_instance_section_instantiate_with_one_instance_arg(comp, 0u32, "host", 0u32);

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	for _, want := range []string{
		"(import",
		"\"h\"",
		"canon lower",
		"(core instance",
		"(core module",
		"with \"host\"",
	} {
		if !strings.Contains(watStr, want) {
			t.Errorf("expected %q in printed component, got:\n%s", want, watStr)
		}
	}
}

// TestWASMComponentExportSectionTwoFuncs covers put_export_section_funcs
// in its multi-entry shape. Builds a component that imports one
// function and re-exports it under two different names — the
// simplest shape that exercises multi-export without needing two
// canon-lifts.
func TestWASMComponentExportSectionTwoFuncs(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    var no_names: string[] = [];
    var no_valtypes: u8[] = [];

    var comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_one_func(comp, no_names, no_valtypes, component.cvaltype_u32());
    comp = component.put_import_section_one_func(comp, "h", 0u32);

    // Re-export the imported func twice under different names.
    var exp_names: string[] = ["g0", "g1"];
    var exp_funcs: u32[] = [0u32, 0u32];
    comp = component.put_export_section_funcs(comp, exp_names, exp_funcs);

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	for _, want := range []string{
		"\"g0\"",
		"\"g1\"",
	} {
		if !strings.Contains(watStr, want) {
			t.Errorf("expected %q in printed component, got:\n%s", want, watStr)
		}
	}
	// Both export declarations should appear.
	if strings.Count(watStr, "(export ") < 2 {
		t.Errorf("expected at least 2 (export) declarations, got:\n%s", watStr)
	}
}

// TestWASMComponentCanonLiftMemRealloc covers
// put_canon_section_lift_mem_realloc. Lifts a core function under
// the canonical-ABI `(memory, realloc)` options — the shape every
// component-level function that returns a string or list goes
// through. The core module exports a memory + a realloc func + the
// lift target, the component aliases all three out of the core
// instance, lifts the target under a string-returning type, and
// exports the lifted func.
//
// wasm-tools accepts the shape even when the lifted signature
// doesn't strictly match the canonical-ABI lowering of the core
// signature; this test pins the BYTE shape of the canon-with-opts
// section, not the runtime-semantic compatibility (which is the
// component runtime's job to enforce).
func TestWASMComponentCanonLiftMemRealloc(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/component";
import "std/wasm/inst";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    // Core module:
    //   type 0: () -> i32 (the lift target's core signature)
    //   type 1: (i32 i32 i32 i32) -> i32 (realloc signature)
    //   func 0: signature 0, body i32.const 7   (the lift target)
    //   func 1: signature 1, body i32.const 0   (the realloc stub)
    //   memory 0: min=1
    //   exports: "f" -> func 0, "alloc" -> func 1, "mem" -> mem 0
    var m: module.Module = module.module_new();
    var p0: u8[] = [];
    var r0: u8[] = [encode.valtype_i32()];
    var p1: u8[] = [encode.valtype_i32(), encode.valtype_i32(), encode.valtype_i32(), encode.valtype_i32()];
    var r1: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0, p1], [r0, r1]);
    m = m.with_functions([0u32, 1u32]);
    m = m.with_memory(1u32, 0 - 1);
    m = m.with_exports(["f", "alloc", "mem"], [sections.export_func(), sections.export_func(), sections.export_memory()], [0u32, 1u32, 0u32]);
    var body_f: u8[] = inst.put_function_body([], inst.put_locals_empty([]), inst.inst_i32_const([], 7));
    var body_alloc: u8[] = inst.put_function_body([], inst.put_locals_empty([]), inst.inst_i32_const([], 0));
    m = m.with_code([body_f, body_alloc]);
    var core_bytes: u8[] = module.build(m);

    // Component: header + core-module + core-instance + 2 alias
    // calls (one for the lift target func, one for the realloc
    // func). Memory aliasing would need a multi-sort alias composer
    // that std/wasm/component does not yet ship; this slice tests
    // the canon-lift binary shape only, so we feed canon-lift a
    // memory idx that doesn t resolve to an alias — wasm-tools
    // accepts the byte shape even when the memory idx is out of
    // range, since validation of the component world is a separate
    // pass.
    var comp: u8[] = component.put_component_header([]);
    comp = component.put_core_module_section(comp, core_bytes);
    comp = component.put_core_instance_section_instantiate(comp, 0u32);
    comp = component.put_alias_section_core_export_func(comp, 0u32, "f");
    comp = component.put_alias_section_core_export_func(comp, 0u32, "alloc");

    // Type: () -> u32 (would-be string in a real lift, but u32 keeps
    // the wasm-tools structural check happy without needing
    // canonical-ABI lowering compatibility).
    var no_names: string[] = [];
    var no_valtypes: u8[] = [];
    comp = component.put_type_section_one_func(comp, no_names, no_valtypes, component.cvaltype_u32());

    // canon lift with mem + realloc opts. core-func 0 is the
    // aliased "f"; we use mem idx 0 and core-func idx 1 (the
    // aliased "alloc"). wasm-tools accepts this even without an
    // explicit core-memory alias because the canon-lift binary
    // form just carries the indices — wasm-tools checks they're
    // within bounds, not that they came from an alias section.
    comp = component.put_canon_section_lift_mem_realloc(comp, 0u32, 0u32, 0u32, 1u32);

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools print failed: %v\nbytes:\n%s\nout:\n%s", err, hexBytes, wat)
	}
	watStr := string(wat)
	for _, want := range []string{
		"canon lift",
		"memory",
		"realloc",
	} {
		if !strings.Contains(watStr, want) {
			t.Errorf("expected %q in printed component, got:\n%s", want, watStr)
		}
	}
}

// TestWASMComponentCanonLowerMemRealloc covers
// put_canon_section_lower_mem_realloc. Mirrors
// TestWASMComponentCanonLiftMemRealloc but in the opposite
// direction: a host-imported function takes a string parameter
// and is lowered with `(memory, realloc)` opts so the canonical-
// ABI can materialise the string bytes in core memory. The
// canonical "lower a host-imported function taking a string"
// shape that preview-2 WASI imports (`print`, `write-file`, …)
// reach for.
func TestWASMComponentCanonLowerMemRealloc(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/component";
import "std/wasm/inst";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    // Core module exporting a memory + a realloc stub. We alias
    // both out of its instance, then lower a host-imported func
    // that takes a string under those opts.
    var m: module.Module = module.module_new();
    // type 0: (i32 i32 i32 i32) -> i32 (realloc signature)
    var p0: u8[] = [encode.valtype_i32(), encode.valtype_i32(), encode.valtype_i32(), encode.valtype_i32()];
    var r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_memory(1u32, 0 - 1);
    m = m.with_exports(["mem", "alloc"], [sections.export_memory(), sections.export_func()], [0u32, 0u32]);
    var body_alloc: u8[] = inst.put_function_body([], inst.put_locals_empty([]), inst.inst_i32_const([], 0));
    m = m.with_code([body_alloc]);
    var core_bytes: u8[] = module.build(m);

    var comp: u8[] = component.put_component_header([]);
    comp = component.put_core_module_section(comp, core_bytes);
    comp = component.put_core_instance_section_instantiate(comp, 0u32);

    // Alias both memory + alloc out of the instance.
    var sorts: u8[] = [component.core_sort_memory(), component.core_sort_func()];
    var instance_idxs: u32[] = [0u32, 0u32];
    var alias_names: string[] = ["mem", "alloc"];
    comp = component.put_alias_section_core_exports(comp, sorts, instance_idxs, alias_names);

    // Type: (s: string) -> u32. Then import "h" of that type.
    var p_names: string[] = ["s"];
    var p_valtypes: u8[] = [component.cvaltype_string()];
    comp = component.put_type_section_one_func(comp, p_names, p_valtypes, component.cvaltype_u32());
    comp = component.put_import_section_one_func(comp, "h", 0u32);

    // Lower the import under mem + realloc opts. mem idx 0
    // (the only aliased memory) and core-func idx 0 (the aliased
    // realloc).
    comp = component.put_canon_section_lower_mem_realloc(comp, 0u32, 0u32, 0u32);

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	for _, want := range []string{
		"canon lower",
		"memory",
		"realloc",
		"\"h\"",
	} {
		if !strings.Contains(watStr, want) {
			t.Errorf("expected %q in printed component, got:\n%s", want, watStr)
		}
	}
}

// TestWASMComponentCanonLiftPostReturn covers
// put_canon_section_lift_mem_realloc_post_return: extends
// canon-lift-with-(memory, realloc) by adding the post-return
// option. Needed when the lifted function returns a string or
// list backed by core memory the canonical-ABI shouldn't keep
// around past the call — the host runtime invokes post-return on
// the canonical-ABI handle after copying out, freeing the
// core-side storage.
func TestWASMComponentCanonLiftPostReturn(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/component";
import "std/wasm/inst";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    // Core module exports: f (() -> i32), alloc (4xi32 -> i32),
    // cleanup (3xi32 -> ()), mem.
    var m: module.Module = module.module_new();
    var p0: u8[] = [];
    var r0: u8[] = [encode.valtype_i32()];
    var p1: u8[] = [encode.valtype_i32(), encode.valtype_i32(), encode.valtype_i32(), encode.valtype_i32()];
    var r1: u8[] = [encode.valtype_i32()];
    var p2: u8[] = [encode.valtype_i32(), encode.valtype_i32(), encode.valtype_i32()];
    var r2: u8[] = [];
    m = m.with_types([p0, p1, p2], [r0, r1, r2]);
    m = m.with_functions([0u32, 1u32, 2u32]);
    m = m.with_memory(1u32, 0 - 1);
    m = m.with_exports(["f", "alloc", "cleanup", "mem"], [sections.export_func(), sections.export_func(), sections.export_func(), sections.export_memory()], [0u32, 1u32, 2u32, 0u32]);
    var body_f: u8[] = inst.put_function_body([], inst.put_locals_empty([]), inst.inst_i32_const([], 0));
    var body_alloc: u8[] = inst.put_function_body([], inst.put_locals_empty([]), inst.inst_i32_const([], 0));
    var body_cleanup: u8[] = inst.put_function_body([], inst.put_locals_empty([]), []);
    m = m.with_code([body_f, body_alloc, body_cleanup]);
    var core_bytes: u8[] = module.build(m);

    var comp: u8[] = component.put_component_header([]);
    comp = component.put_core_module_section(comp, core_bytes);
    comp = component.put_core_instance_section_instantiate(comp, 0u32);

    // Alias all four into the top-level core space.
    var sorts: u8[] = [component.core_sort_func(), component.core_sort_memory(), component.core_sort_func(), component.core_sort_func()];
    var instance_idxs: u32[] = [0u32, 0u32, 0u32, 0u32];
    var alias_names: string[] = ["f", "mem", "alloc", "cleanup"];
    comp = component.put_alias_section_core_exports(comp, sorts, instance_idxs, alias_names);

    var no_names: string[] = [];
    var no_valtypes: u8[] = [];
    comp = component.put_type_section_one_func(comp, no_names, no_valtypes, component.cvaltype_u32());

    // canon-lift with mem (idx 0) + realloc (core-func 1) +
    // post-return (core-func 2). Lift target is core-func 0.
    comp = component.put_canon_section_lift_mem_realloc_post_return(comp, 0u32, 0u32, 0u32, 1u32, 2u32);

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools print failed: %v\nbytes:\n%s\nout:\n%s", err, hexBytes, wat)
	}
	watStr := string(wat)
	for _, want := range []string{
		"canon lift",
		"memory",
		"realloc",
		"post-return",
	} {
		if !strings.Contains(watStr, want) {
			t.Errorf("expected %q in printed component, got:\n%s", want, watStr)
		}
	}
}

// TestWASMComponentLiftedExport is the first end-to-end shape that
// exercises every section composer std/wasm/component currently
// ships: preamble + core-module + core-instance + alias + type +
// canon + export. A core module exports `f : () -> i32`; the
// component aliases it as a core-func, declares a component-level
// type `() -> u32`, lifts the core-func into a component-level
// function, and exports that lifted function as `g`.
//
// This is the structural shape that `wasm-tools component new`
// produces for a self-contained core module — the production target
// of Phase 2. wasmtime won't *run* this as a CLI world (the
// component doesn't satisfy a known WASI world signature), but
// `wasm-tools validate` accepts it and `wasm-tools print` shows the
// `(export "g" (func ...))` + `(canon lift ...)` recipe that proves
// the seven sections compose correctly.
func TestWASMComponentLiftedExport(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/component";
import "std/wasm/inst";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    // Core module: function "f" returns 11.
    var m: module.Module = module.module_new();
    var p0: u8[] = [];
    var r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["f"], [sections.export_func()], [0u32]);
    var bodyExpr: u8[] = inst.inst_i32_const([], 11);
    var fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), bodyExpr);
    m = m.with_code([fn]);
    var core_bytes: u8[] = module.build(m);

    // Component: preamble + the seven-section recipe.
    var comp: u8[] = component.put_component_header([]);
    comp = component.put_core_module_section(comp, core_bytes);
    comp = component.put_core_instance_section_instantiate(comp, 0u32);
    comp = component.put_alias_section_core_export_func(comp, 0u32, "f");
    comp = component.put_type_section_one_func_no_param(comp, component.cvaltype_u32());
    comp = component.put_canon_section_lift_no_opts(comp, 0u32, 0u32);
    comp = component.put_export_section_one_func(comp, "g", 0u32);

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	// Structural assertions — every section we wired has to show
	// up at component level in the printed output.
	for _, want := range []string{
		"(core module",
		"(core instance",
		"(alias core export",
		"(type",
		"canon lift",
		"(export",
		"\"g\"",
	} {
		if !strings.Contains(watStr, want) {
			t.Errorf("expected %q in printed component, got:\n%s", want, watStr)
		}
	}
}

// TestWASMComponentCanonLiftUtf16 covers
// put_canon_section_lift_mem_realloc_encoding with the
// string-encoding-utf16 canonopt. Verifies the explicit-encoding
// variant emits a canon section where `wasm-tools print` surfaces
// `string-encoding=utf16` alongside memory + realloc.
func TestWASMComponentCanonLiftUtf16(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/component";
import "std/wasm/inst";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    // Core module exports: f (() -> i32), alloc (4xi32 -> i32), mem.
    var m: module.Module = module.module_new();
    var p0: u8[] = [];
    var r0: u8[] = [encode.valtype_i32()];
    var p1: u8[] = [encode.valtype_i32(), encode.valtype_i32(), encode.valtype_i32(), encode.valtype_i32()];
    var r1: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0, p1], [r0, r1]);
    m = m.with_functions([0u32, 1u32]);
    m = m.with_memory(1u32, 0 - 1);
    m = m.with_exports(["f", "alloc", "mem"], [sections.export_func(), sections.export_func(), sections.export_memory()], [0u32, 1u32, 0u32]);
    var body_f: u8[] = inst.put_function_body([], inst.put_locals_empty([]), inst.inst_i32_const([], 0));
    var body_alloc: u8[] = inst.put_function_body([], inst.put_locals_empty([]), inst.inst_i32_const([], 0));
    m = m.with_code([body_f, body_alloc]);
    var core_bytes: u8[] = module.build(m);

    var comp: u8[] = component.put_component_header([]);
    comp = component.put_core_module_section(comp, core_bytes);
    comp = component.put_core_instance_section_instantiate(comp, 0u32);

    var sorts: u8[] = [component.core_sort_func(), component.core_sort_memory(), component.core_sort_func()];
    var instance_idxs: u32[] = [0u32, 0u32, 0u32];
    var alias_names: string[] = ["f", "mem", "alloc"];
    comp = component.put_alias_section_core_exports(comp, sorts, instance_idxs, alias_names);

    var no_names: string[] = [];
    var no_valtypes: u8[] = [];
    comp = component.put_type_section_one_func(comp, no_names, no_valtypes, component.cvaltype_string());

    // canon-lift with utf16 string encoding + mem + realloc.
    comp = component.put_canon_section_lift_mem_realloc_encoding(comp, 0u32, 0u32, 0u32, 1u32, component.canonopt_string_encoding_utf16());

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools print failed: %v\nbytes:\n%s\nout:\n%s", err, hexBytes, wat)
	}
	watStr := string(wat)
	for _, want := range []string{
		"canon lift",
		"string-encoding=utf16",
		"memory",
		"realloc",
	} {
		if !strings.Contains(watStr, want) {
			t.Errorf("expected %q in printed component, got:\n%s", want, watStr)
		}
	}
}

// TestWASMComponentTypeFuncNoResult covers
// put_type_section_one_func_no_result: declares
// `(func (param "status" u32))` — no result — and verifies
// wasm-tools surfaces the no-result shape (which appears as
// `(param "status" u32)` with no `(result ...)` form in the
// printed WAT).
//
// Without this composer, the existing single-anon-result functype
// API can't express WASI functions like `wasi:cli/exit::exit` or
// `wasi:io/streams::blocking-flush` that return nothing.
func TestWASMComponentTypeFuncNoResult(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    var names: string[] = ["status"];
    var valtypes: u8[] = [component.cvaltype_u32()];
    var comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_one_func_no_result(comp, names, valtypes);

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	if !strings.Contains(watStr, "\"status\"") {
		t.Errorf("expected \"status\" param in printed component, got:\n%s", watStr)
	}
	if !strings.Contains(watStr, "u32") {
		t.Errorf("expected u32 param valtype, got:\n%s", watStr)
	}
	// No `(result ...)` form should appear — the function returns
	// nothing. The printed WAT has the param but no result clause.
	if strings.Contains(watStr, "(result ") {
		t.Errorf("unexpected (result ...) for a no-result functype:\n%s", watStr)
	}
}

// TestWASMComponentStartSection covers
// put_start_section_no_args_no_results. Declares a no-result
// functype, imports a function of that type, then a start section
// calling the imported function with no value args. Verifies
// wasm-tools picks up the `(start ...)` form.
//
// Component-level start sections are how a component runs build-
// time initialisation (computing constants at instantiation time,
// running setup hooks). This composer covers the simplest case
// (no value args, no result bindings).
func TestWASMComponentStartSection(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    var no_params: string[] = [];
    var no_valtypes: u8[] = [];

    var comp: u8[] = component.put_component_header([]);
    // Type 0: (func)
    comp = component.put_type_section_one_func_no_result(comp, no_params, no_valtypes);
    // Import "f" of type 0.
    comp = component.put_import_section_one_func(comp, "f", 0u32);
    // Start: call func 0 with no args, no result bindings.
    comp = component.put_start_section_no_args_no_results(comp, 0u32);

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	// The component-level start section binds component-level
	// `value`s, which wasm-tools considers an experimental feature.
	// Default `validate` rejects without `--features all`; this
	// just means the start composer's bytes are structurally
	// correct but only usable under hosts that opt in.
	if vout, err := exec.Command("wasm-tools", "validate", "--features", "all", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate (--features all) failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	if !strings.Contains(watStr, "(start") {
		t.Errorf("expected (start ...) in printed component, got:\n%s", watStr)
	}
}

// TestWASMComponentResourceNewsRepsTwo covers
// put_canon_section_resource_news + put_canon_section_resource_reps
// in their multi-entry shapes: two resource types declared, then
// both `new` and `rep` emitted as 2-entry canon sections each.
// Verifies the full multi-entry resource canon family parallels
// the existing drops composer.
func TestWASMComponentResourceNewsRepsTwo(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
import "std/wasm/encode";
function main(): i32 {
    var comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_one_resource(comp, encode.valtype_i32());
    comp = component.put_type_section_one_resource(comp, encode.valtype_i32());
    var idxs: u32[] = [0u32, 1u32];
    comp = component.put_canon_section_resource_news(comp, idxs);
    comp = component.put_canon_section_resource_reps(comp, idxs);

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	if strings.Count(watStr, "resource.new") < 2 {
		t.Errorf("expected at least 2 resource.new entries, got:\n%s", watStr)
	}
	if strings.Count(watStr, "resource.rep") < 2 {
		t.Errorf("expected at least 2 resource.rep entries, got:\n%s", watStr)
	}
}

// TestWASMComponentResourceDropsTwo covers
// put_canon_section_resource_drops in its multi-entry shape: two
// resource types declared, then both dropped in one canon section
// (the shape real WASI worlds use — HTTP alone declares ~10
// resources, each with its own drop).
func TestWASMComponentResourceDropsTwo(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
import "std/wasm/encode";
function main(): i32 {
    var comp: u8[] = component.put_component_header([]);
    // Two resource types, both rep=i32, no dtor.
    comp = component.put_type_section_one_resource(comp, encode.valtype_i32());
    comp = component.put_type_section_one_resource(comp, encode.valtype_i32());
    // Drop both in one canon section.
    var idxs: u32[] = [0u32, 1u32];
    comp = component.put_canon_section_resource_drops(comp, idxs);

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	if strings.Count(watStr, "resource.drop") < 2 {
		t.Errorf("expected at least 2 resource.drop in printed component, got:\n%s", watStr)
	}
}

// TestWASMComponentResourceNewRep covers
// put_canon_section_resource_new + put_canon_section_resource_rep.
// Declares a resource type then emits both resource.new and
// resource.rep canon entries for it. Verifies wasm-tools surfaces
// both keywords in the printed WAT.
func TestWASMComponentResourceNewRep(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
import "std/wasm/encode";
function main(): i32 {
    var comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_one_resource(comp, encode.valtype_i32());
    comp = component.put_canon_section_resource_new(comp, 0u32);
    comp = component.put_canon_section_resource_rep(comp, 0u32);

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	for _, want := range []string{"resource.new", "resource.rep"} {
		if !strings.Contains(watStr, want) {
			t.Errorf("expected %q in printed component, got:\n%s", want, watStr)
		}
	}
}

// TestWASMComponentOwnBorrowHandles covers
// put_type_section_one_own + put_type_section_one_borrow. Declares
// a resource type then `own<$r>` and `borrow<$r>` handles referring
// to it. Verifies wasm-tools picks up both handle forms.
//
// Resource handles are how WASI worlds reference live host objects:
// `own<incoming-request>` for transferred handles,
// `borrow<incoming-request>` for non-owning references.
func TestWASMComponentOwnBorrowHandles(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
import "std/wasm/encode";
function main(): i32 {
    var comp: u8[] = component.put_component_header([]);
    // Type 0: resource with i32 rep, no dtor.
    comp = component.put_type_section_one_resource(comp, encode.valtype_i32());
    // Type 1: own<$0>.
    comp = component.put_type_section_one_own(comp, 0u32);
    // Type 2: borrow<$0>.
    comp = component.put_type_section_one_borrow(comp, 0u32);

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	for _, want := range []string{"resource", "own", "borrow"} {
		if !strings.Contains(watStr, want) {
			t.Errorf("expected %q in printed component, got:\n%s", want, watStr)
		}
	}
}

// TestWASMComponentVariantMixed covers put_type_section_one_variant
// with a mix of payload-carrying and tag-only cases. Declares
// `variant { ok(u32), err(string), abort }` — the typical
// success / error-with-message / no-payload-cancellation shape.
func TestWASMComponentVariantMixed(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    var names: string[] = ["ok", "err", "abort"];
    var has_payload: boolean[] = [true, true, false];
    var payload_vts: u8[] = [component.cvaltype_u32(), component.cvaltype_string(), 0u8];
    var comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_one_variant(comp, names, has_payload, payload_vts);

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	if !strings.Contains(watStr, "variant") {
		t.Errorf("expected variant in printed component, got:\n%s", watStr)
	}
	for _, want := range []string{"\"ok\"", "\"err\"", "\"abort\"", "u32", "string"} {
		if !strings.Contains(watStr, want) {
			t.Errorf("expected %q in printed component, got:\n%s", want, watStr)
		}
	}
}

// TestWASMComponentRecordNameAge covers put_type_section_one_record:
// declares `record { name: string, age: u32 }` and verifies
// `wasm-tools print` surfaces both fields with their names + types.
// Records are the structural product type used pervasively by WASI
// (filesystem-stat, http field-entry, etc.).
func TestWASMComponentRecordNameAge(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    var names: string[] = ["name", "age"];
    var valtypes: u8[] = [component.cvaltype_string(), component.cvaltype_u32()];
    var comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_one_record(comp, names, valtypes);

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	if !strings.Contains(watStr, "record") {
		t.Errorf("expected record in printed component, got:\n%s", watStr)
	}
	for _, want := range []string{"\"name\"", "\"age\"", "string", "u32"} {
		if !strings.Contains(watStr, want) {
			t.Errorf("expected %q in printed component, got:\n%s", want, watStr)
		}
	}
}

// TestWASMComponentFlagsRWX covers put_type_section_one_flags:
// declares `flags { read, write, execute }` and verifies wasm-tools
// surfaces the bitset shape. Same labels-vec encoding as enum;
// only the form byte differs (0x6e vs 0x6d).
func TestWASMComponentFlagsRWX(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    var labels: string[] = ["read", "write", "execute"];
    var comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_one_flags(comp, labels);

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	if !strings.Contains(watStr, "flags") {
		t.Errorf("expected flags in printed component, got:\n%s", watStr)
	}
	for _, want := range []string{"\"read\"", "\"write\"", "\"execute\""} {
		if !strings.Contains(watStr, want) {
			t.Errorf("expected %q label in printed component, got:\n%s", want, watStr)
		}
	}
}

// TestWASMComponentEnumColor covers put_type_section_one_enum:
// declares `enum { red, green, blue }` and verifies wasm-tools picks
// up all three labels. Enums are tag-only variants — used by WASI
// for status codes and small finite-set selectors.
func TestWASMComponentEnumColor(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    var labels: string[] = ["red", "green", "blue"];
    var comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_one_enum(comp, labels);

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	if !strings.Contains(watStr, "enum") {
		t.Errorf("expected enum in printed component, got:\n%s", watStr)
	}
	for _, want := range []string{"\"red\"", "\"green\"", "\"blue\""} {
		if !strings.Contains(watStr, want) {
			t.Errorf("expected %q label in printed component, got:\n%s", want, watStr)
		}
	}
}

// TestWASMComponentResultOkErr covers put_type_section_one_result_ok_err:
// declares `result<u32, string>` and verifies wasm-tools picks up
// both arms. The both-present form is what WASI worlds reach for
// the most — HTTP request handlers, file ops, etc. all return
// `result<T, error-code>`.
func TestWASMComponentResultOkErr(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    var comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_one_result_ok_err(comp, component.cvaltype_u32(), component.cvaltype_string());

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	if !strings.Contains(watStr, "result") {
		t.Errorf("expected result in printed component, got:\n%s", watStr)
	}
	if !strings.Contains(watStr, "u32") {
		t.Errorf("expected u32 (ok arm), got:\n%s", watStr)
	}
	if !strings.Contains(watStr, "string") {
		t.Errorf("expected string (err arm), got:\n%s", watStr)
	}
}

// TestWASMComponentTupleStringU32 covers put_type_section_one_tuple:
// declares `tuple<string, u32>` and verifies `wasm-tools print`
// surfaces it. Tuples are the canonical pair shape in WASI —
// `wasi:cli/environment::get-environment()` returns
// `list<tuple<string, string>>` for the (name, value) entries.
func TestWASMComponentTupleStringU32(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    var elems: u8[] = [component.cvaltype_string(), component.cvaltype_u32()];
    var comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_one_tuple(comp, elems);

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	if !strings.Contains(watStr, "tuple") {
		t.Errorf("expected tuple in printed component, got:\n%s", watStr)
	}
	if !strings.Contains(watStr, "string") {
		t.Errorf("expected string element, got:\n%s", watStr)
	}
	if !strings.Contains(watStr, "u32") {
		t.Errorf("expected u32 element, got:\n%s", watStr)
	}
}

// TestWASMComponentOptionOfU32 covers put_type_section_one_option:
// declares `option<u32>` and verifies `wasm-tools print` surfaces
// `(option u32)`. Options appear pervasively in WASI worlds —
// `wasi:http/types` and `wasi:cli/environment` use them for
// "may or may not be present" fields.
func TestWASMComponentOptionOfU32(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    var comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_one_option(comp, component.cvaltype_u32());

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	if !strings.Contains(watStr, "(option u32)") {
		t.Errorf("expected (option u32) in printed component, got:\n%s", watStr)
	}
}

// TestWASMComponentListOfU8 covers put_type_section_one_list:
// declares `list<u8>` as the sole component-level type and verifies
// `wasm-tools print` surfaces `(list u8)`. Lists are heavily used
// in WASI for byte buffers, env vars, HTTP headers, etc.
func TestWASMComponentListOfU8(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    var comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_one_list(comp, component.cvaltype_u8());

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	if !strings.Contains(watStr, "(list u8)") {
		t.Errorf("expected (list u8) in printed component, got:\n%s", watStr)
	}
}

// TestWASMComponentResourceDrop covers put_type_section_one_resource
// + put_canon_section_resource_drop. Builds a component that
// declares a resource type (rep = i32, no dtor) and emits a canon
// resource.drop for it. Verifies both the resource type
// declaration and the resource.drop canon entry surface in
// `wasm-tools print`.
//
// Resources are the component-model handle type the HTTP world
// uses extensively (incoming-request, outgoing-response, fields,
// streams, …); this slice unblocks any later HTTP-world wrap
// that needs to declare + drop those handles.
func TestWASMComponentResourceDrop(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
import "std/wasm/encode";
function main(): i32 {
    var comp: u8[] = component.put_component_header([]);
    // Resource type 0: representation is i32, no dtor.
    comp = component.put_type_section_one_resource(comp, encode.valtype_i32());
    // canon resource.drop of type 0.
    comp = component.put_canon_section_resource_drop(comp, 0u32);

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wat, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	for _, want := range []string{
		"resource",
		"resource.drop",
	} {
		if !strings.Contains(watStr, want) {
			t.Errorf("expected %q in printed component, got:\n%s", want, watStr)
		}
	}
}

// TestWASMComponentBuildHelperWithParamsRunnable exercises the
// generalised `build_lifted_export_component_with_params` helper
// end-to-end via wasmtime. Builds a core module that exports
// `add(a: i32, b: i32) -> i32`, then wraps it as a component-level
// `add(a: u32, b: u32) -> u32` via the canonical-ABI lift.
// `wasmtime run --invoke 'add(3, 4)'` should print `7`.
func TestWASMComponentBuildHelperWithParamsRunnable(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/component";
import "std/wasm/inst";
import "std/wasm/encode";
import "std/wasm/sections";
import "std/wasm/numeric";
function main(): i32 {
    // Core module: function "add" takes two i32s and returns their sum.
    var m: module.Module = module.module_new();
    var p0: u8[] = [encode.valtype_i32(), encode.valtype_i32()];
    var r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["add"], [sections.export_func()], [0u32]);
    var body: u8[] = inst.inst_local_get([], 0u32);
    body = inst.inst_local_get(body, 1u32);
    body = numeric.inst_i32_add(body);
    var fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), body);
    m = m.with_code([fn]);
    var core_bytes: u8[] = module.build(m);

    // Wrap as a component-level (u32, u32) -> u32.
    var names: string[] = ["a", "b"];
    var valtypes: u8[] = [component.cvaltype_u32(), component.cvaltype_u32()];
    var comp: u8[] = component.build_lifted_export_component_with_params(core_bytes, "add", "add", names, valtypes, component.cvaltype_u32());

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wout, werr := exec.Command("wasmtime", "run", "--invoke", "add(3, 4)", compPath).CombinedOutput()
	if werr != nil {
		t.Fatalf("wasmtime run failed: %v\noutput:\n%s", werr, wout)
	}
	got := strings.TrimSpace(string(wout))
	if got != "7" {
		t.Errorf("wasmtime stdout = %q, want %q", got, "7")
	}
}

// TestWASMComponentBuildHelperRunnable exercises the high-level
// `build_lifted_export_component` helper end-to-end: given core
// bytes that export a no-param func, the helper produces a complete
// component-model envelope in one call. Same wasmtime-invoke check
// as TestWASMComponentRunnableViaWasmtime but with the six-section
// recipe collapsed into the single helper.
func TestWASMComponentBuildHelperRunnable(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/component";
import "std/wasm/inst";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    // Core module: function "f" returns 99.
    var m: module.Module = module.module_new();
    var p0: u8[] = [];
    var r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["f"], [sections.export_func()], [0u32]);
    var bodyExpr: u8[] = inst.inst_i32_const([], 99);
    var fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), bodyExpr);
    m = m.with_code([fn]);
    var core_bytes: u8[] = module.build(m);

    // The whole component recipe in one call.
    var comp: u8[] = component.build_lifted_export_component(core_bytes, "f", "g", component.cvaltype_u32());

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wout, werr := exec.Command("wasmtime", "run", "--invoke", "g()", compPath).CombinedOutput()
	if werr != nil {
		t.Fatalf("wasmtime run failed: %v\noutput:\n%s", werr, wout)
	}
	got := strings.TrimSpace(string(wout))
	if got != "99" {
		t.Errorf("wasmtime stdout = %q, want %q", got, "99")
	}
}

// TestWASMComponentRunnableViaWasmtime is the first end-to-end
// proof that std/wasm/component can produce a wasm component that
// `wasmtime` not only validates but actually RUNS. Builds the same
// "core module exports a function, component lifts it" shape as
// TestWASMComponentLiftedExport, writes the bytes to a file, and
// invokes the lifted export via `wasmtime run --invoke g`.
// wasmtime prints the i32/u32 return value on stdout, which we
// match against the constant the core function returns.
func TestWASMComponentRunnableViaWasmtime(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/component";
import "std/wasm/inst";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    // Core module: function "f" returns 42.
    var m: module.Module = module.module_new();
    var p0: u8[] = [];
    var r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["f"], [sections.export_func()], [0u32]);
    var bodyExpr: u8[] = inst.inst_i32_const([], 42);
    var fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), bodyExpr);
    m = m.with_code([fn]);
    var core_bytes: u8[] = module.build(m);

    // Component envelope: full lift-and-export recipe.
    var no_names: string[] = [];
    var no_valtypes: u8[] = [];
    var comp: u8[] = component.put_component_header([]);
    comp = component.put_core_module_section(comp, core_bytes);
    comp = component.put_core_instance_section_instantiate(comp, 0u32);
    comp = component.put_alias_section_core_export_func(comp, 0u32, "f");
    comp = component.put_type_section_one_func(comp, no_names, no_valtypes, component.cvaltype_u32());
    comp = component.put_canon_section_lift_no_opts(comp, 0u32, 0u32);
    comp = component.put_export_section_one_func(comp, "g", 0u32);

    var output: string = "";
    var i: i32 = 0;
    while (i < comp.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (comp[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}

	dir := t.TempDir()
	compPath := filepath.Join(dir, "lang_built.wasm")
	if err := os.WriteFile(compPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wout, werr := exec.Command("wasmtime", "run", "--invoke", "g()", compPath).CombinedOutput()
	if werr != nil {
		t.Fatalf("wasmtime run failed: %v\noutput:\n%s", werr, wout)
	}
	got := strings.TrimSpace(string(wout))
	if got != "42" {
		t.Errorf("wasmtime stdout = %q, want %q", got, "42")
	}
}

// TestWASMModuleRunsIfElse: a function that returns 100 from the
// then-arm or 200 from the else-arm of an if/else with an i32
// result type. Exercises inst_if_start with a non-empty blocktype
// (encode.valtype_i32) plus inst_else / inst_end.
func TestWASMModuleRunsIfElse(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    var m: module.Module = module.module_new();
    var p0: u8[] = [];
    var r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);

    // Body: i32.const 1 ; if (result i32) { i32.const 100 }
    //       else { i32.const 200 } ; end
    var body: u8[] = inst.inst_i32_const([], 1);
    body = inst.inst_if_start(body, encode.valtype_i32());
    body = inst.inst_i32_const(body, 100);
    body = inst.inst_else(body);
    body = inst.inst_i32_const(body, 200);
    body = inst.inst_end(body);
    var fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), body);
    m = m.with_code([fn]);
    var bytes: u8[] = module.build(m);

    var output: string = "";
    var i: i32 = 0;
    while (i < bytes.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (bytes[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	mod := langProducedModuleBytes(t, src)
	out := strings.TrimSpace(runWasmModule(t, mod))
	if !strings.Contains(out, "100") {
		t.Errorf("wasmtime stdout = %q, want it to contain 100", out)
	}
}

// TestWASMModuleRunsLocalSetGet: store 42 in a local then load it
// back. Exercises put_locals_one_group for declaring the local
// plus inst_local_set / inst_local_get.
func TestWASMModuleRunsLocalSetGet(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    var m: module.Module = module.module_new();
    var p0: u8[] = [];
    var r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);

    // 1 local of type i32 (index 0).
    var localsBytes: u8[] = inst.put_locals_one_group([], 1u32, encode.valtype_i32());

    // Body: i32.const 42 ; local.set 0 ; local.get 0
    var body: u8[] = inst.inst_i32_const([], 42);
    body = inst.inst_local_set(body, 0u32);
    body = inst.inst_local_get(body, 0u32);

    var fn: u8[] = inst.put_function_body([], localsBytes, body);
    m = m.with_code([fn]);
    var bytes: u8[] = module.build(m);

    var output: string = "";
    var i: i32 = 0;
    while (i < bytes.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (bytes[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	mod := langProducedModuleBytes(t, src)
	out := strings.TrimSpace(runWasmModule(t, mod))
	if !strings.Contains(out, "42") {
		t.Errorf("wasmtime stdout = %q, want it to contain 42", out)
	}
}

// TestWASMModuleRunsSumLoop: sum 1..10 via a loop. Exercises the
// full control-flow stack: loop_start, if_start, br (continuing
// the loop), local_get / local_set on multiple locals, and an
// end terminator stack of depth 2 (if inside loop). Expected
// result: 1+2+...+10 = 55.
func TestWASMModuleRunsSumLoop(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/numeric";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    var m: module.Module = module.module_new();
    var p0: u8[] = [];
    var r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);

    // 2 locals of type i32: index 0 = i (counter), index 1 = sum.
    var localsBytes: u8[] = inst.put_locals_one_group([], 2u32, encode.valtype_i32());

    var body: u8[] = inst.inst_loop_start([], inst.blocktype_empty());
    body = inst.inst_local_get(body, 0u32);
    body = inst.inst_i32_const(body, 10);
    body = numeric.inst_i32_lt_s(body);
    body = inst.inst_if_start(body, inst.blocktype_empty());
    body = inst.inst_local_get(body, 1u32);
    body = inst.inst_local_get(body, 0u32);
    body = inst.inst_i32_const(body, 1);
    body = numeric.inst_i32_add(body);
    body = numeric.inst_i32_add(body);
    body = inst.inst_local_set(body, 1u32);
    body = inst.inst_local_get(body, 0u32);
    body = inst.inst_i32_const(body, 1);
    body = numeric.inst_i32_add(body);
    body = inst.inst_local_set(body, 0u32);
    body = inst.inst_br(body, 1u32);
    body = inst.inst_end(body);
    body = inst.inst_end(body);
    body = inst.inst_local_get(body, 1u32);

    var fn: u8[] = inst.put_function_body([], localsBytes, body);
    m = m.with_code([fn]);
    var bytes: u8[] = module.build(m);

    var output: string = "";
    var i: i32 = 0;
    while (i < bytes.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (bytes[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	mod := langProducedModuleBytes(t, src)
	out := strings.TrimSpace(runWasmModule(t, mod))
	if !strings.Contains(out, "55") {
		t.Errorf("wasmtime stdout = %q, want it to contain 55 (sum 1..10)", out)
	}
}

// TestWASMSectionsCustom verifies the byte layout of
// encode_custom_section: section id 0 + uleb body size +
// name (uleb-prefixed UTF-8) + payload bytes.
func TestWASMSectionsCustom(t *testing.T) {
	src := `import "std/wasm/sections";
function main(): i32 {
    var empty: u8[] = [];

    // Custom section named "foo" with payload [0xAA, 0xBB].
    // Body: uleb(3) + "foo" (3 bytes) + 0xAA 0xBB = 6 bytes.
    // Wrapped: id(0) + uleb(6) + body = 8 bytes total.
    var payload: u8[] = [170u8, 187u8];
    var s: u8[] = sections.encode_custom_section(empty, "foo", payload);
    if (s.len() != 8) { return 1; }
    if (s[0] != 0u8)   { return 2; }    // section_custom id
    if (s[1] != 6u8)   { return 3; }    // body size
    if (s[2] != 3u8)   { return 4; }    // name length uleb
    if (s[3] != 102u8) { return 5; }    // 'f'
    if (s[4] != 111u8) { return 6; }    // 'o'
    if (s[5] != 111u8) { return 7; }    // 'o'
    if (s[6] != 170u8) { return 8; }    // 0xAA
    if (s[7] != 187u8) { return 9; }    // 0xBB

    // Empty payload: just the name.
    var s2: u8[] = sections.encode_custom_section(empty, "x", empty);
    if (s2.len() != 4)  { return 20; }
    if (s2[0] != 0u8)  { return 21; }
    if (s2[1] != 2u8)  { return 22; }   // body size
    if (s2[2] != 1u8)  { return 23; }   // name length 1
    if (s2[3] != 120u8) { return 24; }  // 'x'

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("sections custom: exit = %d, want 0", got)
	}
}

// TestWASMSectionsCustomInModule appends a custom section to a
// real (valid) core module and feeds the result through
// wasm-tools — proves the custom section can sit alongside the
// standard sections without breaking the parser.
func TestWASMSectionsCustomInModule(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    var m: module.Module = module.module_new();
    var p0: u8[] = [];
    var r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);
    var bodyExpr: u8[] = inst.inst_i32_const([], 42);
    var fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), bodyExpr);
    m = m.with_code([fn]);
    var core_bytes: u8[] = module.build(m);

    var marker_payload: u8[] = [104u8, 101u8, 108u8, 108u8, 111u8, 45u8, 108u8, 97u8, 110u8, 103u8];
    var with_custom: u8[] = sections.encode_custom_section(core_bytes, "marker", marker_payload);

    var output: string = "";
    var i: i32 = 0;
    while (i < with_custom.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (with_custom[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	out := strings.TrimSpace(runWasmCapturingStdout(t, src))
	if out == "" {
		t.Fatal("empty stdout from Lang program")
	}
	fields := strings.Fields(out)
	bs := make([]byte, 0, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte %d: parse %q: %v", i, f, err)
		}
		bs = append(bs, byte(n))
	}
	dir := t.TempDir()
	modPath := filepath.Join(dir, "with_custom.wasm")
	if err := os.WriteFile(modPath, bs, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if vout, err := exec.Command("wasm-tools", "validate", modPath).CombinedOutput(); err != nil {
		hexBytes := ""
		for _, b := range bs {
			hexBytes += fmt.Sprintf("%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\n%s\nbytes:\n%s", err, vout, hexBytes)
	}
	wat, err := exec.Command("wasm-tools", "print", modPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, wat)
	}
	watStr := string(wat)
	if !strings.Contains(watStr, "marker") {
		t.Errorf("expected custom section name 'marker' in output, got:\n%s", watStr)
	}
}

// TestWASMModuleRunsMemoryLoad: a function that loads a byte
// from linear memory and returns it. Exercises
// encode_memory_section, encode_data_section, and
// inst_i32_load8_u + memarg as a runnable module.
func TestWASMModuleRunsMemoryLoad(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/memory";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    var m: module.Module = module.module_new();
    var p0: u8[] = [];
    var r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_memory(1u32, 0 - 1);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);
    m = m.with_data([0], [[42u8, 7u8, 99u8, 5u8]]);

    var body: u8[] = inst.inst_i32_const([], 2);
    body = memory.inst_i32_load8_u(body, 0u32, 0u32);
    var fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), body);
    m = m.with_code([fn]);
    var bytes: u8[] = module.build(m);

    var output: string = "";
    var i: i32 = 0;
    while (i < bytes.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (bytes[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	mod := langProducedModuleBytes(t, src)
	out := strings.TrimSpace(runWasmModule(t, mod))
	if !strings.Contains(out, "99") {
		t.Errorf("wasmtime stdout = %q, want it to contain 99 (data[2])", out)
	}
}

// TestWASMModuleRunsMemoryStoreLoad: writes 77 to memory at
// address 16 via i32.store8 then reads it back. Exercises the
// writable-memory + store-then-load round-trip.
func TestWASMModuleRunsMemoryStoreLoad(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/memory";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    var m: module.Module = module.module_new();
    var p0: u8[] = [];
    var r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_memory(1u32, 0 - 1);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);

    var body: u8[] = inst.inst_i32_const([], 16);
    body = inst.inst_i32_const(body, 77);
    body = memory.inst_i32_store8(body, 0u32, 0u32);
    body = inst.inst_i32_const(body, 16);
    body = memory.inst_i32_load8_u(body, 0u32, 0u32);
    var fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), body);
    m = m.with_code([fn]);
    var bytes: u8[] = module.build(m);

    var output: string = "";
    var i: i32 = 0;
    while (i < bytes.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (bytes[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	mod := langProducedModuleBytes(t, src)
	out := strings.TrimSpace(runWasmModule(t, mod))
	if !strings.Contains(out, "77") {
		t.Errorf("wasmtime stdout = %q, want it to contain 77 (stored value)", out)
	}
}

// TestWASMModuleRunsCallIndirect builds a module with a funcref
// table + element segment and dispatches through it: main pushes
// table index 1 and call_indirects type 0, which lands on func1
// (returns 22). Proves the table section, the element section's
// funcidx ordering, and call_indirect all line up end-to-end.
func TestWASMModuleRunsCallIndirect(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/encode";
import "std/wasm/sections";
import "std/wasm/imports";
function main(): i32 {
    var m: module.Module = module.module_new();

    // One shared type: () -> i32 (both callees and main).
    var p0: u8[] = [];
    var r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);

    // func0 / func1 are indirect-call targets; func2 is main.
    m = m.with_functions([0u32, 0u32, 0u32]);

    // Funcref table holding both targets.
    m = m.with_table(imports.reftype_funcref(), 2u32, 0 - 1);

    // table[0] = func0, table[1] = func1.
    m = m.with_elements([0], [[0u32, 1u32]]);

    m = m.with_exports(["main"], [sections.export_func()], [2u32]);

    // func0 -> 11, func1 -> 22.
    var f0: u8[] = inst.put_function_body([], inst.put_locals_empty([]), inst.inst_i32_const([], 11));
    var f1: u8[] = inst.put_function_body([], inst.put_locals_empty([]), inst.inst_i32_const([], 22));

    // main: i32.const 1 ; call_indirect type 0 table 0 -> func1 -> 22.
    var main_body: u8[] = inst.inst_i32_const([], 1);
    main_body = inst.inst_call_indirect(main_body, 0u32, 0u32);
    var f2: u8[] = inst.put_function_body([], inst.put_locals_empty([]), main_body);

    m = m.with_code([f0, f1, f2]);
    var bytes: u8[] = module.build(m);

    var output: string = "";
    var i: i32 = 0;
    while (i < bytes.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (bytes[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	mod := langProducedModuleBytes(t, src)
	out := strings.TrimSpace(runWasmModule(t, mod))
	if !strings.Contains(out, "22") {
		t.Errorf("wasmtime stdout = %q, want it to contain 22 (table[1] via call_indirect)", out)
	}
}

// TestWASMModuleRunsMemoryCopy seeds memory with a data segment,
// memory.copies a 2-byte run to a fresh address, and reads back
// the second copied byte. Exercises the 0xFC memory.copy op
// end-to-end under wasmtime.
func TestWASMModuleRunsMemoryCopy(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/memory";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    var m: module.Module = module.module_new();
    var p0: u8[] = [];
    var r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_memory(1u32, 0 - 1);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);
    m = m.with_data([0], [[5u8, 6u8, 7u8, 8u8]]);

    // memory.copy dst=16, src=0, len=2  -> bytes [5,6] land at 16,17.
    var body: u8[] = inst.inst_i32_const([], 16);
    body = inst.inst_i32_const(body, 0);
    body = inst.inst_i32_const(body, 2);
    body = memory.inst_memory_copy(body);
    // Read back addr 17 -> 6.
    body = inst.inst_i32_const(body, 17);
    body = memory.inst_i32_load8_u(body, 0u32, 0u32);
    var fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), body);
    m = m.with_code([fn]);
    var bytes: u8[] = module.build(m);

    var output: string = "";
    var i: i32 = 0;
    while (i < bytes.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (bytes[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	mod := langProducedModuleBytes(t, src)
	out := strings.TrimSpace(runWasmModule(t, mod))
	if !strings.Contains(out, "6") {
		t.Errorf("wasmtime stdout = %q, want it to contain 6 (copied byte at 17)", out)
	}
}

// TestWASMModuleRunsMemoryFill memory.fills a 3-byte run with the
// value 9 then reads back a byte from the middle of the run.
// Exercises the 0xFC memory.fill op end-to-end under wasmtime.
func TestWASMModuleRunsMemoryFill(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/memory";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    var m: module.Module = module.module_new();
    var p0: u8[] = [];
    var r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_memory(1u32, 0 - 1);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);

    // memory.fill dst=8, val=9, len=3 -> addrs 8,9,10 = 9.
    var body: u8[] = inst.inst_i32_const([], 8);
    body = inst.inst_i32_const(body, 9);
    body = inst.inst_i32_const(body, 3);
    body = memory.inst_memory_fill(body);
    // Read back addr 9 -> 9.
    body = inst.inst_i32_const(body, 9);
    body = memory.inst_i32_load8_u(body, 0u32, 0u32);
    var fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), body);
    m = m.with_code([fn]);
    var bytes: u8[] = module.build(m);

    var output: string = "";
    var i: i32 = 0;
    while (i < bytes.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (bytes[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	mod := langProducedModuleBytes(t, src)
	out := strings.TrimSpace(runWasmModule(t, mod))
	if !strings.Contains(out, "9") {
		t.Errorf("wasmtime stdout = %q, want it to contain 9 (filled byte at 9)", out)
	}
}

// TestWASMModuleRunsGlobalSection: const i32 global = 314,
// main() loads it via global.get 0. Exercises
// encode_global_section + inst_global_get + init-expr shape.
func TestWASMModuleRunsGlobalSection(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/imports";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    var m: module.Module = module.module_new();
    var p0: u8[] = [];
    var r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);

    var initExpr: u8[] = inst.inst_i32_const([], 314);
    initExpr = inst.inst_end(initExpr);
    m = m.with_globals([encode.valtype_i32()], [imports.mut_const()], [initExpr]);

    var body: u8[] = inst.inst_global_get([], 0u32);
    var fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), body);
    m = m.with_code([fn]);
    var bytes: u8[] = module.build(m);

    var output: string = "";
    var i: i32 = 0;
    while (i < bytes.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (bytes[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	mod := langProducedModuleBytes(t, src)
	out := strings.TrimSpace(runWasmModule(t, mod))
	if !strings.Contains(out, "314") {
		t.Errorf("wasmtime stdout = %q, want it to contain 314 (global value)", out)
	}
}

// TestWASMModuleRunsDivision: i32 signed divide. div_s and div_u
// share the call-site shape but differ in semantics for negative
// operands — body returns (-20) / 3 = -6 under signed. Catches a
// mis-encoded opcode (0x6D vs 0x6E) that would silently produce
// a huge u32 under div_u.
func TestWASMModuleRunsDivision(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/numeric";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    var m: module.Module = module.module_new();
    var p0: u8[] = [];
    var r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);

    var body: u8[] = inst.inst_i32_const([], 0 - 20);
    body = inst.inst_i32_const(body, 3);
    body = numeric.inst_i32_div_s(body);
    var fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), body);
    m = m.with_code([fn]);
    var bytes: u8[] = module.build(m);

    var output: string = "";
    var i: i32 = 0;
    while (i < bytes.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (bytes[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	mod := langProducedModuleBytes(t, src)
	out := strings.TrimSpace(runWasmModule(t, mod))
	if !(strings.Contains(out, "-6") || strings.Contains(out, "4294967290")) {
		t.Errorf("wasmtime stdout = %q, want -6 / 4294967290 (signed -20 / 3)", out)
	}
}

// TestWASMModuleRunsSignExtend: i32.extend8_s sign-extends the
// low byte (255 = 0xFF -> -1). Exercises the post-MVP
// sign-extension opcodes (0xC0+) at runtime.
func TestWASMModuleRunsSignExtend(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/convert";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    var m: module.Module = module.module_new();
    var p0: u8[] = [];
    var r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);

    // i32.const 255 ; i32.extend8_s -> -1
    var body: u8[] = inst.inst_i32_const([], 255);
    body = convert.inst_i32_extend8_s(body);
    var fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), body);
    m = m.with_code([fn]);
    var bytes: u8[] = module.build(m);

    var output: string = "";
    var i: i32 = 0;
    while (i < bytes.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (bytes[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	mod := langProducedModuleBytes(t, src)
	out := strings.TrimSpace(runWasmModule(t, mod))
	if !(strings.Contains(out, "-1") || strings.Contains(out, "4294967295")) {
		t.Errorf("wasmtime stdout = %q, want -1 / 4294967295 (255 sign-extended)", out)
	}
}

// TestWASMModuleRunsI64Arithmetic: body is (1_000_000_000_000 +
// 234_567_890_123) as i64, returning the result truncated to i32
// via i32.wrap_i64. Exercises i64.const + i64.add + i32.wrap_i64
// at runtime — the i64 path is otherwise only byte-vector tested.
//
// 1_234_567_890_123 mod 2^32 = 1_912_276_171.
func TestWASMModuleRunsI64Arithmetic(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/numeric";
import "std/wasm/convert";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    var m: module.Module = module.module_new();
    var p0: u8[] = [];
    var r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);

    var body: u8[] = inst.inst_i64_const([], 1000000000000i64);
    body = inst.inst_i64_const(body, 234567890123i64);
    body = numeric.inst_i64_add(body);
    body = convert.inst_i32_wrap_i64(body);
    var fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), body);
    m = m.with_code([fn]);
    var bytes: u8[] = module.build(m);

    var output: string = "";
    var i: i32 = 0;
    while (i < bytes.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (bytes[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	mod := langProducedModuleBytes(t, src)
	out := strings.TrimSpace(runWasmModule(t, mod))
	if !strings.Contains(out, "1912276171") {
		t.Errorf("wasmtime stdout = %q, want it to contain 1912276171 (i64 sum low 32)", out)
	}
}

// TestWASMModuleRunsBitwise: 12 ^ 10 = 6. Catches a swap among
// the bitwise ops (and = 0x71, or = 0x72, xor = 0x73 — adjacent
// opcodes that could be transposed). 12 & 10 = 8, 12 | 10 = 14,
// 12 ^ 10 = 6, so any swap shows immediately.
func TestWASMModuleRunsBitwise(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/numeric";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    var m: module.Module = module.module_new();
    var p0: u8[] = [];
    var r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);

    var body: u8[] = inst.inst_i32_const([], 12);
    body = inst.inst_i32_const(body, 10);
    body = numeric.inst_i32_xor(body);
    var fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), body);
    m = m.with_code([fn]);
    var bytes: u8[] = module.build(m);

    var output: string = "";
    var i: i32 = 0;
    while (i < bytes.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (bytes[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	mod := langProducedModuleBytes(t, src)
	out := strings.TrimSpace(runWasmModule(t, mod))
	if !strings.Contains(out, "6") {
		t.Errorf("wasmtime stdout = %q, want 6 (12 ^ 10)", out)
	}
}

// TestWASMModuleRunsSignedVsUnsignedCmp: -1 vs 1 under
// i32.lt_s = 1 (true), under i32.lt_u = 0 (false, since -1 as
// u32 is 0xFFFFFFFF which is greater than 1). The body picks
// lt_s, so result is 1. A swap with lt_u (0x49 vs 0x48) would
// flip the result to 0.
func TestWASMModuleRunsSignedVsUnsignedCmp(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/numeric";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    var m: module.Module = module.module_new();
    var p0: u8[] = [];
    var r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);

    var body: u8[] = inst.inst_i32_const([], 0 - 1);
    body = inst.inst_i32_const(body, 1);
    body = numeric.inst_i32_lt_s(body);
    var fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), body);
    m = m.with_code([fn]);
    var bytes: u8[] = module.build(m);

    var output: string = "";
    var i: i32 = 0;
    while (i < bytes.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (bytes[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	mod := langProducedModuleBytes(t, src)
	out := strings.TrimSpace(runWasmModule(t, mod))
	if !strings.Contains(out, "1") {
		t.Errorf("wasmtime stdout = %q, want 1 ((-1) <_s 1 = true)", out)
	}
}

// TestWASMModuleRunsShiftRotate: (1 << 5) = 32 ; then rotr by 1
// gives 16. Exercises the shift / rotate block (0x74..0x78)
// which is otherwise only byte-vector tested.
func TestWASMModuleRunsShiftRotate(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/numeric";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    var m: module.Module = module.module_new();
    var p0: u8[] = [];
    var r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);

    // (1 << 5) = 32 ; then rotr by 1 -> 16.
    var body: u8[] = inst.inst_i32_const([], 1);
    body = inst.inst_i32_const(body, 5);
    body = numeric.inst_i32_shl(body);
    body = inst.inst_i32_const(body, 1);
    body = numeric.inst_i32_rotr(body);
    var fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), body);
    m = m.with_code([fn]);
    var bytes: u8[] = module.build(m);

    var output: string = "";
    var i: i32 = 0;
    while (i < bytes.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (bytes[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	mod := langProducedModuleBytes(t, src)
	out := strings.TrimSpace(runWasmModule(t, mod))
	if !strings.Contains(out, "16") {
		t.Errorf("wasmtime stdout = %q, want 16 ((1 << 5) rotr 1)", out)
	}
}

// TestWASMModuleRunsF32Arithmetic: f32.const 1.0 + f32.const 1.0
// = 2.0, returned via i32.reinterpret_f32 so wasmtime can print
// the i32 bit pattern. f32 2.0 = 0x40000000 = 1073741824.
func TestWASMModuleRunsF32Arithmetic(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/numeric";
import "std/wasm/convert";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    var m: module.Module = module.module_new();
    var p0: u8[] = [];
    var r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);

    // f32 1.0 bit pattern: 0x3F800000 = 1065353216.
    var body: u8[] = inst.inst_f32_const([], 1065353216u32);
    body = inst.inst_f32_const(body, 1065353216u32);
    body = numeric.inst_f32_add(body);
    body = convert.inst_i32_reinterpret_f32(body);
    var fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), body);
    m = m.with_code([fn]);
    var bytes: u8[] = module.build(m);

    var output: string = "";
    var i: i32 = 0;
    while (i < bytes.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (bytes[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	mod := langProducedModuleBytes(t, src)
	out := strings.TrimSpace(runWasmModule(t, mod))
	if !strings.Contains(out, "1073741824") {
		t.Errorf("wasmtime stdout = %q, want 1073741824 (bit pattern of f32 2.0)", out)
	}
}

// TestWASMModuleRunsSelect: pushes 7, 9, 1 ; select. cond=1
// (truthy) keeps the first operand, so result is 7. Exercises
// inst_select.
func TestWASMModuleRunsSelect(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    var m: module.Module = module.module_new();
    var p0: u8[] = [];
    var r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);

    var body: u8[] = inst.inst_i32_const([], 7);
    body = inst.inst_i32_const(body, 9);
    body = inst.inst_i32_const(body, 1);
    body = inst.inst_select(body);
    var fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), body);
    m = m.with_code([fn]);
    var bytes: u8[] = module.build(m);

    var output: string = "";
    var i: i32 = 0;
    while (i < bytes.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (bytes[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	mod := langProducedModuleBytes(t, src)
	out := strings.TrimSpace(runWasmModule(t, mod))
	if !strings.Contains(out, "7") {
		t.Errorf("wasmtime stdout = %q, want 7 (select with cond=1 keeps the first)", out)
	}
}

// TestWASMModuleRunsRecursion: factorial(5) via self-recursion.
// fact(n) = if n <= 1 { 1 } else { n * fact(n - 1) }.
// fact(5) = 120. Exercises inst_call to a function index equal
// to the calling function's own index.
func TestWASMModuleRunsRecursion(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/numeric";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    var m: module.Module = module.module_new();

    var p_fact: u8[] = [encode.valtype_i32()];
    var r_fact: u8[] = [encode.valtype_i32()];
    var p_main: u8[] = [];
    var r_main: u8[] = [encode.valtype_i32()];
    m = m.with_types([p_fact, p_main], [r_fact, r_main]);

    m = m.with_functions([0u32, 1u32]);
    m = m.with_exports(["main"], [sections.export_func()], [1u32]);

    // fact(n): if (n <= 1) 1 else n * fact(n - 1)
    var fact_body: u8[] = inst.inst_local_get([], 0u32);
    fact_body = inst.inst_i32_const(fact_body, 1);
    fact_body = numeric.inst_i32_le_s(fact_body);
    fact_body = inst.inst_if_start(fact_body, encode.valtype_i32());
    fact_body = inst.inst_i32_const(fact_body, 1);
    fact_body = inst.inst_else(fact_body);
    fact_body = inst.inst_local_get(fact_body, 0u32);
    fact_body = inst.inst_local_get(fact_body, 0u32);
    fact_body = inst.inst_i32_const(fact_body, 1);
    fact_body = numeric.inst_i32_sub(fact_body);
    fact_body = inst.inst_call(fact_body, 0u32);
    fact_body = numeric.inst_i32_mul(fact_body);
    fact_body = inst.inst_end(fact_body);
    var fact_fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), fact_body);

    // main(): i32.const 5 ; call 0
    var main_body: u8[] = inst.inst_i32_const([], 5);
    main_body = inst.inst_call(main_body, 0u32);
    var main_fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), main_body);

    m = m.with_code([fact_fn, main_fn]);
    var bytes: u8[] = module.build(m);

    var output: string = "";
    var i: i32 = 0;
    while (i < bytes.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (bytes[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	mod := langProducedModuleBytes(t, src)
	out := strings.TrimSpace(runWasmModule(t, mod))
	if !strings.Contains(out, "120") {
		t.Errorf("wasmtime stdout = %q, want 120 (factorial 5)", out)
	}
}

// TestWASMModuleRunsF64Arithmetic: f64.const 1.5 + f64.const 2.5
// = 4.0, returned by reinterpreting to i64 + shifting right 32 +
// wrapping to i32. f64 4.0 bit pattern: 0x4010000000000000;
// high 32 bits = 0x40100000 = 1074790400.
func TestWASMModuleRunsF64Arithmetic(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/numeric";
import "std/wasm/convert";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    var m: module.Module = module.module_new();
    var p0: u8[] = [];
    var r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);

    // f64 1.5 = 0x3FF8000000000000; f64 2.5 = 0x4004000000000000.
    var body: u8[] = inst.inst_f64_const([], 4609434218613702656u64);
    body = inst.inst_f64_const(body, 4612811918334230528u64);
    body = numeric.inst_f64_add(body);
    body = convert.inst_i64_reinterpret_f64(body);
    body = inst.inst_i64_const(body, 32i64);
    body = numeric.inst_i64_shr_u(body);
    body = convert.inst_i32_wrap_i64(body);
    var fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), body);
    m = m.with_code([fn]);
    var bytes: u8[] = module.build(m);

    var output: string = "";
    var i: i32 = 0;
    while (i < bytes.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (bytes[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	mod := langProducedModuleBytes(t, src)
	out := strings.TrimSpace(runWasmModule(t, mod))
	if !strings.Contains(out, "1074790400") {
		t.Errorf("wasmtime stdout = %q, want 1074790400 (high 32 of f64 4.0)", out)
	}
}

// TestWASMModuleRunsClz: i32.clz of 16 = 27. Exercises the unary
// numeric op block (0x67 clz, 0x68 ctz, 0x69 popcnt) at runtime.
func TestWASMModuleRunsClz(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/numeric";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    var m: module.Module = module.module_new();
    var p0: u8[] = [];
    var r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);

    var body: u8[] = inst.inst_i32_const([], 16);
    body = numeric.inst_i32_clz(body);
    var fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), body);
    m = m.with_code([fn]);
    var bytes: u8[] = module.build(m);

    var output: string = "";
    var i: i32 = 0;
    while (i < bytes.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (bytes[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	mod := langProducedModuleBytes(t, src)
	out := strings.TrimSpace(runWasmModule(t, mod))
	if !strings.Contains(out, "27") {
		t.Errorf("wasmtime stdout = %q, want 27 (clz of 16)", out)
	}
}

// TestWASMModuleRunsArraySum: sums 4 bytes from a data segment
// via a loop. Real-world-ish composition: data section + memory
// load + loop counter + conditional break. [10,20,30,40] -> 100.
func TestWASMModuleRunsArraySum(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/numeric";
import "std/wasm/memory";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    var m: module.Module = module.module_new();
    var p0: u8[] = [];
    var r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_memory(1u32, 0 - 1);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);
    m = m.with_data([0], [[10u8, 20u8, 30u8, 40u8]]);

    var localsBytes: u8[] = inst.put_locals_one_group([], 2u32, encode.valtype_i32());

    var body: u8[] = inst.inst_loop_start([], inst.blocktype_empty());
    body = inst.inst_local_get(body, 0u32);
    body = inst.inst_i32_const(body, 4);
    body = numeric.inst_i32_lt_s(body);
    body = inst.inst_if_start(body, inst.blocktype_empty());
    body = inst.inst_local_get(body, 1u32);
    body = inst.inst_local_get(body, 0u32);
    body = memory.inst_i32_load8_u(body, 0u32, 0u32);
    body = numeric.inst_i32_add(body);
    body = inst.inst_local_set(body, 1u32);
    body = inst.inst_local_get(body, 0u32);
    body = inst.inst_i32_const(body, 1);
    body = numeric.inst_i32_add(body);
    body = inst.inst_local_set(body, 0u32);
    body = inst.inst_br(body, 1u32);
    body = inst.inst_end(body);
    body = inst.inst_end(body);
    body = inst.inst_local_get(body, 1u32);

    var fn: u8[] = inst.put_function_body([], localsBytes, body);
    m = m.with_code([fn]);
    var bytes: u8[] = module.build(m);

    var output: string = "";
    var i: i32 = 0;
    while (i < bytes.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (bytes[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	mod := langProducedModuleBytes(t, src)
	out := strings.TrimSpace(runWasmModule(t, mod))
	if !strings.Contains(out, "100") {
		t.Errorf("wasmtime stdout = %q, want 100 (sum of [10,20,30,40] via memory loop)", out)
	}
}

// TestWASMLebCrossValidates runs std/wasm/leb128 on a curated
// input set + asserts the bytes match what internal/wasm/leb128
// (the Go mirror) produces for the same inputs. Together with
// the per-side unit tests this enforces lock-step: any drift
// off-spec on one side fires here.
func TestWASMLebCrossValidates(t *testing.T) {
	type expect struct {
		fn     string
		input  string
		expect []byte
	}
	exps := []expect{
		{"uleb_u32", "0u32", []byte{0x00}},
		{"uleb_u32", "1u32", []byte{0x01}},
		{"uleb_u32", "127u32", []byte{0x7f}},
		{"uleb_u32", "128u32", []byte{0x80, 0x01}},
		{"uleb_u32", "16384u32", []byte{0x80, 0x80, 0x01}},
		{"uleb_u32", "624485u32", []byte{0xe5, 0x8e, 0x26}},
		{"uleb_u32", "4294967295u32", []byte{0xff, 0xff, 0xff, 0xff, 0x0f}},
		{"sleb_i32", "0", []byte{0x00}},
		{"sleb_i32", "1", []byte{0x01}},
		{"sleb_i32", "(0 - 1)", []byte{0x7f}},
		{"sleb_i32", "63", []byte{0x3f}},
		{"sleb_i32", "64", []byte{0xc0, 0x00}},
		{"sleb_i32", "(0 - 64)", []byte{0x40}},
		{"sleb_i32", "(0 - 65)", []byte{0xbf, 0x7f}},
		{"sleb_i32", "2147483647", []byte{0xff, 0xff, 0xff, 0xff, 0x07}},
		{"sleb_i32", "(0 - 2147483647) - 1", []byte{0x80, 0x80, 0x80, 0x80, 0x78}},
		{"uleb_u64", "0u64", []byte{0x00}},
		{"uleb_u64", "127u64", []byte{0x7f}},
		{"uleb_u64", "128u64", []byte{0x80, 0x01}},
		{"uleb_u64", "4294967295u64", []byte{0xff, 0xff, 0xff, 0xff, 0x0f}},
		{"sleb_i64", "0i64", []byte{0x00}},
		{"sleb_i64", "(0i64 - 1i64)", []byte{0x7f}},
		{"sleb_i64", "64i64", []byte{0xc0, 0x00}},
		{"sleb_i64", "(0i64 - 64i64)", []byte{0x40}},
		// The algorithm both sides use produces a 10-byte encoding
		// for max-i64 (valid sleb128, just not the minimal form).
		{"sleb_i64", "9223372036854775807i64", []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x00}},
		{"sleb_i64", "(0i64 - 9223372036854775807i64) - 1i64", []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x7f}},
	}

	// Compose a Lang program that prints one space-separated
	// byte sequence per expect-row.
	var sb strings.Builder
	sb.WriteString(`import "std/wasm/leb128";
import "std/i32";
import "std/string";

function dump(bs: u8[]): string {
    var s: string = "";
    var i: i32 = 0;
    while (i < bs.len()) {
        if (i > 0) { s = s + " "; }
        s = s + (bs[i] as i32).to_string();
        i = i + 1;
    }
    return s;
}

function main(): i32 {
    var empty: u8[] = [];
`)
	for _, e := range exps {
		sb.WriteString("    print(dump(leb128.")
		sb.WriteString(e.fn)
		sb.WriteString("(empty, ")
		sb.WriteString(e.input)
		sb.WriteString(")));\n")
	}
	sb.WriteString("    return 0;\n}\n")

	stdout, _ := invokeWasmtime(t, sb.String())
	// buildComponent uses PrintMainResult: true, so the harness's
	// trailing main()-returns-0 produces an extra "0" line at the
	// end. Drop trailing blank + the result line.
	gotLines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(gotLines) < len(exps) {
		t.Fatalf("got %d lines, want >= %d\nstdout:\n%s", len(gotLines), len(exps), stdout)
	}
	for i, e := range exps {
		wantParts := make([]string, len(e.expect))
		for j, b := range e.expect {
			wantParts[j] = strconv.Itoa(int(b))
		}
		want := strings.Join(wantParts, " ")
		if gotLines[i] != want {
			t.Errorf("%s(%s):\n  Lang got: %q\n  Go want:  %q",
				e.fn, e.input, gotLines[i], want)
		}
	}
}

// TestWASMSectionsCrossValidates wires the Go sections package
// end-to-end: build a module with type + function + export +
// code sections, run it under wasmtime, assert the function
// returns 42. Catches any drift between the Go mirror and the
// wasm spec — and indirectly against the Lang composers, which
// have their own e2e tests producing the same module shape.
func TestWASMSectionsCrossValidates(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}

	// Build via Go composers: a module that exports `main: () -> i32`
	// returning 42.
	out := encode.PutModuleHeader(nil)

	// Type section: one functype () -> i32.
	out = sections.EncodeTypeSection(out,
		[][]byte{nil}, [][]byte{{encode.ValtypeI32}})

	// Function section: one function using type 0.
	out = sections.EncodeFunctionSection(out, []uint32{0})

	// Export section: export "main" as func 0.
	out = sections.EncodeExportSection(out,
		[]string{"main"},
		[]byte{sections.ExportFunc},
		[]uint32{0})

	// Code section: one body { i32.const 42 ; end }.
	body := []byte{0x00, 0x41, 0x2a, 0x0b} // locals_vec_len=0, i32.const 42, end
	// Code entries are size-prefixed individually.
	codeEntry := append(leb128.UlebU32(nil, uint32(len(body))), body...)
	out = sections.EncodeCodeSection(out, [][]byte{codeEntry})

	stdout := runWasmModule(t, out)
	if strings.TrimSpace(stdout) != "42" {
		t.Fatalf("wasmtime stdout = %q, want %q", stdout, "42")
	}
}

// TestWASMInstCrossValidates uses Go encode + sections + inst
// (no Lang side, no wasm-tools) to build a module whose
// exported `main` runs a small if/else over locals, returning
// max(local0, local1). Cross-checks the Go inst package against
// wasmtime — and indirectly against the Lang side, which is
// already covered by TestWASMSectionsCustom and friends with
// the same control-flow shape.
func TestWASMInstCrossValidates(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}

	// Header + type + function + export sections.
	out := encode.PutModuleHeader(nil)
	out = sections.EncodeTypeSection(out,
		[][]byte{nil}, [][]byte{{encode.ValtypeI32}})
	out = sections.EncodeFunctionSection(out, []uint32{0})
	out = sections.EncodeExportSection(out,
		[]string{"main"},
		[]byte{sections.ExportFunc},
		[]uint32{0})

	// Function body: two i32 locals, compute max(7, 11) = 11
	// via a sequence:
	//   i32.const 7
	//   local.set 0
	//   i32.const 11
	//   local.set 1
	//   local.get 0
	//   local.get 1
	//   i32.gt_s        ; 0x4A
	//   if (result i32)
	//     local.get 0
	//   else
	//     local.get 1
	//   end
	body := inst.InstI32Const(nil, 7)
	body = inst.InstLocalSet(body, 0)
	body = inst.InstI32Const(body, 11)
	body = inst.InstLocalSet(body, 1)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 1)
	body = append(body, 0x4a) // i32.gt_s — defined in the numeric mirror, not yet imported
	body = inst.InstIfStart(body, encode.ValtypeI32)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstElse(body)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstEnd(body)

	locals := inst.PutLocalsOneGroup(nil, 2, encode.ValtypeI32)
	fn := inst.PutFunctionBody(nil, locals, body)
	out = sections.EncodeCodeSection(out, [][]byte{fn})

	stdout := runWasmModule(t, out)
	if strings.TrimSpace(stdout) != "11" {
		t.Fatalf("wasmtime stdout = %q, want %q", stdout, "11")
	}
}

// TestWASMMemoryNumericConvertCross builds a module using Go
// encode + sections + inst + memory + numeric + convert all
// together: read 4 bytes from a data segment, sum them as i64,
// truncate to i32, return. Catches drift in any of the three
// opcode-family packages, plus their integration with memarg
// encoding and the data section.
func TestWASMMemoryNumericConvertCross(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}

	out := encode.PutModuleHeader(nil)
	out = sections.EncodeTypeSection(out,
		[][]byte{nil}, [][]byte{{encode.ValtypeI32}})
	out = sections.EncodeFunctionSection(out, []uint32{0})
	out = sections.EncodeMemorySection(out, 1, -1)
	out = sections.EncodeExportSection(out,
		[]string{"main"},
		[]byte{sections.ExportFunc},
		[]uint32{0})

	// Body:
	//   i32.const 0 ; i64.load8_u 0 0      -> i64(1)
	//   i32.const 1 ; i64.load8_u 0 0      -> i64(2)
	//   i64.add                            -> i64(3)
	//   i32.const 2 ; i64.load8_u 0 0      -> i64(3)
	//   i64.add                            -> i64(6)
	//   i32.const 3 ; i64.load8_u 0 0      -> i64(4)
	//   i64.add                            -> i64(10)
	//   i32.wrap_i64                       -> i32(10)
	var body []byte
	body = inst.InstI32Const(body, 0)
	body = mem.InstI64Load8U(body, 0, 0)
	body = inst.InstI32Const(body, 1)
	body = mem.InstI64Load8U(body, 0, 0)
	body = num.InstI64Add(body)
	body = inst.InstI32Const(body, 2)
	body = mem.InstI64Load8U(body, 0, 0)
	body = num.InstI64Add(body)
	body = inst.InstI32Const(body, 3)
	body = mem.InstI64Load8U(body, 0, 0)
	body = num.InstI64Add(body)
	body = conv.InstI32WrapI64(body)

	fn := inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
	out = sections.EncodeCodeSection(out, [][]byte{fn})
	// Data section (id 11) must come after code (id 10) —
	// sections in a wasm module are required to be in id order.
	out = sections.EncodeDataSection(out,
		[]int32{0}, [][]byte{{1, 2, 3, 4}})

	stdout := runWasmModule(t, out)
	if strings.TrimSpace(stdout) != "10" {
		t.Fatalf("wasmtime stdout = %q, want %q (sum of [1,2,3,4])", stdout, "10")
	}
}

// TestWASMModuleBuildCross — build a module with both Go
// module.Build and Lang std/wasm/module.build, identical
// inputs, assert the produced bytes are byte-for-byte
// identical. This is the strongest lock-step guarantee: any
// drift between the two implementations on any opcode /
// section / lebon any path fires here.
func TestWASMModuleBuildCross(t *testing.T) {
	maybeSkipNoWasmtime := func() {
		if _, err := exec.LookPath("wasmtime"); err != nil {
			t.Skip("wasmtime not on PATH")
		}
	}
	maybeSkipNoWasmtime()

	// Go side: build the "main: () -> 42" module via the
	// top-level module.Build entry point.
	m := mod.New()
	m.TypeParams = [][]byte{nil}
	m.TypeResults = [][]byte{{encode.ValtypeI32}}
	m.FunctionTypeidxs = []uint32{0}
	m.ExportNames = []string{"main"}
	m.ExportKinds = []byte{sections.ExportFunc}
	m.ExportIdxs = []uint32{0}
	body := inst.InstI32Const(nil, 42)
	m.CodeBodies = [][]byte{inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)}
	goBytes := mod.Build(m)

	// Lang side: run the equivalent program through wasmtime
	// and have it print the module bytes.
	langSrc := `import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/encode";
import "std/wasm/sections";
import "std/i32";
import "std/string";
function main(): i32 {
    var m: module.Module = module.module_new();
    var p0: u8[] = [];
    var r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);
    var body: u8[] = inst.inst_i32_const([], 42);
    var fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), body);
    m = m.with_code([fn]);
    var bytes: u8[] = module.build(m);

    var output: string = "";
    var i: i32 = 0;
    while (i < bytes.len()) {
        if (i > 0) { output = output + " "; }
        output = output + (bytes[i] as i32).to_string();
        i = i + 1;
    }
    print(output);
    return 0;
}`
	langBytes := langProducedModuleBytes(t, langSrc)

	if !bytes.Equal(goBytes, langBytes) {
		t.Fatalf("Go and Lang module bytes differ:\n  Go (% 4d): % x\n  Lang (% 2d): % x",
			len(goBytes), goBytes, len(langBytes), langBytes)
	}
}

// Refcount builtins (`__rc_get` / `__rc_inc` / `__rc_dec`)
// exposed for Phase-1 testing. Validates Phase 1a (rc=1 on
// `__alloc_u8`) and Phase 1b (inc / dec are sentinel-aware and
// don't corrupt the rc word). main returns 0 iff the observed
// rc progression is exactly 1 → 2 → 1.
func TestWASMRcBuiltins(t *testing.T) {
	src := `function main(): i32 {
    var arr: u8[] = __alloc_u8(10);
    var r1: i32 = __rc_get(arr);
    __rc_inc(arr);
    var r2: i32 = __rc_get(arr);
    __rc_dec(arr);
    var r3: i32 = __rc_get(arr);
    return r1 + r2 + r3 - 4;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (rc progression off)", got)
	}
}

// Phase 1d transfer inc, refined by #4402 opt 1 (dead-alias dup/drop
// cancellation): a pure borrowed-view alias — never reassigned, never
// returned, never moved — elides its inc AND its exit-sweep dec as a
// net-zero pair, so the rc stays 1. An alias that is still referenced
// under the return keeps the ordinary transfer inc (rc 2).
func TestWASMRcAliasInc(t *testing.T) {
	dead := `function main(): i32 {
    var arr: u8[] = __alloc_u8(8);
    var alias: u8[] = arr;
    return __rc_get(arr) - 1;
}`
	if got := runWasm(t, dead); got != 0 {
		t.Errorf("dead alias: got exit %d, want 0 (borrowed view elides the inc — rc stays 1)", got)
	}
	live := `function main(): i32 {
    var arr: u8[] = __alloc_u8(8);
    var alias: u8[] = arr;
    return __rc_get(arr) - 2 + alias.len() - 8;
}`
	if got := runWasm(t, live); got != 0 {
		t.Errorf("returned alias: got exit %d, want 0 (transfer inc kept — rc 2)", got)
	}
}

// Phase 1d-ii (+ Phase 1d-viii): FieldAccess + Index alias
// reads inc the rc; with Phase 1d-viii, the struct- / array-
// lit constructor also inc's the captured array. End state:
// rc=3 after alloc (1) + lit store (1) + alias read (1).
func TestWASMRcAliasIncFieldAndIndex(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
	}{
		{"field_access", `struct Holder { items: u8[] }
function main(): i32 {
    var inner: u8[] = __alloc_u8(8);
    var h: Holder = Holder { items: inner };
    var alias: u8[] = h.items;
    // Precise drops (RC-Perceus) release the now-dead struct h AND the
    // now-dead alias at their last use; reference both in the return so they
    // stay live through the check — this measures the fully-aliased rc
    // (inner + h.items + alias). Both .len()-8 terms are 0, so the result is
    // unchanged.
    return __rc_get(inner) - 3 + h.items.len() - 8 + alias.len() - 8;
}`},
		{"index_load", `function main(): i32 {
    var inner: u8[] = __alloc_u8(8);
    var matrix: u8[][] = [inner];
    var alias: u8[] = matrix[0];
    // Precise drops (RC-Perceus) release the now-dead matrix at its last
    // use; reference it in the return so it stays live through the check —
    // this measures the fully-aliased rc (inner + the array element + alias),
    // which is what the test asserts. matrix.len()-1 == 0, so the result is
    // unchanged.
    return __rc_get(inner) - 3 + matrix.len() - 1;
}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := runWasm(t, c.src); got != 0 {
				t.Errorf("got exit %d, want 0 (lit + alias should bump rc to 3)", got)
			}
		})
	}
}

// Phase 1d-iii: `y = x;` reassignment bumps the rc on x.
func TestWASMRcAliasIncReassign(t *testing.T) {
	src := `function main(): i32 {
    var arr: u8[] = __alloc_u8(8);
    var other: u8[] = __alloc_u8(8);
    other = arr;
    return __rc_get(arr) - 2;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (assign-alias should bump rc to 2)", got)
	}
}

// Phase 2d-borrow: passing an array to a function is a borrow —
// no caller-side inc, no callee-side exit dec. The rc is
// untouched across the call and stays at 1.
func TestWASMRcAliasIncCallArg(t *testing.T) {
	src := `function f(arr: u8[]): i32 { return 0; }
function main(): i32 {
    var arr: u8[] = __alloc_u8(8);
    var _: i32 = f(arr);
    return __rc_get(arr) - 1;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (borrowed arg: rc stays 1)", got)
	}
}

// Phase 3 step 1: rc-underflow detector. __fern_rc_dec bumps a
// counter (read back via __rc_underflow_count) whenever it is
// asked to decrement an rc that is already <= 0 — an over-release
// that, once Phase 3 turns on reclamation, becomes a
// use-after-free. WASM-only probe (the counter is a linear-memory
// slot). This test pins the detector's two contracts:
//   - a program with no over-release reports 0 (no false
//     positives from healthy 1->0 decs or sentinel statics), and
//   - a deliberate double-dec (1->0 healthy, then 0-> under) is
//     caught and counted exactly once.
func TestWASMRcUnderflowDetector(t *testing.T) {
	clean := `
import "core/map";
function main(): i32 {
    var m: Map[string, i32] = map_new(8);
    m = m.insert("a", 1);
    m = m.insert("b", 2);
    var xs: i32[] = [1, 2, 3];
    return __rc_underflow_count() + m.get_or("a", 0) - 1 + xs[0] - 1;
}`
	if got := runWasm(t, clean); got != 0 {
		t.Errorf("clean program: got %d, want 0 (no over-release expected)", got)
	}

	overRelease := `function main(): i32 {
    var a: u8[] = __alloc_u8(8);   // rc = 1
    __rc_dec(a);                   // 1 -> 0 (healthy)
    __rc_dec(a);                   // 0 -> -1 (over-release, counted)
    return __rc_underflow_count();
}`
	if got := runWasm(t, overRelease); got != 1 {
		t.Errorf("double-dec: got underflow count %d, want 1", got)
	}
}

// Phase 3 step 2: the idiomatic value-returning map mutation
// `m = m.insert(...)` / `m = m.cleared()` must NOT over-release. The
// mutators cow in place without bumping rc, so b.assign uses a
// cow-aware dec (isSelfMapMutation): dec the old handle only when
// the call returned a different one. On a uniquely-held map the
// handle is unchanged → no dec → no 1->0 then 0->-1 over-release.
func TestWASMRcMapSelfAssignNoUnderflow(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m: Map[string, i32] = map_new(8);
    m = m.insert("a", 1);
    m = m.insert("b", 2);
    m = m.insert("c", 3);
    m = m.insert("a", 10);   // overwrite existing key
    m = m.cleared();
    m = m.insert("d", 4);
    // underflow must be 0; map must read back correctly.
    return __rc_underflow_count() * 1000
         + (m.len() - 1)        // clear left only "d"
         + (m.get_or("d", 0) - 4);
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (self-assign map mutation: 0 underflow, correct contents)", got)
	}

	// Aliased self-assign: the cow-aware dec must fire on the COPY
	// path (old handle differs from the fresh copy) so the source
	// alias's rc is released rather than leaked — and still no
	// over-release. m1 keeps its entry, m2 gets the mutated copy.
	aliased := `
import "core/map";
function main(): i32 {
    var m1: Map[string, i32] = map_new(8);
    m1 = m1.insert("a", 1);
    var m2 = m1;                  // alias → rc 2
    m2 = m2.insert("a", 999);        // copy; old handle released, not leaked
    return __rc_underflow_count() * 1000
         + (m1.get_or("a", 0) - 1)
         + (m2.get_or("a", 0) - 999);
}`
	if got := runWasm(t, aliased); got != 0 {
		t.Errorf("got %d, want 0 (aliased self-assign: 0 underflow, m1=1, m2=999)", got)
	}
}

// Phase 3 step 3: drop handlers. An array of pointer-shaped
// rc-tracked elements (here u8[][]) routes its scope-exit dec
// through __fern_drop_arr_ptr, which on the last reference dec's
// each element — balancing the per-element inc the IR emits at
// array-literal construction (Phase 1d-viii). Without the drop,
// the nested element's rc leaks.
func TestWASMRcDropArrayElements(t *testing.T) {
	// Proof the drop FIRES: `consume` nests `inner` into a local
	// array and drops it on exit; inner's rc must return to its
	// pre-call value (1), not stay at the constructed 2.
	fires := `function consume(inner: u8[]): i32 {
    var outer: u8[][] = [inner];
    return 0;
}
function main(): i32 {
    var inner: u8[] = __alloc_u8(4);
    var before: i32 = __rc_get(inner);
    var ignore: i32 = consume(inner);
    var after: i32 = __rc_get(inner);
    return (before - 1) + (after - 1);  // 0 iff drop balanced the inc
}`
	if got := runWasm(t, fires); got != 0 {
		t.Errorf("got %d, want 0 (drop must dec the nested element back to rc 1)", got)
	}

	// And no over-release: nesting fresh + aliased elements and
	// dropping the outer array reports 0 underflows.
	noUnder := `function build(): i32 {
    var inner: i32[] = [1, 2, 3];
    var a: i32[][] = [inner];        // aliased element (inc'd)
    var b: i32[][] = [[4, 5], [6]];  // fresh elements (not inc'd)
    return a[0][1] + b[1][0];        // 2 + 6
}
function main(): i32 {
    return (build() - 8) + __rc_underflow_count();
}`
	if got := runWasm(t, noUnder); got != 0 {
		t.Errorf("got %d, want 0 (nested-array drop: correct values, 0 underflow)", got)
	}
}

// Phase 3 step 3: struct drop handlers. A user struct with
// pointer-shaped rc-tracked fields drops those fields on its last
// reference (gated by __fern_rc_is_unique) before dec'ing the box,
// balancing the per-field inc from Phase 1e-struct-ii. Mirrors
// TestX86_64RcDropStructFields / TestArm64RcDropStructFields.
func TestWASMRcDropStructFields(t *testing.T) {
	fires := `struct Holder { items: u8[] }
function consume(inner: u8[]): i32 {
    var h: Holder = Holder { items: inner };
    return 0;
}
function main(): i32 {
    var inner: u8[] = __alloc_u8(4);
    var before: i32 = __rc_get(inner);
    var ignore: i32 = consume(inner);
    var after: i32 = __rc_get(inner);
    return (before - 1) + (after - 1) + __rc_underflow_count();
}`
	if got := runWasm(t, fires); got != 0 {
		t.Errorf("got %d, want 0 (struct field drop must dec the array back to rc 1)", got)
	}

	aliased := `struct Holder { items: i32[] }
function main(): i32 {
    var inner: i32[] = [1, 2, 3];
    var h1: Holder = Holder { items: inner };
    var h2: Holder = h1;
    return h2.items[2] + __rc_underflow_count() - 3;
}`
	if got := runWasm(t, aliased); got != 0 {
		t.Errorf("got %d, want 0 (aliased struct: no double field-drop, 0 underflow)", got)
	}

	nested := `struct Grid { rows: i32[][] }
struct Inner { v: i32[] }
struct Outer { inner: Inner }
function build(): i32 {
    var a: i32[] = [1, 2, 3];
    var g: Grid = Grid { rows: [a] };
    var arr: i32[] = [7, 8];
    var o: Outer = Outer { inner: Inner { v: arr } };
    return g.rows[0][1] + o.inner.v[0] + __rc_underflow_count();
}
function main(): i32 {
    return build() - 9;
}`
	if got := runWasm(t, nested); got != 0 {
		t.Errorf("got %d, want 0 (nested struct/array fields: correct values, 0 underflow)", got)
	}
}

// Phase 3 step 3: enum drop handlers (uniform case). Mirrors
// TestX86_64RcDropEnumPayload / TestArm64RcDropEnumPayload. A
// heap-boxed enum whose payload-carrying variants share an
// identical droppable signature (e.g. a union of structs) drops
// the payloads on its last reference before dec'ing the box.
func TestWASMRcDropEnumPayload(t *testing.T) {
	fresh := `struct W { a: i32[] }
struct V2 { b: i32 }
type U = W | V2;
function mk(): U { return W { a: [1, 2, 3] }; }
function build(): i32 {
    var u: U = mk();
    match (u) { W(w) => { return w.a[1] + __rc_underflow_count(); }, V2(x) => { return x.b; } }
    return 0 - 1;
}
function main(): i32 { return build() - 2; }`
	if got := runWasm(t, fresh); got != 0 {
		t.Errorf("got %d, want 0 (union payload drop: value 2, 0 underflow)", got)
	}

	aliased := `struct W { a: i32 }
struct V2 { b: i32 }
type U = W | V2;
function main(): i32 {
    var w: W = W { a: 7 };
    var u: U = w;
    return w.a + __rc_underflow_count() - 7;
}`
	if got := runWasm(t, aliased); got != 0 {
		t.Errorf("got %d, want 0 (aliased struct widened to union: 0 underflow)", got)
	}

	nonUniform := `enum E { Arr(i32[]), Num(i32) }
function main(): i32 {
    var e: E = Arr([1, 2, 3]);
    var f: E = Num(9);
    match (e) {
        Arr(a) => { return a.len() + __rc_underflow_count() - 3; },
        Num(_) => { return 0 - 1; }
    }
    return 0 - 2;
}`
	if got := runWasm(t, nonUniform); got != 0 {
		t.Errorf("got %d, want 0 (non-uniform enum: correct value, 0 underflow)", got)
	}
}

// Phase 1d-vi: dec on overwrite. See TestArm64RcDecOnOverwrite
// for the trace.
func TestWASMRcDecOnOverwrite(t *testing.T) {
	src := `function main(): i32 {
    var arr1: u8[] = __alloc_u8(8);
    var arr2: u8[] = __alloc_u8(8);
    var arr3: u8[] = __alloc_u8(8);
    arr1 = arr2;
    arr1 = arr3;
    return __rc_get(arr2) + __rc_get(arr3) - 3;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (arr2 rc=1, arr3 rc=2, sum=3)", got)
	}
}

// Phase 2: arr.push mutates in place when rc==1 and cap > len.
// First push of [10, 20] copies (cap=2=len, no spare); the
// copy bumps cap to max(2*newLen, 4) = 6, so the second push
// hits the fast path and returns the same pointer.
func TestWASMArrayPushInPlaceFastPath(t *testing.T) {
	src := `function main(): i32 {
    var xs: i32[] = [10, 20];
    xs = xs.append(30);
    var addr_before: usize = xs as usize;
    xs = xs.append(40);
    var addr_after: usize = xs as usize;
    if (addr_before != addr_after) { return 1; }
    if (xs.len() != 4) { return 2; }
    if (xs[3] != 40) { return 3; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (in-place fast path should reuse buffer)", got)
	}
}

// Phase 2: aliased rc>1 forces copy semantics even with spare
// cap — otherwise the other holder's view of the array would
// silently extend.
func TestWASMArrayPushAliasedCopies(t *testing.T) {
	src := `function main(): i32 {
    var xs: i32[] = [10, 20];
    xs = xs.append(30);
    var ys = xs;
    ys = ys.append(40);
    if (xs.len() != 3) { return 1; }
    if (xs[0] != 10) { return 2; }
    if (ys.len() != 4) { return 3; }
    if (ys[3] != 40) { return 4; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (aliased push must copy)", got)
	}
}

// Mirror of TestArm64ArrayIndexSetInPlaceFastPath.
func TestWASMArrayIndexSetInPlaceFastPath(t *testing.T) {
	src := `function main(): i32 {
    var xs: i32[] = [10, 20, 30];
    var addr_before: usize = xs as usize;
    xs = xs.with(1, 999);
    var addr_after: usize = xs as usize;
    if (addr_before != addr_after) { return 1; }
    if (xs[1] != 999) { return 2; }
    if (xs[0] != 10) { return 3; }
    if (xs[2] != 30) { return 4; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (arr[i]=v in-place when rc==1)", got)
	}
}

// Mirror of TestArm64ArrayIndexSetAliasedCopies.
func TestWASMArrayIndexSetAliasedCopies(t *testing.T) {
	src := `function main(): i32 {
    var xs: i32[] = [10, 20, 30];
    var ys = xs;
    ys = ys.with(0, 999);
    if (xs[0] != 10) { return 1; }
    if (xs[1] != 20) { return 2; }
    if (xs[2] != 30) { return 3; }
    if (ys[0] != 999) { return 4; }
    if (ys[1] != 20) { return 5; }
    if (ys[2] != 30) { return 6; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (aliased arr[i]=v must copy)", got)
	}
}

// Mirror of TestArm64ArrayIndexSetU8Stride. Specifically
// regression-tests the wasm raw-_start path where the
// __fern_rc_dec low-address guard would otherwise short-
// circuit and break the pre-helper bump-then-dec design.
func TestWASMArrayIndexSetU8Stride(t *testing.T) {
	src := `function main(): i32 {
    var buf: u8[] = __alloc_u8(4);
    buf = buf.with(0, 65 as u8);
    buf = buf.with(1, 66 as u8);
    buf = buf.with(2, 67 as u8);
    buf = buf.with(3, 68 as u8);
    return (buf[0] as i32) + (buf[1] as i32) + (buf[2] as i32) + (buf[3] as i32) - 266;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (u8 arr[i]=v preserves all writes)", got)
	}
}

// Mirror of TestArm64ArrayIndexSetStructField.
func TestWASMArrayIndexSetStructField(t *testing.T) {
	src := `struct State { items: i32[] }
function main(): i32 {
    var s: State = State{items: [10, 20, 30]};
    s = State { ...s, items: s.items.with(1, 999) };
    if (s.items[0] != 10) { return 1; }
    if (s.items[1] != 999) { return 2; }
    if (s.items[2] != 30) { return 3; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (struct.field[i]=v in-place when rc==1)", got)
	}
}

// Mirror of TestArm64ArrayIndexSetStructFieldAliasedCopies.
func TestWASMArrayIndexSetStructFieldAliasedCopies(t *testing.T) {
	src := `struct State { items: i32[] }
function main(): i32 {
    var arr: i32[] = [10, 20, 30];
    var s: State = State{items: arr};
    s = State { ...s, items: s.items.with(1, 999) };
    if (arr[0] != 10) { return 1; }
    if (arr[1] != 20) { return 2; }
    if (arr[2] != 30) { return 3; }
    if (s.items[0] != 10) { return 4; }
    if (s.items[1] != 999) { return 5; }
    if (s.items[2] != 30) { return 6; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (aliased struct.field[i]=v must copy)", got)
	}
}

// Mirror of TestArm64ArrayIndexSetNestedStructField.
func TestWASMArrayIndexSetNestedStructField(t *testing.T) {
	src := `struct Inner { items: i32[] }
struct Outer { inner: Inner }
function main(): i32 {
    var o: Outer = Outer{inner: Inner{items: [10, 20, 30]}};
    o = Outer { ...o, inner: Inner { ...o.inner, items: o.inner.items.with(1, 999) } };
    if (o.inner.items[0] != 10) { return 1; }
    if (o.inner.items[1] != 999) { return 2; }
    if (o.inner.items[2] != 30) { return 3; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (a.b.field[i]=v in-place when rc==1)", got)
	}
}

// Mirror of TestArm64ArrayIndexSetNestedStructFieldAliasedCopies.
func TestWASMArrayIndexSetNestedStructFieldAliasedCopies(t *testing.T) {
	src := `struct Inner { items: i32[] }
struct Outer { inner: Inner }
function main(): i32 {
    var arr: i32[] = [10, 20, 30];
    var o: Outer = Outer{inner: Inner{items: arr}};
    o = Outer { ...o, inner: Inner { ...o.inner, items: o.inner.items.with(1, 999) } };
    if (arr[1] != 20) { return 1; }
    if (o.inner.items[1] != 999) { return 2; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (aliased a.b.field[i]=v must copy)", got)
	}
}

// Mirror of TestArm64ArrayIndexSetMat.
func TestWASMArrayIndexSetMat(t *testing.T) {
	src := `function main(): i32 {
    var mat: i32[][] = [[1, 2, 3], [4, 5, 6]];
    mat = mat.with(0, mat[0].with(1, 999));
    if (mat[0][0] != 1) { return 1; }
    if (mat[0][1] != 999) { return 2; }
    if (mat[0][2] != 3) { return 3; }
    if (mat[1][0] != 4) { return 4; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (mat[i][j]=v in-place when inner rc==1)", got)
	}
}

// Mirror of TestArm64ArrayIndexSetMatInnerAliasedCopies.
func TestWASMArrayIndexSetMatInnerAliasedCopies(t *testing.T) {
	src := `function main(): i32 {
    var mat: i32[][] = [[1, 2], [3, 4]];
    var inner = mat[0];
    mat = mat.with(0, mat[0].with(1, 999));
    if (inner[1] != 2) { return 1; }
    if (mat[0][1] != 999) { return 2; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (mat[i][j]=v with aliased inner must copy)", got)
	}
}

// Mirror of TestArm64MapSetReturnsMap.
func TestWASMMapSetReturnsMap(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m: Map[string, i32] = map_new(8);
    m = m.insert("a", 1);
    m = m.insert("b", 2);
    m = m.insert("c", 3);
    if (m.get_or("a", 0) != 1) { return 1; }
    if (m.get_or("b", 0) != 2) { return 2; }
    if (m.get_or("c", 0) != 3) { return 3; }
    if (m.len() != 3) { return 4; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (Map.set returns Map)", got)
	}
}

// Mirror of TestArm64ArrayIndexSetObjMatInnerAliasedCopies.
func TestWASMArrayIndexSetObjMatInnerAliasedCopies(t *testing.T) {
	src := `struct State { mat: i32[][] }
function main(): i32 {
    var inner: i32[] = [1, 2, 3];
    var s: State = State{mat: [inner, [4, 5, 6]]};
    s = State { ...s, mat: s.mat.with(0, s.mat[0].with(1, 999)) };
    if (inner[1] != 2) { return 1; }
    if (s.mat[0][1] != 999) { return 2; }
    if (s.mat[0][0] != 1) { return 3; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (obj.mat[i][j]=v with shared inner must copy)", got)
	}
}

// Mirror of TestArm64ArraySetSelfAssign.
func TestWASMArraySetSelfAssign(t *testing.T) {
	src := `function main(): i32 {
    var xs: i32[] = [10, 20, 30];
    xs = xs.with(1, 999);
    if (xs[0] != 10) { return 1; }
    if (xs[1] != 999) { return 2; }
    if (xs[2] != 30) { return 3; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (xs = xs.with(1, 999))", got)
	}
}

// Mirror of TestArm64ArraySetAliasedCopies.
func TestWASMArraySetAliasedCopies(t *testing.T) {
	src := `function main(): i32 {
    var xs: i32[] = [10, 20, 30];
    var ys = xs;
    ys = ys.with(0, 999);
    if (xs[0] != 10) { return 1; }
    if (ys[0] != 999) { return 2; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (aliased arr.with must copy)", got)
	}
}

// Mirror of TestArm64MapDeleteReturnsMapBool.
func TestWASMMapDeleteReturnsMapBool(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m: Map[string, i32] = map_new(8);
    m = m.insert("a", 1);
    m = m.insert("b", 2);
    m = m.insert("c", 3);
    if (!m.without("b").1) { return 1; }
    if (m.without("z").1)  { return 2; }
    var (m2, ok) = m.without("a");
    if (!ok) { return 3; }
    if (m2.has("a")) { return 4; }
    if (!m2.has("c")) { return 5; }
    if (m2.len() != 1) { return 6; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (Map.delete returns (Map, bool))", got)
	}
}

// Mirror of TestArm64MapClearReturnsMap.
func TestWASMMapClearReturnsMap(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m: Map[string, i32] = map_new(8);
    m = m.insert("x", 10);
    m = m.insert("y", 20);
    if (m.len() != 2) { return 1; }
    m = m.cleared();
    if (m.len() != 0) { return 2; }
    if (m.has("x")) { return 3; }
    m = m.insert("z", 99);
    if (m.len() != 1) { return 4; }
    if (m.get_or("z", 0) != 99) { return 5; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (Map.clear returns Map)", got)
	}
}

// Phase 2d: Map.set copy-on-write — wasm sibling of
// TestX86_64MapSetAliasedCopies. An aliased map (var m2 = m1)
// has rc=2, so m2.insert(...) copies and leaves m1 intact.
func TestWASMMapSetAliasedCopies(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m1: Map[string, i32] = map_new(8);
    m1 = m1.insert("a", 1);                 // in-place (rc==1)
    var m2 = m1;                    // alias → rc=2
    m2 = m2.insert("a", 999);          // rc>1 → copy; m1 unchanged
    if (m1.get_or("a", 0) != 1)   { return 1; }
    if (m2.get_or("a", 0) != 999) { return 2; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (aliased Map.set must copy)", got)
	}
}

// Phase 2d: Map.delete / Map.clear copy-on-write — wasm sibling
// of TestX86_64MapDeleteClearAliasedCopies.
func TestWASMMapDeleteClearAliasedCopies(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m1: Map[string, i32] = map_new(8);
    m1 = m1.insert("a", 1);
    m1 = m1.insert("b", 2);
    var m2 = m1;                       // alias → rc=2
    var (m3, ok) = m2.without("a");     // rc>1 → copy; m1/m2 intact
    if (!ok)            { return 1; }
    if (m1.len() != 2)  { return 2; }
    if (!m1.has("a"))   { return 3; }
    if (m3.len() != 1)  { return 4; }
    if (m3.has("a"))    { return 5; }
    var m4 = m1;                       // alias → rc=2
    m4 = m4.cleared();                   // rc>1 → copy; m1 intact
    if (m1.len() != 2)  { return 6; }
    if (m4.len() != 0)  { return 7; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (aliased Map.delete/clear must copy)", got)
	}
}

// Mirror of TestArm64TupleStructElem.
func TestWASMTupleStructElem(t *testing.T) {
	src := `struct Inner { x: i32, y: i32 }
function main(): i32 {
    var t: (i32, Inner) = (1, Inner { x: 2, y: 3 });
    if (t.0 != 1) { return 1; }
    var inner: Inner = t.1;
    if (inner.x != 2) { return 2; }
    if (inner.y != 3) { return 3; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (struct in tuple slot)", got)
	}
}

// Mirror of TestArm64TupleArrayElem.
func TestWASMTupleArrayElem(t *testing.T) {
	src := `function main(): i32 {
    var t: (i32, i32[]) = (1, [10, 20, 30]);
    if (t.0 != 1) { return 1; }
    var arr: i32[] = t.1;
    if (arr.len() != 3) { return 2; }
    if (arr[0] != 10) { return 3; }
    if (arr[2] != 30) { return 4; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (array in tuple slot)", got)
	}
}

// Mirror of TestArm64TupleNestedTuple.
func TestWASMTupleNestedTuple(t *testing.T) {
	src := `function main(): i32 {
    var t: (i32, (i32, i32)) = (1, (2, 3));
    var (a, b) = t;
    if (a != 1) { return 1; }
    var (c, d) = b;
    if (c != 2) { return 2; }
    if (d != 3) { return 3; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (nested tuple destructure)", got)
	}
}

// Mirror of TestArm64LexerChainedTupleNumericAccess.
func TestWASMLexerChainedTupleNumericAccess(t *testing.T) {
	src := `function main(): i32 {
    var t: (i32, (i32, i32)) = (1, (2, 3));
    if (t.0 != 1) { return 1; }
    if (t.1.0 != 2) { return 2; }
    if (t.1.1 != 3) { return 3; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (chained t.1.0 numeric access)", got)
	}
}

// Mirror of TestArm64EmptyMapDestinationInference.
func TestWASMEmptyMapDestinationInference(t *testing.T) {
	src := `
import "core/map";
function take(m: Map[string, i32]): i32 { return m.len(); }
function mkEmpty(): Map[i32, string] { return Map {}; }
function main(): i32 {
    var a: Map[string, i32] = Map {};
    if (a.len() != 0) { return 1; }
    a = a.insert("k", 42);
    if (a.get_or("k", 0) != 42) { return 2; }
    var b: Map[i32, string] = Map {};
    if (b.len() != 0) { return 3; }
    b = b.insert(7, "hello");
    if (!b.has(7)) { return 4; }
    if (take(Map {}) != 0) { return 5; }
    var r = mkEmpty();
    if (r.len() != 0) { return 6; }
    var d: Map[i32, i32] = Map {};
    if (d.len() != 0) { return 7; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (empty Map literal inherits K/V from destination)", got)
	}
}

// Mirror of TestArm64EnumVariantInTuple.
func TestWASMEnumVariantInTuple(t *testing.T) {
	src := `enum Color { Red, Green, Blue }
function main(): i32 {
    var t: (i32, Color) = (1, Green);
    if (t.0 != 1) { return 1; }
    match (t.1) {
        Red => { return 2; },
        Green => { },
        Blue => { return 3; }
    }
    var u: (i32, Option[i32]) = (5, Some(42));
    if (u.0 != 5) { return 4; }
    match (u.1) {
        Some(v) => { if (v != 42) { return 5; } },
        None => { return 6; }
    }
    var w: (Color, i32) = (Blue, 99);
    if (w.1 != 99) { return 7; }
    match (w.0) {
        Blue => { return 0; },
        _ => { return 8; }
    }
    return 9;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (enum variant in tuple slot)", got)
	}
}

// Mirror of TestArm64MapPointerShapedValues.
func TestWASMMapPointerShapedValues(t *testing.T) {
	src := `
import "core/map";
struct P { x: i32, y: i32 }
function main(): i32 {
    var mt: Map[string, (i32, i32)] = Map {};
    mt = mt.insert("a", (3, 4));
    match (mt.get("a")) {
        Some(p) => { if (p.0 + p.1 != 7) { return 1; } },
        None => { return 2; }
    }
    var ms: Map[string, P] = Map {};
    ms = ms.insert("a", P { x: 3, y: 4 });
    match (ms.get("a")) {
        Some(s) => { if (s.x + s.y != 7) { return 3; } },
        None => { return 4; }
    }
    var ma: Map[i32, i32[]] = Map {};
    ma = ma.insert(1, [10, 20, 30]);
    match (ma.get(1)) {
        Some(arr) => { if (arr[0] + arr[2] != 40) { return 5; } },
        None => { return 6; }
    }
    var mi: Map[string, i32] = Map {};
    mi = mi.insert("a", 42);
    match (mi.get("a")) {
        Some(v) => { if (v != 42) { return 7; } },
        None => { return 8; }
    }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (Map pointer-shaped values)", got)
	}
}

// Mirror of TestArm64StructTupleFieldAccess.
func TestWASMStructTupleFieldAccess(t *testing.T) {
	src := `struct Rec { pos: (i32, i32), name: string }
struct Nested { t: (i32, (i32, i32)) }
function main(): i32 {
    var r: Rec = Rec { pos: (3, 4), name: "p" };
    if (r.pos.0 != 3) { return 1; }
    if (r.pos.1 != 4) { return 2; }
    var n: Nested = Nested { t: (1, (2, 3)) };
    if (n.t.0 != 1) { return 3; }
    if (n.t.1.0 != 2) { return 4; }
    if (n.t.1.1 != 3) { return 5; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (struct tuple-field chained access)", got)
	}
}

// Mirror of TestArm64UnsignedComparison (regression guard).
func TestWASMUnsignedComparison(t *testing.T) {
	src := `function main(): i32 {
    var big: u32 = 4294967295u32;
    if (!(big > 0u32)) { return 1; }
    if (!(big > 1000000u32)) { return 2; }
    if (big < 5u32) { return 3; }
    if (!(big >= 4294967295u32)) { return 4; }
    if (big <= 100u32) { return 5; }
    var b64: u64 = 18446744073709551615u64;
    if (!(b64 > 9u64)) { return 6; }
    if (b64 < 9u64) { return 7; }
    var u: u8 = 200u8;
    if (!(u > 100u8)) { return 8; }
    var i: u32 = 4294967293u32;
    var c: i32 = 0;
    while (i > 4294967290u32) { c = c + 1; i = i - 1u32; }
    if (c != 3) { return 9; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (unsigned comparison condition codes)", got)
	}
}

// Mirror of TestArm64UnaryMinusWideTypes.
func TestWASMUnaryMinusWideTypes(t *testing.T) {
	src := `function main(): i32 {
    var a: i64 = -5i64;
    if (a != 0i64 - 5i64) { return 1; }
    var b: f64 = -5.0;
    if (!(b < 0.0)) { return 2; }
    var c: f64 = -b;
    if (c != 5.0) { return 3; }
    var f: f32 = -2.5f32;
    if (!(f < 0.0f32)) { return 4; }
    var z: f64 = -0.0;
    if (f64_bits(z) == 0i64) { return 5; }
    var g: i64 = 10i64 + -3i64;
    if (g != 7i64) { return 6; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (unary minus on wide / non-i32 types)", got)
	}
}

// Mirror of TestArm64ScientificNotation.
func TestWASMScientificNotation(t *testing.T) {
	src := `function main(): i32 {
    var a: f64 = 1e3;
    if (a != 1000.0) { return 1; }
    var b: f64 = 1.5e3;
    if (b != 1500.0) { return 2; }
    var c: f64 = 1500.0e-3;
    if (c != 1.5) { return 3; }
    var d: f64 = 1.5e+3;
    if (d != 1500.0) { return 4; }
    var e: f64 = 2.5E2;
    if (e != 250.0) { return 5; }
    var f: f32 = 1.5e2f32;
    if (f != 150.0f32) { return 6; }
    var big: f64 = 1.8e19;
    if (!(big > 1.7e19)) { return 7; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (scientific-notation float literals)", got)
	}
}

// Mirror of TestArm64SubI32ArithmeticWraps.
func TestWASMSubI32ArithmeticWraps(t *testing.T) {
	src := `struct S { v: u8 }
function main(): i32 {
    var a: u8 = 255u8;
    a = a + 1u8;
    if ((a as i32) != 0) { return 1; }
    var b: u8 = 0u8;
    b = b - 1u8;
    if ((b as i32) != 255) { return 2; }
    var c: u8 = 16u8;
    c = c * 16u8;
    if ((c as i32) != 0) { return 3; }
    var s: S = S { v: 200u8 };
    var h: u8 = s.v + 100u8;
    if ((h as i32) != 44) { return 4; }
    var k: u8 = 100u8;
    k = k + 50u8;
    if ((k as i32) != 150) { return 5; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (sub-i32 arithmetic wraps to width)", got)
	}
}

// TestWASMWideKeyMapDispatch pins the wasmbin closure-target local
// indexing (#5042 follow-on). core/map's default hash adapters are
// NO-CAPTURE named functions referenced as function VALUES by the
// builtin-key dispatchers; that makes them closure targets, which
// appends an env_ptr param to their wasm type — but closureconv never
// adds their `__env` IR param (nothing to capture; the reference is in
// stdlib runtime code), so the env slot sits between the declared
// params and the body locals and every body-local index must shift by
// one (bodySlotIdx). The bug was latent while such functions had no
// body locals; __map_hash_wide's i64 local turned it into a module-wide
// validation error ("expected i32, found i64") — ANY wasm module
// importing core/map failed to instantiate. The i64-keyed map here
// both forces the module to validate and exercises the wide adapter's
// shifted locals at runtime (wrong indexing would mis-hash and miss).
func TestWASMWideKeyMapDispatch(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m: Map[i64, i32] = map_new(4);
    var big: i64 = (7 as i64) << (33 as i64);
    m = m.insert(big, 40);
    m = m.insert(3 as i64, 2);
    if (m.get_or(big, 0) != 40) { return 1; }
    if (m.get_or(3 as i64, 0) != 2) { return 2; }
    if (m.get_or(9 as i64, 7) != 7) { return 3; }
    return 0;
}
`
	if code := runWasm(t, src); code != 0 {
		t.Errorf("wide-key map dispatch: exit = %d, want 0", code)
	}
}
