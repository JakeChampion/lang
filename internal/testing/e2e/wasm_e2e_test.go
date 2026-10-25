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

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/syntax/parser"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
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

var (
	preview2Once sync.Once
	preview2Err  error
)

const wasmtimePin = "46.0.1"

func wasmtimeVersion() (string, error) {
	out, err := exec.Command("wasmtime", "--version").Output()
	if err != nil {
		return "", fmt.Errorf("wasmtime --version: %w", err)
	}
	fields := strings.Fields(string(out))
	if len(fields) < 2 {
		return "", fmt.Errorf("wasmtime --version: unparseable output %q", out)
	}
	return fields[1], nil
}

func skipIfPreview2Missing(t *testing.T) {
	t.Helper()
	preview2Once.Do(func() {
		if runtime.GOOS == "windows" {
			preview2Err = errors.New("preview-2 toolchain not exercised on windows")
			return
		}
		if _, err := exec.LookPath("wasmtime"); err != nil {
			preview2Err = errors.New("wasmtime not on PATH")
			return
		}
		if v, err := wasmtimeVersion(); err != nil {
			preview2Err = err
		} else if v != wasmtimePin {
			preview2Err = fmt.Errorf("wasmtime %s on PATH, but this suite is pinned to %s "+
				"(see mise.toml; `eval \"$(scripts/toolchain-env)\"` installs it); the "+
				"component-model-async ABI differs between them", v, wasmtimePin)
		}
	})
	if preview2Err != nil {
		t.Skipf("preview-2 prerequisites missing: %v", preview2Err)
	}
}

type runOpts struct {
	args         []string
	stdin        string
	stdinFile    *os.File
	stdoutFile   *os.File
	envs         []string
	workDir      string
	fdLimit      int
	maxResources int
}

func buildWasmCore(t *testing.T, src string) string {
	t.Helper()
	return buildWasmCoreMulti(t, "main.fern", map[string]string{"main.fern": src})
}

func buildWasmCoreMulti(t *testing.T, entry string, files map[string]string) string {
	t.Helper()
	skipIfPreview2Missing(t)
	dir := t.TempDir()
	for path, contents := range files {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return e2eharness.CompileSelfHostFile(t, e2eharness.TargetWasm32Wasi, filepath.Join(dir, entry), nil)
}

func buildCLIComponent(t *testing.T, src string) string {
	t.Helper()
	skipIfPreview2Missing(t)
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	comp := filepath.Join(dir, "prog.component.wasm")
	if out, err := e2eharness.SelfHostCompileCmd(t, e2eharness.TargetWasm32Wasi, srcPath, comp).CombinedOutput(); err != nil {
		t.Fatalf("SELFHOST-COMPILE-FAIL -target wasm32-wasi: %v\n%s\nsrc:\n%s", err, out, src)
	}
	return comp
}

func buildResultComponent(t *testing.T, src string) string {
	t.Helper()
	const entry = "function main(): i32 {"
	if !strings.Contains(src, entry) {
		t.Fatalf("no %q to wrap in:\n%s", entry, src)
	}
	wrapped := "import \"std/i32\";\n" + strings.Replace(src, entry, "function result_main(): i32 {", 1) +
		"\nfunction main(): i32 {\n    print(result_main().to_string());\n    return 0;\n}\n"
	return buildCLIComponent(t, wrapped)
}

func runResultStdout(t *testing.T, src string, opts runOpts) string {
	t.Helper()
	s, e, ec := runWasmArtifact(t, buildResultComponent(t, src), opts)
	if ec != 0 {
		t.Fatalf("wasmtime exit %d\nstdout:\n%s\nstderr:\n%s", ec, s, e)
	}
	return s
}

func runWasmResult(t *testing.T, src string) int {
	t.Helper()
	return parseMainResult(t, runResultStdout(t, src, runOpts{}))
}

func isCoreModule(t *testing.T, path string) bool {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	head := make([]byte, 8)
	if _, err := io.ReadFull(f, head); err != nil {
		t.Fatalf("read wasm header of %s: %v", path, err)
	}
	return bytes.Equal(head, []byte{0, 'a', 's', 'm', 1, 0, 0, 0})
}

func runWasmArtifact(t *testing.T, wasmPath string, opts runOpts) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmdArgs := []string{"run"}
	if opts.workDir != "" {
		cmdArgs = append(cmdArgs, "--dir="+opts.workDir)
	}
	for _, e := range opts.envs {
		cmdArgs = append(cmdArgs, "--env", e)
	}
	if isCoreModule(t, wasmPath) {
		cmdArgs = append(cmdArgs, "--invoke", "main")
	}
	if opts.maxResources > 0 {
		cmdArgs = append(cmdArgs, "-S", "max-resources="+strconv.Itoa(opts.maxResources))
	}
	cmdArgs = append(cmdArgs, wasmPath)
	cmdArgs = append(cmdArgs, opts.args...)
	cmd := exec.Command("wasmtime", cmdArgs...)
	if opts.fdLimit > 0 {
		cmd = exec.Command("sh", append([]string{"-c", `ulimit -n "$0" && exec wasmtime "$@"`, strconv.Itoa(opts.fdLimit)}, cmdArgs...)...)
	}
	cmd.Stdin = strings.NewReader(opts.stdin)
	if opts.stdinFile != nil {
		cmd.Stdin = opts.stdinFile
	}
	var so, se bytes.Buffer
	cmd.Stdout = &so
	if opts.stdoutFile != nil {
		cmd.Stdout = opts.stdoutFile
	}
	cmd.Stderr = &se
	_ = cmd.Run()
	code := cmd.ProcessState.ExitCode()
	stderr = strings.Replace(se.String(), "warning: using `--invoke` with a function that returns values is experimental and may break in the future\n", "", 1)
	if rejected, why := wasmRejected(stderr); rejected {
		t.Fatalf("wasmtime REJECTED the module — this is not a wrong answer, the artifact is invalid or incomplete (%s)\nstderr:\n%s", why, stderr)
	}
	return so.String(), stderr, code
}

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

func invokeWasmtime(t *testing.T, src string) (stdout, stderr string) {
	t.Helper()
	p := buildWasmCore(t, src)
	s, e, ec := runWasmArtifact(t, p, runOpts{})
	if ec != 0 {
		t.Fatalf("wasmtime exit %d\nstdout:\n%s\nstderr:\n%s", ec, s, e)
	}
	return s, e
}

func invokeWasmtimeMultiFile(t *testing.T, entry string, files map[string]string) (stdout, stderr string) {
	t.Helper()
	p := buildWasmCoreMulti(t, entry, files)
	s, e, ec := runWasmArtifact(t, p, runOpts{})
	if ec != 0 {
		t.Fatalf("wasmtime exit %d\nstdout:\n%s\nstderr:\n%s", ec, s, e)
	}
	return s, e
}

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

func TestWASMCrossModuleStructType(t *testing.T) {
	got := runWasmMultiFile(t, "main.fern", map[string]string{
		"point.fern": `pub struct Point { x: i32, y: i32 }
pub function make(x: i32, y: i32): Point {
	return Point { x: x, y: y };
}`,
		"main.fern": `import "./point";
function main(): i32 {
	let p: point.Point = point.make(3, 4);
	return p.x + p.y;
}`,
	})
	if got != 7 {
		t.Errorf("got %d, want 7 (3 + 4)", got)
	}
}

func invokeWasmtimeWithArgs(t *testing.T, src string, extraArgs ...string) (stdout, stderr string) {
	t.Helper()
	p := buildWasmCore(t, src)
	s, e, _ := runWasmArtifact(t, p, runOpts{args: extraArgs})
	return s, e
}

func TestWASMHttpParseRequest(t *testing.T) {
	src := `
import "std/http";
function main(): i32 {
    let raw: string = "POST /todos HTTP/1.1\r\nHost: localhost\r\nContent-Length: 13\r\n\r\nhello, world!";
    match (http.http_parse_request(raw)) {
        Some(req) => {
            if (req.method != "POST") { return 1; }
            if (req.path != "/todos") { return 2; }
            match (req.body_string()) {
                Ok(text) => { if (text != "hello, world!") { return 3; } },
                Err(e) => { return 4; }
            }
            return 42;
        },
        None => { return 99; }
    }
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42 (POST /todos with 13-byte body parses)", got)
	}
}

func TestWASMHttpParseRequestNoBody(t *testing.T) {
	src := `
import "std/http";
function main(): i32 {
    let raw: string = "GET /hello?name=world HTTP/1.1\r\nHost: localhost\r\n\r\n";
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

func TestWASMHttpParseRequestPartial(t *testing.T) {
	src := `
import "std/http";
function main(): i32 {
    let raw: string = "GET /partial HTTP/1.1\r\nHost: loca";
    match (http.http_parse_request(raw)) {
        Some(_) => { return 1; },
        None => { return 42; }
    }
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42 (partial buffer rejects)", got)
	}
}

func TestWASMHttpSerializeResponse(t *testing.T) {
	src := `
import "std/http";
function main(): i32 {
    let resp: HttpResponse = http.ok("hi");
    let wire: string = http.http_serialize_response(resp);
    let expected: string = "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nhi";
    if (wire != expected) { return 1; }
    return 42;
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42 (serialised response matches curl-readable shape)", got)
	}
}

func TestWASMHttpSerializeResponse404(t *testing.T) {
	src := `
import "std/http";
function main(): i32 {
    let resp: HttpResponse = http.text(404, "not found");
    let wire: string = http.http_serialize_response(resp);
    if (!wire.starts_with("HTTP/1.1 404 Not Found\r\n")) { return 1; }
    return 42;
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42 (404 reason phrase)", got)
	}
}

func TestWASMArgsBuiltin(t *testing.T) {
	src := `function main(): i32 {
		let a: string[] = args();
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

func TestWASMArgsBuiltinReadsValue(t *testing.T) {
	src := `function main(): i32 {
		let a: string[] = args();
		print(a[1]);
		return 0;
	}`
	stdout, _ := invokeWasmtimeWithArgs(t, src, "hello")
	if !strings.Contains(stdout, "hello") {
		t.Errorf("expected stdout to contain `hello`, got %q", stdout)
	}
}

func TestWASMArgsBuiltinCopiesLongValue(t *testing.T) {
	src := `function main(): i32 {
		let a: string[] = args();
		print(a[1]);
		return 0;
	}`
	const long = "supercalifragilistic-expialidocious"
	stdout, _ := invokeWasmtimeWithArgs(t, src, long)
	if !strings.Contains(stdout, long) {
		t.Errorf("expected stdout to contain the long arg %q, got %q", long, stdout)
	}
}

func runWasmStdinEnv(t *testing.T, src, stdin string, envs []string) (stdout, stderr string, exitCode int) {
	t.Helper()
	p := buildWasmCore(t, src)
	return runWasmArtifact(t, p, runOpts{stdin: stdin, envs: envs})
}

func runCLIComponent(t *testing.T, src string, opts runOpts) (stdout, stderr string, exitCode int) {
	t.Helper()
	return runWasmArtifact(t, buildCLIComponent(t, src), opts)
}

func TestWASMReadLine(t *testing.T) {
	src := `import "std/io";

function main(): i32 {
		match (io.read_line(stdin())) {
			Ok(Some(line)) => { write(line); return line.len(); },
			_ => { return -1; }
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

func TestWASMReadLineEOF(t *testing.T) {
	src := `import "std/io";

function main(): i32 {
		match (io.read_line(stdin())) {
			Ok(None()) => { return 0; },
			_ => { return 1; }
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

func TestWASMFStringInterpolation(t *testing.T) {
	src := `
import "std/string";
function main(): i32 {
		let name: string = "world";
		let n: i32 = 7;
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

func TestWASMWriterWriteStdoutNoCorruption(t *testing.T) {
	const src = `
import "std/string";
function emit(s: string): i32 {
    let w: Writer = stdout();
    match (w.write(s)) { Err(_) => { return 1; }, Ok(_) => {}, }
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

func TestWASMGenericUnions(t *testing.T) {
	src := `struct Leaf[T] { v: T }
struct Pair[T] { a: T, b: T }
struct Lit { v: i32 }

type Tree[T] = Leaf[T] | Pair[T] | Lit;

function leafOf(t: Tree[i32]): i32 {
    match (t) {
        Leaf(l) => { return l.v; },
        Pair(p) => { return p.a + p.b; },
        Lit(x) => { return x.v; },
    }
}

function main(): i32 {
    let a: Tree[i32] = Tree.Leaf(Leaf[i32] { v: 4 });
    let b: Tree[i32] = Pair[i32] { a: 5, b: 6 };
    let c: Tree[i32] = Lit { v: 7 };
    if (leafOf(a) + leafOf(b) + leafOf(c) == 22) { return 0; }
    return 1;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("generic union on wasm: got %d, want 0 (the three arms must sum to 22)", got)
	}
}

func runWasm(t *testing.T, src string) int {
	t.Helper()
	stdout, _ := invokeWasmtime(t, src)
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

func TestWASMReturn42(t *testing.T) {
	if got := runWasm(t, `function main(): i32 { return 42; }`); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

func TestWASMI64Arithmetic(t *testing.T) {
	src := `function add64(x: i64, y: i64): i64 { return x + y; }
function main(): i32 {
    let z: i64 = add64(40, 2);
    if (z == 42) { return 0; }
    return 1;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (i64 arith mismatch)", got)
	}
}

func TestWASMPolymorphicNumericLiterals(t *testing.T) {
	src := `function add64(x: i64, y: i64): i64 { return x + y; }
function main(): i32 {
    let a: i64 = 40;
    let b: u32 = 4294967295;
    let c: u64 = 1 << 62;
    if (add64(a, 2) != 42) { return 1; }
    if (b / 2 != 2147483647) { return 2; }
    if (c <= 0) { return 3; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (polymorphic literal mismatch)", got)
	}
}

func TestPolymorphicLiteralI32Overflow(t *testing.T) {
	src := `function bad(): i32 { let x: i32 = 5000000000; return x; }`
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

func TestWASMUsize(t *testing.T) {
	src := `function dbl(x: usize): usize { return x + x; }
function main(): i32 {
    let n: usize = 21;
    return dbl(n) as i32;
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42 (usize on wasm32)", got)
	}
}

func TestWASMSubI32Widths(t *testing.T) {
	src := `function main(): i32 {
    let a: u8 = 200;
    let b: u8 = 50;
    let sum: u8 = (a + b) as u8;
    if (sum != 250) { return 1; }
    let u: u8 = 250;
    let widened: u32 = u as u32;
    if (widened != 250) { return 2; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (sub-i32 width arithmetic)", got)
	}
}

func TestSubI32LiteralOverflow(t *testing.T) {
	src := `function bad(): i32 { let x: u8 = 300; return x as i32; }`
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

func TestWASMU8Array(t *testing.T) {
	src := `function main(): i32 {
    let bytes: u8[] = [255, 0, 66];
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

func TestWASMEnumMethod(t *testing.T) {
	src := `enum Color { Red, Green, Blue }
function (self: Color) is_red(): boolean {
    match (self) {
        Red => { return true; },
        _ => { return false; },
    }
}
function main(): i32 {
    let c: Color = Red;
    if (c.is_red()) { return 0; }
    return 1;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (enum method dispatch)", got)
	}
}

func TestWASMEnumMethodGeneric(t *testing.T) {
	src := `function (self: Option[i32]) unwrap_or(fallback: i32): i32 {
    match (self) {
        Some(v) => { return v; },
        None => { return fallback; },
    }
}
function main(): i32 {
    let s: Option[i32] = Some(7);
    let n: Option[i32] = None;
    if (s.unwrap_or(0) != 7) { return 1; }
    if (n.unwrap_or(99) != 99) { return 2; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (generic enum method)", got)
	}
}

func TestWASMMapBasics(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    let m: Map[i32, i32] = map_new(8);
    if (m.len() != 0) { return 1; }
    if (m.contains(7)) { return 2; }
    m = m.insert(7, 42);
    m = m.insert(11, 99);
    if (m.len() != 2) { return 3; }
    if (!m.contains(7)) { return 4; }
    if (!m.contains(11)) { return 5; }
    if (m.contains(13)) { return 6; }
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

func TestWASMMapHasShortCircuit(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    let m: Map[i32, i32] = map_new(8);
    m = m.insert(1, 10);
    if (m.contains(1) && !m.contains(2)) { return 7; }
    return 0;
}`
	if got := runWasm(t, src); got != 7 {
		t.Errorf("got %d, want 7 (m.contains(1) && !m.contains(2) short-circuit, issue #2652)", got)
	}
}

func TestWASMMapFieldStructRebind(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"insert-then-cleared", `
import "core/map";
struct IntSet { m: Map[i32, i32] }
function (s: IntSet) insert(x: i32): IntSet { return IntSet { m: s.m.insert(x, 1) }; }
function (s: IntSet) wipe(): IntSet { return IntSet { m: s.m.cleared() }; }
function (s: IntSet) len(): i32 { return s.m.len(); }
function main(): i32 {
    let m0: Map[i32, i32] = map_new(4);
    let s: IntSet = IntSet { m: m0 };
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

func TestWASMMapResize(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    let m: Map[i32, i32] = map_new(2);
    let i: i32 = 0;
    while (i < 12) {
        m = m.insert(i, i * 10);
        i = i + 1;
    }
    if (m.len() != 12) { return 1; }
    let j: i32 = 0;
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

func TestWASMMapKeysValues(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    let m: Map[i32, i32] = map_new(4);
    m = m.insert(10, 100);
    m = m.insert(20, 200);
    m = m.insert(30, 300);
    let ks: i32[] = m.keys();
    let vs: i32[] = m.values();
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
    let i: i32 = 0;
    let sum: i32 = 0;
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

func TestWASMMapValuesWideI64(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    let m: Map[i32, i64] = map_new(4);
    m = m.insert(1, 1000000000000i64);
    m = m.insert(2, 2000000000000i64);
    m = m.insert(3, 3000000000000i64);
    let vs: i64[] = m.values();
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
    let m: Map[i32, f64] = map_new(4);
    m = m.insert(1, 1.5f64);
    m = m.insert(2, 2.5f64);
    let vs: f64[] = m.values();
    if (vs.len() != 2) { return 1; }
    if (vs[0] != 1.5f64) { return 2; }
    if (vs[1] != 2.5f64) { return 3; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (Map[i32,f64].values() preserves wide bits)", got)
	}
}

func TestWASMWideKeyMapBasic(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    let m: Map[i64, i32] = map_new(4);
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
    let m: Map[i64, i32] = map_new(4);
    m = m.insert(7i64, 100);
    m = m.insert(42i64, 200);
    if (!m.contains(7i64)) { return 1; }
    if (!m.contains(42i64)) { return 2; }
    if (m.contains(99i64)) { return 3; }
    let had7: boolean = m.contains(7i64);
    let m7 = m.remove(7i64);
    if (!had7) { return 4; }
    m = m7;
    if (m.contains(7i64)) { return 5; }
    if (m.remove(7i64).len() != m.len()) { return 6; }
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
    let m: Map[i64, i32] = map_new(4);
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
    let m: Map[i64, i32] = map_new(2);
    let i: i32 = 0;
    while (i < 20) {
        m = m.insert(i as i64, i * 10);
        i = i + 1;
    }
    if (m.len() != 20) { return 1; }
    let sum: i32 = 0;
    let j: i32 = 0;
    while (j < 20) {
        sum = sum + m.get_or(j as i64, 0);
        j = j + 1;
    }
    return sum;
}`
	if got := runWasm(t, src); got != 1900 {
		t.Errorf("got %d, want 1900 (grow + reprobe with wide keys)", got)
	}
}

func TestWASMWideKeyMapHighBitsDistinct(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    let m: Map[i64, i32] = map_new(8);
    let k1: i64 = 0i64;
    let k2: i64 = 1i64 << 33i64;
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
    let m: Map[u64, i32] = map_new(4);
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
    let m: Map[i64, string] = map_new(4);
    m = m.insert(1i64, "hello");
    return (m.get_or(1i64, "")).len();
}`
	if got := runWasm(t, src); got != 5 {
		t.Errorf("got %d, want 5 (len('hello'))", got)
	}
}

func TestWASMWideKeyMapKeysSnapshot(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    let m: Map[i64, i32] = map_new(4);
    m = m.insert(1i64, 10);
    m = m.insert(1000000000000i64, 20);
    let keys: i64[] = m.keys();
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

func TestWASMMapDelete(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    let m: Map[i32, i32] = map_new(4);
    m = m.insert(1, 10);
    m = m.insert(2, 20);
    m = m.insert(3, 30);
    if (m.len() != 3) { return 1; }
    let had2: boolean = m.contains(2);
    let m2 = m.remove(2);
    if (!had2) { return 2; }   // present → true
    m = m2;
    if (m.len() != 2) { return 3; }
    if (m.contains(2)) { return 4; }
    if let Some(_) = m.get(2) { return 5; }
    if (m.remove(99).len() != m.len()) { return 6; }   // missing → unchanged
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

func TestWASMMapLiteral(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    let m: Map[i32, i32] = Map { 1: 10, 2: 20, 3: 30 };
    if (m.len() != 3) { return 1; }
    if let Some(v) = m.get(2) {
        if (v != 20) { return 2; }
    } else {
        return 3;
    }
    if let Some(_) = m.get(99) { return 4; }
    // Empty literal works too.
    let empty: Map[i32, i32] = Map {};
    if (empty.len() != 0) { return 5; }
    if (empty.contains(0)) { return 6; }
    // Trailing comma is fine.
    let m2: Map[i32, i32] = Map { 7: 700, };
    if (m2.len() != 1) { return 7; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (map literal)", got)
	}
}

func TestWASMSubI32ArrayWrites(t *testing.T) {
	src := `function main(): i32 {
    let bytes: u8[] = [1, 2, 3, 4];
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

func TestWASMI64Array(t *testing.T) {
	src := `function i64Sum(xs: i64[], n: i32): i64 {
    let i: i32 = 0;
    let s: i64 = 0;
    while (i < n) {
        s = s + xs[i];
        i = i + 1;
    }
    return s;
}
function main(): i32 {
    let xs: i64[] = [1, 2, 3, 4];
    xs = xs.with(1, (1 << 62) + 1);
    let s: i64 = i64Sum(xs, 4);
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

func TestWASMF64Array(t *testing.T) {
	src := `function main(): i32 {
    let xs: f64[] = [1.5, 2.5, 3.5];
    xs = xs.with(1, 99.25);
    if (xs[0] != 1.5) { return 1; }
    if (xs[1] != 99.25) { return 2; }
    if (xs[2] != 3.5) { return 3; }
    let sum: f64 = xs[0] + xs[1] + xs[2];
    if (sum != 104.25) { return 4; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (f64 array)", got)
	}
}

func TestWASMSubI32Slices(t *testing.T) {
	src := `function main(): i32 {
    let bytes: u8[] = [10, 20, 30, 40, 50];
    let view: [u8] = bytes[1:4];
    if (view.len() != 3) { return 1; }
    if (view[0] != 20) { return 2; }
    if (view[1] != 30) { return 3; }
    if (view[2] != 40) { return 4; }

    let words: i32[] = [1, 2, 3, 4];
    let wordview: [i32] = words[2:];
    if (wordview.len() != 2) { return 5; }
    if (wordview[0] != 3) { return 6; }
    if (wordview[1] != 4) { return 7; }

    let wide: i64[] = [(1 << 40), (1 << 41), (1 << 42)];
    let wview: [i64] = wide[1:3];
    if (wview.len() != 2) { return 8; }
    if (wview[0] != (1 << 41)) { return 9; }
    if (wview[1] != (1 << 42)) { return 10; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (byte / word / wide slices)", got)
	}
}

func TestWASMStringSlice(t *testing.T) {
	src := `function main(): i32 {
    let greeting: string = "hello world";
    let hello: str = slice_unchecked(greeting, 0, 5);
    let world: str = slice_unchecked(greeting, 6, 11);
    let dot: str = slice_unchecked(greeting, 5, 6);
    if (hello.len() != 5) { return 1; }
    if (world.len() != 5) { return 2; }
    if (dot.len() != 1) { return 3; }
    if (hello != "hello") { return 4; }
    if (world != "world") { return 5; }
    if (dot != " ") { return 6; }
    // Empty slice.
    let empty: str = slice_unchecked(greeting, 3, 3);
    if (empty.len() != 0) { return 9; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (string slicing)", got)
	}
}

func TestWASMStringSliceOption(t *testing.T) {
	src := `function main(): i32 {
    let g: string = "hello world";
    match (g[0:5]) { Some(v) => { if (v != "hello") { return 1; } }, None => { return 2; } }
    match (g[:5]) { Some(v) => { if (v != "hello") { return 3; } }, None => { return 4; } }
    match (g[6:]) { Some(v) => { if (v != "world") { return 5; } }, None => { return 6; } }
    match (g[3:3]) { Some(v) => { if (v.len() != 0) { return 7; } }, None => { return 8; } }
    // Out of range is None, not a trap.
    match (g[1:99]) { Some(v) => { return 9; }, None => {} }
    match (g[4:2]) { Some(v) => { return 10; }, None => {} }
    // "héllo" is 68 C3 A9 6C 6C 6F: offset 2 is inside the é.
    let m: string = "héllo";
    match (m[1:3]) { Some(v) => { if (v.len() != 2) { return 11; } }, None => { return 12; } }
    match (m[1:2]) { Some(v) => { return 13; }, None => {} }
    match (m[2:4]) { Some(v) => { return 14; }, None => {} }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (checked string slicing)", got)
	}
}

func TestWASMStringMethods(t *testing.T) {
	src := `
import "std/string";
function main(): i32 {
    let s: string = "hello world";
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

func TestWASMStringMethodsExtra(t *testing.T) {
	src := `import "std/unicode";

import "std/string";
import "std/option";
function main(): i32 {
    let s: string = "hello world";
    if (s.index_of("hello").unwrap_or(-1) != 0) { return 1; }
    if (s.index_of("world").unwrap_or(-1) != 6) { return 2; }
    if (s.index_of(" ").unwrap_or(-1) != 5) { return 3; }
    if (s.index_of("xyz").is_some()) { return 4; }
    if (s.index_of("").unwrap_or(-1) != 0) { return 5; }
    if (s.index_of("hello world!").is_some()) { return 6; }

    let padded: string = "  hello   ";
    let trimmed: str = padded.trim();
    if (trimmed != "hello") { return 7; }
    if (trimmed.len() != 5) { return 8; }
    let blank: string = "    ";
    if (blank.trim() != "") { return 9; }
    let nopad: string = "abc";
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

func TestWASMStringSplit(t *testing.T) {
	src := `
import "std/string";
function main(): i32 {
    let s: string = "a,b,c,d";
    let parts: string[] = s.split(",");
    if (parts.len() != 4) { return 1; }
    if (parts[0] != "a") { return 2; }
    if (parts[1] != "b") { return 3; }
    if (parts[2] != "c") { return 4; }
    if (parts[3] != "d") { return 5; }

    // No occurrence: single-element array holding the whole input.
    let none: string[] = "hello".split(",");
    if (none.len() != 1) { return 6; }
    if (none[0] != "hello") { return 7; }

    // Multi-byte separator.
    let s2: string = "alpha::beta::gamma";
    let p2: string[] = s2.split("::");
    if (p2.len() != 3) { return 8; }
    if (p2[0] != "alpha") { return 9; }
    if (p2[1] != "beta") { return 10; }
    if (p2[2] != "gamma") { return 11; }

    // Empty separator splits into chars.
    let chars: string[] = "abc".split("");
    if (chars.len() != 3) { return 12; }
    if (chars[0] != "a") { return 13; }
    if (chars[1] != "b") { return 14; }
    if (chars[2] != "c") { return 15; }

    // Empty pieces around separators are preserved.
    let trailing: string[] = ",a,b,".split(",");
    if (trailing.len() != 4) { return 16; }
    if (trailing[0] != "") { return 17; }
    if (trailing[3] != "") { return 18; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (string split)", got)
	}
}

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
    let lhs: Expr = Add(Add { l: 2, r: 3 });
    let rhs: Expr = Lit(Lit { v: 4 });
    let prod: Expr = Mul(Mul { l: eval(lhs), r: eval(rhs) });
    return eval(prod);
}`
	if got := runWasm(t, src); got != 20 {
		t.Errorf("got %d, want 20 ((2+3)*4)", got)
	}
}

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
    let a: Expr = Add { l: 2, r: 3 };
    let sum: i32 = eval(Lit { v: 5 });
    a = Mul { l: 2, r: sum };
    let built: Expr = mk_add(1, 2);
    return eval(a) + eval(built) + sum;
}`
	if got := runWasm(t, src); got != 18 {
		t.Errorf("got %d, want 18", got)
	}
}

func TestWASMStringLines(t *testing.T) {
	src := `
import "std/string";
function main(): i32 {
    // LF-only.
    let lf: string[] = "a\nb\nc".lines();
    if (lf.len() != 3) { return 1; }
    if (lf[0] != "a") { return 2; }
    if (lf[1] != "b") { return 3; }
    if (lf[2] != "c") { return 4; }

    // CRLF stripped.
    let crlf: string[] = "a\r\nb\r\nc".lines();
    if (crlf.len() != 3) { return 5; }
    if (crlf[0] != "a") { return 6; }
    if (crlf[1] != "b") { return 7; }
    if (crlf[2] != "c") { return 8; }

    // Trailing '\n' drops the phantom empty line.
    let trail: string[] = "a\nb\n".lines();
    if (trail.len() != 2) { return 9; }
    if (trail[1] != "b") { return 10; }

    // Single "\n" → one empty line.
    let solo: string[] = "\n".lines();
    if (solo.len() != 1) { return 11; }
    if (solo[0] != "") { return 12; }

    // Empty input → no lines at all.
    let empty: string[] = "".lines();
    if (empty.len() != 0) { return 13; }

    // No trailing newline: partial line still emits.
    let partial: string[] = "abc".lines();
    if (partial.len() != 1) { return 14; }
    if (partial[0] != "abc") { return 15; }

    // Mixed CRLF / LF.
    let mixed: string[] = "x\r\ny\nz".lines();
    if (mixed.len() != 3) { return 16; }
    if (mixed[0] != "x") { return 17; }
    if (mixed[1] != "y") { return 18; }
    if (mixed[2] != "z") { return 19; }

    // Bare '\r' is NOT a separator on its own — it stays in
    // the line. Only the '\r' immediately before '\n' (the
    // CRLF tail) is stripped.
    let bareCR: string[] = "a\rb\nc".lines();
    if (bareCR.len() != 2) { return 20; }
    if (bareCR[0] != "a\rb") { return 21; }
    if (bareCR[1] != "c") { return 22; }

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (string lines)", got)
	}
}

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

func TestWASMNumberToString(t *testing.T) {
	src := `
import "std/i32";
import "std/i64";
import "std/u32";
import "std/u64";
function main(): i32 {
    let a: i32 = 42;
    if (a.to_string() != "42") { return 1; }
    let b: i32 = -123;
    if (b.to_string() != "-123") { return 2; }
    let z: i32 = 0;
    if (z.to_string() != "0") { return 3; }

    let u: u32 = 4294967295;
    if (u.to_string() != "4294967295") { return 4; }
    let u2: u32 = 1;
    if (u2.to_string() != "1") { return 5; }

    let i: i64 = (1 << 40) + 7;
    if (i.to_string() != "1099511627783") { return 6; }
    let ineg: i64 = 0 - ((1 << 40) + 7);
    if (ineg.to_string() != "-1099511627783") { return 7; }

    let big: u64 = (1 << 63);
    if (big.to_string() != "9223372036854775808") { return 8; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (number to_string)", got)
	}
}

func TestWASMStringBytes(t *testing.T) {
	src := `
import "std/string";
function main(): i32 {
    let s: string = "hello";
    let bs: u8[] = s.bytes();
    if (bs.len() != 5) { return 1; }
    if (bs[0] != 104) { return 2; }   // 'h'
    if (bs[1] != 101) { return 3; }   // 'e'
    if (bs[2] != 108) { return 4; }   // 'l'
    if (bs[3] != 108) { return 5; }   // 'l'
    if (bs[4] != 111) { return 6; }   // 'o'
    // Mutating the bytes shouldn't affect the source string.
    bs = bs.with(0, 72); // 'H'
    if (s != "hello") { return 7; }
    let s2: string = string_from_bytes_unchecked(bs);
    if (s2 != "Hello") { return 8; }
    if (s2.len() != 5) { return 9; }
    // Empty string round-trip.
    let es: string = "";
    let ebs: u8[] = es.bytes();
    if (ebs.len() != 0) { return 10; }
    if (string_from_bytes_unchecked(ebs) != "") { return 11; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (string <-> bytes round-trip)", got)
	}
}

func TestWASMStringAsBytes(t *testing.T) {
	src := `
import "std/string";
function main(): i32 {
    let s: string = "hello";
    let view: [u8] = s.as_bytes();
    if (view.len() != 5) { return 1; }
    if (view[0] != 104) { return 2; }   // 'h'
    if (view[4] != 111) { return 3; }   // 'o'
    // Sub-slicing the view should still alias the source.
    let tail: [u8] = view[1:5];
    if (tail.len() != 4) { return 4; }
    if (tail[0] != 101) { return 5; }   // 'e'

    // Empty string -> zero-length view, no allocation.
    let es: string = "";
    let ev: [u8] = es.as_bytes();
    if (ev.len() != 0) { return 6; }

    // Read parity with the copying bytes() variant.
    let copied: u8[] = s.bytes();
    let i: i32 = 0;
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

func TestWASMParseInt(t *testing.T) {
	src := `
import "std/i32";
import "std/string";
function main(): i32 {
		// Successes:
		match (i32.parse_i32("42")) {
			Ok(v) => { if (v != 42) { return 1; } },
			Err(_) => { return 2; }
		}
		match (i32.parse_i32("0")) {
			Ok(v) => { if (v != 0) { return 3; } },
			Err(_) => { return 4; }
		}
		match (i32.parse_i32("-7")) {
			Ok(v) => { if (v != -7) { return 5; } },
			Err(_) => { return 6; }
		}
		// 2^31 - 1 (max i32).
		match (i32.parse_i32("2147483647")) {
			Ok(v) => { if (v != 2147483647) { return 7; } },
			Err(_) => { return 8; }
		}
		// -2^31 (min i32). The literal -2147483648 isn't
		// directly expressible (the lexer reads the minus
		// separately, leaving the positive literal one past
		// i32 max), so verify via arithmetic:
		// v + 2147483647 must equal -1.
		match (i32.parse_i32("-2147483648")) {
			Ok(v) => { if (v + 2147483647 != -1) { return 9; } },
			Err(_) => { return 10; }
		}

		// Failures: each must come back None.
		match (i32.parse_i32("")) { Ok(_) => { return 20; }, Err(_) => {} }
		match (i32.parse_i32("-")) { Ok(_) => { return 21; }, Err(_) => {} }
		match (i32.parse_i32("abc")) { Ok(_) => { return 22; }, Err(_) => {} }
		match (i32.parse_i32("12 34")) { Ok(_) => { return 23; }, Err(_) => {} }
		match (i32.parse_i32("12a")) { Ok(_) => { return 24; }, Err(_) => {} }
		// Overflow (2^31 is one past i32 max).
		match (i32.parse_i32("2147483648")) { Ok(_) => { return 25; }, Err(_) => {} }
		// Underflow.
		match (i32.parse_i32("-2147483649")) { Ok(_) => { return 26; }, Err(_) => {} }
		// Way out of range.
		match (i32.parse_i32("99999999999999")) { Ok(_) => { return 27; }, Err(_) => {} }
		// Plus sign not accepted.
		match (i32.parse_i32("+1")) { Ok(n) => { if (n != 1) { return 28; } }, Err(_) => { return 29; } }
		return 0;
	}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (parse_int)", got)
	}
}

func TestWASMParseFloat(t *testing.T) {
	src := `
import "std/f64";
import "std/string";
function near(actual: f64, expected: f64, eps: f64): boolean {
		let diff: f64 = actual - expected;
		if (diff < 0.0) { diff = 0.0 - diff; }
		let bound: f64 = expected;
		if (bound < 0.0) { bound = 0.0 - bound; }
		let rel: f64 = bound * eps;
		if (rel < eps) { rel = eps; }
		return diff < rel;
	}
	function main(): i32 {
		// Successes — integer / fraction / exponent shapes.
		match (f64.parse_f64("3.14")) {
			Ok(v) => { if (!near(v, 3.14, 0.001)) { return 1; } },
			Err(_) => { return 2; }
		}
		match (f64.parse_f64("0")) {
			Ok(v) => { if (v != 0.0) { return 3; } },
			Err(_) => { return 4; }
		}
		match (f64.parse_f64("-2.5")) {
			Ok(v) => { if (!near(v, -2.5, 0.001)) { return 5; } },
			Err(_) => { return 6; }
		}
		match (f64.parse_f64(".5")) {
			Ok(v) => { if (!near(v, 0.5, 0.001)) { return 7; } },
			Err(_) => { return 8; }
		}
		match (f64.parse_f64("42.")) {
			Ok(v) => { if (!near(v, 42.0, 0.001)) { return 9; } },
			Err(_) => { return 10; }
		}
		match (f64.parse_f64("1e3")) {
			Ok(v) => { if (!near(v, 1000.0, 0.001)) { return 11; } },
			Err(_) => { return 12; }
		}
		match (f64.parse_f64("1.5e2")) {
			Ok(v) => { if (!near(v, 150.0, 0.001)) { return 13; } },
			Err(_) => { return 14; }
		}
		match (f64.parse_f64("2.5E-2")) {
			Ok(v) => { if (!near(v, 0.025, 0.0001)) { return 15; } },
			Err(_) => { return 16; }
		}
		match (f64.parse_f64("1e+5")) {
			Ok(v) => { if (!near(v, 100000.0, 0.001)) { return 17; } },
			Err(_) => { return 18; }
		}

		// Failures — bad shapes must come back None.
		match (f64.parse_f64("")) { Ok(_) => { return 30; }, Err(_) => {} }
		match (f64.parse_f64("-")) { Ok(_) => { return 31; }, Err(_) => {} }
		match (f64.parse_f64(".")) { Ok(_) => { return 32; }, Err(_) => {} }
		match (f64.parse_f64("abc")) { Ok(_) => { return 33; }, Err(_) => {} }
		match (f64.parse_f64("1.2.3")) { Ok(_) => { return 34; }, Err(_) => {} }
		match (f64.parse_f64("1e")) { Ok(_) => { return 35; }, Err(_) => {} }
		match (f64.parse_f64("1ex")) { Ok(_) => { return 36; }, Err(_) => {} }
		match (f64.parse_f64("1e-")) { Ok(_) => { return 37; }, Err(_) => {} }
		match (f64.parse_f64("1.5x")) { Ok(_) => { return 38; }, Err(_) => {} }
		match (f64.parse_f64("+1")) { Ok(v) => { if (v != 1.0) { return 39; } }, Err(_) => { return 40; } }

		return 0;
	}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (parse_float)", got)
	}
}

func TestWASMMapStringKeysInlineSSO(t *testing.T) {
	src := `
import "core/map";
import "core/int";
function main(): i32 {
    let m: Map[string, i32] = map_new(8);

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
    if (!m.contains("a")) { return 19; }
    if (!m.contains("ok")) { return 20; }
    if (m.contains("xy")) { return 21; }

    // Update + delete an inline key.
    m = m.insert("GET", 30);
    if let Some(v) = m.get("GET") {
        if (v != 30) { return 22; }
    } else { return 23; }
    let hadg: boolean = m.contains("GET");
    let mg = m.remove("GET");
    if (!hadg) { return 24; }
    m = mg;
    if (m.contains("GET")) { return 25; }
    if (m.remove("GET").len() != m.len()) { return 26; }
    if (m.len() != 5) { return 27; }

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (Map[string, i32] with inline-form keys)", got)
	}
}

func TestWASMMapStringKeys(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    let m: Map[string, i32] = map_new(8);
    m = m.insert("hello", 1);
    m = m.insert("world", 2);
    m = m.insert("foo", 3);
    if (m.len() != 3) { return 1; }
    if (!m.contains("hello")) { return 2; }
    if (!m.contains("world")) { return 3; }
    if (m.contains("missing")) { return 4; }
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
    let hadf: boolean = m.contains("foo");
    let mf = m.remove("foo");
    if (!hadf) { return 13; }
    m = mf;
    if (m.contains("foo")) { return 14; }
    if (m.len() != 2) { return 15; }
    if (m.remove("foo").len() != 2) { return 16; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (Map[string, i32])", got)
	}
}

func TestWASMMapStringStringValues(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    let headers: Map[string, string] = map_new(4);
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

func TestWASMMapHashStress(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    let m: Map[i32, i32] = map_new(4);
    let i: i32 = 0;
    while (i < 100) {
        m = m.insert(i, i * 7 + 1);
        i = i + 1;
    }
    if (m.len() != 100) { return 1; }
    // Verify every insert is reachable.
    let j: i32 = 0;
    while (j < 100) {
        if let Some(v) = m.get(j) {
            if (v != j * 7 + 1) { return 100 + j; }
        } else {
            return 200 + j;
        }
        j = j + 1;
    }
    // Update every entry's value.
    let k: i32 = 0;
    while (k < 100) {
        m = m.insert(k, k * 11 + 2);
        k = k + 1;
    }
    if (m.len() != 100) { return 2; }
    // Delete every even key. After deletion, even keys must
    // miss and odd keys must still hit.
    let d: i32 = 0;
    while (d < 100) {
        let hadd: boolean = m.contains(d);
        let md = m.remove(d);
        if (!hadd) { return 300 + d; }
        m = md;
        d = d + 2;
    }
    if (m.len() != 50) { return 3; }
    let c: i32 = 0;
    while (c < 100) {
        if (c % 2 == 0) {
            if (m.contains(c)) { return 400 + c; }
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
    let r: i32 = 0;
    while (r < 100) {
        m = m.insert(r, r);
        r = r + 2;
    }
    if (m.len() != 100) { return 4; }
    // Every key should now be present.
    let f: i32 = 0;
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
    let m: Map[string, i32] = map_new(4);
    let i: i32 = 0;
    while (i < 80) {
        m = m.insert(key_of(i), i);
        i = i + 1;
    }
    if (m.len() != 80) { return 1; }
    let j: i32 = 0;
    while (j < 80) {
        if let Some(v) = m.get(key_of(j)) {
            if (v != j) { return 100 + j; }
        } else {
            return 200 + j;
        }
        j = j + 1;
    }
    if (m.contains("k_99")) { return 2; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (Map string-key stress)", got)
	}
}

func TestWASMMapIter(t *testing.T) {
	src := `
import "core/map"; import "core/iter";
function main(): i32 {
    let m: Map[i32, i32] = Map { 10: 100, 20: 200, 30: 300, 40: 400 };
    let sum_keys: i32 = 0;
    let sum_vals: i32 = 0;
    let count: i32 = 0;
    let it: iter.ArrayIter[(i32, i32)] = m.iter();
    for (__mapkey_it, __mapvalue_it) in it {
        sum_keys = sum_keys + __mapkey_it;
        sum_vals = sum_vals + __mapvalue_it;
        count = count + 1;

    }
    if (count != 4) { return 1; }
    if (sum_keys != 100) { return 2; }   // 10+20+30+40
    if (sum_vals != 1000) { return 3; }  // 100+200+300+400
    // Iteration over an empty map yields zero steps.
    let empty: Map[i32, i32] = map_new(4);
    let it2: iter.ArrayIter[(i32, i32)] = empty.iter();
    if let Some(__mapstep_it2) = it2.next() { let __mapkey_it2 = __mapstep_it2.0.0; let __mapvalue_it2 = __mapstep_it2.0.1; return 4; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (Map.iter cursor)", got)
	}
}

func TestWASMForTupleInMap(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    let m: Map[i32, i32] = Map { 1: 10, 2: 20, 3: 30, 4: 40 };
    let sum_keys: i32 = 0;
    let sum_vals: i32 = 0;
    let count: i32 = 0;
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
    let labels: Map[string, i32] = Map { "a": 1, "b": 2, "c": 3 };
    let keys_concat: string = "";
    let val_sum: i32 = 0;
    for (k2, v2) in labels {
        keys_concat = keys_concat + k2;
        val_sum = val_sum + v2;
    }
    if (keys_concat != "abc") { return 4; }
    if (val_sum != 6) { return 5; }

    // An empty map's foreach is a no-op.
    let empty: Map[i32, i32] = map_new(4);
    let ran: i32 = 0;
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

func TestWASMMapValueI64(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    let m: Map[i32, i64] = map_new(8);
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
    let fallback: i64 = m.get_or(99, 7777777777 as i64);
    if (fallback != (7777777777 as i64)) { return 6; }
    let hit: i64 = m.get_or(1, 0 as i64);
    if (hit != (4294967296 as i64)) { return 7; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (Map[i32, i64] roundtrip)", got)
	}
}

func TestWASMMapValueF64(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    let m: Map[string, f64] = map_new(8);
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

func TestWASMMapLiteralWideValue(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    let m: Map[i32, i64] = Map { 1: 4294967296 as i64, 2: 8589934592 as i64 };
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

func TestWASMMapIterValueWide(t *testing.T) {
	src := `
import "core/map"; import "core/iter";
function main(): i32 {
    let m: Map[i32, i64] = Map { 1: 4294967296 as i64, 2: 8589934592 as i64 };
    let sum: i64 = 0 as i64;
    let it: iter.ArrayIter[(i32, i64)] = m.iter();
    for (__mapkey_it, __mapvalue_it) in it {
        sum = sum + __mapvalue_it;

    }
    if (sum == (12884901888 as i64)) { return 0; }
    return 1;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (MapIter.value wide V)", got)
	}
}

func TestWASMMapIterStringKeys(t *testing.T) {
	src := `
import "core/map"; import "core/iter";
function main(): i32 {
    let m: Map[string, i32] = Map { "a": 1, "b": 2, "c": 3 };
    let concat: string = "";
    let sum: i32 = 0;
    let it: iter.ArrayIter[(string, i32)] = m.iter();
    for (__mapkey_it, __mapvalue_it) in it {
        concat = concat + __mapkey_it;
        sum = sum + __mapvalue_it;

    }
    if (concat != "abc") { return 1; }
    if (sum != 6) { return 2; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (Map.iter string keys)", got)
	}
}

func TestWASMBase64(t *testing.T) {
	src := `
import "core/int";
import "std/base64";
import "std/codec";
import "std/string";
function main(): i32 {
    if (base64.encode("".bytes()) != "") { return 1; }
    if (base64.encode("f".bytes()) != "Zg==") { return 2; }
    if (base64.encode("fo".bytes()) != "Zm8=") { return 3; }
    if (base64.encode("foo".bytes()) != "Zm9v") { return 4; }
    if (base64.encode("foob".bytes()) != "Zm9vYg==") { return 5; }
    if (base64.encode("fooba".bytes()) != "Zm9vYmE=") { return 6; }
    if (base64.encode("foobar".bytes()) != "Zm9vYmFy") { return 7; }
    if (base64.encode("hello world".bytes()) != "aGVsbG8gd29ybGQ=") { return 8; }
    if (string_from_bytes_unchecked(__codec_bytes(base64.decode(""))) != "") { return 9; }
    if (string_from_bytes_unchecked(__codec_bytes(base64.decode("Zg=="))) != "f") { return 10; }
    if (string_from_bytes_unchecked(__codec_bytes(base64.decode("Zm8="))) != "fo") { return 11; }
    if (string_from_bytes_unchecked(__codec_bytes(base64.decode("Zm9v"))) != "foo") { return 12; }
    if (string_from_bytes_unchecked(__codec_bytes(base64.decode("Zm9vYg=="))) != "foob") { return 13; }
    if (string_from_bytes_unchecked(__codec_bytes(base64.decode("Zm9vYmE="))) != "fooba") { return 14; }
    if (string_from_bytes_unchecked(__codec_bytes(base64.decode("Zm9vYmFy"))) != "foobar") { return 15; }
    if (string_from_bytes_unchecked(__codec_bytes(base64.decode("aGVsbG8gd29ybGQ="))) != "hello world") { return 16; }
    return 0;
}
// Decode a valid fixture. Report a malformed fixture before ending the test.
function __codec_bytes(value: Result[u8[], codec.DecodeError]): u8[] {
  match (value) {
    Ok(bytes) => { return bytes; },
    Err(problem) => { eprint(problem.message()); exit(98); return []; }
  }
}
`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (base64)", got)
	}
}

func TestWASMDeferBasic(t *testing.T) {
	src := `
function inner(trace: Cell[i32]): i32 {
    trace.set(trace.get() * 10 + 1);
    defer trace.set(trace.get() * 10 + 3);
    defer trace.set(trace.get() * 10 + 4);
    trace.set(trace.get() * 10 + 2);
    return 42;
}
function main(): i32 {
    let trace: Cell[i32] = cell_new(0);
    let r: i32 = inner(trace);
    if (r != 42) { return 1; }
    // Body steps 1 and 2, then the defers in LIFO order: 4, then 3.
    if (trace.get() != 1243) { return 2; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (defer basic)", got)
	}
}

func TestWASMDeferConditional(t *testing.T) {
	src := `
function run(fired: Cell[i32], taken: boolean): i32 {
    if (taken) {
        defer fired.set(fired.get() + 1);
    }
    defer fired.set(fired.get() + 2);
    return 0;
}
function main(): i32 {
    let fired: Cell[i32] = cell_new(0);
    run(fired, false);
    if (fired.get() != 2) { return 1; }
    fired.set(0);
    run(fired, true);
    if (fired.get() != 3) { return 3; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (defer conditional)", got)
	}
}

func TestWASMDeferEarlyReturn(t *testing.T) {
	src := `
function early(counts: Cell[i32], branch: i32): i32 {
    defer counts.set(counts.get() + 1);
    if (branch == 1) {
        return 10;
    }
    if (branch == 2) {
        return 20;
    }
    return 30;
}
function main(): i32 {
    let counts: Cell[i32] = cell_new(0);
    if (early(counts, 1) != 10) { return 1; }
    if (early(counts, 2) != 20) { return 2; }
    if (early(counts, 0) != 30) { return 3; }
    if (counts.get() != 3) { return 4; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (defer early-return)", got)
	}
}

func TestWASMMapGetOr(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    let counts: Map[string, i32] = Map { "apple": 3, "banana": 5 };
    if (counts.get_or("apple", 0) != 3) { return 1; }
    if (counts.get_or("banana", 0) != 5) { return 2; }
    if (counts.get_or("missing", -1) != -1) { return 3; }
    if (counts.get_or("missing", 100) != 100) { return 4; }

    let ints: Map[i32, i32] = Map { 1: 10, 2: 20 };
    if (ints.get_or(1, 999) != 10) { return 5; }
    if (ints.get_or(99, 999) != 999) { return 6; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (Map.get_or)", got)
	}
}

func TestWASMMapClear(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    let m: Map[string, i32] = Map { "a": 1, "b": 2, "c": 3 };
    if (m.len() != 3) { return 1; }
    m = m.cleared();
    if (m.len() != 0) { return 2; }
    if (m.contains("a")) { return 3; }
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
    if (m.contains("a")) { return 10; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (Map.clear)", got)
	}
}

func TestWASMMapStringKeyLiteral(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    let m: Map[string, i32] = Map { "a": 1, "b": 2, "c": 3 };
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

func TestWASMF64Arithmetic(t *testing.T) {
	src := `function add64f(x: f64, y: f64): f64 { return x + y; }
function main(): i32 {
    let z: f64 = add64f(1.5, 2.5);
    if (z == 4.0) { return 0; }
    return 1;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (f64 arith mismatch)", got)
	}
}

func TestWASMFloatCasts(t *testing.T) {
	src := `function main(): i32 {
    let a: f32 = 1.25;
    let b: f64 = a as f64;
    let c: f32 = b as f32;
    if (c == a) { return 0; }
    return 1;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (f32/f64 cast mismatch)", got)
	}
}

func TestF32F64MixWidening(t *testing.T) {
	src := `function main(): i32 { let x: f32 = 1.25; let y: f64 = 2.5; if (x + y == 3.75f64) { return 0; } return 1; }`
	if got := runWasm(t, src); got != 0 {
		t.Fatalf("mixed f32/f64 sum returned %d, want 0", got)
	}
}

func TestWASMU32Unsigned(t *testing.T) {
	src := `function main(): i32 {
    let x: u32 = 4294967295;
    let half: u32 = x / 2;
    if (half != 2147483647) { return 1; }
    if (!(x > 1)) { return 2; }
    if ((x >> 1) != 2147483647) { return 3; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (u32 unsigned arithmetic)", got)
	}
}

func TestWASMU64Unsigned(t *testing.T) {
	src := `function main(): i32 {
    let x: u64 = 1 << 63;
    let half: u64 = x / 2;
    if (half != 1 << 62) { return 1; }
    if (!(x > 1)) { return 2; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (u64 unsigned arithmetic)", got)
	}
}

func TestU32SignedMixRejected(t *testing.T) {
	src := `function bad(): i32 { let x: i32 = 1; let y: u32 = 2 as u32; return x + y; }`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err == nil {
		t.Fatalf("expected checker error for mixed i32/u32 add, got none")
	}
}

func TestWASMTupleMultiReturn(t *testing.T) {
	src := `function divmod(a: i32, b: i32): (i32, i32) {
    return (a / b, a - (a / b) * b);
}
function main(): i32 {
    let p = divmod(17, 5);
    if (p.0 == 3 && p.1 == 2) { return 0; }
    return 1;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (divmod tuple mismatch)", got)
	}
}

func TestWASMPipeOperator(t *testing.T) {
	src := `function double(n: i32): i32 { return n * 2; }
function add(a: i32, b: i32): i32 { return a + b; }
function main(): i32 {
    let x = 5 |> double;
    let y = x |> add(3);
    let z = y |> double |> add(4);
    if (x == 10 && y == 13 && z == 30) { return 0; }
    return 1;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (pipe chain mismatch)", got)
	}
}

func TestWASMGenericFunctionInfersFromArg(t *testing.T) {
	src := `function id[T](x: T): T { return x; }
function main(): i32 {
    let a = id(42);
    let b = id(7);
    if (a == 42 && b == 7) { return 0; }
    return 1;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (id() generic mismatch)", got)
	}
}

func TestWASMGenericFunctionMultipleInstantiations(t *testing.T) {
	src := `function first[T](xs: T[]): T { return xs[0]; }
function main(): i32 {
    let ints: i32[] = [10, 20, 30];
    let strs: string[] = ["hello", "world"];
    let n = first(ints);
    let s = first(strs);
    print(s);
    return n;
}`
	out := runWasmCapturingStdout(t, src)
	if out != "hello" {
		t.Errorf("output = %q, want \"hello\"", out)
	}
}

func TestWASMGenericStructInfersAndMonomorphises(t *testing.T) {
	src := `struct Pair[A, B] { first: A, second: B }
function main(): i32 {
    let p = Pair { first: 42, second: "hello" };
    print(p.second);
    return p.first;
}`
	out := runWasmCapturingStdout(t, src)
	if out != "hello" {
		t.Errorf("output = %q, want \"hello\"", out)
	}
}

func TestWASMGenericFunctionOverGenericStruct(t *testing.T) {
	src := `struct Box[T] { val: T }
function unbox[T](b: Box[T]): T { return b.val; }
function main(): i32 {
    let i = Box { val: 7 };
    let s = Box { val: "world" };
    print(unbox(s));
    return unbox(i);
}`
	out := runWasmCapturingStdout(t, src)
	if out != "world" {
		t.Errorf("output = %q, want \"world\"", out)
	}
}

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

func TestWASMUseInferredTypeFromGenericCallee(t *testing.T) {
	src := `function each[T](items: T[], cb: (T) => i32): i32 {
    return cb(items[0]);
}
function main(): i32 {
    let nums: i32[] = [10, 20, 30];
    use n <- each(nums);
    return n + 1;
}`
	if got := runWasm(t, src); got != 11 {
		t.Errorf("got %d, want 11 (generic-callee inference)", got)
	}
}

func TestWASMUseInferredTypeThroughEnumPayload(t *testing.T) {
	src := `function try_opt[T](opt: Option[T], cb: (T) => i32): i32 {
    if let Some(v) = opt { return cb(v); }
    return 0;
}
function main(): i32 {
    let x: Option[i32] = Some(7);
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

func TestWASMIfLetMatch(t *testing.T) {
	src := `function main(): i32 {
    let o: Option[i32] = Some(42);
    if let Some(n) = o {
        return n;
    }
    return -1;
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42 (if-let Some(42) bind)", got)
	}
}

func TestWASMIfLetMismatchTakesElse(t *testing.T) {
	src := `function main(): i32 {
    let o: Option[i32] = None;
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

func TestWASMMatchRebindSameName(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runWasm(t, tc.src); got != tc.want {
				t.Errorf("wasm %s: got %d, want %d (issue #2644)", tc.name, got, tc.want)
			}
		})
	}
}

func TestWASMSliceViews(t *testing.T) {
	src := `function main(): i32 {
    let arr: i32[] = [10, 20, 30, 40, 50];
    let s: [i32] = arr[1:4];          // [20, 30, 40]
    if (s.len() != 3) { return 1; }
    if (s[0] != 20) { return 2; }
    if (s[1] != 30) { return 3; }
    if (s[2] != 40) { return 4; }
    let t = s[1:3];                   // [30, 40]
    if (t.len() != 2) { return 5; }
    if (t[0] != 30 || t[1] != 40) { return 6; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (slice view mismatch at step %d)", got, got)
	}
}

func TestWASMSliceHalfBoundedForms(t *testing.T) {
	src := `function main(): i32 {
    let arr: i32[] = [1, 2, 3, 4, 5];
    let head: [i32] = arr[:3];
    if (head.len() != 3 || head[0] != 1 || head[2] != 3) { return 1; }
    let tail: [i32] = arr[2:];
    if (tail.len() != 3 || tail[0] != 3 || tail[2] != 5) { return 2; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (half-bounded slice mismatch at step %d)", got, got)
	}
}

func TestWASMTupleHeterogeneous(t *testing.T) {
	src := `function pair(): (i32, string) { return (42, "hello"); }
function main(): i32 {
    let p = pair();
    if (p.0 == 42) { return 0; }
    return 1;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (tuple heterogeneous mismatch)", got)
	}
}

func TestWASMGenericStructMethod(t *testing.T) {
	src := `struct Box[T] { value: T }
pub function [T] (b: Box[T]) unwrap(): T { return b.value; }
function main(): i32 {
    let b: Box[i32] = Box { value: 42 };
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
    let b: Box[i64] = Box { value: 1000000000000i64 };
    let v: i64 = b.unwrap();
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
    let b: Box[string] = Box { value: "hello" };
    return (b.unwrap()).len();
}`
	if got := runWasm(t, src); got != 5 {
		t.Errorf("got %d, want 5", got)
	}
}

func TestWASMGenericStructMethodMultipleTypeParams(t *testing.T) {
	src := `struct Pair[A, B] { a: A, b: B }
pub function [A, B] (p: Pair[A, B]) first(): A { return p.a; }
pub function [A, B] (p: Pair[A, B]) second(): B { return p.b; }
function main(): i32 {
    let p: Pair[i32, string] = Pair { a: 42, b: "hello" };
    return p.first() + (p.second()).len();
}`
	if got := runWasm(t, src); got != 47 {
		t.Errorf("got %d, want 47", got)
	}
}

func TestWASMGenericMethodReturnsBoxOfT(t *testing.T) {
	src := `struct Box[T] { value: T }
pub function [T] (b: Box[T]) replace(v: T): Box[T] {
    return Box { value: v };
}
function main(): i32 {
    let b: Box[i32] = Box { value: 1 };
    let c = b.replace(42);
    return c.value;
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

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
	if got := runWasm(t, src); got != 12 {
		t.Errorf("got %d, want 12", got)
	}
}

func TestWASMImmediateLambdaCall(t *testing.T) {
	src := `function main(): i32 {
    let n: i32 = ((x: i32): i32 => { return x * 2; })(21);
    return n;
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42 (immediate lambda call)", got)
	}
}

func TestWASMLambdaInGenericFn(t *testing.T) {
	src := `function makeId[T](): (T) => T {
    return (x: T): T => { return x; };
}
function main(): i32 {
    let f = makeId[i32]();
    return f(42);
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

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
    let p = pair[i32, string](42, "hi");
    if (p.0 != 42) { return 1; }
    if (p.1 != "hi") { return 2; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}

func TestWASMMatchLiteralInt(t *testing.T) {
	src := `function main(): i32 {
    let n: i32 = 1;
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
    let b: boolean = true;
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
    let s: string = "world";
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

func TestWASMLambdaBasic(t *testing.T) {
	src := `function main(): i32 {
    let f = (x: i32): i32 => { return x + 1; };
    return f(41);
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

func TestWASMLambdaCaptures(t *testing.T) {
	src := `function main(): i32 {
    let n: i32 = 10;
    let f = (x: i32): i32 => { return x + n; };
    return f(32);
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

func TestWASMLambdaReturned(t *testing.T) {
	src := `function makeAdder(n: i32): (i32) => i32 {
    return (x: i32): i32 => { return x + n; };
}
function main(): i32 {
    let add10 = makeAdder(10);
    return add10(32);
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

func TestWASMLambdaAsArg(t *testing.T) {
	src := `function apply(f: (i32) => i32, x: i32): i32 { return f(x); }
function main(): i32 {
    return apply((n: i32): i32 => { return n * 2; }, 21);
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

func TestWASMVarTupleDestructure(t *testing.T) {
	src := `function getit(): (i64, i32) {
    return (1000000000000i64, 42);
}
function main(): i32 {
    let (a, b) = getit();
    if (a != 1000000000000i64) { return 1; }
    if (b != 42) { return 2; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (let tuple destructure)", got)
	}
}

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

func TestWASMArrayOfClosures(t *testing.T) {
	src := `function makeAdder(n: i32): (i32) => i32 {
    function add(x: i32): i32 { return x + n; }
    return add;
}
function main(): i32 {
    let arr: ((i32) => i32)[] = [makeAdder(1), makeAdder(10), makeAdder(100)];
    return arr[0](1) + arr[1](2) + arr[2](3);
}`
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
    let arr: ((i32) => i32)[] = [makeAdder(1), makeAdder(2), makeAdder(3)];
    let sl: [(i32) => i32] = arr[1:3];
    return sl[0](10) + sl[1](10);
}`
	if got := runWasm(t, src); got != 25 {
		t.Errorf("got %d, want 25 (slice of closures)", got)
	}
}

func TestWASMClosureInLoopCapturesIterVar(t *testing.T) {
	src := `function main(): i32 {
    let arr: ((i32) => i32)[] = [];
    let i: i32 = 0;
    while (i < 3) {
        let ic: i32 = i;
        function f(x: i32): i32 { return x + ic; }
        arr = arr.append(f);
        i = i + 1;
    }
    return arr[0](10) + arr[1](10) + arr[2](10);
}`
	if got := runWasm(t, src); got != 33 {
		t.Errorf("got %d, want 33 (per-iteration capture)", got)
	}
}

func TestI64ImplicitWideningAccepted(t *testing.T) {
	src := `function f(): i64 { let x: i32 = 1; let y: i64 = 2i64; return x + y; }
function main(): i32 { return f() as i32; }`
	if got := runWasm(t, src); got != 3 {
		t.Errorf("got %d, want 3 (i32 + i64 auto-widens)", got)
	}
}

func TestMixedSignednessWidening(t *testing.T) {
	for _, src := range []string{
		`function good(): i64 { let x: i32 = 1; let y: u32 = 2 as u32; return x + y; }`,
		`function good(): i64 { let x: u32 = 1 as u32; let y: i64 = 2i64; return x + y; }`,
	} {
		prog, err := parser.Parse(src)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if _, err := checker.Check(prog); err != nil {
			t.Fatalf("lossless mixed operands: %v", err)
		}
	}

	src := `function bad(): i32 { let x: i32 = 1; let y: u32 = 2 as u32; return x + y; }`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err == nil {
		t.Fatal("expected a refusal to narrow the i64 sum to i32")
	} else if !strings.Contains(err.Error(), "expected i32, got i64") {
		t.Errorf("expected return type mismatch, got: %v", err)
	}
	if got := runWasm(t, `function main(): i32 { let x: i32 = 1; let y: u32 = 2 as u32; return (x + y) as i32; }`); got != 3 {
		t.Errorf("mixed sum = %d, want 3", got)
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
		let sum = 0;
		for (let i = 0; i < 10; i = i + 1) {
			if (i < 5) { continue; }
			if (i == 8) { break; }
			sum = sum + i;
		}
		return sum;
	}`
	if got := runWasm(t, src); got != 18 {
		t.Errorf("got %d, want 18", got)
	}
}

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

func runWasmCapturingStdout(t *testing.T, src string) string {
	t.Helper()
	stdout, _ := invokeWasmtime(t, src)
	return stripMainResult(stdout)
}

func stripMainResult(stdout string) string {
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

func TestWASMForEachOverArray(t *testing.T) {
	src := `
		function main(): i32 {
			let sum: i32 = 0;
			for x in [10, 20, 30] {
				sum = sum + x;
			}
			return sum;
		}`
	if got := runWasm(t, src); got != 60 {
		t.Errorf("got %d, want 60 (10+20+30)", got)
	}
}

func TestWASMForEachBreakContinue(t *testing.T) {
	src := `
		function main(): i32 {
			let sum: i32 = 0;
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
		let a: i32[] = [10, 20, 30, 40];
		a = a.with(2, 100);
		return a[0] + a[1] + a[2] + a[3];
	}`
	if got := runWasm(t, src); got != 170 {
		t.Errorf("got %d, want 170 (10+20+100+40)", got)
	}
}

func TestWASMNestedArrayLits(t *testing.T) {
	src := `function main(): i32 {
		let inner: i32[] = [3, 4];
		let outer: i32[] = [1, 2, inner[0]];
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

func TestWASMIndirectCallStringParam(t *testing.T) {
	src := `
		function apply(f: (string, string) => i32, a: string, b: string): i32 {
			return f(a, b);
		}
		function main(): i32 {
			let cmp: (string, string) => i32 = (a: string, b: string) => a.len() + b.len();
			return apply(cmp, "hi", "world");
		}`
	if got := runWasm(t, src); got != 7 {
		t.Errorf("got %d, want 7 (len(\"hi\")+len(\"world\"))", got)
	}
}

func TestWASMIndirectCallSingleStringParam(t *testing.T) {
	src := `
		function apply(f: (string) => i32, a: string): i32 { return f(a); }
		function main(): i32 {
			let g: (string) => i32 = (a: string) => a.len() * 10;
			return apply(g, "hello");
		}`
	if got := runWasm(t, src); got != 50 {
		t.Errorf("got %d, want 50 (len(\"hello\")*10)", got)
	}
}

func TestWASMIndirectCallMixedScalarStringParam(t *testing.T) {
	src := `
		function apply(f: (i32, string) => i32, a: i32, b: string): i32 { return f(a, b); }
		function main(): i32 {
			let g: (i32, string) => i32 = (a: i32, b: string) => a + b.len();
			return apply(g, 3, "hello");
		}`
	if got := runWasm(t, src); got != 8 {
		t.Errorf("got %d, want 8 (3 + len(\"hello\"))", got)
	}
}

func TestWASMStringComparatorSort(t *testing.T) {
	src := `
		function sort_by(xs: string[], cmp: (string, string) => i32): string[] {
			let out: string[] = xs;
			let i: i32 = 0;
			while (i < out.len()) {
				let j: i32 = i + 1;
				while (j < out.len()) {
					if (cmp(out[j], out[i]) < 0) {
						let t: string = out[i];
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
			let xs: string[] = ["ccc", "a", "bb"];
			let s: string[] = sort_by(xs, by_len);
			if (s[0] == "a" && s[1] == "bb" && s[2] == "ccc") { return 0; }
			return 1;
		}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (string comparator sort by length)", got)
	}
}

func TestWASMArrayElemStringClosureArg(t *testing.T) {
	src := `
		function scmp(a: string, b: string): i32 { if (a.len() < b.len()) { return 0-1; } return 1; }
		function g(arr: string[], cmp: (string, string) => i32): i32 { return cmp(arr[0], "zz"); }
		function main(): i32 { let xs: string[] = ["a","bb"]; if (g(xs, scmp) == 0-1) { return 0; } return 1; }`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (scmp(\"a\",\"zz\")=-1) — #4816 string helper funcidx", got)
	}
}

func TestWASMGenericStringSortTwoInstantiations(t *testing.T) {
	src := `
		function asc(a: string, b: string): i32 { if (a.len() < b.len()) { return 0-1; } if (a.len() > b.len()) { return 1; } return 0; }
		function desc(a: string, b: string): i32 { if (a.len() > b.len()) { return 0-1; } if (a.len() < b.len()) { return 1; } return 0; }
		function sort_by[T](arr: T[], cmp: (T, T) => i32): T[] {
			let n: i32 = arr.len(); if (n < 2) { return arr; } let out: T[] = arr; let i: i32 = 1;
			while (i < n) { let j: i32 = i; while (j > 0 && cmp(out[j], out[j-1]) < 0) { let t: T = out[j]; out = out.with(j, out[j-1]); out = out.with(j-1, t); j = j - 1; } i = i + 1; }
			return out;
		}
		function main(): i32 {
			let xs: string[] = ["cc","a","bb"];
			let up: string[] = sort_by(xs, asc);
			let dn: string[] = sort_by(xs, desc);
			if (up[0] == "a" && dn[0].len() == 2) { return 0; }
			return 1;
		}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (two sort_by[string] instantiations) — #4816", got)
	}
}

func TestWASMFunctionValueInVar(t *testing.T) {
	src := `
		function dbl(x: i32): i32 { return x * 2; }
		function main(): i32 {
			let f = dbl;
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

func TestWASMOptionTryHappyPath(t *testing.T) {
	src := `
import "core/map";
function chained(m: Map[i32, i32], k: i32): Option[i32] {
    let v: i32 = m.get(k)?;
    return Some(v + 1);
}
function main(): i32 {
    let m: Map[i32, i32] = Map { 7: 100 };
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

func TestWASMOptionTryNoneEarlyReturn(t *testing.T) {
	src := `
import "core/map";
function chained(m: Map[i32, i32], k: i32): Option[i32] {
    let v: i32 = m.get(k)?;
    return Some(v + 1);
}
function main(): i32 {
    let m: Map[i32, i32] = Map { 7: 100 };
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

func TestWASMResultTryHappyPath(t *testing.T) {
	src := `function inner(n: i32): Result[i32, i32] {
    if (n < 0) { return Err(0 - n); }
    return Ok(n * 2);
}
function outer(n: i32): Result[i32, i32] {
    let v: i32 = inner(n)?;
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

func TestWASMPolyIntPromotesToF32Mul(t *testing.T) {
	src := `function main(): i32 {
    let r: f32 = 1.5f32;
    let s: f32 = r * 2;
    if (s == 3.0f32) { return 0; }
    return 1;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (1.5 * 2 = 3.0 as f32)", got)
	}
}

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
    let c: Shape = Circle(0.5f32);
    return classify(c);
}`
	if got := runWasm(t, src); got != 2 {
		t.Errorf("got %d, want 2 (positive Circle hits non-guarded arm)", got)
	}
}

func TestWASMPolyIntPromotesToF64(t *testing.T) {
	src := `function main(): i32 {
    let x: f64 = 100.5f64;
    let y: f64 = x - 100;
    if (y == 0.5f64) { return 0; }
    return 1;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (100.5 - 100 = 0.5 as f64)", got)
	}
}

func TestWASMNumericLiteralSuffixI64(t *testing.T) {
	src := `function main(): i32 {
    let n: i64 = 1000000i64;
    let m: i64 = n + 23i64;
    if (m == 1000023i64) { return 0; }
    return 1;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (i64 arithmetic via suffix)", got)
	}
}

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
    let c: Shape = Circle(0.5f32);
    return classify(c);
}`
	if got := runWasm(t, src); got != 2 {
		t.Errorf("got %d, want 2 (positive-radius Circle hits second arm)", got)
	}
}

func TestWASMArrayPushI64(t *testing.T) {
	src := `function main(): i32 {
    let xs: i64[] = [10i64, 20i64];
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

func TestWASMArrayPushF64(t *testing.T) {
	src := `function main(): i32 {
    let xs: f64[] = [1.5f64, 2.5f64];
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

func TestWASMArrayPushU8(t *testing.T) {
	src := `function main(): i32 {
    let xs: u8[] = [];
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

func TestWASMArrayPushI64EmptyStart(t *testing.T) {
	src := `function main(): i32 {
    let xs: i64[] = [];
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
    let xs: i32[] = [1, 2];
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
	src := `enum V { N, S(string) }
function main(): i32 {
    let xs: V[] = [];
    xs = xs.append(S("a"));
    xs = xs.append(S("bb"));
    return match (xs[1]) {
        S(s) => s.len(),
        _    => 0 - 1
    };
}`
	if got := runWasm(t, src); got != 2 {
		t.Errorf("got %d, want 2 ((\"bb\").len() after enum push)", got)
	}
}

func TestWASMArrayPushString(t *testing.T) {
	src := `function main(): i32 {
    let xs: string[] = ["a", "b"];
    xs = xs.append("c");
    xs = xs.append("d");
    return xs.len();
}`
	if got := runWasm(t, src); got != 4 {
		t.Errorf("got %d, want 4 (4 elements after pushes)", got)
	}
}

func TestWASMArrayPushStringValuesPreserved(t *testing.T) {
	src := `function main(): i32 {
    let xs: string[] = [];
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

func TestWASMMatchExprComposesInExpr(t *testing.T) {
	src := `function main(): i32 {
    let o: Option[i32] = Some(10);
    return 1 + match (o) {
        Some(x) => x * 2,
        None    => 0
    };
}`
	if got := runWasm(t, src); got != 21 {
		t.Errorf("got %d, want 21 (1 + 10*2)", got)
	}
}

func TestWASMResultTryErrPropagates(t *testing.T) {
	src := `function inner(n: i32): Result[i32, i32] {
    if (n < 0) { return Err(0 - n); }
    return Ok(n * 2);
}
function outer(n: i32): Result[i32, i32] {
    let v: i32 = inner(n)?;
    return Ok(v + 1);
}
function main(): i32 {
    match (outer(0 - 7)) {
        Ok(v)  => { return v; },
        Err(e) => { return e; }
    }
    return 0;
}`
	if got := runWasm(t, src); got != 7 {
		t.Errorf("got %d, want 7 (Err(7) propagated)", got)
	}
}

func TestWASMCompoundAssign(t *testing.T) {
	src := `function main(): i32 {
		let x: i32 = 1;
		x += 2;
		x *= 5;
		x -= 1;
		return x;
	}`
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
		let s: string = "abc";
		let byte: i32 = s[1] as i32;
		let equal: boolean = "yes" == "yes";
		let different: boolean = "yes" == "no";
		// 'b' = 98; equal=1, different=0 → 98 + 1 - 0 = 99
		let ok: i32 = 0;
		if (equal) { ok = ok + 1; }
		if (different) { ok = ok - 1; }
		return byte + ok;
	}`
	if got := runWasm(t, src); got != 99 {
		t.Errorf("got %d, want 99", got)
	}
}

func TestWASMStructBasic(t *testing.T) {
	src := `struct Point { x: i32, y: i32 }
		function main(): i32 {
			let p: Point = Point { x: 10, y: 32 };
			p = Point { ...p, x: p.x + 5 };
			return p.x + p.y;
		}`
	if got := runWasm(t, src); got != 47 {
		t.Errorf("got %d, want 47", got)
	}
}

func TestWASMStructFunctionalUpdate(t *testing.T) {
	src := `struct Box { v: i32 }
		function bump(b: Box): Box { return Box { ...b, v: b.v + 100 }; }
		function main(): i32 {
			let b: Box = Box { v: 5 };
			b = bump(b);
			return b.v;
		}`
	if got := runWasm(t, src); got != 105 {
		t.Errorf("got %d, want 105", got)
	}
}

func TestWASMStringConcat(t *testing.T) {
	src := `function main(): i32 {
		let a: string = "hello, ";
		let b: string = "world";
		let c: string = a + b;
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

func TestWASMEmptyStringSentinelConcat(t *testing.T) {
	src := `function main(): i32 {
		let s: string = "abcd";
		let a: str = slice_unchecked(s, 0, 0);
		let b: str = slice_unchecked(s, 0, 0);
		let c: string = a + b;
		return c.len();
	}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (empty + empty)", got)
	}
}

func TestWASMEmptyStringSentinelSlice(t *testing.T) {
	src := `function main(): i32 {
		let s: string = "abcd";
		let empty: str = slice_unchecked(s, 2, 2);
		return empty.len();
	}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (zero-width slice)", got)
	}
}

func TestWASMEmptyU8Sentinel(t *testing.T) {
	src := `function main(): i32 {
        let bs: u8[] = __alloc_u8(0);
        return bs.len();
    }`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (len of __alloc_u8(0))", got)
	}
}

func TestWASMEmptyStringSentinelFromBytes(t *testing.T) {
	src := `function main(): i32 {
		let bs: u8[] = __alloc_u8(0);
		let s: string = string_from_bytes_unchecked(bs);
		return s.len();
	}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (string_from_bytes_unchecked of empty u8[])", got)
	}
}

func TestWASMEmptyStringSentinelRoundtrip(t *testing.T) {
	src := `function main(): i32 {
		let s: string = "world";
		let empty: str = slice_unchecked(s, 0, 0);
		let greeting: string = "hello, " + empty + s;
		return greeting.len();
	}`
	if got := runWasm(t, src); got != 12 {
		t.Errorf("got %d, want 12 (\"hello, \" + \"\" + \"world\")", got)
	}
}

func runWasmExpectingTrap(t *testing.T, src string) (stdout, stderr string, ok bool) {
	t.Helper()
	p := buildWasmCore(t, src)
	s, e, ec := runWasmArtifact(t, p, runOpts{})
	return s, e, ec != 0
}

func TestWASMArrayOutOfBoundsTraps(t *testing.T) {
	src := `function main(): i32 {
		let a: i32[] = [1, 2, 3];
		return a[10];
	}`
	stdout, stderr, ok := runWasmExpectingTrap(t, src)
	if !ok {
		t.Errorf("expected wasmtime to trap on a[10], but it succeeded\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
}

func TestWASMNegativeIndexTraps(t *testing.T) {
	src := `function main(): i32 {
		let a: i32[] = [1, 2, 3];
		return a[0 - 1];
	}`
	if _, _, ok := runWasmExpectingTrap(t, src); !ok {
		t.Errorf("expected wasmtime to trap on a[-1], but it succeeded")
	}
}

func TestWASMInBoundsStillWorks(t *testing.T) {
	src := `function main(): i32 {
		let a: i32[] = [10, 20, 30];
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
		let f = makeAdder(7);
		return f(35);
	}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

func TestWASMClosureMultipleInstances(t *testing.T) {
	src := `function makeAdder(n: i32): (i32) => i32 {
		function add(x: i32): i32 { return x + n; }
		return add;
	}
	function main(): i32 {
		let add5 = makeAdder(5);
		let add10 = makeAdder(10);
		return add5(1) + add10(1);
	}`
	if got := runWasm(t, src); got != 17 {
		t.Errorf("got %d, want 17", got)
	}
}

func TestWASMClosureCapturesParamAndVar(t *testing.T) {
	src := `function outer(seed: i32): i32 {
		let bonus: i32 = 100;
		function inner(x: i32): i32 { return x + seed + bonus; }
		return inner(2);
	}
	function main(): i32 { return outer(40); }`
	if got := runWasm(t, src); got != 142 {
		t.Errorf("got %d, want 142", got)
	}
}

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

func TestWASMLambdaWithBodyLocals(t *testing.T) {
	src := `function main(): i32 {
    let greet = "hi";
    let f = (n: i32): i32 => {
        let sq = n * n;
        let tag = greet + "!";
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

func TestWASMLambdaCallsMethodOnCapturedString(t *testing.T) {
	src := `
import "std/string";
function main(): i32 {
    let s: string = "  hi  ";
    let f = (): string => { return s.trim().to_owned(); };
    let got = f();
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
    let xs: i32[] = [10, 20, 30, 40];
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
    let p: Pt = Pt { x: 10, y: 32 };
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

func TestWASMClosureCapturesI64(t *testing.T) {
	src := `function outer(seed: i64): i64 {
    function inner(): i64 { return seed + 1i64; }
    return inner();
}
function main(): i32 {
    let v: i64 = outer(1000000000000i64);
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
    let r: f64 = outer(3.5f64);
    if (r != 7.0f64) { return 1; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (f64 capture round-trip)", got)
	}
}

func TestWASMClosureCapturesMixedWidths(t *testing.T) {
	src := `function outer(a: i32, b: i64, c: i32): i64 {
    function inner(): i64 { return (a as i64) + b + (c as i64); }
    return inner();
}
function main(): i32 {
    let r: i64 = outer(7, 1000000000000i64, 3);
    if (r != 1000000000010i64) { return 1; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (mixed-width capture offsets)", got)
	}
}

func TestWASMClosureCapturesMixedPointers(t *testing.T) {
	src := `function outer(s: string, xs: i32[]): i32 {
    function inner(): i32 { return s.len() + xs.len(); }
    return inner();
}
function main(): i32 {
    let xs: i32[] = [1, 2, 3];
    return outer("hi", xs);
}`
	if got := runWasm(t, src); got != 5 {
		t.Errorf("got %d, want 5 (len(\"hi\")=2 + len([1,2,3])=3)", got)
	}
}

func TestWASMClosureCapturesTuple(t *testing.T) {
	src := `function build(): () => i64 {
    let t: (i64, i64) = (1000000000000i64, 2000000000000i64);
    function read(): i64 { return t.0 + t.1; }
    return read;
}
function main(): i32 {
    let f = build();
    if (f() != 3000000000000i64) { return 1; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (closure captures (i64, i64))", got)
	}
}

func TestWASMChainedClosureCallResult(t *testing.T) {
	src := `function makeMaker(n: i32): () => () => i32 {
    function level2(): () => i32 {
        function level1(): i32 { return n; }
        return level1;
    }
    return level2;
}
function main(): i32 {
    let m = makeMaker(42);
    return m()();
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42 (f()() chained)", got)
	}
}

func TestWASMTransitiveClosureCapture(t *testing.T) {
	src := `function makeChain(): () => () => () => i32 {
    let x: i32 = 42;
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
    let f3 = makeChain();
    let f2 = f3();
    let f1 = f2();
    return f1();
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42 (transitive capture through 3 levels)", got)
	}
}

func TestWASMClosureShadowedParamCapture(t *testing.T) {
	src := `function makeReader(n: i32): () => i32 {
    let n: i32 = n * 2;
    function build(): i32 { return n; }
    return build;
}
function main(): i32 {
    let f = makeReader(21);
    return f();
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42 (closure captures shadowed local)", got)
	}
}

func TestWASMCallClosureFromTupleElem(t *testing.T) {
	src := `function makeAdder(n: i32): (i32) => i32 {
    function add(x: i32): i32 { return x + n; }
    return add;
}
function main(): i32 {
    let t: ((i32) => i32, i32) = (makeAdder(10), 32);
    return (t.0)(t.1);
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42 (closure stored in tuple element)", got)
	}
}

func TestWASMCallClosurePatternBound(t *testing.T) {
	src := `function makeAdder(n: i32): (i32) => i32 {
    function add(x: i32): i32 { return x + n; }
    return add;
}
function main(): i32 {
    let o: Option[(i32) => i32] = Some(makeAdder(10));
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

func TestWASMCallClosureFromStructField(t *testing.T) {
	src := `struct Box { f: (i32) => i32 }
function makeAdder(n: i32): (i32) => i32 {
    function add(x: i32): i32 { return x + n; }
    return add;
}
function main(): i32 {
    let b: Box = Box { f: makeAdder(10) };
    return (b.f)(32);
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42 (closure stored in struct field)", got)
	}
}

func TestWASMClosureResultStructFieldAccess(t *testing.T) {
	src := `struct P { x: i32, y: i32 }
function makeReader(): () => P {
    function build(): P { return P { x: 10, y: 32 }; }
    return build;
}
function main(): i32 {
    let f = makeReader();
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
    let f = makeReader();
    return f().0 + f().1;
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

func TestWASMSiblingLocalFnCapture(t *testing.T) {
	src := `function makeMaker(): () => () => i32 {
    function inner(): i32 { return 42; }
    function outer(): () => i32 { return inner; }
    return outer;
}
function main(): i32 {
    let mk = makeMaker();
    let f = mk();
    return f();
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

func TestWASMLenOfClosureReturningString(t *testing.T) {
	src := `function makeReader(): () => string {
    function build(): string { return "hello"; }
    return build;
}
function main(): i32 {
    let f = makeReader();
    return (f()).len();
}`
	if got := runWasm(t, src); got != 5 {
		t.Errorf("got %d, want 5 (len of closure-returned string)", got)
	}
}

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
    let f = makeReader(Some(42));
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
    let f = makeReader(Some(42));
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
    let f = makeReader(Some(42));
    return f();
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

func TestWASMClosureFStringCapture(t *testing.T) {
	src := `
import "std/string";
function makeNamer(name: string): () => string {
    function build(): string { return f"hello, {name}!"; }
    return build;
}
function main(): i32 {
    let f = makeNamer("world");
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
    let f = makeMap(7, 42);
    let m = f();
    return m.get_or(7, 0);
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42 (Map { capK: capV } inside closure)", got)
	}
}

func TestWASMMutableCapturedVar(t *testing.T) {
	src := `function makeCounter(): () => i32 {
    let count: i32 = 0;
    function tick(): i32 {
        count = count + 1;
        return count;
    }
    return tick;
}
function main(): i32 {
    let c = makeCounter();
    let a: i32 = c();
    let b: i32 = c();
    let d: i32 = c();
    return a + b + d;
}`
	if got := runWasm(t, src); got != 6 {
		t.Errorf("got %d, want 6 (counter increments in env)", got)
	}
}

func TestWASMMutableCapturedI64(t *testing.T) {
	src := `function makeCounter(): () => i64 {
    let count: i64 = 0i64;
    function tick(): i64 {
        count = count + 1i64;
        return count;
    }
    return tick;
}
function main(): i32 {
    let c = makeCounter();
    let a: i64 = c();
    let b: i64 = c();
    if (a != 1i64) { return 1; }
    if (b != 2i64) { return 2; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}

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
    let a = makeAdder(10);
    let ap = makeApplier(a);
    return ap(5);
}`
	if got := runWasm(t, src); got != 16 {
		t.Errorf("got %d, want 16", got)
	}
}

func TestWASMClosureRecursiveSelfCall(t *testing.T) {
	src := `function makeFact(): (i32) => i32 {
    function fact(n: i32): i32 {
        if (n <= 1) { return 1; }
        return n * fact(n - 1);
    }
    return fact;
}
function main(): i32 {
    let f = makeFact();
    return f(5);
}`
	if got := runWasm(t, src); got != 120 {
		t.Errorf("got %d, want 120 (5!)", got)
	}
}

func TestWASMMethodOnStruct(t *testing.T) {
	src := `struct Point { x: i32, y: i32 }
		function (p: Point) sum(): i32 { return p.x + p.y; }
		function main(): i32 {
			let p: Point = Point { x: 10, y: 32 };
			return p.sum();
		}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

func TestWASMMethodWithExtraArg(t *testing.T) {
	src := `struct Box { v: i32 }
		function (b: Box) shifted(n: i32): i32 { return b.v + n; }
		function main(): i32 {
			let b: Box = Box { v: 5 };
			return b.shifted(37);
		}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

func TestWASMEnumMatchPayload(t *testing.T) {
	src := `enum Pair { Two(i32, i32) }
		function main(): i32 {
			let p: Pair = Two(7, 5);
			match (p) {
				Two(a, b) => { return a + b; }
			}
			return -1;
		}`
	if got := runWasm(t, src); got != 12 {
		t.Errorf("got %d, want 12", got)
	}
}

func TestWASMEnumMatchPayloadI64(t *testing.T) {
	src := `function main(): i32 {
    let o: Option[i64] = Some(4294967296 as i64);
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

func TestWASMEnumMatchPayloadF64(t *testing.T) {
	src := `function main(): i32 {
    let o: Option[f64] = Some(3.5 as f64);
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

func TestWASMEnumPayloadInferredI64(t *testing.T) {
	src := `function main(): i32 {
    let o: Option[i64] = Some(4294967296);
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

func TestWASMEnumPayloadInferredF64(t *testing.T) {
	src := `function main(): i32 {
    let o: Option[f64] = Some(3.5);
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

func TestWASMEnumMatchPayloadWideMixed(t *testing.T) {
	src := `enum Wide { W(i64, i32) }
function main(): i32 {
    let w: Wide = W(8589934592, 7);
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

func TestWASMIfLetPayloadI64(t *testing.T) {
	src := `function main(): i32 {
    let o: Option[i64] = Some(4294967296 as i64);
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

func TestWASMLetElsePayloadI64(t *testing.T) {
	src := `function main(): i32 {
    let o: Option[i64] = Some(4294967296 as i64);
    let Some(n) = o else { return -1; };
    if (n == (4294967296 as i64)) { return 1; }
    return 0;
}`
	if got := runWasm(t, src); got != 1 {
		t.Errorf("got %d, want 1 (let-else Option[i64])", got)
	}
}

func TestWASMEnumMatchDispatch(t *testing.T) {
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

func TestWASMGenericResult(t *testing.T) {
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

func TestWASMRandomBytes(t *testing.T) {
	src := `function main(): i32 {
		let a: u8[] = random_bytes(16);
		let b: u8[] = random_bytes(16);
		if (a.len() != 16) { return 1; }
		if (b.len() != 16) { return 2; }
		let same: i32 = 1;
		let i: i32 = 0;
		while (i < 16) {
			if ((a[i] as i32) != (b[i] as i32)) { same = 0; }
			i = i + 1;
		}
		if (same == 1) { return 3; }
		return 0;
	}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("WASM random_bytes: exit = %d, want 0", got)
	}
}

func TestWASMRandomI32(t *testing.T) {
	src := `function main(): i32 {
		let a: i32 = random_i32();
		let b: i32 = random_i32();
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

func runWasmInDir(t *testing.T, src string, seed map[string]string) (stdout, stderr string, exitCode int, dir string) {
	t.Helper()
	return runWasmInDirOpts(t, src, seed, runOpts{})
}

func runWasmInDirOpts(t *testing.T, src string, seed map[string]string, opts runOpts) (stdout, stderr string, exitCode int, dir string) {
	t.Helper()
	p := buildWasmCore(t, src)
	dir = t.TempDir()
	for name, content := range seed {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	opts.workDir = dir
	s, e, ec := runWasmArtifact(t, p, opts)
	return s, e, ec, dir
}

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

func TestWASMReadFileBytesOk(t *testing.T) {
	src := `function main(): i32 {
		match (read_file_bytes("blob.bin")) {
			Ok(b) => {
				if (b.len() != 4) { write("FAIL len"); return 1; }
				if ((b[0] as i32) != 0) { write("FAIL b0"); return 2; }
				if ((b[1] as i32) != 255) { write("FAIL b1"); return 3; }
				if ((b[2] as i32) != 128) { write("FAIL b2"); return 4; }
				if ((b[3] as i32) != 10) { write("FAIL b3"); return 5; }
				write("bytes-ok");
				return 0;
			},
			Err(_) => { write("FAIL err"); return 6; }
		}
		return 7;
	}`
	stdout, _, _, _ := runWasmInDir(t, src, map[string]string{
		"blob.bin": "\x00\xff\x80\n",
	})
	if !strings.Contains(stdout, "bytes-ok") {
		t.Errorf("expected bytes-ok; got %q", stdout)
	}
}

func TestWASMReadFileBytesNotFound(t *testing.T) {
	src := `function main(): i32 {
		match (read_file_bytes("does_not_exist.bin")) {
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
	if !strings.Contains(stdout, "does_not_exist.bin") {
		t.Errorf("stdout should echo the missing path; got %q", stdout)
	}
}

func TestWASMReadFileInvalidUtf8(t *testing.T) {
	src := `function main(): i32 {
		match (read_file("valid.txt")) {
			Ok(s) => { if (s.len() != 15) { write("FAIL valid-len"); return 10; } },
			Err(_) => { write("FAIL valid-err"); return 11; }
		}
		match (read_file("bad.bin")) {
			Ok(_) => { write("FAIL accepted"); return 1; },
			Err(e) => {
				match (e) {
					InvalidUtf8(p) => { if (p != "bad.bin") { write("FAIL path"); return 2; } },
					_ => { write("FAIL variant"); return 3; }
				}
			}
		}
		match (read_file_bytes("bad.bin")) {
			Ok(b) => { if (b.len() != 2) { write("FAIL bytes-len"); return 4; } },
			Err(_) => { write("FAIL bytes-err"); return 5; }
		}
		write("invalid-utf8-ok");
		return 0;
	}`
	stdout, _, _, _ := runWasmInDir(t, src, map[string]string{
		"valid.txt": "h\xc3\xa9llo \xe2\x82\xac \xf0\x9f\x99\x82",
		"bad.bin":   "\xff\xfe",
	})
	if !strings.Contains(stdout, "invalid-utf8-ok") {
		t.Errorf("expected invalid-utf8-ok; got %q", stdout)
	}
}

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

func TestWASMStdStreams(t *testing.T) {
	src := `function main(): i32 {
		match (stdout().write("out\n")) { Err(_) => { return 1; }, Ok(_) => {} }
		match (stderr().write("err\n")) { Err(_) => { return 2; }, Ok(_) => {} }
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

func TestWASMStreamingRoundtrip(t *testing.T) {
	src := `import "std/io";

function main(): i32 {
		match (open_writer("out.txt")) {
			Ok(w) => {
				match (w.write("line 1\n")) { Err(_) => { return 1; }, Ok(_) => {} }
				match (w.write("line 2\n")) { Err(_) => { return 2; }, Ok(_) => {} }
				match (w.close()) { Err(_) => { return 3; }, Ok(_) => {} }
			},
			Err(_) => { return 4; }
		}
		match (open_reader("out.txt")) {
			Ok(r) => {
				match (io.read_line(r)) { Ok(Some(line)) => { write(line); }, _ => { return 5; } }
				match (io.read_line(r)) { Ok(Some(line)) => { write(line); }, _ => { return 6; } }
				match (io.read_line(r)) { Ok(None()) => {}, _ => { return 7; } }
				match (r.close()) { Err(_) => { return 8; }, Ok(_) => {} }
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

func TestWASMReaderReadChunk(t *testing.T) {
	src := `function main(): i32 {
		match (open_writer("rc.txt")) {
			Ok(w) => {
				match (w.write("hello world")) { Err(_) => { return 1; }, Ok(_) => {} }
				match (w.close()) { Err(_) => { return 2; }, Ok(_) => {} }
			},
			Err(_) => { return 3; }
		}
		match (open_reader("rc.txt")) {
			Ok(r) => {
				match (r.read_chunk(5)) {
					Ok(s) => { write(s); write(":"); },
					Err(_) => { return 4; }
				}
				match (r.read_chunk(20)) {
					Ok(s) => { write(s); },
					Err(_) => { return 5; }
				}
				match (r.read_chunk(20)) {
					Ok(s) => { if (s.len() > 0) { return 6; } return 0; },
					Err(_) => { return 8; }
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

func TestWASMOpenAppender(t *testing.T) {
	src := `function main(): i32 {
		match (open_writer("ap.txt")) {
			Ok(w) => {
				match (w.write("first")) { Err(_) => { return 1; }, Ok(_) => {} }
				match (w.close()) { Err(_) => { return 2; }, Ok(_) => {} }
			},
			Err(_) => { return 3; }
		}
		match (open_appender("ap.txt")) {
			Ok(w) => {
				match (w.write("-second")) { Err(_) => { return 4; }, Ok(_) => {} }
				match (w.close()) { Err(_) => { return 5; }, Ok(_) => {} }
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

func TestWASMHex(t *testing.T) {
	src := `
import "core/int";
import "std/hex";
import "std/codec";
import "std/string";
function main(): i32 {
		// empty round-trips to empty
		if ((hex.encode("".bytes())).len() != 0) { return 1; }
		if ((__codec_bytes(hex.decode(""))).len() != 0) { return 2; }

		// "hi" -> "6869"
		if (hex.encode("hi".bytes()) != "6869") { return 3; }
		if (string_from_bytes_unchecked(__codec_bytes(hex.decode("6869"))) != "hi") { return 4; }

		// every nibble: byte 0xab -> "ab"
		if (hex.encode("hello world".bytes()) != "68656c6c6f20776f726c64") { return 5; }
		if (string_from_bytes_unchecked(__codec_bytes(hex.decode("68656c6c6f20776f726c64"))) != "hello world") { return 6; }

		// uppercase hex digits decode the same
		if (string_from_bytes_unchecked(__codec_bytes(hex.decode("48454C4C4F"))) != "HELLO") { return 7; }

		// Odd lengths and invalid bytes reject the complete input.
		// The error offset points to the end of an incomplete pair.
		match (hex.decode("414")) { Err(e) => { if (e.offset != 3 || e.kind != codec.InvalidLength) { return 8; } }, Ok(_) => { return 8; } }
		// Invalid-byte errors identify the first invalid byte.
		match (hex.decode("41xx")) { Err(e) => { if (e.offset != 2 || e.kind != codec.InvalidByte(120 as u8)) { return 9; } }, Ok(_) => { return 9; } }

		return 0;
	}
// Decode a valid fixture. Report a malformed fixture before ending the test.
function __codec_bytes(value: Result[u8[], codec.DecodeError]): u8[] {
  match (value) {
    Ok(bytes) => { return bytes; },
    Err(problem) => { eprint(problem.message()); exit(98); return []; }
  }
}
`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("WASM hex: exit = %d, want 0", got)
	}
}

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

func TestWASMQueryParse(t *testing.T) {
	src := `
import "core/int";
import "core/map";
import "std/url";
function main(): i32 {
		// Standard pairs — each unique key has a 1-element
		// string[] containing the decoded value.
		let m: Map[string, string[]] = url.query_parse("a=1&b=2&c=hello%20world");
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
		let m2: Map[string, string[]] = url.query_parse("k%3D%26=v");
		match (m2.get("k=&")) {
			Some(arr) => { if (arr[0] != "v") { return 10; } },
			None => { return 11; }
		}

		// Pair without '=' -> single-element empty value.
		let m3: Map[string, string[]] = url.query_parse("flag&x=1");
		if (m3.len() != 2) { return 20; }
		match (m3.get("flag")) {
			Some(arr) => {
				if (arr.len() != 1) { return 21; }
				if (arr[0] != "") { return 22; }
			},
			None => { return 23; }
		}

		// Empty input -> empty map.
		let m4: Map[string, string[]] = url.query_parse("");
		if (m4.len() != 0) { return 30; }

		// Trailing '&' is ignored.
		let m5: Map[string, string[]] = url.query_parse("a=1&");
		if (m5.len() != 1) { return 40; }

		// Duplicate keys: all values preserved in order.
		let m6: Map[string, string[]] = url.query_parse("tag=a&tag=b&tag=c");
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
		let m7: Map[string, string[]] = url.query_parse("k=1&j=x&k=2");
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

func TestWASMJsonEncode(t *testing.T) {
	src := `
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
		if (json.json_encode(JObject(json.object())) != "{}") { return 20; }

		// Heterogeneous array.
		let a: json.JsonValue[] = [JNumber("1"), JString("two"), JBool(true), JNull];
		if (json.json_encode(JArray(a)) != "[1,\"two\",true,null]") { return 30; }

		// Object — insertion order preserved.
		let m: json.JsonObject = json.object();
		m = m.insert("name", JString("alice"));
		m = m.insert("age", JNumber("30"));
		m = m.insert("admin", JBool(false));
		if (json.json_encode(JObject(m)) != "{\"name\":\"alice\",\"age\":30,\"admin\":false}") {
			return 40;
		}

		// Nested: object containing an array of numbers.
		let inner: json.JsonValue[] = [JNumber("1"), JNumber("2"), JNumber("3")];
		let outer: json.JsonObject = json.object();
		outer = outer.insert("nums", JArray(inner));
		if (json.json_encode(JObject(outer)) != "{\"nums\":[1,2,3]}") { return 50; }

		return 0;
	}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("WASM json_encode: exit = %d, want 0", got)
	}
}

func TestWASMJsonParse(t *testing.T) {
	src := `
import "std/json";
function main(): i32 {
		// Primitives.
		match (json.parse("null")) {
			Ok(v) => { match (v) { JNull => {}, JBool(_) => { return 1; }, JNumber(_) => { return 1; }, JString(_) => { return 1; }, JArray(_) => { return 1; }, JObject(_) => { return 1; } } },
			Err(_) => { return 2; }
		}
		match (json.parse("true")) {
			Ok(v) => { match (v) {
				JBool(b) => { if (!b) { return 3; } },
				JNull => { return 4; }, JNumber(_) => { return 4; }, JString(_) => { return 4; }, JArray(_) => { return 4; }, JObject(_) => { return 4; }
			} },
			Err(_) => { return 5; }
		}
		match (json.parse("false")) {
			Ok(v) => { match (v) {
				JBool(b) => { if (b) { return 6; } },
				JNull => { return 7; }, JNumber(_) => { return 7; }, JString(_) => { return 7; }, JArray(_) => { return 7; }, JObject(_) => { return 7; }
			} },
			Err(_) => { return 8; }
		}
		match (json.parse("42")) {
			Ok(v) => { match (v) {
				JNumber(n) => { if (n != "42") { return 10; } },
				JNull => { return 11; }, JBool(_) => { return 11; }, JString(_) => { return 11; }, JArray(_) => { return 11; }, JObject(_) => { return 11; }
			} },
			Err(_) => { return 12; }
		}
		match (json.parse("-3.14")) {
			Ok(v) => { match (v) {
				JNumber(n) => { if (n != "-3.14") { return 13; } },
				JNull => { return 14; }, JBool(_) => { return 14; }, JString(_) => { return 14; }, JArray(_) => { return 14; }, JObject(_) => { return 14; }
			} },
			Err(_) => { return 15; }
		}
		match (json.parse("\"hi\"")) {
			Ok(v) => { match (v) {
				JString(s) => { if (s != "hi") { return 20; } },
				JNull => { return 21; }, JBool(_) => { return 21; }, JNumber(_) => { return 21; }, JArray(_) => { return 21; }, JObject(_) => { return 21; }
			} },
			Err(_) => { return 22; }
		}
		// String with escapes.
		match (json.parse("\"a\\nb\\\"c\"")) {
			Ok(v) => { match (v) {
				JString(s) => { if (s != "a\nb\"c") { return 30; } },
				JNull => { return 31; }, JBool(_) => { return 31; }, JNumber(_) => { return 31; }, JArray(_) => { return 31; }, JObject(_) => { return 31; }
			} },
			Err(_) => { return 32; }
		}
		// Empty array.
		match (json.parse("[]")) {
			Ok(v) => { match (v) {
				JArray(arr) => { if (arr.len() != 0) { return 40; } },
				JNull => { return 41; }, JBool(_) => { return 41; }, JNumber(_) => { return 41; }, JString(_) => { return 41; }, JObject(_) => { return 41; }
			} },
			Err(_) => { return 42; }
		}
		// Heterogeneous array.
		match (json.parse("[1,\"two\",true,null]")) {
			Ok(v) => { match (v) {
				JArray(arr) => {
					if (arr.len() != 4) { return 50; }
					match (arr[0]) { JNumber(n) => { if (n != "1") { return 51; } }, JNull => { return 52; }, JBool(_) => { return 52; }, JString(_) => { return 52; }, JArray(_) => { return 52; }, JObject(_) => { return 52; } }
					match (arr[1]) { JString(s) => { if (s != "two") { return 53; } }, JNull => { return 54; }, JBool(_) => { return 54; }, JNumber(_) => { return 54; }, JArray(_) => { return 54; }, JObject(_) => { return 54; } }
				},
				JNull => { return 55; }, JBool(_) => { return 55; }, JNumber(_) => { return 55; }, JString(_) => { return 55; }, JObject(_) => { return 55; }
			} },
			Err(_) => { return 56; }
		}
		// Empty object.
		match (json.parse("{}")) {
			Ok(v) => { match (v) {
				JObject(m) => { if (m.len() != 0) { return 60; } },
				JNull => { return 61; }, JBool(_) => { return 61; }, JNumber(_) => { return 61; }, JString(_) => { return 61; }, JArray(_) => { return 61; }
			} },
			Err(_) => { return 62; }
		}
		// Object with string keys, mixed values.
		match (json.parse("{\"a\":1,\"b\":\"two\"}")) {
			Ok(v) => { match (v) {
				JObject(m) => {
					if (m.len() != 2) { return 70; }
					match (m.get("a")) {
						Some(av) => { match (av) { JNumber(n) => { if (n != "1") { return 71; } }, JNull => { return 72; }, JBool(_) => { return 72; }, JString(_) => { return 72; }, JArray(_) => { return 72; }, JObject(_) => { return 72; } } },
						None => { return 73; }
					}
				},
				JNull => { return 74; }, JBool(_) => { return 74; }, JNumber(_) => { return 74; }, JString(_) => { return 74; }, JArray(_) => { return 74; }
			} },
			Err(_) => { return 75; }
		}
		// Whitespace tolerance.
		match (json.parse("  [ 1 , 2 ] ")) {
			Ok(v) => { match (v) {
				JArray(arr) => { if (arr.len() != 2) { return 80; } },
				JNull => { return 81; }, JBool(_) => { return 81; }, JNumber(_) => { return 81; }, JString(_) => { return 81; }, JObject(_) => { return 81; }
			} },
			Err(_) => { return 82; }
		}
		// Garbage -> None.
		match (json.parse("xyz")) { Ok(_) => { return 90; }, Err(_) => {} }
		match (json.parse("[1,]")) { Ok(_) => { return 91; }, Err(_) => {} }
		match (json.parse("{")) { Ok(_) => { return 92; }, Err(_) => {} }
		match (json.parse("")) { Ok(_) => { return 93; }, Err(_) => {} }
		return 0;
	}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("WASM parse: exit = %d, want 0", got)
	}
}

func TestWASMJsonParseSurrogatePairs(t *testing.T) {
	src := "" +
		"import \"std/json\";\n" +
		"function main(): i32 {\n" +
		"    // Astral pair: \\uD83D\\uDE00 = U+1F600 = F0 9F 98 80.\n" +
		"    match (json.parse(\"\\\"\\\\uD83D\\\\uDE00\\\"\")) {\n" +
		"        Ok(v) => { match (v) {\n" +
		"            JString(s) => {\n" +
		"                if (s.len() != 4) { return 1; }\n" +
		"                if ((s[0] as i32) != 240) { return 2; }\n" +
		"                if ((s[1] as i32) != 159) { return 3; }\n" +
		"                if ((s[2] as i32) != 152) { return 4; }\n" +
		"                if ((s[3] as i32) != 128) { return 5; }\n" +
		"            },\n" +
		"            JNull => { return 6; }, JBool(_) => { return 6; }, JNumber(_) => { return 6; }, JArray(_) => { return 6; }, JObject(_) => { return 6; }\n" +
		"        } },\n" +
		"        Err(_) => { return 7; }\n" +
		"    }\n" +
		"    // Lone high surrogate: emits U+FFFD = EF BF BD.\n" +
		"    match (json.parse(\"\\\"\\\\uD800x\\\"\")) {\n" +
		"        Ok(v) => { match (v) {\n" +
		"            JString(s) => {\n" +
		"                if (s.len() != 4) { return 8; }\n" +
		"                if ((s[0] as i32) != 239) { return 9; }\n" +
		"                if ((s[1] as i32) != 191) { return 10; }\n" +
		"                if ((s[2] as i32) != 189) { return 11; }\n" +
		"                if ((s[3] as i32) != 120) { return 12; }   // 'x'\n" +
		"            },\n" +
		"            JNull => { return 13; }, JBool(_) => { return 13; }, JNumber(_) => { return 13; }, JArray(_) => { return 13; }, JObject(_) => { return 13; }\n" +
		"        } },\n" +
		"        Err(_) => { return 14; }\n" +
		"    }\n" +
		"    // Lone low surrogate: emits U+FFFD too.\n" +
		"    match (json.parse(\"\\\"\\\\uDC00\\\"\")) {\n" +
		"        Ok(v) => { match (v) {\n" +
		"            JString(s) => {\n" +
		"                if (s.len() != 3) { return 15; }\n" +
		"                if ((s[0] as i32) != 239) { return 16; }\n" +
		"                if ((s[1] as i32) != 191) { return 17; }\n" +
		"                if ((s[2] as i32) != 189) { return 18; }\n" +
		"            },\n" +
		"            JNull => { return 19; }, JBool(_) => { return 19; }, JNumber(_) => { return 19; }, JArray(_) => { return 19; }, JObject(_) => { return 19; }\n" +
		"        } },\n" +
		"        Err(_) => { return 20; }\n" +
		"    }\n" +
		"    return 0;\n" +
		"}"
	if got := runWasm(t, src); got != 0 {
		t.Errorf("parse surrogate pairs: exit = %d, want 0", got)
	}
}

func TestWASMFloatToString(t *testing.T) {
	src := `
import "std/f64";
function main(): i32 {
		// Integer values lose the decimal point entirely.
		let a: f32 = 0.0;
		if (a.to_string() != "0") { return 1; }
		let b: f32 = 42.0;
		if (b.to_string() != "42") { return 2; }
		let c: f32 = -7.0;
		if (c.to_string() != "-7") { return 3; }

		// Common fractional values.
		let d: f32 = 0.5;
		if (d.to_string() != "0.5") { return 10; }
		let e: f32 = -0.25;
		if (e.to_string() != "-0.25") { return 11; }

		// f64 renders shortest-round-trip, same as f32.
		let f: f64 = 0.5;
		if (f.to_string() != "0.5") { return 20; }
		let g: f64 = 1.5;
		if (g.to_string() != "1.5") { return 21; }

		// Round-trip through parse_float (f64 domain) for tolerance check.
		match (f64.parse_f64("3.14")) {
			Ok(x) => {
				// x.to_string() should produce something that
				// parses back to ≈ 3.14.
				match (f64.parse_f64(x.to_string())) {
					Ok(y) => {
						let diff: f64 = y - 3.14;
						if (diff < 0.0) { diff = 0.0 - diff; }
						if (diff > 0.001) { return 30; }
					},
					Err(_) => { return 31; }
				}
			},
			Err(_) => { return 32; }
		}

		return 0;
	}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("WASM float to_string: exit = %d, want 0", got)
	}
}

func TestWASMTailCall(t *testing.T) {
	src := `function sum_to(n: i32, acc: i32): i32 {
    if (n == 0) { return acc; }
    return sum_to(n - 1, acc + n);
}
function main(): i32 {
    return sum_to(100000, 0);
}`
	if got := runWasm(t, src); got != 705082704 {
		t.Errorf("sum_to(100000, 0) → %d, want 705082704", got)
	}
}

func TestWASMNoPreludeStdlibImports(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
	}{
		{"i32_string_cycle", `
import "std/i32";
function main(): i32 {
    let s: string = (42).to_string_padded(6);
    if (s == "000042") { return 0; }
    return 1;
}`},
		{"array_method_chain", `
import "std/array";
function main(): i32 {
    let xs: i32[] = [0 - 3, 4, 0 - 1];
    let ys = xs.abs_each();
    if (ys[0] + ys[1] + ys[2] == 8) { return 0; }
    return 1;
}`},
		{"qualified_int_call", `
import "core/int";
function main(): i32 {
    let s: string = int.int_to_string_radix(255, 16);
    if (s == "ff") { return 0; }
    return 1;
}`},
		{"mixed_stdlib", `
import "std/i32";
import "std/string";
import "std/array";
function main(): i32 {
    let s: string = (0 - 42).to_string();
    if (s != "-42") { return 1; }
    let strs: string[] = ["b", "a", "c"];
    let joined: string = strs.join(",");
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

func TestWASMLeb128Uleb(t *testing.T) {
	src := `import "std/wasm/leb128";
function main(): i32 {
    let empty: u8[] = [];

    // 0 -> [0x00]
    let b1: u8[] = leb128.uleb_u32(empty, 0u32);
    if (b1.len() != 1) { return 1; }
    if (b1[0] != 0u8) { return 2; }

    // 127 -> [0x7F]
    let b2: u8[] = leb128.uleb_u32(empty, 127u32);
    if (b2.len() != 1) { return 10; }
    if (b2[0] != 127u8) { return 11; }

    // 128 -> [0x80, 0x01]
    let b3: u8[] = leb128.uleb_u32(empty, 128u32);
    if (b3.len() != 2) { return 20; }
    if (b3[0] != 128u8) { return 21; }
    if (b3[1] != 1u8) { return 22; }

    // 624485 -> [0xE5, 0x8E, 0x26]
    let b4: u8[] = leb128.uleb_u32(empty, 624485u32);
    if (b4.len() != 3) { return 30; }
    if (b4[0] != 229u8) { return 31; }
    if (b4[1] != 142u8) { return 32; }
    if (b4[2] != 38u8) { return 33; }

    // u32 max -> [0xFF, 0xFF, 0xFF, 0xFF, 0x0F]
    let b5: u8[] = leb128.uleb_u32(empty, 4294967295u32);
    if (b5.len() != 5) { return 40; }
    if (b5[0] != 255u8) { return 41; }
    if (b5[1] != 255u8) { return 42; }
    if (b5[2] != 255u8) { return 43; }
    if (b5[3] != 255u8) { return 44; }
    if (b5[4] != 15u8) { return 45; }

    // Append preserves seed bytes.
    let seed: u8[] = [10u8, 20u8];
    let b6: u8[] = leb128.uleb_u32(seed, 128u32);
    if (b6.len() != 4) { return 50; }
    if (b6[0] != 10u8) { return 51; }
    if (b6[1] != 20u8) { return 52; }
    if (b6[2] != 128u8) { return 53; }
    if (b6[3] != 1u8) { return 54; }

    // u64 path with a value above 2^32: 8589934592 (= 2^33).
    // Encoding: [0x80, 0x80, 0x80, 0x80, 0x20].
    let b7: u8[] = leb128.uleb_u64(empty, 8589934592u64);
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

func TestWASMLeb128Sleb(t *testing.T) {
	src := `import "std/wasm/leb128";
function main(): i32 {
    let empty: u8[] = [];

    // 0 -> [0x00]
    let b1: u8[] = leb128.sleb_i32(empty, 0);
    if (b1.len() != 1) { return 1; }
    if (b1[0] != 0u8) { return 2; }

    // -1 -> [0x7F]   (the canonical "all-ones" sleb terminator)
    let b2: u8[] = leb128.sleb_i32(empty, 0 - 1);
    if (b2.len() != 1) { return 10; }
    if (b2[0] != 127u8) { return 11; }

    // 63 -> [0x3F]   (largest single-byte positive)
    let b3: u8[] = leb128.sleb_i32(empty, 63);
    if (b3.len() != 1) { return 20; }
    if (b3[0] != 63u8) { return 21; }

    // 64 -> [0xC0, 0x00]   (bit-6 set forces a continuation byte)
    let b4: u8[] = leb128.sleb_i32(empty, 64);
    if (b4.len() != 2) { return 30; }
    if (b4[0] != 192u8) { return 31; }
    if (b4[1] != 0u8) { return 32; }

    // -64 -> [0x40]   (smallest single-byte negative)
    let b5: u8[] = leb128.sleb_i32(empty, 0 - 64);
    if (b5.len() != 1) { return 40; }
    if (b5[0] != 64u8) { return 41; }

    // -65 -> [0xBF, 0x7F]
    let b6: u8[] = leb128.sleb_i32(empty, 0 - 65);
    if (b6.len() != 2) { return 50; }
    if (b6[0] != 191u8) { return 51; }
    if (b6[1] != 127u8) { return 52; }

    // -123456 -> [0xC0, 0xBB, 0x78]   (multi-byte negative)
    let b7: u8[] = leb128.sleb_i32(empty, 0 - 123456);
    if (b7.len() != 3) { return 60; }
    if (b7[0] != 192u8) { return 61; }
    if (b7[1] != 187u8) { return 62; }
    if (b7[2] != 120u8) { return 63; }

    // i64 wide value: 8589934592 (= 2^33).
    // Encoding: [0x80, 0x80, 0x80, 0x80, 0x20].
    let b8: u8[] = leb128.sleb_i64(empty, 8589934592i64);
    if (b8.len() != 5) { return 70; }
    if (b8[0] != 128u8) { return 71; }
    if (b8[4] != 32u8) { return 75; }

    // i64 negative wide value: -8589934592.
    // Encoding: [0x80, 0x80, 0x80, 0x80, 0x60].
    let b9: u8[] = leb128.sleb_i64(empty, 0i64 - 8589934592i64);
    if (b9.len() != 5) { return 80; }
    if (b9[0] != 128u8) { return 81; }
    if (b9[4] != 96u8) { return 85; }

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("sleb encoder: exit = %d, want 0", got)
	}
}

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

func TestWASMEncodePreamble(t *testing.T) {
	src := `import "std/wasm/encode";
function main(): i32 {
    let empty: u8[] = [];

    // Module preamble: \0asm 0x01000000.
    let hdr: u8[] = encode.put_module_header(empty);
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
    let le: u8[] = encode.put_u32_le(empty, 305419896u32);
    if (le.len() != 4) { return 20; }
    if (le[0] != 120u8) { return 21; }   // 0x78
    if (le[1] != 86u8) { return 22; }    // 0x56
    if (le[2] != 52u8) { return 23; }    // 0x34
    if (le[3] != 18u8) { return 24; }    // 0x12

    // put_u32_le on a seeded buffer appends, not replaces.
    let seed: u8[] = [255u8];
    let le2: u8[] = encode.put_u32_le(seed, 1u32);
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

func TestWASMEncodeNameAndSection(t *testing.T) {
	src := `import "std/wasm/encode";
function main(): i32 {
    let empty: u8[] = [];

    // Empty name: uleb(0) only.
    let n0: u8[] = encode.put_name(empty, "");
    if (n0.len() != 1) { return 1; }
    if (n0[0] != 0u8) { return 2; }

    // "hi" -> [0x02, 'h', 'i']
    let n1: u8[] = encode.put_name(empty, "hi");
    if (n1.len() != 3) { return 10; }
    if (n1[0] != 2u8) { return 11; }
    if (n1[1] != 104u8) { return 12; }   // 'h'
    if (n1[2] != 105u8) { return 13; }   // 'i'

    // Empty section body: id + uleb(0) = 2 bytes.
    let s0: u8[] = encode.put_section(empty, encode.section_type(), empty);
    if (s0.len() != 2) { return 20; }
    if (s0[0] != 1u8) { return 21; }     // section_type id
    if (s0[1] != 0u8) { return 22; }     // size 0

    // Non-empty section body: id + uleb(2) + body.
    let body: u8[] = [170u8, 187u8];     // 0xAA 0xBB
    let s1: u8[] = encode.put_section(empty, encode.section_function(), body);
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

func TestWASMEncodeMinimalModule(t *testing.T) {
	src := `import "std/wasm/encode";
import "std/wasm/leb128";
function main(): i32 {
    let bytes: u8[] = [];
    bytes = encode.put_module_header(bytes);

    // Build the type section body: vec(functype) count=1, then
    // one functype "(i32) -> (i32)".
    let type_body: u8[] = [];
    type_body = leb128.uleb_u32(type_body, 1u32);
    let params: u8[] = [encode.valtype_i32()];
    let results: u8[] = [encode.valtype_i32()];
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
    let ft2: u8[] = [];
    let ps: u8[] = [encode.valtype_i32(), encode.valtype_i64()];
    let rs: u8[] = [];
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

func TestWASMInstConsts(t *testing.T) {
	src := `import "std/wasm/inst";
function main(): i32 {
    let empty: u8[] = [];

    // i32.const 0 -> 0x41 0x00
    let c0: u8[] = inst.inst_i32_const(empty, 0);
    if (c0.len() != 2) { return 1; }
    if (c0[0] != 65u8) { return 2; }
    if (c0[1] != 0u8) { return 3; }

    // i32.const 63 -> 0x41 0x3F (largest single-byte sleb positive
    // — bit-6 clear so no continuation needed).
    let c63: u8[] = inst.inst_i32_const(empty, 63);
    if (c63.len() != 2) { return 10; }
    if (c63[0] != 65u8) { return 11; }
    if (c63[1] != 63u8) { return 12; }

    // i32.const 127 -> 0x41 0xFF 0x00. 127 has bit-6 set, so the
    // sleb form needs a continuation byte; otherwise [0x7F] would
    // decode to -1. This is where wasm hex dumps commonly go wrong.
    let c127: u8[] = inst.inst_i32_const(empty, 127);
    if (c127.len() != 3) { return 15; }
    if (c127[0] != 65u8) { return 16; }
    if (c127[1] != 255u8) { return 17; }
    if (c127[2] != 0u8) { return 18; }

    // i32.const -1 -> 0x41 0x7F (sleb 0x7F is the all-ones term)
    let cn1: u8[] = inst.inst_i32_const(empty, 0 - 1);
    if (cn1.len() != 2) { return 20; }
    if (cn1[1] != 127u8) { return 22; }

    // i32.const 128 -> 0x41 0x80 0x01 (sleb boundary)
    let c128: u8[] = inst.inst_i32_const(empty, 128);
    if (c128.len() != 3) { return 30; }
    if (c128[0] != 65u8) { return 31; }
    if (c128[1] != 128u8) { return 32; }
    if (c128[2] != 1u8) { return 33; }

    // i64.const 42 -> 0x42 0x2A
    let ci64: u8[] = inst.inst_i64_const(empty, 42i64);
    if (ci64.len() != 2) { return 40; }
    if (ci64[0] != 66u8) { return 41; }
    if (ci64[1] != 42u8) { return 42; }

    // f32.const with bit pattern 0x3F800000 (= 1.0): 0x43 followed
    // by four LE bytes [0x00, 0x00, 0x80, 0x3F].
    let f1: u8[] = inst.inst_f32_const(empty, 1065353216u32);
    if (f1.len() != 5) { return 50; }
    if (f1[0] != 67u8) { return 51; }
    if (f1[1] != 0u8) { return 52; }
    if (f1[2] != 0u8) { return 53; }
    if (f1[3] != 128u8) { return 54; }
    if (f1[4] != 63u8) { return 55; }

    // f64.const with bit pattern 0x3FF0000000000000 (= 1.0): 0x44
    // followed by eight LE bytes ending in 0x3F.
    let f2: u8[] = inst.inst_f64_const(empty, 4607182418800017408u64);
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

func TestWASMInstVariable(t *testing.T) {
	src := `import "std/wasm/inst";
function main(): i32 {
    let empty: u8[] = [];

    // local.get 0 -> 0x20 0x00
    let lg0: u8[] = inst.inst_local_get(empty, 0u32);
    if (lg0.len() != 2) { return 1; }
    if (lg0[0] != 32u8) { return 2; }
    if (lg0[1] != 0u8) { return 3; }

    // local.set 5 -> 0x21 0x05
    let ls5: u8[] = inst.inst_local_set(empty, 5u32);
    if (ls5.len() != 2) { return 10; }
    if (ls5[0] != 33u8) { return 11; }
    if (ls5[1] != 5u8) { return 12; }

    // local.tee 1 -> 0x22 0x01
    let lt1: u8[] = inst.inst_local_tee(empty, 1u32);
    if (lt1.len() != 2) { return 20; }
    if (lt1[0] != 34u8) { return 21; }
    if (lt1[1] != 1u8) { return 22; }

    // local.get 130 -> 0x20 0x82 0x01 (uleb of 130)
    let lg130: u8[] = inst.inst_local_get(empty, 130u32);
    if (lg130.len() != 3) { return 30; }
    if (lg130[0] != 32u8) { return 31; }
    if (lg130[1] != 130u8) { return 32; }
    if (lg130[2] != 1u8) { return 33; }

    // global.get 7 -> 0x23 0x07
    let gg7: u8[] = inst.inst_global_get(empty, 7u32);
    if (gg7.len() != 2) { return 40; }
    if (gg7[0] != 35u8) { return 41; }
    if (gg7[1] != 7u8) { return 42; }

    // global.set 0 -> 0x24 0x00
    let gs0: u8[] = inst.inst_global_set(empty, 0u32);
    if (gs0.len() != 2) { return 50; }
    if (gs0[0] != 36u8) { return 51; }
    if (gs0[1] != 0u8) { return 52; }

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("inst variable: exit = %d, want 0", got)
	}
}

func TestWASMInstControl(t *testing.T) {
	src := `import "std/wasm/inst";
import "std/wasm/encode";
function main(): i32 {
    let empty: u8[] = [];

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
    let bk: u8[] = inst.inst_block_start(empty, inst.blocktype_empty());
    if (bk.len() != 2) { return 10; }
    if (bk[0] != 2u8) { return 11; }
    if (bk[1] != 64u8) { return 12; }

    // loop bt=i32 -> 0x03 0x7F
    let lp: u8[] = inst.inst_loop_start(empty, encode.valtype_i32());
    if (lp.len() != 2) { return 20; }
    if (lp[0] != 3u8) { return 21; }
    if (lp[1] != 127u8) { return 22; }

    // if bt=empty -> 0x04 0x40
    let ifs: u8[] = inst.inst_if_start(empty, inst.blocktype_empty());
    if (ifs[0] != 4u8) { return 30; }
    if (ifs[1] != 64u8) { return 31; }

    // br 0 -> 0x0C 0x00
    let br0: u8[] = inst.inst_br(empty, 0u32);
    if (br0[0] != 12u8) { return 40; }
    if (br0[1] != 0u8) { return 41; }

    // br_if 3 -> 0x0D 0x03
    let bri: u8[] = inst.inst_br_if(empty, 3u32);
    if (bri[0] != 13u8) { return 50; }
    if (bri[1] != 3u8) { return 51; }

    // call 7 -> 0x10 0x07
    let c7: u8[] = inst.inst_call(empty, 7u32);
    if (c7[0] != 16u8) { return 60; }
    if (c7[1] != 7u8) { return 61; }

    // call_indirect typeidx=2 tableidx=0 -> 0x11 0x02 0x00
    let ci: u8[] = inst.inst_call_indirect(empty, 2u32, 0u32);
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

func TestWASMInstFunctionBody(t *testing.T) {
	src := `import "std/wasm/inst";
import "std/wasm/encode";
function main(): i32 {
    let empty: u8[] = [];

    // Build body: i32.const 42 (no locals).
    let body: u8[] = inst.inst_i32_const(empty, 42);

    let locals: u8[] = inst.put_locals_empty(empty);

    let entry: u8[] = inst.put_function_body(empty, locals, body);

    // Expected inner bytes: 0x00 (locals=0), 0x41 0x2A (i32.const 42),
    // 0x0B (end). Wrapped: 0x04 (size=4) + the 4 inner bytes.
    if (entry.len() != 5) { return 1; }
    if (entry[0] != 4u8) { return 2; }     // size
    if (entry[1] != 0u8) { return 3; }     // locals_vec(0)
    if (entry[2] != 65u8) { return 4; }    // i32.const
    if (entry[3] != 42u8) { return 5; }    // 42
    if (entry[4] != 11u8) { return 6; }    // end

    // Locals: one group of 3 i32s.
    let locals3: u8[] = inst.put_locals_one_group(empty, 3u32, encode.valtype_i32());
    if (locals3.len() != 3) { return 10; }
    if (locals3[0] != 1u8) { return 11; }      // num groups
    if (locals3[1] != 3u8) { return 12; }      // count
    if (locals3[2] != 127u8) { return 13; }    // i32 valtype

    // Wrap a body with those locals.
    let body2: u8[] = inst.inst_local_get(empty, 0u32);  // 0x20 0x00
    body2 = inst.inst_return(body2);                     // 0x0F
    let entry2: u8[] = inst.put_function_body(empty, locals3, body2);
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

func TestWASMNumericI32(t *testing.T) {
	src := `import "std/wasm/numeric";
function main(): i32 {
    let e: u8[] = [];

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

func TestWASMNumericI64(t *testing.T) {
	src := `import "std/wasm/numeric";
function main(): i32 {
    let e: u8[] = [];

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

func TestWASMNumericFloat(t *testing.T) {
	src := `import "std/wasm/numeric";
function main(): i32 {
    let e: u8[] = [];

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

func TestWASMNumericCompose(t *testing.T) {
	src := `import "std/wasm/inst";
import "std/wasm/numeric";
function main(): i32 {
    let body: u8[] = [];
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
    let body2: u8[] = [];
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

func TestWASMMemoryLoad(t *testing.T) {
	src := `import "std/wasm/memory";
function main(): i32 {
    let e: u8[] = [];

    // i32.load align=2 offset=0 -> 0x28 0x02 0x00
    let l32: u8[] = memory.inst_i32_load(e, 2u32, 0u32);
    if (l32.len() != 3)       { return 1; }
    if (l32[0] != 40u8)      { return 2; }
    if (l32[1] != 2u8)       { return 3; }
    if (l32[2] != 0u8)       { return 4; }

    // i64.load align=3 offset=128 -> 0x29 0x03 0x80 0x01 (uleb 128)
    let l64: u8[] = memory.inst_i64_load(e, 3u32, 128u32);
    if (l64.len() != 4)       { return 10; }
    if (l64[0] != 41u8)      { return 11; }
    if (l64[1] != 3u8)       { return 12; }
    if (l64[2] != 128u8)     { return 13; }
    if (l64[3] != 1u8)       { return 14; }

    // i32.load8_u align=0 offset=0 -> 0x2D 0x00 0x00
    let lu8: u8[] = memory.inst_i32_load8_u(e, 0u32, 0u32);
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

func TestWASMMemoryStore(t *testing.T) {
	src := `import "std/wasm/memory";
function main(): i32 {
    let e: u8[] = [];

    // i32.store align=2 offset=4 -> 0x36 0x02 0x04
    let s32: u8[] = memory.inst_i32_store(e, 2u32, 4u32);
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

func TestWASMMemorySizeGrow(t *testing.T) {
	src := `import "std/wasm/memory";
function main(): i32 {
    let e: u8[] = [];

    let sz: u8[] = memory.inst_memory_size(e);
    if (sz.len() != 2)  { return 1; }
    if (sz[0] != 63u8) { return 2; }
    if (sz[1] != 0u8)  { return 3; }

    let gr: u8[] = memory.inst_memory_grow(e);
    if (gr.len() != 2)  { return 10; }
    if (gr[0] != 64u8) { return 11; }
    if (gr[1] != 0u8)  { return 12; }

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("memory size/grow: exit = %d, want 0", got)
	}
}

func TestWASMMemoryBulk(t *testing.T) {
	src := `import "std/wasm/memory";
function main(): i32 {
    let e: u8[] = [];

    let cp: u8[] = memory.inst_memory_copy(e);
    if (cp.len() != 4)  { return 1; }
    if (cp[0] != 252u8) { return 2; }   // 0xFC prefix
    if (cp[1] != 10u8)  { return 3; }   // 0x0A memory.copy
    if (cp[2] != 0u8)   { return 4; }   // dst memidx
    if (cp[3] != 0u8)   { return 5; }   // src memidx

    let fl: u8[] = memory.inst_memory_fill(e);
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

func TestWASMConvertIntWidth(t *testing.T) {
	src := `import "std/wasm/convert";
function main(): i32 {
    let e: u8[] = [];

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

func TestWASMConvertFloatInt(t *testing.T) {
	src := `import "std/wasm/convert";
function main(): i32 {
    let e: u8[] = [];

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

func TestWASMSectionsFunction(t *testing.T) {
	src := `import "std/wasm/sections";
function main(): i32 {
    let empty: u8[] = [];

    // No functions: section id=3, size=1, body=[0x00] (vec count 0).
    let noidxs: u32[] = [];
    let s0: u8[] = sections.encode_function_section(empty, noidxs);
    if (s0.len() != 3) { return 1; }
    if (s0[0] != 3u8) { return 2; }
    if (s0[1] != 1u8) { return 3; }
    if (s0[2] != 0u8) { return 4; }

    // Three functions with typeidxs 0, 1, 2 -> body [0x03, 0x00,
    // 0x01, 0x02]. Wrapped: [0x03, 0x04, 0x03, 0x00, 0x01, 0x02].
    let idxs: u32[] = [0u32, 1u32, 2u32];
    let s3: u8[] = sections.encode_function_section(empty, idxs);
    if (s3.len() != 6) { return 10; }
    if (s3[0] != 3u8) { return 11; }    // section_function id
    if (s3[1] != 4u8) { return 12; }    // size = 4
    if (s3[2] != 3u8) { return 13; }    // vec count = 3
    if (s3[3] != 0u8) { return 14; }
    if (s3[4] != 1u8) { return 15; }
    if (s3[5] != 2u8) { return 16; }

    // Multi-byte typeidx (128 = uleb 0x80 0x01) -> body is
    // count=1 + 0x80 0x01 = 3 bytes; wrapped = 5 bytes.
    let big: u32[] = [128u32];
    let s4: u8[] = sections.encode_function_section(empty, big);
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

func TestWASMSectionsStartMemory(t *testing.T) {
	src := `import "std/wasm/sections";
function main(): i32 {
    let empty: u8[] = [];

    // start: id=8, size=1, body=[funcidx]. funcidx=0 -> [0x08, 0x01, 0x00].
    let ss: u8[] = sections.encode_start_section(empty, 0u32);
    if (ss.len() != 3) { return 1; }
    if (ss[0] != 8u8) { return 2; }
    if (ss[1] != 1u8) { return 3; }
    if (ss[2] != 0u8) { return 4; }

    // memory, no max: id=5, body=[count=1, flag=0, min=1]
    // -> wrapped 5 bytes.
    let mNoMax: u8[] = sections.encode_memory_section(empty, 1u32, 0 - 1);
    if (mNoMax.len() != 5) { return 10; }
    if (mNoMax[0] != 5u8) { return 11; }   // section_memory id
    if (mNoMax[1] != 3u8) { return 12; }   // size = 3
    if (mNoMax[2] != 1u8) { return 13; }   // count = 1
    if (mNoMax[3] != 0u8) { return 14; }   // flag = no max
    if (mNoMax[4] != 1u8) { return 15; }   // min = 1

    // memory, with max: id=5, body=[count=1, flag=1, min=1, max=2]
    // -> wrapped 6 bytes.
    let mWithMax: u8[] = sections.encode_memory_section(empty, 1u32, 2);
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

func TestWASMSectionsExport(t *testing.T) {
	src := `import "std/wasm/sections";
function main(): i32 {
    let empty: u8[] = [];

    // One func export named "main" -> 0 + 0x00 + 0
    // body: count=1, "main"=[4, 0x6D, 0x61, 0x69, 0x6E], kind=0, idx=0
    let nms: string[] = ["main"];
    let ks: u8[] = [sections.export_func()];
    let ixs: u32[] = [0u32];
    let s1: u8[] = sections.encode_export_section(empty, nms, ks, ixs);
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
    let nms2: string[] = ["memory", "g"];
    let ks2: u8[] = [sections.export_memory(), sections.export_global()];
    let ixs2: u32[] = [0u32, 2u32];
    let s2: u8[] = sections.encode_export_section(empty, nms2, ks2, ixs2);
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

func TestWASMSectionsTypeCode(t *testing.T) {
	src := `import "std/wasm/inst";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    let empty: u8[] = [];

    // Type section with one (i32) -> (i32) functype.
    let p: u8[] = [encode.valtype_i32()];
    let r: u8[] = [encode.valtype_i32()];
    let pp: u8[][] = [p];
    let rr: u8[][] = [r];
    let ts: u8[] = sections.encode_type_section(empty, pp, rr);
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
    let bodyExpr: u8[] = inst.inst_i32_const(empty, 42);
    let locals: u8[] = inst.put_locals_empty(empty);
    let fn: u8[] = inst.put_function_body(empty, locals, bodyExpr);
    // fn = [size=4, 0x00 locals, 0x41 0x2A, 0x0B end] = 5 bytes.
    let bodies: u8[][] = [fn];
    let cs: u8[] = sections.encode_code_section(empty, bodies);
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

func TestWASMSectionsData(t *testing.T) {
	src := `import "std/wasm/sections";
function main(): i32 {
    let empty: u8[] = [];

    // One segment at offset 50 with init bytes [0xAA, 0xBB].
    // Offset 50 (= 0x32) has bit-6 clear so sleb fits in 1 byte;
    // 100 would need 2 bytes (bit-6 set), which is a fine
    // boundary to bear in mind for the wider IR-walker tests.
    let offs: i32[] = [50];
    let initBytes: u8[] = [170u8, 187u8];
    let inits: u8[][] = [initBytes];
    let ds: u8[] = sections.encode_data_section(empty, offs, inits);
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

func TestWASMSectionsTableElement(t *testing.T) {
	src := `import "std/wasm/sections";
import "std/wasm/imports";
function main(): i32 {
    let e: u8[] = [];

    // Table: one funcref table, min 2, no max.
    let tbl: u8[] = sections.encode_table_section(e, imports.reftype_funcref(), 2u32, 0 - 1);
    // [id=4, size=4, count=1, reftype=0x70, flag=0, min=2]
    if (tbl.len() != 6)  { return 1; }
    if (tbl[0] != 4u8)   { return 2; }   // section_table id
    if (tbl[1] != 4u8)   { return 3; }   // body size 4
    if (tbl[2] != 1u8)   { return 4; }   // one table
    if (tbl[3] != 112u8) { return 5; }   // funcref 0x70
    if (tbl[4] != 0u8)   { return 6; }   // limits flag: no max
    if (tbl[5] != 2u8)   { return 7; }   // min 2

    // Element: one active segment at offset 0, funcidxs [0, 1].
    let offs: i32[] = [0];
    let fids: u32[][] = [[0u32, 1u32]];
    let el: u8[] = sections.encode_element_section(e, offs, fids);
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

func TestWASMImportsFunc(t *testing.T) {
	src := `import "std/wasm/imports";
import "std/wasm/encode";
function main(): i32 {
    let empty: u8[] = [];

    // One function import: ("env", "log", func, typeidx=0).
    let mods: string[] = ["env"];
    let nms: string[] = ["log"];
    let ks: u8[] = [imports.import_func()];
    let descs: u8[][] = [imports.import_desc_func(0u32)];
    let s1: u8[] = imports.encode_import_section(empty, mods, nms, ks, descs);
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
    let mods2: string[] = ["wasi", "env"];
    let nms2: string[] = ["fd_write", "g"];
    let ks2: u8[] = [imports.import_func(), imports.import_global()];
    let d_func: u8[] = imports.import_desc_func(7u32);
    let d_glob: u8[] = imports.import_desc_global(encode.valtype_i32(), imports.mut_var());
    let descs2: u8[][] = [d_func, d_glob];
    let s2: u8[] = imports.encode_import_section(empty, mods2, nms2, ks2, descs2);
    // Spot-check the global descriptor at the tail: valtype + mut
    // are the last two bytes.
    if (s2.len() < 20) { return 20; }
    if (s2[0] != 2u8) { return 21; }
    let last: i32 = s2.len() - 1;
    if (s2[last] != 1u8)       { return 22; }   // mut_var
    if (s2[last - 1] != 127u8) { return 23; }   // valtype_i32

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("imports func: exit = %d, want 0", got)
	}
}

func TestWASMImportsDescBuilders(t *testing.T) {
	src := `import "std/wasm/imports";
import "std/wasm/encode";
function main(): i32 {
    // import_desc_func(typeidx=5) -> [0x05]
    let df: u8[] = imports.import_desc_func(5u32);
    if (df.len() != 1) { return 1; }
    if (df[0] != 5u8) { return 2; }

    // import_desc_global(i32, const) -> [0x7F, 0x00]
    let dg: u8[] = imports.import_desc_global(encode.valtype_i32(), imports.mut_const());
    if (dg.len() != 2)    { return 10; }
    if (dg[0] != 127u8)  { return 11; }
    if (dg[1] != 0u8)    { return 12; }

    // import_desc_memory(min=1, no max) -> [0x00, 0x01]
    let dm1: u8[] = imports.import_desc_memory(1u32, 0 - 1);
    if (dm1.len() != 2) { return 20; }
    if (dm1[0] != 0u8) { return 21; }
    if (dm1[1] != 1u8) { return 22; }

    // import_desc_memory(min=1, max=2) -> [0x01, 0x01, 0x02]
    let dm2: u8[] = imports.import_desc_memory(1u32, 2);
    if (dm2.len() != 3) { return 30; }
    if (dm2[0] != 1u8) { return 31; }
    if (dm2[1] != 1u8) { return 32; }
    if (dm2[2] != 2u8) { return 33; }

    // import_desc_table(funcref, min=0, no max) -> [0x70, 0x00, 0x00]
    let dt: u8[] = imports.import_desc_table(imports.reftype_funcref(), 0u32, 0 - 1);
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

func TestWASMImportsGlobalSection(t *testing.T) {
	src := `import "std/wasm/imports";
import "std/wasm/encode";
import "std/wasm/inst";
function main(): i32 {
    let empty: u8[] = [];

    // One mutable i32 global, initialized to 0.
    // init_expr: i32.const 0 ; end = [0x41, 0x00, 0x0B] (3 bytes)
    let init0: u8[] = inst.inst_i32_const(empty, 0);
    init0 = inst.inst_end(init0);

    let vts: u8[] = [encode.valtype_i32()];
    let ms: u8[] = [imports.mut_var()];
    let inits: u8[][] = [init0];
    let gs: u8[] = imports.encode_global_section(empty, vts, ms, inits);
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

func TestWASMModuleEmpty(t *testing.T) {
	src := `import "std/wasm/module";
function main(): i32 {
    let m: module.Module = module.module_new();
    let bytes: u8[] = module.build(m);
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

func TestWASMModuleMinimal(t *testing.T) {
	src := `import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    let m: module.Module = module.module_new();

    // Type: one functype () -> i32.
    let p0: u8[] = [];
    let r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);

    // Function: one function with typeidx 0.
    m = m.with_functions([0u32]);

    // Export: ("main", func, 0).
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);

    // Code: body is i32.const 42 with no locals.
    let bodyExpr: u8[] = inst.inst_i32_const([], 42);
    let localsBytes: u8[] = inst.put_locals_empty([]);
    let fn: u8[] = inst.put_function_body([], localsBytes, bodyExpr);
    m = m.with_code([fn]);

    let bytes: u8[] = module.build(m);

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

func TestWASMModuleSectionOrder(t *testing.T) {
	src := `import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/encode";
import "std/wasm/sections";
import "std/wasm/imports";
function main(): i32 {
    let m: module.Module = module.module_new();

    // Type: one functype () -> ().
    let pe: u8[] = [];
    let re: u8[] = [];
    m = m.with_types([pe], [re]);

    // Import: one ("env", "h", func, typeidx=0).
    m = m.with_imports(["env"], ["h"], [imports.import_func()], [imports.import_desc_func(0u32)]);

    // Function: one with typeidx 0.
    m = m.with_functions([0u32]);

    // Memory: present, min=1.
    m = m.with_memory(1u32, 0 - 1);

    // Global: one const i32 = 0.
    let g0: u8[] = inst.inst_i32_const([], 0);
    g0 = inst.inst_end(g0);
    m = m.with_globals([encode.valtype_i32()], [imports.mut_const()], [g0]);

    // Export: ("e", memory, 0).
    m = m.with_exports(["e"], [sections.export_memory()], [0u32]);

    // Start: funcidx 1 (the imported func is 0, our func is 1).
    m = m.with_start(1u32);

    // Code: empty body — just the terminating 0x0B.
    let localsBytes: u8[] = inst.put_locals_empty([]);
    let emptyBody: u8[] = [];
    let fn: u8[] = inst.put_function_body([], localsBytes, emptyBody);
    m = m.with_code([fn]);

    // Data: one segment at offset 0 with byte 0xAB.
    m = m.with_data([0], [[171u8]]);

    let bytes: u8[] = module.build(m);

    // Scan for section IDs in order. Section IDs the spec assigns:
    // type=1, import=2, function=3, table=4, memory=5, global=6,
    // export=7, start=8, element=9, code=10, data=11.
    // We expect 1, 2, 3, 5, 6, 7, 8, 10, 11 (no table, no element).
    let expected_ids: u8[] = [1u8, 2u8, 3u8, 5u8, 6u8, 7u8, 8u8, 10u8, 11u8];

    // Walk the byte stream: byte 8 is the first section id; after
    // each id comes uleb size + that many body bytes; repeat.
    let pos: i32 = 8;  // skip preamble
    let idx: i32 = 0;
    while (idx < expected_ids.len()) {
        if (pos >= bytes.len()) { return 100 + idx; }
        if (bytes[pos] != expected_ids[idx]) { return 1 + idx; }
        pos = pos + 1;
        // Read uleb size — small enough that bit-7 is clear here.
        let sz: i32 = bytes[pos] as i32;
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
    let m: module.Module = module.module_new();

    // Type: one functype () -> i32.
    let p0: u8[] = [];
    let r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);

    // Function: one function with typeidx 0.
    m = m.with_functions([0u32]);

    // Export: ("main", func, 0). wasm-tools print uses the
    // export to label the function in its output.
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);

    // Code: body is i32.const 42 with no locals.
    let bodyExpr: u8[] = inst.inst_i32_const([], 42);
    let localsBytes: u8[] = inst.put_locals_empty([]);
    let fn: u8[] = inst.put_function_body([], localsBytes, bodyExpr);
    m = m.with_code([fn]);

    let bytes: u8[] = module.build(m);

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMModuleRunsConst42(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    let m: module.Module = module.module_new();
    let p0: u8[] = [];
    let r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);
    let bodyExpr: u8[] = inst.inst_i32_const([], 42);
    let localsBytes: u8[] = inst.put_locals_empty([]);
    let fn: u8[] = inst.put_function_body([], localsBytes, bodyExpr);
    m = m.with_code([fn]);
    let bytes: u8[] = module.build(m);

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMModuleRunsAddition(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/numeric";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    let m: module.Module = module.module_new();
    let p0: u8[] = [];
    let r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);

    let body: u8[] = inst.inst_i32_const([], 3);
    body = inst.inst_i32_const(body, 4);
    body = numeric.inst_i32_add(body);
    let localsBytes: u8[] = inst.put_locals_empty([]);
    let fn: u8[] = inst.put_function_body([], localsBytes, body);
    m = m.with_code([fn]);
    let bytes: u8[] = module.build(m);

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMModuleRunsTwoFunctions(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/numeric";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    let m: module.Module = module.module_new();

    // Two types:
    //   type 0: (i32, i32) -> i32   for the add callee
    //   type 1: () -> i32           for main
    let p_add: u8[] = [encode.valtype_i32(), encode.valtype_i32()];
    let r_add: u8[] = [encode.valtype_i32()];
    let p_main: u8[] = [];
    let r_main: u8[] = [encode.valtype_i32()];
    m = m.with_types([p_add, p_main], [r_add, r_main]);

    // Function 0 uses type 0 (add), function 1 uses type 1 (main).
    m = m.with_functions([0u32, 1u32]);

    // Export main as funcidx 1.
    m = m.with_exports(["main"], [sections.export_func()], [1u32]);

    // add(a, b): local.get 0 ; local.get 1 ; i32.add.
    let add_body: u8[] = inst.inst_local_get([], 0u32);
    add_body = inst.inst_local_get(add_body, 1u32);
    add_body = numeric.inst_i32_add(add_body);
    let add_fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), add_body);

    // main(): i32.const 10 ; i32.const 32 ; call 0.
    let main_body: u8[] = inst.inst_i32_const([], 10);
    main_body = inst.inst_i32_const(main_body, 32);
    main_body = inst.inst_call(main_body, 0u32);
    let main_fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), main_body);

    m = m.with_code([add_fn, main_fn]);
    let bytes: u8[] = module.build(m);

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMComponentHeader(t *testing.T) {
	src := `import "std/wasm/component";
function main(): i32 {
    let empty: u8[] = [];
    let hdr: u8[] = component.put_component_header(empty);
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
    let m: module.Module = module.module_new();
    let p0: u8[] = [];
    let r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);
    let bodyExpr: u8[] = inst.inst_i32_const([], 42);
    let fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), bodyExpr);
    m = m.with_code([fn]);
    let core_bytes: u8[] = module.build(m);

    // Wrap it in a component envelope.
    let comp: u8[] = component.put_component_header([]);
    comp = component.put_core_module_section(comp, core_bytes);

    let output: string = "";
    let i: i32 = 0;
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
    let m: module.Module = module.module_new();
    let p0: u8[] = [];
    let r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["f"], [sections.export_func()], [0u32]);
    let bodyExpr: u8[] = inst.inst_i32_const([], 7);
    let fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), bodyExpr);
    m = m.with_code([fn]);
    let core_bytes: u8[] = module.build(m);

    // Component envelope: preamble + core-module + core-instance.
    let comp: u8[] = component.put_component_header([]);
    comp = component.put_core_module_section(comp, core_bytes);
    comp = component.put_core_instance_section_instantiate(comp, 0u32);

    let output: string = "";
    let i: i32 = 0;
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
	if !strings.Contains(watStr, "instantiate") {
		t.Errorf("expected `instantiate` keyword in printed output, got:\n%s", watStr)
	}
}

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
    let m: module.Module = module.module_new();
    let p0: u8[] = [];
    let r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["f"], [sections.export_func()], [0u32]);
    let bodyExpr: u8[] = inst.inst_i32_const([], 9);
    let fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), bodyExpr);
    m = m.with_code([fn]);
    let core_bytes: u8[] = module.build(m);

    // Component: preamble + core-module + core-instance + alias.
    let comp: u8[] = component.put_component_header([]);
    comp = component.put_core_module_section(comp, core_bytes);
    comp = component.put_core_instance_section_instantiate(comp, 0u32);
    comp = component.put_alias_section_core_export_func(comp, 0u32, "f");

    let output: string = "";
    let i: i32 = 0;
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
    let m: module.Module = module.module_new();
    let p0: u8[] = [];
    let r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_memory(1u32, 0 - 1);
    m = m.with_exports(["f", "mem"], [sections.export_func(), sections.export_memory()], [0u32, 0u32]);
    let fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), inst.inst_i32_const([], 1));
    m = m.with_code([fn]);
    let core_bytes: u8[] = module.build(m);

    let comp: u8[] = component.put_component_header([]);
    comp = component.put_core_module_section(comp, core_bytes);
    comp = component.put_core_instance_section_instantiate(comp, 0u32);

    // Alias section with two entries of different core sorts.
    let sorts: u8[] = [component.core_sort_func(), component.core_sort_memory()];
    let instance_idxs: u32[] = [0u32, 0u32];
    let names: string[] = ["f", "mem"];
    comp = component.put_alias_section_core_exports(comp, sorts, instance_idxs, names);

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMComponentInstanceTypeWithResult(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    let names: string[] = ["len"];
    let vts: u8[] = [component.cvaltype_u64()];
    let comp: u8[] = component.put_component_header([]);
    // Type 0: instance { export get-info(len: u64) -> u32 }
    comp = component.put_type_section_one_instance_one_func_with_result_export(comp, "get-info", names, vts, component.cvaltype_u32());
    comp = component.put_import_section_one_instance(comp, "test:reader/iface", 0u32);

    let output: string = "";
    let i: i32 = 0;
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
    let m: module.Module = module.module_new();
    let p_exit: u8[] = [encode.valtype_i32()];
    let r_void: u8[] = [];
    let p_args: u8[] = [];
    m = m.with_types([p_exit, p_args], [r_void, r_void]);
    m = m.with_imports(["wasi-exit", "wasi-env"], ["exit", "get-args"], [imports.import_func(), imports.import_func()], [imports.import_desc_func(0u32), imports.import_desc_func(1u32)]);
    m = m.with_functions([]);
    m = m.with_code([]);
    let core_bytes: u8[] = module.build(m);

    // Two WASI imports, threaded in one call.
    let interfaces: string[] = ["wasi:cli/exit@0.2.0", "wasi:cli/environment@0.2.0"];
    let funcs: string[] = ["exit", "get-args"];
    let p0: string[] = ["code"];
    let v0: u8[] = [component.cvaltype_u32()];
    let p1: string[] = [];
    let v1: u8[] = [];
    let names_per: string[][] = [p0, p1];
    let valtypes_per: u8[][] = [v0, v1];
    let core_modules: string[] = ["wasi-exit", "wasi-env"];

    let comp: u8[] = component.build_wasi_multi_imported_component(core_bytes, interfaces, funcs, names_per, valtypes_per, core_modules);

    let output: string = "";
    let i: i32 = 0;
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
		cmd := exec.Command(buildFernCLI(t), "-target", "wasm32-wasi", "-o", compPath, srcPath)
		cmd.Dir = projectRoot(t)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fern -component-wrap-cli (%s) failed: %v\n%s", name, err, out)
		}
		if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
			t.Fatalf("wasm-tools validate (%s) failed: %v\n%s", name, err, out)
		}
		return compPath
	}

	full := build("tcpfull", `function main(): i32 {
    let s: i32 = tcp_listen(8080);
    if (s < 0) { return 1; }
    let c: i32 = tcp_accept(s);
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

	echo := build("tcpecho", `function main(): i32 {
    let s: i32 = tcp_listen(8080);
    let c: i32 = tcp_accept(s);
    let d: u8[] = __alloc_u8(1024);
    let n: i32 = tcp_recv_into(c, d);
    if (n > 0) { tcp_send_bytes(c, d); }
    tcp_close(c);
    tcp_close(s);
    return 0;
}`)
	echoOut, err := exec.Command("wasm-tools", "print", echo).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, echoOut)
	}
	for _, want := range []string{"input-stream.read", "output-stream.blocking-write-and-flush"} {
		if !bytes.Contains(echoOut, []byte(want)) {
			t.Errorf("expected %q in echo component, got:\n%s", want, echoOut)
		}
	}

	srv := build("tcplisten", `function main(): i32 {
    let s: i32 = tcp_listen(8080);
    if (s < 0) { return 1; }
    tcp_close(s);
    return 0;
}`)
	if err := exec.Command("wasmtime", "run", "-S", "inherit-network", srv).Run(); err != nil {
		t.Errorf("tcp listen+close: wasmtime run failed (want exit 0): %v", err)
	}
}

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

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "envecho.fern")
	src := `import "std/net";

function port_from_env(): i32 {
    match (env("PORT")) {
        Some(s) => {
            let n: i32 = 0;
            let i: i32 = 0;
            while (i < s.len()) {
                let b: i32 = s[i] as i32;
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
    let sock = tcp_listen(port_from_env());
    if (sock < 0) { return 1; }
    let conn = tcp_accept(sock);
    if (conn < 0) { return 2; }
    let msg: u8[] = match (net.read_watched(conn, 1024, 5000i64, 1)) {
        Ok(net.Chunk(b)) => b,
        _ => { return 4; },
    };
    match (net.write(conn, msg)) { Err(_) => { return 3; }, Ok(_) => {} }
    tcp_close(conn);
    tcp_close(sock);
    return 0;
}`
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "envecho.wasm")
	build := exec.Command(buildFernCLI(t), "-target", "wasm32-wasi", "-o", compPath, srcPath)
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
		"input-stream.read", "output-stream.blocking-write-and-flush",
		"wasi:cli/run@0.2.0",
	} {
		if !bytes.Contains(printOut, []byte(want)) {
			t.Errorf("expected %q in component, got:\n%s", want, printOut)
		}
	}

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
			cmd := exec.Command(buildFernCLI(t),
				"-target", "wasm32-wasi", "-o", compPath, srcPath)
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
	cmd := exec.Command(buildFernCLI(t),
		"-target", "wasm32-wasi", "-o", compPath, srcPath)
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
		cmd := exec.Command(buildFernCLI(t), "-target", "wasm32-wasi", "-o", compPath, srcPath)
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
    let r: i32 = random_i32();
    if (r == r) { return 0; }
    return 1;
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "rand.wasm")
	cmd := exec.Command(buildFernCLI(t),
		"-target", "wasm32-wasi", "-o", compPath, srcPath)
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
    let t: i64 = monotonic_ns();
    if (t == t) { return 0; }
    return 1;
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "mono.wasm")
	cmd := exec.Command(buildFernCLI(t),
		"-target", "wasm32-wasi", "-o", compPath, srcPath)
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
    let t: i64 = now_ns();
    if (t >= 0i64) { return 0; }
    return 1;
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "now.wasm")
	cmd := exec.Command(buildFernCLI(t),
		"-target", "wasm32-wasi", "-o", compPath, srcPath)
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
    let t: i64 = monotonic_ns();
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "void.wasm")
	cmd := exec.Command(buildFernCLI(t),
		"-target", "wasm32-wasi", "-o", compPath, srcPath)
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
	cmd := exec.Command(buildFernCLI(t),
		"-target", "wasm32-wasi", "-o", compPath, srcPath)
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

func TestCmdLangComponentWrapCliWithReadLine(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "rd.fern")
	src := []byte(`import "std/io";

function main(): i32 {
    match (io.read_line(stdin())) {
        Ok(Some(line)) => { if (line.len() == 6) { return 0; } return 2; },
        _ => { return 1; }
    }
    return 1;
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "rd.wasm")
	cmd := exec.Command(buildFernCLI(t),
		"-target", "wasm32-wasi", "-o", compPath, srcPath)
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
	some := exec.Command("wasmtime", "run", compPath)
	some.Stdin = strings.NewReader("hello\n")
	if err := some.Run(); err != nil {
		t.Errorf("read_line with input: wasmtime run failed (want exit 0): %v", err)
	}
	none := exec.Command("wasmtime", "run", compPath)
	none.Stdin = strings.NewReader("")
	if err := none.Run(); err == nil {
		t.Errorf("read_line at EOF: wasmtime run exited 0, want non-zero (None arm)")
	}
}

func TestCmdLangComponentWrapCliWithReadLinePrint(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "rp.fern")
	src := []byte(`import "std/io";

function main(): i32 {
    match (io.read_line(stdin())) {
        Ok(Some(line)) => { print(line); return 0; },
        _ => { return 1; }
    }
    return 1;
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "rp.wasm")
	cmd := exec.Command(buildFernCLI(t), "-target", "wasm32-wasi", "-o", compPath, srcPath)
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
	none := exec.Command("wasmtime", "run", compPath)
	none.Stdin = strings.NewReader("")
	if err := none.Run(); err == nil {
		t.Errorf("read_line+print EOF: wasmtime run exited 0, want non-zero")
	}
}

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
		cmd := exec.Command(buildFernCLI(t), "-target", "wasm32-wasi", "-o", compPath, srcPath)
		cmd.Dir = projectRoot(t)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fern -component-wrap-cli (%s) failed: %v\n%s", name, err, out)
		}
		if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
			t.Fatalf("wasm-tools validate (%s) failed: %v\n%s", name, err, out)
		}
		return compPath
	}

	rle := build("rle", `import "std/io";

function main(): i32 {
    match (io.read_line(stdin())) {
        Ok(Some(line)) => { exit(0); return 0; },
        _ => { exit(1); return 1; }
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

	rlpe := build("rlpe", `import "std/io";

function main(): i32 {
    match (io.read_line(stdin())) {
        Ok(Some(line)) => { print(line); exit(0); return 0; },
        _ => { exit(2); return 2; }
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
		cmd := exec.Command(buildFernCLI(t), "-target", "wasm32-wasi", "-o", compPath, srcPath)
		cmd.Dir = projectRoot(t)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fern -component-wrap-cli (%s) failed: %v\n%s", name, err, out)
		}
		if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
			t.Fatalf("wasm-tools validate (%s) failed: %v\n%s", name, err, out)
		}
		return compPath
	}

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
		cmd := exec.Command(buildFernCLI(t), "-target", "wasm32-wasi", "-o", compPath, srcPath)
		cmd.Dir = projectRoot(t)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fern -component-wrap-cli (%s) failed: %v\n%s", name, err, out)
		}
		if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
			t.Fatalf("wasm-tools validate (%s) failed: %v\n%s", name, err, out)
		}
		return compPath
	}

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
		cmd := exec.Command(buildFernCLI(t), "-target", "wasm32-wasi", "-o", compPath, srcPath)
		cmd.Dir = projectRoot(t)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fern -component-wrap-cli (%s) failed: %v\n%s", name, err, out)
		}
		if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
			t.Fatalf("wasm-tools validate (%s) failed: %v\n%s", name, err, out)
		}
		return compPath
	}

	np := build("np", `function main(): i32 {
    let t: i64 = now_unix_ms();
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

	ae := build("ae", `function main(): i32 {
    if (args().len() >= 1) { exit(0); return 0; }
    exit(3);
    return 3;
}`)
	if err := exec.Command("wasmtime", "run", ae, "progname").Run(); err != nil {
		t.Errorf("args()+exit: want exit 0 (argc>=1), got %v", err)
	}
}

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
	cmd := exec.Command(buildFernCLI(t),
		"-target", "wasm32-wasi", "-o", compPath, srcPath)
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
	three := exec.Command("wasmtime", "run", compPath, "x", "y")
	if err := three.Run(); err != nil {
		t.Errorf("args with 2 extra: wasmtime run failed (want exit 0): %v", err)
	}
	one := exec.Command("wasmtime", "run", compPath)
	if err := one.Run(); err == nil {
		t.Errorf("args with none: wasmtime run exited 0, want non-zero")
	}
}

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
	cmd := exec.Command(buildFernCLI(t),
		"-target", "wasm32-wasi", "-o", compPath, srcPath)
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
	set := exec.Command("wasmtime", "run", "--env", "FOO=bar", compPath)
	if err := set.Run(); err != nil {
		t.Errorf("env FOO set: wasmtime run failed (want exit 0): %v", err)
	}
	unset := exec.Command("wasmtime", "run", compPath)
	if err := unset.Run(); err == nil {
		t.Errorf("env FOO unset: wasmtime run exited 0, want non-zero (None arm)")
	}
}

func TestCmdLangComponentWrapCliWithReadFile(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
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
	cmd := exec.Command(buildFernCLI(t),
		"-target", "wasm32-wasi", "-o", compPath, srcPath)
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
	ok := exec.Command("wasmtime", "run", "--dir", dir, compPath)
	if err := ok.Run(); err != nil {
		t.Errorf("read_file present: wasmtime run failed (want exit 0): %v", err)
	}
	miss := filepath.Join(dir, "rfmiss.fern")
	if err := os.WriteFile(miss, []byte(`function main(): i32 {
    match (read_file("nope.txt")) { Ok(s) => { return 0; }, Err(e) => { return 1; } }
    return 1;
}`), 0o644); err != nil {
		t.Fatalf("write miss src: %v", err)
	}
	missComp := filepath.Join(dir, "rfmiss.wasm")
	mc := exec.Command(buildFernCLI(t), "-target", "wasm32-wasi", "-o", missComp, miss)
	mc.Dir = projectRoot(t)
	if out, err := mc.CombinedOutput(); err != nil {
		t.Fatalf("fern (read_file miss) failed: %v\n%s", err, out)
	}
	if err := exec.Command("wasmtime", "run", "--dir", dir, missComp).Run(); err == nil {
		t.Errorf("read_file missing: wasmtime run exited 0, want non-zero (Err arm)")
	}
}

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
		cmd := exec.Command(buildFernCLI(t), "-target", "wasm32-wasi", "-o", compPath, srcPath)
		cmd.Dir = projectRoot(t)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fern -component-wrap-cli (%s) failed: %v\n%s", name, err, out)
		}
		if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
			t.Fatalf("wasm-tools validate (%s) failed: %v\n%s", name, err, out)
		}
		return compPath
	}
	rl := build("orl", `import "std/io";

function main(): i32 {
    match (open_reader("data.txt")) {
        Ok(r) => {
            match (io.read_line(r)) {
                Ok(Some(line)) => { if (line.len() == 6) { return 0; } return 2; },
                _ => { return 3; }
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
	rc := build("orc", `function main(): i32 {
    match (open_reader("data.txt")) {
        Ok(r) => {
            match (r.read_chunk(4)) {
                Ok(chunk) => { if (chunk.len() == 4) { return 0; } return 2; },
                Err(e) => { return 3; }
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
	miss := build("ormiss", `function main(): i32 {
    match (open_reader("nope.txt")) {
        Ok(r) => { match (r.read_chunk(4)) { Ok(_) => { return 7; }, Err(_) => { return 8; } } return 4; },
        Err(e) => { return 1; }
    }
    return 5;
}`)
	if err := exec.Command("wasmtime", "run", "--dir", dir, miss).Run(); err == nil {
		t.Errorf("open_reader missing: wasmtime run exited 0, want non-zero (Err arm)")
	}
}

func TestCmdLangComponentWrapCliWithWriteFile(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
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
	cmd := exec.Command(buildFernCLI(t),
		"-target", "wasm32-wasi", "-o", compPath, srcPath)
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
                Err(e) => { return 2; },
                Ok(_) => { return 0; }
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
	cmd := exec.Command(buildFernCLI(t), "-target", "wasm32-wasi", "-o", compPath, srcPath)
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
		cmd := exec.Command(buildFernCLI(t), "-target", "wasm32-wasi", "-o", compPath, srcPath)
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
	run("bareread", `function main(): i32 {
    match (open_reader("data.txt")) {
        Ok(r) => { return 0; },
        Err(e) => { return 1; }
    }
    return 5;
}`)
	run("barewrite", `function main(): i32 {
    match (open_writer("bw_out.txt")) {
        Ok(w) => { return 0; },
        Err(e) => { return 1; }
    }
    return 5;
}`)
}

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
                Err(e) => { return 2; },
                Ok(_) => { return 0; }
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
	cmd := exec.Command(buildFernCLI(t), "-target", "wasm32-wasi", "-o", compPath, srcPath)
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
	if string(got) != "line1\nline2\n" {
		t.Errorf("log.txt = %q, want %q", string(got), "line1\nline2\n")
	}
}

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
	cmd := exec.Command(buildFernCLI(t),
		"-target", "wasm32-wasi", "-o", compPath, srcPath)
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
	cmd := exec.Command(buildFernCLI(t),
		"-target", "wasm32-wasi", "-o", compPath, srcPath)
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
	cmd := exec.Command(buildFernCLI(t),
		"-target", "wasm32-wasi", "-o", compPath, srcPath)
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
    let r: i32 = math.random_int(0, 100);
    if (r >= 0 && r < 100) { return 0; }
    return 1;
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "randint.wasm")
	cmd := exec.Command(buildFernCLI(t),
		"-target", "wasm32-wasi", "-o", compPath, srcPath)
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
    let r: i32 = random_i32();
    if (r == r) {
        exit(0);
    }
    return 0;
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "multi.wasm")
	cmd := exec.Command(buildFernCLI(t),
		"-target", "wasm32-wasi", "-o", compPath, srcPath)
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
    let b: u8[] = random_bytes(3);
    if (b.len() == 3) { return 0; }
    return 1;
}`)
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "randbytes.wasm")
	cmd := exec.Command(buildFernCLI(t),
		"-target", "wasm32-wasi", "-o", compPath, srcPath)
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
	cmd := exec.Command(buildFernCLI(t),
		"-target", "wasm32-wasi",
		"-o", compPath, srcPath)
	cmd.Dir = projectRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm32-wasi (no adapter) failed: %v\n%s", err, out)
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

func TestWASMComponentGoLangByteEquivalence(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/component";
import "std/wasm/encode";
import "std/wasm/imports";
function main(): i32 {
    let m: module.Module = module.module_new();
    let p0: u8[] = [encode.valtype_i32()];
    let r0: u8[] = [];
    m = m.with_types([p0], [r0]);
    m = m.with_imports(["wasi-exit"], ["exit"], [imports.import_func()], [imports.import_desc_func(0u32)]);
    m = m.with_functions([]);
    m = m.with_code([]);
    let core_bytes: u8[] = module.build(m);

    let pnames: string[] = ["code"];
    let pvals: u8[] = [component.cvaltype_u32()];
    let comp: u8[] = component.build_wasi_imported_component(core_bytes, "wasi:cli/exit@0.2.0", "exit", pnames, pvals, "wasi-exit");

    let output: string = "";
    let i: i32 = 0;
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

	goMod := mod.New()
	goMod.TypeParams = [][]byte{{encode.ValtypeI32}}
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

func TestWASMCoreEncoderGoLangByteEquivalence(t *testing.T) {
	goMod := mod.New()
	goMod.TypeParams = [][]byte{{}}
	goMod.TypeResults = [][]byte{{encode.ValtypeI32}}
	goMod.FunctionTypeidxs = []uint32{0, 0}
	goMod.MemoryPresent = true
	goMod.MemoryMin = 1
	goMod.MemoryMax = -1
	goMod.TablePresent = true
	goMod.TableMin = 1
	goMod.TableMax = -1
	goMod.ElementOffsets = []int32{0}
	goMod.ElementFuncidxs = [][]uint32{{0}}
	goMod.ExportNames = []string{"main"}
	goMod.ExportKinds = []byte{sections.ExportFunc}
	goMod.ExportIdxs = []uint32{1}

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

	f1 := inst.InstI32Const(nil, 0)
	f1 = inst.InstCallIndirect(f1, 0, 0)
	f1body := inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), f1)

	goMod.CodeBodies = [][]byte{f0body, f1body}
	goBytes := mod.Build(goMod)

	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/memory";
import "std/wasm/encode";
import "std/wasm/sections";
import "std/wasm/imports";
function main(): i32 {
    let m: module.Module = module.module_new();
    let p0: u8[] = [];
    let r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32, 0u32]);
    m = m.with_memory(1u32, 0 - 1);
    m = m.with_table(imports.reftype_funcref(), 1u32, 0 - 1);
    m = m.with_elements([0], [[0u32]]);
    m = m.with_exports(["main"], [sections.export_func()], [1u32]);

    let f0: u8[] = inst.inst_i32_const([], 0);
    f0 = inst.inst_i32_const(f0, 9);
    f0 = inst.inst_i32_const(f0, 1);
    f0 = memory.inst_memory_fill(f0);
    f0 = inst.inst_i32_const(f0, 4);
    f0 = inst.inst_i32_const(f0, 0);
    f0 = inst.inst_i32_const(f0, 1);
    f0 = memory.inst_memory_copy(f0);
    f0 = inst.inst_i32_const(f0, 7);
    let f0body: u8[] = inst.put_function_body([], inst.put_locals_empty([]), f0);

    let f1: u8[] = inst.inst_i32_const([], 0);
    f1 = inst.inst_call_indirect(f1, 0u32, 0u32);
    let f1body: u8[] = inst.put_function_body([], inst.put_locals_empty([]), f1);

    m = m.with_code([f0body, f1body]);
    let bytes: u8[] = module.build(m);

    let output: string = "";
    let i: i32 = 0;
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
    let m: module.Module = module.module_new();
    let p0: u8[] = [encode.valtype_i32()];
    let r0: u8[] = [];
    m = m.with_types([p0], [r0]);
    m = m.with_imports(["wasi"], ["exit"], [imports.import_func()], [imports.import_desc_func(0u32)]);
    m = m.with_functions([]);
    m = m.with_code([]);
    let core_bytes: u8[] = module.build(m);

    let pnames: string[] = ["code"];
    let pvals: u8[] = [component.cvaltype_u32()];
    let comp: u8[] = component.build_wasi_imported_component(core_bytes, "wasi:cli/exit@0.2.0", "exit", pnames, pvals, "wasi");

    let output: string = "";
    let i: i32 = 0;
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
    let m: module.Module = module.module_new();
    let p0: u8[] = [encode.valtype_i32()];
    let r0: u8[] = [];
    m = m.with_types([p0], [r0]);
    m = m.with_imports(["wasi"], ["exit"], [imports.import_func()], [imports.import_desc_func(0u32)]);
    m = m.with_functions([]);
    m = m.with_code([]);
    let core_bytes: u8[] = module.build(m);

    // Component build.
    let pnames: string[] = ["code"];
    let pvals: u8[] = [component.cvaltype_u32()];

    let comp: u8[] = component.put_component_header([]);
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

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMComponentWasiStdMultiExport(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    let names: string[] = ["flush", "close"];
    let ps0: string[] = [];
    let pv0: u8[] = [];
    let ps1: string[] = [];
    let pv1: u8[] = [];
    let names_per: string[][] = [ps0, ps1];
    let valtypes_per: u8[][] = [pv0, pv1];

    let comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_one_instance_with_func_exports(comp, names, names_per, valtypes_per);
    comp = component.put_import_section_one_instance(comp, "test:multi/iface", 0u32);

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMComponentWasiExitImport(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    let comp: u8[] = component.put_component_header([]);
    // Type 0: instance { export "exit" (func (param "code" u32)) }
    let names: string[] = ["code"];
    let valtypes: u8[] = [component.cvaltype_u32()];
    comp = component.put_type_section_one_instance_one_func_export(comp, "exit", names, valtypes);
    // Import "wasi:cli/exit@0.2.0" of type 0 (the instance type).
    comp = component.put_import_section_one_instance(comp, "wasi:cli/exit@0.2.0", 0u32);

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMComponentCoreTypeFunc(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
import "std/wasm/encode";
function main(): i32 {
    let params: u8[] = [encode.valtype_i32()];
    let results: u8[] = [encode.valtype_i32()];
    let comp: u8[] = component.put_component_header([]);
    comp = component.put_core_type_section_one_func(comp, params, results);

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMComponentTypeFuncU32(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    let comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_one_func_no_param(comp, component.cvaltype_u32());

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMComponentTypeFuncWithParams(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    let names: string[] = ["x", "y"];
    let valtypes: u8[] = [component.cvaltype_u32(), component.cvaltype_string()];
    let comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_one_func(comp, names, valtypes, component.cvaltype_bool());

    let output: string = "";
    let i: i32 = 0;
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
    let names0: string[] = [];
    let valtypes0: u8[] = [];
    let names1: string[] = ["n"];
    let valtypes1: u8[] = [component.cvaltype_u32()];
    let names_per: string[][] = [names0, names1];
    let valtypes_per: u8[][] = [valtypes0, valtypes1];
    let results_per: u8[] = [component.cvaltype_u32(), component.cvaltype_string()];

    let comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_funcs(comp, names_per, valtypes_per, results_per);

    let output: string = "";
    let i: i32 = 0;
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
    let payload: u8[] = [];
    payload = payload.append(116u8);   // 't' = 0x74
    payload = payload.append(101u8);   // 'e' = 0x65
    payload = payload.append(115u8);   // 's' = 0x73
    payload = payload.append(116u8);   // 't' = 0x74

    let comp: u8[] = component.put_component_header([]);
    comp = component.put_component_type_section(comp, payload);

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMComponentImportFunc(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    let comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_one_func_no_param(comp, component.cvaltype_u32());
    comp = component.put_import_section_one_func(comp, "host-func", 0u32);

    let output: string = "";
    let i: i32 = 0;
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
    let names0: string[] = [];
    let valtypes0: u8[] = [];
    let names1: string[] = ["n"];
    let valtypes1: u8[] = [component.cvaltype_u32()];
    let names_per: string[][] = [names0, names1];
    let valtypes_per: u8[][] = [valtypes0, valtypes1];
    let results_per: u8[] = [component.cvaltype_u32(), component.cvaltype_string()];

    let comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_funcs(comp, names_per, valtypes_per, results_per);

    // Two imports, one of each type.
    let imp_names: string[] = ["h0", "h1"];
    let imp_types: u32[] = [0u32, 1u32];
    comp = component.put_import_section_funcs(comp, imp_names, imp_types);

    let output: string = "";
    let i: i32 = 0;
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
	if strings.Count(watStr, "(import ") < 2 {
		t.Errorf("expected at least 2 (import) declarations, got:\n%s", watStr)
	}
}

func TestWASMComponentCanonLower(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    let names: string[] = ["x"];
    let valtypes: u8[] = [component.cvaltype_u32()];

    let comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_one_func(comp, names, valtypes, component.cvaltype_u32());
    comp = component.put_import_section_one_func(comp, "h", 0u32);
    comp = component.put_canon_section_lower_no_opts(comp, 0u32);

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMComponentCanonLowersTwo(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    // One functype: () -> u32. Both imports re-use it.
    let no_names: string[] = [];
    let no_valtypes: u8[] = [];
    let imp_names: string[] = ["h0", "h1"];
    let imp_types: u32[] = [0u32, 0u32];

    let comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_one_func(comp, no_names, no_valtypes, component.cvaltype_u32());
    comp = component.put_import_section_funcs(comp, imp_names, imp_types);

    // Lower both imported funcs in one canon section.
    let lower_funcs: u32[] = [0u32, 1u32];
    comp = component.put_canon_section_lowers_no_opts(comp, lower_funcs);

    let output: string = "";
    let i: i32 = 0;
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
    let no_names: string[] = [];
    let no_valtypes: u8[] = [];
    let names_per: string[][] = [no_names, no_names];
    let valtypes_per: u8[][] = [no_valtypes, no_valtypes];
    let results_per: u8[] = [component.cvaltype_u32(), component.cvaltype_u32()];

    let comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_funcs(comp, names_per, valtypes_per, results_per);

    // Two imports + lower each to a core func.
    let imp_names: string[] = ["h0", "h1"];
    let imp_types: u32[] = [0u32, 1u32];
    comp = component.put_import_section_funcs(comp, imp_names, imp_types);

    let lower_funcs: u32[] = [0u32, 1u32];
    comp = component.put_canon_section_lowers_no_opts(comp, lower_funcs);

    // Package both lowered core funcs into one core instance.
    let exp_names: string[] = ["stdout", "exit"];
    let exp_core_funcs: u32[] = [0u32, 1u32];
    comp = component.put_core_instance_section_from_func_exports(comp, exp_names, exp_core_funcs);

    let output: string = "";
    let i: i32 = 0;
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
    let m: module.Module = module.module_new();
    let p0: u8[] = [];
    let r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_imports(["host_a", "host_b"], ["f", "f"], [imports.import_func(), imports.import_func()], [imports.import_desc_func(0u32), imports.import_desc_func(0u32)]);
    // No funcs of our own.
    m = m.with_functions([]);
    m = m.with_code([]);
    let core_bytes: u8[] = module.build(m);

    // Component: 1 type, 2 imports, 2 lowers, 2 single-export
    // instances, then instantiate the core with both.
    let no_names: string[] = [];
    let no_valtypes: u8[] = [];
    let comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_one_func(comp, no_names, no_valtypes, component.cvaltype_u32());

    let imp_names: string[] = ["a", "b"];
    let imp_types: u32[] = [0u32, 0u32];
    comp = component.put_import_section_funcs(comp, imp_names, imp_types);

    let lower_funcs: u32[] = [0u32, 1u32];
    comp = component.put_canon_section_lowers_no_opts(comp, lower_funcs);

    // Two instances, each wrapping one lowered core func under name "f".
    comp = component.put_core_instance_section_from_one_func_export(comp, "f", 0u32);
    comp = component.put_core_instance_section_from_one_func_export(comp, "f", 1u32);

    // Core module section + instantiate-with-two-args section.
    comp = component.put_core_module_section(comp, core_bytes);
    let arg_names: string[] = ["host_a", "host_b"];
    let arg_instance_idxs: u32[] = [0u32, 1u32];
    comp = component.put_core_instance_section_instantiate_with_instance_args(comp, 0u32, arg_names, arg_instance_idxs);

    let output: string = "";
    let i: i32 = 0;
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
    let m: module.Module = module.module_new();
    let p0: u8[] = [];
    let r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    // Type 0 = () -> i32. Import "host"."f" of type 0.
    m = m.with_imports(["host"], ["f"], [imports.import_func()], [imports.import_desc_func(0u32)]);
    // Function 0 is the imported func (after imports). Function 1
    // is our own; type 0 too. Function 1 = "main", body: call 0.
    m = m.with_functions([0u32]);
    m = m.with_exports(["main"], [sections.export_func()], [1u32]);  // imports shift the export idx by 1
    let bodyExpr: u8[] = inst.inst_call([], 0u32);
    let fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), bodyExpr);
    m = m.with_code([fn]);
    let core_bytes: u8[] = module.build(m);

    // Component envelope.
    let names: string[] = [];
    let valtypes: u8[] = [];
    let comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_one_func(comp, names, valtypes, component.cvaltype_u32());
    comp = component.put_import_section_one_func(comp, "h", 0u32);
    comp = component.put_canon_section_lower_no_opts(comp, 0u32);
    comp = component.put_core_instance_section_from_one_func_export(comp, "f", 0u32);
    comp = component.put_core_module_section(comp, core_bytes);
    comp = component.put_core_instance_section_instantiate_with_one_instance_arg(comp, 0u32, "host", 0u32);

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMComponentExportSectionTwoFuncs(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    let no_names: string[] = [];
    let no_valtypes: u8[] = [];

    let comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_one_func(comp, no_names, no_valtypes, component.cvaltype_u32());
    comp = component.put_import_section_one_func(comp, "h", 0u32);

    // Re-export the imported func twice under different names.
    let exp_names: string[] = ["g0", "g1"];
    let exp_funcs: u32[] = [0u32, 0u32];
    comp = component.put_export_section_funcs(comp, exp_names, exp_funcs);

    let output: string = "";
    let i: i32 = 0;
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
	if strings.Count(watStr, "(export ") < 2 {
		t.Errorf("expected at least 2 (export) declarations, got:\n%s", watStr)
	}
}

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
    let m: module.Module = module.module_new();
    let p0: u8[] = [];
    let r0: u8[] = [encode.valtype_i32()];
    let p1: u8[] = [encode.valtype_i32(), encode.valtype_i32(), encode.valtype_i32(), encode.valtype_i32()];
    let r1: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0, p1], [r0, r1]);
    m = m.with_functions([0u32, 1u32]);
    m = m.with_memory(1u32, 0 - 1);
    m = m.with_exports(["f", "alloc", "mem"], [sections.export_func(), sections.export_func(), sections.export_memory()], [0u32, 1u32, 0u32]);
    let body_f: u8[] = inst.put_function_body([], inst.put_locals_empty([]), inst.inst_i32_const([], 7));
    let body_alloc: u8[] = inst.put_function_body([], inst.put_locals_empty([]), inst.inst_i32_const([], 0));
    m = m.with_code([body_f, body_alloc]);
    let core_bytes: u8[] = module.build(m);

    // Component: header + core-module + core-instance + 2 alias
    // calls (one for the lift target func, one for the realloc
    // func). Memory aliasing would need a multi-sort alias composer
    // that std/wasm/component does not yet ship; this slice tests
    // the canon-lift binary shape only, so we feed canon-lift a
    // memory idx that doesn t resolve to an alias — wasm-tools
    // accepts the byte shape even when the memory idx is out of
    // range, since validation of the component world is a separate
    // pass.
    let comp: u8[] = component.put_component_header([]);
    comp = component.put_core_module_section(comp, core_bytes);
    comp = component.put_core_instance_section_instantiate(comp, 0u32);
    comp = component.put_alias_section_core_export_func(comp, 0u32, "f");
    comp = component.put_alias_section_core_export_func(comp, 0u32, "alloc");

    // Type: () -> u32 (would-be string in a real lift, but u32 keeps
    // the wasm-tools structural check satisfied without needing
    // canonical-ABI lowering compatibility).
    let no_names: string[] = [];
    let no_valtypes: u8[] = [];
    comp = component.put_type_section_one_func(comp, no_names, no_valtypes, component.cvaltype_u32());

    // canon lift with mem + realloc opts. core-func 0 is the
    // aliased "f"; we use mem idx 0 and core-func idx 1 (the
    // aliased "alloc"). wasm-tools accepts this even without an
    // explicit core-memory alias because the canon-lift binary
    // form just carries the indices — wasm-tools checks they're
    // within bounds, not that they came from an alias section.
    comp = component.put_canon_section_lift_mem_realloc(comp, 0u32, 0u32, 0u32, 1u32);

    let output: string = "";
    let i: i32 = 0;
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
    let m: module.Module = module.module_new();
    // type 0: (i32 i32 i32 i32) -> i32 (realloc signature)
    let p0: u8[] = [encode.valtype_i32(), encode.valtype_i32(), encode.valtype_i32(), encode.valtype_i32()];
    let r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_memory(1u32, 0 - 1);
    m = m.with_exports(["mem", "alloc"], [sections.export_memory(), sections.export_func()], [0u32, 0u32]);
    let body_alloc: u8[] = inst.put_function_body([], inst.put_locals_empty([]), inst.inst_i32_const([], 0));
    m = m.with_code([body_alloc]);
    let core_bytes: u8[] = module.build(m);

    let comp: u8[] = component.put_component_header([]);
    comp = component.put_core_module_section(comp, core_bytes);
    comp = component.put_core_instance_section_instantiate(comp, 0u32);

    // Alias both memory + alloc out of the instance.
    let sorts: u8[] = [component.core_sort_memory(), component.core_sort_func()];
    let instance_idxs: u32[] = [0u32, 0u32];
    let alias_names: string[] = ["mem", "alloc"];
    comp = component.put_alias_section_core_exports(comp, sorts, instance_idxs, alias_names);

    // Type: (s: string) -> u32. Then import "h" of that type.
    let p_names: string[] = ["s"];
    let p_valtypes: u8[] = [component.cvaltype_string()];
    comp = component.put_type_section_one_func(comp, p_names, p_valtypes, component.cvaltype_u32());
    comp = component.put_import_section_one_func(comp, "h", 0u32);

    // Lower the import under mem + realloc opts. mem idx 0
    // (the only aliased memory) and core-func idx 0 (the aliased
    // realloc).
    comp = component.put_canon_section_lower_mem_realloc(comp, 0u32, 0u32, 0u32);

    let output: string = "";
    let i: i32 = 0;
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
    let m: module.Module = module.module_new();
    let p0: u8[] = [];
    let r0: u8[] = [encode.valtype_i32()];
    let p1: u8[] = [encode.valtype_i32(), encode.valtype_i32(), encode.valtype_i32(), encode.valtype_i32()];
    let r1: u8[] = [encode.valtype_i32()];
    let p2: u8[] = [encode.valtype_i32(), encode.valtype_i32(), encode.valtype_i32()];
    let r2: u8[] = [];
    m = m.with_types([p0, p1, p2], [r0, r1, r2]);
    m = m.with_functions([0u32, 1u32, 2u32]);
    m = m.with_memory(1u32, 0 - 1);
    m = m.with_exports(["f", "alloc", "cleanup", "mem"], [sections.export_func(), sections.export_func(), sections.export_func(), sections.export_memory()], [0u32, 1u32, 2u32, 0u32]);
    let body_f: u8[] = inst.put_function_body([], inst.put_locals_empty([]), inst.inst_i32_const([], 0));
    let body_alloc: u8[] = inst.put_function_body([], inst.put_locals_empty([]), inst.inst_i32_const([], 0));
    let body_cleanup: u8[] = inst.put_function_body([], inst.put_locals_empty([]), []);
    m = m.with_code([body_f, body_alloc, body_cleanup]);
    let core_bytes: u8[] = module.build(m);

    let comp: u8[] = component.put_component_header([]);
    comp = component.put_core_module_section(comp, core_bytes);
    comp = component.put_core_instance_section_instantiate(comp, 0u32);

    // Alias all four into the top-level core space.
    let sorts: u8[] = [component.core_sort_func(), component.core_sort_memory(), component.core_sort_func(), component.core_sort_func()];
    let instance_idxs: u32[] = [0u32, 0u32, 0u32, 0u32];
    let alias_names: string[] = ["f", "mem", "alloc", "cleanup"];
    comp = component.put_alias_section_core_exports(comp, sorts, instance_idxs, alias_names);

    let no_names: string[] = [];
    let no_valtypes: u8[] = [];
    comp = component.put_type_section_one_func(comp, no_names, no_valtypes, component.cvaltype_u32());

    // canon-lift with mem (idx 0) + realloc (core-func 1) +
    // post-return (core-func 2). Lift target is core-func 0.
    comp = component.put_canon_section_lift_mem_realloc_post_return(comp, 0u32, 0u32, 0u32, 1u32, 2u32);

    let output: string = "";
    let i: i32 = 0;
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
    let m: module.Module = module.module_new();
    let p0: u8[] = [];
    let r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["f"], [sections.export_func()], [0u32]);
    let bodyExpr: u8[] = inst.inst_i32_const([], 11);
    let fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), bodyExpr);
    m = m.with_code([fn]);
    let core_bytes: u8[] = module.build(m);

    // Component: preamble + the seven-section recipe.
    let comp: u8[] = component.put_component_header([]);
    comp = component.put_core_module_section(comp, core_bytes);
    comp = component.put_core_instance_section_instantiate(comp, 0u32);
    comp = component.put_alias_section_core_export_func(comp, 0u32, "f");
    comp = component.put_type_section_one_func_no_param(comp, component.cvaltype_u32());
    comp = component.put_canon_section_lift_no_opts(comp, 0u32, 0u32);
    comp = component.put_export_section_one_func(comp, "g", 0u32);

    let output: string = "";
    let i: i32 = 0;
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
    let m: module.Module = module.module_new();
    let p0: u8[] = [];
    let r0: u8[] = [encode.valtype_i32()];
    let p1: u8[] = [encode.valtype_i32(), encode.valtype_i32(), encode.valtype_i32(), encode.valtype_i32()];
    let r1: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0, p1], [r0, r1]);
    m = m.with_functions([0u32, 1u32]);
    m = m.with_memory(1u32, 0 - 1);
    m = m.with_exports(["f", "alloc", "mem"], [sections.export_func(), sections.export_func(), sections.export_memory()], [0u32, 1u32, 0u32]);
    let body_f: u8[] = inst.put_function_body([], inst.put_locals_empty([]), inst.inst_i32_const([], 0));
    let body_alloc: u8[] = inst.put_function_body([], inst.put_locals_empty([]), inst.inst_i32_const([], 0));
    m = m.with_code([body_f, body_alloc]);
    let core_bytes: u8[] = module.build(m);

    let comp: u8[] = component.put_component_header([]);
    comp = component.put_core_module_section(comp, core_bytes);
    comp = component.put_core_instance_section_instantiate(comp, 0u32);

    let sorts: u8[] = [component.core_sort_func(), component.core_sort_memory(), component.core_sort_func()];
    let instance_idxs: u32[] = [0u32, 0u32, 0u32];
    let alias_names: string[] = ["f", "mem", "alloc"];
    comp = component.put_alias_section_core_exports(comp, sorts, instance_idxs, alias_names);

    let no_names: string[] = [];
    let no_valtypes: u8[] = [];
    comp = component.put_type_section_one_func(comp, no_names, no_valtypes, component.cvaltype_string());

    // canon-lift with utf16 string encoding + mem + realloc.
    comp = component.put_canon_section_lift_mem_realloc_encoding(comp, 0u32, 0u32, 0u32, 1u32, component.canonopt_string_encoding_utf16());

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMComponentTypeFuncNoResult(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    let names: string[] = ["status"];
    let valtypes: u8[] = [component.cvaltype_u32()];
    let comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_one_func_no_result(comp, names, valtypes);

    let output: string = "";
    let i: i32 = 0;
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
	if strings.Contains(watStr, "(result ") {
		t.Errorf("unexpected (result ...) for a no-result functype:\n%s", watStr)
	}
}

func TestWASMComponentStartSection(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    let no_params: string[] = [];
    let no_valtypes: u8[] = [];

    let comp: u8[] = component.put_component_header([]);
    // Type 0: (func)
    comp = component.put_type_section_one_func_no_result(comp, no_params, no_valtypes);
    // Import "f" of type 0.
    comp = component.put_import_section_one_func(comp, "f", 0u32);
    // Start: call func 0 with no args, no result bindings.
    comp = component.put_start_section_no_args_no_results(comp, 0u32);

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMComponentResourceNewsRepsTwo(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
import "std/wasm/encode";
function main(): i32 {
    let comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_one_resource(comp, encode.valtype_i32());
    comp = component.put_type_section_one_resource(comp, encode.valtype_i32());
    let idxs: u32[] = [0u32, 1u32];
    comp = component.put_canon_section_resource_news(comp, idxs);
    comp = component.put_canon_section_resource_reps(comp, idxs);

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMComponentResourceDropsTwo(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
import "std/wasm/encode";
function main(): i32 {
    let comp: u8[] = component.put_component_header([]);
    // Two resource types, both rep=i32, no dtor.
    comp = component.put_type_section_one_resource(comp, encode.valtype_i32());
    comp = component.put_type_section_one_resource(comp, encode.valtype_i32());
    // Drop both in one canon section.
    let idxs: u32[] = [0u32, 1u32];
    comp = component.put_canon_section_resource_drops(comp, idxs);

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMComponentResourceNewRep(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
import "std/wasm/encode";
function main(): i32 {
    let comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_one_resource(comp, encode.valtype_i32());
    comp = component.put_canon_section_resource_new(comp, 0u32);
    comp = component.put_canon_section_resource_rep(comp, 0u32);

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMComponentOwnBorrowHandles(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
import "std/wasm/encode";
function main(): i32 {
    let comp: u8[] = component.put_component_header([]);
    // Type 0: resource with i32 rep, no dtor.
    comp = component.put_type_section_one_resource(comp, encode.valtype_i32());
    // Type 1: own<$0>.
    comp = component.put_type_section_one_own(comp, 0u32);
    // Type 2: borrow<$0>.
    comp = component.put_type_section_one_borrow(comp, 0u32);

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMComponentVariantMixed(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    let names: string[] = ["ok", "err", "abort"];
    let has_payload: boolean[] = [true, true, false];
    let payload_vts: u8[] = [component.cvaltype_u32(), component.cvaltype_string(), 0u8];
    let comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_one_variant(comp, names, has_payload, payload_vts);

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMComponentRecordNameAge(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    let names: string[] = ["name", "age"];
    let valtypes: u8[] = [component.cvaltype_string(), component.cvaltype_u32()];
    let comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_one_record(comp, names, valtypes);

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMComponentFlagsRWX(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    let labels: string[] = ["read", "write", "execute"];
    let comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_one_flags(comp, labels);

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMComponentEnumColor(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    let labels: string[] = ["red", "green", "blue"];
    let comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_one_enum(comp, labels);

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMComponentResultOkErr(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    let comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_one_result_ok_err(comp, component.cvaltype_u32(), component.cvaltype_string());

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMComponentTupleStringU32(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    let elems: u8[] = [component.cvaltype_string(), component.cvaltype_u32()];
    let comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_one_tuple(comp, elems);

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMComponentOptionOfU32(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    let comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_one_option(comp, component.cvaltype_u32());

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMComponentListOfU8(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
function main(): i32 {
    let comp: u8[] = component.put_component_header([]);
    comp = component.put_type_section_one_list(comp, component.cvaltype_u8());

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMComponentResourceDrop(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `
import "std/i32";
import "std/wasm/component";
import "std/wasm/encode";
function main(): i32 {
    let comp: u8[] = component.put_component_header([]);
    // Resource type 0: representation is i32, no dtor.
    comp = component.put_type_section_one_resource(comp, encode.valtype_i32());
    // canon resource.drop of type 0.
    comp = component.put_canon_section_resource_drop(comp, 0u32);

    let output: string = "";
    let i: i32 = 0;
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
    let m: module.Module = module.module_new();
    let p0: u8[] = [encode.valtype_i32(), encode.valtype_i32()];
    let r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["add"], [sections.export_func()], [0u32]);
    let body: u8[] = inst.inst_local_get([], 0u32);
    body = inst.inst_local_get(body, 1u32);
    body = numeric.inst_i32_add(body);
    let fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), body);
    m = m.with_code([fn]);
    let core_bytes: u8[] = module.build(m);

    // Wrap as a component-level (u32, u32) -> u32.
    let names: string[] = ["a", "b"];
    let valtypes: u8[] = [component.cvaltype_u32(), component.cvaltype_u32()];
    let comp: u8[] = component.build_lifted_export_component_with_params(core_bytes, "add", "add", names, valtypes, component.cvaltype_u32());

    let output: string = "";
    let i: i32 = 0;
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
    let m: module.Module = module.module_new();
    let p0: u8[] = [];
    let r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["f"], [sections.export_func()], [0u32]);
    let bodyExpr: u8[] = inst.inst_i32_const([], 99);
    let fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), bodyExpr);
    m = m.with_code([fn]);
    let core_bytes: u8[] = module.build(m);

    // The whole component recipe in one call.
    let comp: u8[] = component.build_lifted_export_component(core_bytes, "f", "g", component.cvaltype_u32());

    let output: string = "";
    let i: i32 = 0;
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
    let m: module.Module = module.module_new();
    let p0: u8[] = [];
    let r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["f"], [sections.export_func()], [0u32]);
    let bodyExpr: u8[] = inst.inst_i32_const([], 42);
    let fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), bodyExpr);
    m = m.with_code([fn]);
    let core_bytes: u8[] = module.build(m);

    // Component envelope: full lift-and-export recipe.
    let no_names: string[] = [];
    let no_valtypes: u8[] = [];
    let comp: u8[] = component.put_component_header([]);
    comp = component.put_core_module_section(comp, core_bytes);
    comp = component.put_core_instance_section_instantiate(comp, 0u32);
    comp = component.put_alias_section_core_export_func(comp, 0u32, "f");
    comp = component.put_type_section_one_func(comp, no_names, no_valtypes, component.cvaltype_u32());
    comp = component.put_canon_section_lift_no_opts(comp, 0u32, 0u32);
    comp = component.put_export_section_one_func(comp, "g", 0u32);

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMModuleRunsIfElse(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    let m: module.Module = module.module_new();
    let p0: u8[] = [];
    let r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);

    // Body: i32.const 1 ; if (result i32) { i32.const 100 }
    //       else { i32.const 200 } ; end
    let body: u8[] = inst.inst_i32_const([], 1);
    body = inst.inst_if_start(body, encode.valtype_i32());
    body = inst.inst_i32_const(body, 100);
    body = inst.inst_else(body);
    body = inst.inst_i32_const(body, 200);
    body = inst.inst_end(body);
    let fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), body);
    m = m.with_code([fn]);
    let bytes: u8[] = module.build(m);

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMModuleRunsLocalSetGet(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    let m: module.Module = module.module_new();
    let p0: u8[] = [];
    let r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);

    // 1 local of type i32 (index 0).
    let localsBytes: u8[] = inst.put_locals_one_group([], 1u32, encode.valtype_i32());

    // Body: i32.const 42 ; local.set 0 ; local.get 0
    let body: u8[] = inst.inst_i32_const([], 42);
    body = inst.inst_local_set(body, 0u32);
    body = inst.inst_local_get(body, 0u32);

    let fn: u8[] = inst.put_function_body([], localsBytes, body);
    m = m.with_code([fn]);
    let bytes: u8[] = module.build(m);

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMModuleRunsSumLoop(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/numeric";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    let m: module.Module = module.module_new();
    let p0: u8[] = [];
    let r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);

    // 2 locals of type i32: index 0 = i (counter), index 1 = sum.
    let localsBytes: u8[] = inst.put_locals_one_group([], 2u32, encode.valtype_i32());

    let body: u8[] = inst.inst_loop_start([], inst.blocktype_empty());
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

    let fn: u8[] = inst.put_function_body([], localsBytes, body);
    m = m.with_code([fn]);
    let bytes: u8[] = module.build(m);

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMSectionsCustom(t *testing.T) {
	src := `import "std/wasm/sections";
function main(): i32 {
    let empty: u8[] = [];

    // Custom section named "foo" with payload [0xAA, 0xBB].
    // Body: uleb(3) + "foo" (3 bytes) + 0xAA 0xBB = 6 bytes.
    // Wrapped: id(0) + uleb(6) + body = 8 bytes total.
    let payload: u8[] = [170u8, 187u8];
    let s: u8[] = sections.encode_custom_section(empty, "foo", payload);
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
    let s2: u8[] = sections.encode_custom_section(empty, "x", empty);
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
    let m: module.Module = module.module_new();
    let p0: u8[] = [];
    let r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);
    let bodyExpr: u8[] = inst.inst_i32_const([], 42);
    let fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), bodyExpr);
    m = m.with_code([fn]);
    let core_bytes: u8[] = module.build(m);

    let marker_payload: u8[] = [104u8, 101u8, 108u8, 108u8, 111u8, 45u8, 108u8, 97u8, 110u8, 103u8];
    let with_custom: u8[] = sections.encode_custom_section(core_bytes, "marker", marker_payload);

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMModuleRunsMemoryLoad(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/memory";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    let m: module.Module = module.module_new();
    let p0: u8[] = [];
    let r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_memory(1u32, 0 - 1);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);
    m = m.with_data([0], [[42u8, 7u8, 99u8, 5u8]]);

    let body: u8[] = inst.inst_i32_const([], 2);
    body = memory.inst_i32_load8_u(body, 0u32, 0u32);
    let fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), body);
    m = m.with_code([fn]);
    let bytes: u8[] = module.build(m);

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMModuleRunsMemoryStoreLoad(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/memory";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    let m: module.Module = module.module_new();
    let p0: u8[] = [];
    let r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_memory(1u32, 0 - 1);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);

    let body: u8[] = inst.inst_i32_const([], 16);
    body = inst.inst_i32_const(body, 77);
    body = memory.inst_i32_store8(body, 0u32, 0u32);
    body = inst.inst_i32_const(body, 16);
    body = memory.inst_i32_load8_u(body, 0u32, 0u32);
    let fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), body);
    m = m.with_code([fn]);
    let bytes: u8[] = module.build(m);

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMModuleRunsCallIndirect(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/encode";
import "std/wasm/sections";
import "std/wasm/imports";
function main(): i32 {
    let m: module.Module = module.module_new();

    // One shared type: () -> i32 (both callees and main).
    let p0: u8[] = [];
    let r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);

    // func0 / func1 are indirect-call targets; func2 is main.
    m = m.with_functions([0u32, 0u32, 0u32]);

    // Funcref table holding both targets.
    m = m.with_table(imports.reftype_funcref(), 2u32, 0 - 1);

    // table[0] = func0, table[1] = func1.
    m = m.with_elements([0], [[0u32, 1u32]]);

    m = m.with_exports(["main"], [sections.export_func()], [2u32]);

    // func0 -> 11, func1 -> 22.
    let f0: u8[] = inst.put_function_body([], inst.put_locals_empty([]), inst.inst_i32_const([], 11));
    let f1: u8[] = inst.put_function_body([], inst.put_locals_empty([]), inst.inst_i32_const([], 22));

    // main: i32.const 1 ; call_indirect type 0 table 0 -> func1 -> 22.
    let main_body: u8[] = inst.inst_i32_const([], 1);
    main_body = inst.inst_call_indirect(main_body, 0u32, 0u32);
    let f2: u8[] = inst.put_function_body([], inst.put_locals_empty([]), main_body);

    m = m.with_code([f0, f1, f2]);
    let bytes: u8[] = module.build(m);

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMModuleRunsMemoryCopy(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/memory";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    let m: module.Module = module.module_new();
    let p0: u8[] = [];
    let r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_memory(1u32, 0 - 1);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);
    m = m.with_data([0], [[5u8, 6u8, 7u8, 8u8]]);

    // memory.copy dst=16, src=0, len=2  -> bytes [5,6] land at 16,17.
    let body: u8[] = inst.inst_i32_const([], 16);
    body = inst.inst_i32_const(body, 0);
    body = inst.inst_i32_const(body, 2);
    body = memory.inst_memory_copy(body);
    // Read back addr 17 -> 6.
    body = inst.inst_i32_const(body, 17);
    body = memory.inst_i32_load8_u(body, 0u32, 0u32);
    let fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), body);
    m = m.with_code([fn]);
    let bytes: u8[] = module.build(m);

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMModuleRunsMemoryFill(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/memory";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    let m: module.Module = module.module_new();
    let p0: u8[] = [];
    let r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_memory(1u32, 0 - 1);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);

    // memory.fill dst=8, val=9, len=3 -> addrs 8,9,10 = 9.
    let body: u8[] = inst.inst_i32_const([], 8);
    body = inst.inst_i32_const(body, 9);
    body = inst.inst_i32_const(body, 3);
    body = memory.inst_memory_fill(body);
    // Read back addr 9 -> 9.
    body = inst.inst_i32_const(body, 9);
    body = memory.inst_i32_load8_u(body, 0u32, 0u32);
    let fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), body);
    m = m.with_code([fn]);
    let bytes: u8[] = module.build(m);

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMModuleRunsGlobalSection(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/imports";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    let m: module.Module = module.module_new();
    let p0: u8[] = [];
    let r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);

    let initExpr: u8[] = inst.inst_i32_const([], 314);
    initExpr = inst.inst_end(initExpr);
    m = m.with_globals([encode.valtype_i32()], [imports.mut_const()], [initExpr]);

    let body: u8[] = inst.inst_global_get([], 0u32);
    let fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), body);
    m = m.with_code([fn]);
    let bytes: u8[] = module.build(m);

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMModuleRunsDivision(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/numeric";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    let m: module.Module = module.module_new();
    let p0: u8[] = [];
    let r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);

    let body: u8[] = inst.inst_i32_const([], 0 - 20);
    body = inst.inst_i32_const(body, 3);
    body = numeric.inst_i32_div_s(body);
    let fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), body);
    m = m.with_code([fn]);
    let bytes: u8[] = module.build(m);

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMModuleRunsSignExtend(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/convert";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    let m: module.Module = module.module_new();
    let p0: u8[] = [];
    let r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);

    // i32.const 255 ; i32.extend8_s -> -1
    let body: u8[] = inst.inst_i32_const([], 255);
    body = convert.inst_i32_extend8_s(body);
    let fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), body);
    m = m.with_code([fn]);
    let bytes: u8[] = module.build(m);

    let output: string = "";
    let i: i32 = 0;
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
    let m: module.Module = module.module_new();
    let p0: u8[] = [];
    let r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);

    let body: u8[] = inst.inst_i64_const([], 1000000000000i64);
    body = inst.inst_i64_const(body, 234567890123i64);
    body = numeric.inst_i64_add(body);
    body = convert.inst_i32_wrap_i64(body);
    let fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), body);
    m = m.with_code([fn]);
    let bytes: u8[] = module.build(m);

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMModuleRunsBitwise(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/numeric";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    let m: module.Module = module.module_new();
    let p0: u8[] = [];
    let r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);

    let body: u8[] = inst.inst_i32_const([], 12);
    body = inst.inst_i32_const(body, 10);
    body = numeric.inst_i32_xor(body);
    let fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), body);
    m = m.with_code([fn]);
    let bytes: u8[] = module.build(m);

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMModuleRunsSignedVsUnsignedCmp(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/numeric";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    let m: module.Module = module.module_new();
    let p0: u8[] = [];
    let r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);

    let body: u8[] = inst.inst_i32_const([], 0 - 1);
    body = inst.inst_i32_const(body, 1);
    body = numeric.inst_i32_lt_s(body);
    let fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), body);
    m = m.with_code([fn]);
    let bytes: u8[] = module.build(m);

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMModuleRunsShiftRotate(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/numeric";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    let m: module.Module = module.module_new();
    let p0: u8[] = [];
    let r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);

    // (1 << 5) = 32 ; then rotr by 1 -> 16.
    let body: u8[] = inst.inst_i32_const([], 1);
    body = inst.inst_i32_const(body, 5);
    body = numeric.inst_i32_shl(body);
    body = inst.inst_i32_const(body, 1);
    body = numeric.inst_i32_rotr(body);
    let fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), body);
    m = m.with_code([fn]);
    let bytes: u8[] = module.build(m);

    let output: string = "";
    let i: i32 = 0;
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
    let m: module.Module = module.module_new();
    let p0: u8[] = [];
    let r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);

    // f32 1.0 bit pattern: 0x3F800000 = 1065353216.
    let body: u8[] = inst.inst_f32_const([], 1065353216u32);
    body = inst.inst_f32_const(body, 1065353216u32);
    body = numeric.inst_f32_add(body);
    body = convert.inst_i32_reinterpret_f32(body);
    let fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), body);
    m = m.with_code([fn]);
    let bytes: u8[] = module.build(m);

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMModuleRunsSelect(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    let m: module.Module = module.module_new();
    let p0: u8[] = [];
    let r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);

    let body: u8[] = inst.inst_i32_const([], 7);
    body = inst.inst_i32_const(body, 9);
    body = inst.inst_i32_const(body, 1);
    body = inst.inst_select(body);
    let fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), body);
    m = m.with_code([fn]);
    let bytes: u8[] = module.build(m);

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMModuleRunsRecursion(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/numeric";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    let m: module.Module = module.module_new();

    let p_fact: u8[] = [encode.valtype_i32()];
    let r_fact: u8[] = [encode.valtype_i32()];
    let p_main: u8[] = [];
    let r_main: u8[] = [encode.valtype_i32()];
    m = m.with_types([p_fact, p_main], [r_fact, r_main]);

    m = m.with_functions([0u32, 1u32]);
    m = m.with_exports(["main"], [sections.export_func()], [1u32]);

    // fact(n): if (n <= 1) 1 else n * fact(n - 1)
    let fact_body: u8[] = inst.inst_local_get([], 0u32);
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
    let fact_fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), fact_body);

    // main(): i32.const 5 ; call 0
    let main_body: u8[] = inst.inst_i32_const([], 5);
    main_body = inst.inst_call(main_body, 0u32);
    let main_fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), main_body);

    m = m.with_code([fact_fn, main_fn]);
    let bytes: u8[] = module.build(m);

    let output: string = "";
    let i: i32 = 0;
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
    let m: module.Module = module.module_new();
    let p0: u8[] = [];
    let r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);

    // f64 1.5 = 0x3FF8000000000000; f64 2.5 = 0x4004000000000000.
    let body: u8[] = inst.inst_f64_const([], 4609434218613702656u64);
    body = inst.inst_f64_const(body, 4612811918334230528u64);
    body = numeric.inst_f64_add(body);
    body = convert.inst_i64_reinterpret_f64(body);
    body = inst.inst_i64_const(body, 32i64);
    body = numeric.inst_i64_shr_u(body);
    body = convert.inst_i32_wrap_i64(body);
    let fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), body);
    m = m.with_code([fn]);
    let bytes: u8[] = module.build(m);

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMModuleRunsClz(t *testing.T) {
	src := `
import "std/i32";
import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/numeric";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    let m: module.Module = module.module_new();
    let p0: u8[] = [];
    let r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);

    let body: u8[] = inst.inst_i32_const([], 16);
    body = numeric.inst_i32_clz(body);
    let fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), body);
    m = m.with_code([fn]);
    let bytes: u8[] = module.build(m);

    let output: string = "";
    let i: i32 = 0;
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
    let m: module.Module = module.module_new();
    let p0: u8[] = [];
    let r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_memory(1u32, 0 - 1);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);
    m = m.with_data([0], [[10u8, 20u8, 30u8, 40u8]]);

    let localsBytes: u8[] = inst.put_locals_one_group([], 2u32, encode.valtype_i32());

    let body: u8[] = inst.inst_loop_start([], inst.blocktype_empty());
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

    let fn: u8[] = inst.put_function_body([], localsBytes, body);
    m = m.with_code([fn]);
    let bytes: u8[] = module.build(m);

    let output: string = "";
    let i: i32 = 0;
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
		{"sleb_i64", "9223372036854775807i64", []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x00}},
		{"sleb_i64", "(0i64 - 9223372036854775807i64) - 1i64", []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x7f}},
	}

	var sb strings.Builder
	sb.WriteString(`import "std/wasm/leb128";
import "std/i32";
import "std/string";

function dump(bs: u8[]): string {
    let s: string = "";
    let i: i32 = 0;
    while (i < bs.len()) {
        if (i > 0) { s = s + " "; }
        s = s + (bs[i] as i32).to_string();
        i = i + 1;
    }
    return s;
}

function main(): i32 {
    let empty: u8[] = [];
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

func TestWASMSectionsCrossValidates(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}

	out := encode.PutModuleHeader(nil)

	out = sections.EncodeTypeSection(out,
		[][]byte{nil}, [][]byte{{encode.ValtypeI32}})

	out = sections.EncodeFunctionSection(out, []uint32{0})

	out = sections.EncodeExportSection(out,
		[]string{"main"},
		[]byte{sections.ExportFunc},
		[]uint32{0})

	body := []byte{0x00, 0x41, 0x2a, 0x0b}
	codeEntry := append(leb128.UlebU32(nil, uint32(len(body))), body...)
	out = sections.EncodeCodeSection(out, [][]byte{codeEntry})

	stdout := runWasmModule(t, out)
	if strings.TrimSpace(stdout) != "42" {
		t.Fatalf("wasmtime stdout = %q, want %q", stdout, "42")
	}
}

func TestWASMInstCrossValidates(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}

	out := encode.PutModuleHeader(nil)
	out = sections.EncodeTypeSection(out,
		[][]byte{nil}, [][]byte{{encode.ValtypeI32}})
	out = sections.EncodeFunctionSection(out, []uint32{0})
	out = sections.EncodeExportSection(out,
		[]string{"main"},
		[]byte{sections.ExportFunc},
		[]uint32{0})

	body := inst.InstI32Const(nil, 7)
	body = inst.InstLocalSet(body, 0)
	body = inst.InstI32Const(body, 11)
	body = inst.InstLocalSet(body, 1)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 1)
	body = append(body, 0x4a)
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
	out = sections.EncodeDataSection(out,
		[]int32{0}, [][]byte{{1, 2, 3, 4}})

	stdout := runWasmModule(t, out)
	if strings.TrimSpace(stdout) != "10" {
		t.Fatalf("wasmtime stdout = %q, want %q (sum of [1,2,3,4])", stdout, "10")
	}
}

func TestWASMModuleBuildCross(t *testing.T) {
	maybeSkipNoWasmtime := func() {
		if _, err := exec.LookPath("wasmtime"); err != nil {
			t.Skip("wasmtime not on PATH")
		}
	}
	maybeSkipNoWasmtime()

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

	langSrc := `import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/encode";
import "std/wasm/sections";
import "std/i32";
import "std/string";
function main(): i32 {
    let m: module.Module = module.module_new();
    let p0: u8[] = [];
    let r0: u8[] = [encode.valtype_i32()];
    m = m.with_types([p0], [r0]);
    m = m.with_functions([0u32]);
    m = m.with_exports(["main"], [sections.export_func()], [0u32]);
    let body: u8[] = inst.inst_i32_const([], 42);
    let fn: u8[] = inst.put_function_body([], inst.put_locals_empty([]), body);
    m = m.with_code([fn]);
    let bytes: u8[] = module.build(m);

    let output: string = "";
    let i: i32 = 0;
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

func TestWASMRcMapSelfAssignNoUnderflow(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    let m: Map[string, i32] = map_new(8);
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

	aliased := `
import "core/map";
function main(): i32 {
    let m1: Map[string, i32] = map_new(8);
    m1 = m1.insert("a", 1);
    let m2 = m1;                  // alias → rc 2
    m2 = m2.insert("a", 999);        // copy; old handle released, not leaked
    return __rc_underflow_count() * 1000
         + (m1.get_or("a", 0) - 1)
         + (m2.get_or("a", 0) - 999);
}`
	if got := runWasm(t, aliased); got != 0 {
		t.Errorf("got %d, want 0 (aliased self-assign: 0 underflow, m1=1, m2=999)", got)
	}
}

func TestWASMRcDropEnumPayload(t *testing.T) {
	fresh := `struct W { a: i32[] }
struct V2 { b: i32 }
type U = W | V2;
function mk(): U { return W { a: [1, 2, 3] }; }
function build(): i32 {
    let u: U = mk();
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
    let w: W = W { a: 7 };
    let u: U = w;
    return w.a + __rc_underflow_count() - 7;
}`
	if got := runWasm(t, aliased); got != 0 {
		t.Errorf("got %d, want 0 (aliased struct widened to union: 0 underflow)", got)
	}

	nonUniform := `enum E { Arr(i32[]), Num(i32) }
function main(): i32 {
    let e: E = Arr([1, 2, 3]);
    let f: E = Num(9);
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

func TestWASMArrayPushAliasedCopies(t *testing.T) {
	src := `function main(): i32 {
    let xs: i32[] = [10, 20];
    xs = xs.append(30);
    let ys = xs;
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

func TestWASMArrayIndexSetAliasedCopies(t *testing.T) {
	src := `function main(): i32 {
    let xs: i32[] = [10, 20, 30];
    let ys = xs;
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

func TestWASMArrayIndexSetU8Stride(t *testing.T) {
	src := `function main(): i32 {
    let buf: u8[] = __alloc_u8(4);
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

func TestWASMArrayIndexSetStructField(t *testing.T) {
	src := `struct State { items: i32[] }
function main(): i32 {
    let s: State = State{items: [10, 20, 30]};
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

func TestWASMArrayIndexSetStructFieldAliasedCopies(t *testing.T) {
	src := `struct State { items: i32[] }
function main(): i32 {
    let arr: i32[] = [10, 20, 30];
    let s: State = State{items: arr};
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

func TestWASMArrayIndexSetNestedStructField(t *testing.T) {
	src := `struct Inner { items: i32[] }
struct Outer { inner: Inner }
function main(): i32 {
    let o: Outer = Outer{inner: Inner{items: [10, 20, 30]}};
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

func TestWASMArrayIndexSetNestedStructFieldAliasedCopies(t *testing.T) {
	src := `struct Inner { items: i32[] }
struct Outer { inner: Inner }
function main(): i32 {
    let arr: i32[] = [10, 20, 30];
    let o: Outer = Outer{inner: Inner{items: arr}};
    o = Outer { ...o, inner: Inner { ...o.inner, items: o.inner.items.with(1, 999) } };
    if (arr[1] != 20) { return 1; }
    if (o.inner.items[1] != 999) { return 2; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (aliased a.b.field[i]=v must copy)", got)
	}
}

func TestWASMArrayIndexSetMat(t *testing.T) {
	src := `function main(): i32 {
    let mat: i32[][] = [[1, 2, 3], [4, 5, 6]];
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

func TestWASMArrayIndexSetMatInnerAliasedCopies(t *testing.T) {
	src := `function main(): i32 {
    let mat: i32[][] = [[1, 2], [3, 4]];
    let inner = mat[0];
    mat = mat.with(0, mat[0].with(1, 999));
    if (inner[1] != 2) { return 1; }
    if (mat[0][1] != 999) { return 2; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (mat[i][j]=v with aliased inner must copy)", got)
	}
}

func TestWASMMapSetReturnsMap(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    let m: Map[string, i32] = map_new(8);
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

func TestWASMArrayIndexSetObjMatInnerAliasedCopies(t *testing.T) {
	src := `struct State { mat: i32[][] }
function main(): i32 {
    let inner: i32[] = [1, 2, 3];
    let s: State = State{mat: [inner, [4, 5, 6]]};
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

func TestWASMArraySetSelfAssign(t *testing.T) {
	src := `function main(): i32 {
    let xs: i32[] = [10, 20, 30];
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

func TestWASMArraySetAliasedCopies(t *testing.T) {
	src := `function main(): i32 {
    let xs: i32[] = [10, 20, 30];
    let ys = xs;
    ys = ys.with(0, 999);
    if (xs[0] != 10) { return 1; }
    if (ys[0] != 999) { return 2; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (aliased arr.with must copy)", got)
	}
}

func TestWASMMapDeleteReturnsMapBool(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    let m: Map[string, i32] = map_new(8);
    m = m.insert("a", 1);
    m = m.insert("b", 2);
    m = m.insert("c", 3);
    let hadb: boolean = m.contains("b");
    let mb = m.remove("b");
    if (!hadb) { return 1; }
    m = mb;
    if (m.remove("z").len() != m.len())  { return 2; }
    let ok: boolean = m.contains("a");
    let m2 = m.remove("a");
    if (!ok) { return 3; }
    if (m2.contains("a")) { return 4; }
    if (!m2.contains("c")) { return 5; }
    if (m2.len() != 1) { return 6; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (Map.delete returns (Map, bool))", got)
	}
}

func TestWASMMapClearReturnsMap(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    let m: Map[string, i32] = map_new(8);
    m = m.insert("x", 10);
    m = m.insert("y", 20);
    if (m.len() != 2) { return 1; }
    m = m.cleared();
    if (m.len() != 0) { return 2; }
    if (m.contains("x")) { return 3; }
    m = m.insert("z", 99);
    if (m.len() != 1) { return 4; }
    if (m.get_or("z", 0) != 99) { return 5; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (Map.clear returns Map)", got)
	}
}

func TestWASMMapSetAliasedCopies(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    let m1: Map[string, i32] = map_new(8);
    m1 = m1.insert("a", 1);                 // in-place (rc==1)
    let m2 = m1;                    // alias → rc=2
    m2 = m2.insert("a", 999);          // rc>1 → copy; m1 unchanged
    if (m1.get_or("a", 0) != 1)   { return 1; }
    if (m2.get_or("a", 0) != 999) { return 2; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (aliased Map.set must copy)", got)
	}
}

func TestWASMMapDeleteClearAliasedCopies(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    let m1: Map[string, i32] = map_new(8);
    m1 = m1.insert("a", 1);
    m1 = m1.insert("b", 2);
    let m2 = m1;                       // alias → rc=2
    let ok: boolean = m2.contains("a");
    let m3 = m2.remove("a");     // rc>1 → copy; m1/m2 intact
    if (!ok)            { return 1; }
    if (m1.len() != 2)  { return 2; }
    if (!m1.contains("a"))   { return 3; }
    if (m3.len() != 1)  { return 4; }
    if (m3.contains("a"))    { return 5; }
    let m4 = m1;                       // alias → rc=2
    m4 = m4.cleared();                   // rc>1 → copy; m1 intact
    if (m1.len() != 2)  { return 6; }
    if (m4.len() != 0)  { return 7; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (aliased Map.delete/clear must copy)", got)
	}
}

func TestWASMTupleStructElem(t *testing.T) {
	src := `struct Inner { x: i32, y: i32 }
function main(): i32 {
    let t: (i32, Inner) = (1, Inner { x: 2, y: 3 });
    if (t.0 != 1) { return 1; }
    let inner: Inner = t.1;
    if (inner.x != 2) { return 2; }
    if (inner.y != 3) { return 3; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (struct in tuple slot)", got)
	}
}

func TestWASMTupleArrayElem(t *testing.T) {
	src := `function main(): i32 {
    let t: (i32, i32[]) = (1, [10, 20, 30]);
    if (t.0 != 1) { return 1; }
    let arr: i32[] = t.1;
    if (arr.len() != 3) { return 2; }
    if (arr[0] != 10) { return 3; }
    if (arr[2] != 30) { return 4; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (array in tuple slot)", got)
	}
}

func TestWASMTupleNestedTuple(t *testing.T) {
	src := `function main(): i32 {
    let t: (i32, (i32, i32)) = (1, (2, 3));
    let (a, b) = t;
    if (a != 1) { return 1; }
    let (c, d) = b;
    if (c != 2) { return 2; }
    if (d != 3) { return 3; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (nested tuple destructure)", got)
	}
}

func TestWASMLexerChainedTupleNumericAccess(t *testing.T) {
	src := `function main(): i32 {
    let t: (i32, (i32, i32)) = (1, (2, 3));
    if (t.0 != 1) { return 1; }
    if (t.1.0 != 2) { return 2; }
    if (t.1.1 != 3) { return 3; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (chained t.1.0 numeric access)", got)
	}
}

func TestWASMEmptyMapDestinationInference(t *testing.T) {
	src := `
import "core/map";
function take(m: Map[string, i32]): i32 { return m.len(); }
function mkEmpty(): Map[i32, string] { return Map {}; }
function main(): i32 {
    let a: Map[string, i32] = Map {};
    if (a.len() != 0) { return 1; }
    a = a.insert("k", 42);
    if (a.get_or("k", 0) != 42) { return 2; }
    let b: Map[i32, string] = Map {};
    if (b.len() != 0) { return 3; }
    b = b.insert(7, "hello");
    if (!b.contains(7)) { return 4; }
    if (take(Map {}) != 0) { return 5; }
    let r = mkEmpty();
    if (r.len() != 0) { return 6; }
    let d: Map[i32, i32] = Map {};
    if (d.len() != 0) { return 7; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (empty Map literal inherits K/V from destination)", got)
	}
}

func TestWASMEnumVariantInTuple(t *testing.T) {
	src := `enum Color { Red, Green, Blue }
function main(): i32 {
    let t: (i32, Color) = (1, Green);
    if (t.0 != 1) { return 1; }
    match (t.1) {
        Red => { return 2; },
        Green => { },
        Blue => { return 3; }
    }
    let u: (i32, Option[i32]) = (5, Some(42));
    if (u.0 != 5) { return 4; }
    match (u.1) {
        Some(v) => { if (v != 42) { return 5; } },
        None => { return 6; }
    }
    let w: (Color, i32) = (Blue, 99);
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

func TestWASMMapPointerShapedValues(t *testing.T) {
	src := `
import "core/map";
struct P { x: i32, y: i32 }
function main(): i32 {
    let mt: Map[string, (i32, i32)] = Map {};
    mt = mt.insert("a", (3, 4));
    match (mt.get("a")) {
        Some(p) => { if (p.0 + p.1 != 7) { return 1; } },
        None => { return 2; }
    }
    let ms: Map[string, P] = Map {};
    ms = ms.insert("a", P { x: 3, y: 4 });
    match (ms.get("a")) {
        Some(s) => { if (s.x + s.y != 7) { return 3; } },
        None => { return 4; }
    }
    let ma: Map[i32, i32[]] = Map {};
    ma = ma.insert(1, [10, 20, 30]);
    match (ma.get(1)) {
        Some(arr) => { if (arr[0] + arr[2] != 40) { return 5; } },
        None => { return 6; }
    }
    let mi: Map[string, i32] = Map {};
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

func TestWASMStructTupleFieldAccess(t *testing.T) {
	src := `struct Rec { pos: (i32, i32), name: string }
struct Nested { t: (i32, (i32, i32)) }
function main(): i32 {
    let r: Rec = Rec { pos: (3, 4), name: "p" };
    if (r.pos.0 != 3) { return 1; }
    if (r.pos.1 != 4) { return 2; }
    let n: Nested = Nested { t: (1, (2, 3)) };
    if (n.t.0 != 1) { return 3; }
    if (n.t.1.0 != 2) { return 4; }
    if (n.t.1.1 != 3) { return 5; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (struct tuple-field chained access)", got)
	}
}

func TestWASMUnsignedComparison(t *testing.T) {
	src := `function main(): i32 {
    let big: u32 = 4294967295u32;
    if (!(big > 0u32)) { return 1; }
    if (!(big > 1000000u32)) { return 2; }
    if (big < 5u32) { return 3; }
    if (!(big >= 4294967295u32)) { return 4; }
    if (big <= 100u32) { return 5; }
    let b64: u64 = 18446744073709551615u64;
    if (!(b64 > 9u64)) { return 6; }
    if (b64 < 9u64) { return 7; }
    let u: u8 = 200u8;
    if (!(u > 100u8)) { return 8; }
    let i: u32 = 4294967293u32;
    let c: i32 = 0;
    while (i > 4294967290u32) { c = c + 1; i = i - 1u32; }
    if (c != 3) { return 9; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (unsigned comparison condition codes)", got)
	}
}

func TestWASMUnaryMinusWideTypes(t *testing.T) {
	src := `function main(): i32 {
    let a: i64 = -5i64;
    if (a != 0i64 - 5i64) { return 1; }
    let b: f64 = -5.0;
    if (!(b < 0.0)) { return 2; }
    let c: f64 = -b;
    if (c != 5.0) { return 3; }
    let f: f32 = -2.5f32;
    if (!(f < 0.0f32)) { return 4; }
    let z: f64 = -0.0;
    if (f64_bits(z) == 0i64) { return 5; }
    let g: i64 = 10i64 + -3i64;
    if (g != 7i64) { return 6; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (unary minus on wide / non-i32 types)", got)
	}
}

func TestWASMScientificNotation(t *testing.T) {
	src := `function main(): i32 {
    let a: f64 = 1e3;
    if (a != 1000.0) { return 1; }
    let b: f64 = 1.5e3;
    if (b != 1500.0) { return 2; }
    let c: f64 = 1500.0e-3;
    if (c != 1.5) { return 3; }
    let d: f64 = 1.5e+3;
    if (d != 1500.0) { return 4; }
    let e: f64 = 2.5E2;
    if (e != 250.0) { return 5; }
    let f: f32 = 1.5e2f32;
    if (f != 150.0f32) { return 6; }
    let big: f64 = 1.8e19;
    if (!(big > 1.7e19)) { return 7; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (scientific-notation float literals)", got)
	}
}

func TestWASMSubI32ArithmeticWraps(t *testing.T) {
	src := `struct S { v: u8 }
function main(): i32 {
    let a: u8 = 255u8;
    a = a + 1u8;
    if ((a as i32) != 0) { return 1; }
    let b: u8 = 0u8;
    b = b - 1u8;
    if ((b as i32) != 255) { return 2; }
    let c: u8 = 16u8;
    c = c * 16u8;
    if ((c as i32) != 0) { return 3; }
    let s: S = S { v: 200u8 };
    let h: u8 = s.v + 100u8;
    if ((h as i32) != 44) { return 4; }
    let k: u8 = 100u8;
    k = k + 50u8;
    if ((k as i32) != 150) { return 5; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got exit %d, want 0 (sub-i32 arithmetic wraps to width)", got)
	}
}

func TestWASMWideKeyMapDispatch(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    let m: Map[i64, i32] = map_new(4);
    let big: i64 = (7 as i64) << (33 as i64);
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

func TestWASMVoidCallThroughFunctionValue(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"fn-typed-parameter", `
function apply(f: (i32) => void, x: i32): void { f(x); }
function main(): i32 {
    let seen: i32 = 0;
    apply((n: i32): void => { seen = n; }, 7);
    if (seen != 7) { return 1; }
    return 0;
}
`},
		{"local-lambda", `
function main(): i32 {
    let seen: i32 = 0;
    let lam: (i32) => void = (n: i32): void => { seen = n; };
    lam(7);
    if (seen != 7) { return 1; }
    return 0;
}
`},
		{"capturing-closure", `
function main(): i32 {
    let base: i32 = 10;
    let seen: i32 = 0;
    let cap: (i32) => void = (n: i32): void => { seen = n + base; };
    cap(7);
    if (seen != 17) { return 1; }
    return 0;
}
`},
		{"array-element", `
function main(): i32 {
    let seen: i32 = 0;
    let arr: ((i32) => void)[] = [(n: i32): void => { seen = n; }];
    arr[0](7);
    if (seen != 7) { return 1; }
    return 0;
}
`},
		{"higher-order-result", `
function main(): i32 {
    let seen: i32 = 0;
    let pick: () => (i32) => void = (): (i32) => void => {
        return (n: i32): void => { seen = n; };
    };
    pick()(7);
    if (seen != 7) { return 1; }
    return 0;
}
`},
		{"inline-arrow-lambda", `
function main(): i32 {
    let sink: i32 = 0;
    ((x: i32) => { sink = sink + x; })(4);
    return sink - 4;
}
`},
		{"inline-arrow-lambda-discarded-value", `
function main(): i32 {
    let sink: i32 = 0;
    ((x: i32) => { sink = sink + x; return sink; })(4);
    return sink - 4;
}
`},
		{"pvec-for-each", `
import "std/pvec";
function main(): i32 {
    let v: pvec.PVec[i32] = pvec.pvec_new();
    v = v.append(1);
    v = v.append(2);
    let total: i32 = 0;
    v.for_each((x: i32): void => { total = total + x; });
    if (total != 3) { return 1; }
    return 0;
}
`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if code := runWasm(t, c.src); code != 0 {
				t.Errorf("%s: exit = %d, want 0 (a validation failure shows as a non-zero exit here)", c.name, code)
			}
		})
	}
}
