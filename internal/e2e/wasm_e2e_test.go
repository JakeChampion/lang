// E2E tests for the WASM backend, executed against a preview-2
// Component Model component under wasmtime. The pipeline is parse
// → check → wasm.EmitWithOptions{PrintMainResult: true} →
// wasm-tools parse + component embed + component new (with the
// wasi-preview1-component-adapter for the legacy entry-point
// trampoline only) → `wasmtime run`. Tests skip when any of
// wasm-tools / wasmtime / `LANG_WASI_ADAPTER` is missing so
// `go test ./...` stays green on machines without the toolchain.
package e2e

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/wasm"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
	"github.com/jakechampion/lang/internal/parser"
)

// preview2 tool discovery: cached across the whole test run since
// the answers don't change. preview2Setup and witDirSetup hold the
// memoised lookup state; callers go through skipIfPreview2Missing
// + witRoot.
var (
	preview2Once sync.Once
	preview2Err  error

	witOnce sync.Once
	witDir  string
	witErr  error
)

func skipIfPreview2Missing(t *testing.T) {
	t.Helper()
	preview2Once.Do(func() {
		if runtime.GOOS == "windows" {
			preview2Err = errors.New("preview-2 toolchain not exercised on windows")
			return
		}
		if _, err := exec.LookPath("wasm-tools"); err != nil {
			preview2Err = errors.New("wasm-tools not on PATH")
			return
		}
		if _, err := exec.LookPath("wasmtime"); err != nil {
			preview2Err = errors.New("wasmtime not on PATH")
			return
		}
		adapter := os.Getenv("LANG_WASI_ADAPTER")
		if adapter == "" {
			preview2Err = errors.New("LANG_WASI_ADAPTER not set (CI sets this)")
			return
		}
		if _, err := os.Stat(adapter); err != nil {
			preview2Err = err
			return
		}
	})
	if preview2Err != nil {
		t.Skipf("preview-2 prerequisites missing: %v", preview2Err)
	}
}

// witRoot locates the `cmd/lang/wit` tree on disk by walking up
// from the current working directory. wasm-tools resolves WIT
// imports through a real filesystem path, so we can't ship the
// tree as `embed.FS` from this package.
func witRoot(t *testing.T) string {
	t.Helper()
	witOnce.Do(func() {
		cwd, err := os.Getwd()
		if err != nil {
			witErr = err
			return
		}
		for d := cwd; d != "/" && d != "."; d = filepath.Dir(d) {
			cand := filepath.Join(d, "cmd", "lang", "wit")
			if _, err := os.Stat(filepath.Join(cand, "lang.wit")); err == nil {
				witDir = cand
				return
			}
		}
		witErr = errors.New("cmd/lang/wit not found above " + cwd)
	})
	if witErr != nil {
		t.Fatal(witErr)
	}
	return witDir
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
func buildComponent(t *testing.T, src string) string {
	t.Helper()
	skipIfPreview2Missing(t)

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
	wat, err := wasm.EmitWithOptions(prog, info, wasm.EmitOptions{
		PrintMainResult: true,
	})
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	return finishComponent(t, wat)
}

// buildComponentMulti is buildComponent for a module set loaded
// through modload (the multi-file analogue).
func buildComponentMulti(t *testing.T, entry string, files map[string]string) string {
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
	wat, err := wasm.EmitWithOptions(prog, info, wasm.EmitOptions{
		PrintMainResult: true,
	})
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	return finishComponent(t, wat)
}

// finishComponent runs the wasm-tools parse + component embed +
// component new pipeline against the WAT text produced by
// wasm.Emit. Returns the path of the resulting component .wasm.
func finishComponent(t *testing.T, wat string) string {
	t.Helper()
	dir := t.TempDir()
	watPath := filepath.Join(dir, "prog.wat")
	if err := os.WriteFile(watPath, []byte(wat), 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	modulePath := filepath.Join(dir, "prog.wasm")
	if out, err := exec.Command("wasm-tools", "parse", watPath, "-o", modulePath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools parse: %v\n%s\n--- wat ---\n%s", err, out, wat)
	}
	embeddedPath := filepath.Join(dir, "prog.embedded.wasm")
	if out, err := exec.Command("wasm-tools", "component", "embed",
		witRoot(t), "-w", "lang",
		modulePath, "-o", embeddedPath,
	).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools component embed: %v\n%s", err, out)
	}
	componentPath := filepath.Join(dir, "prog.component.wasm")
	if out, err := exec.Command("wasm-tools", "component", "new",
		"--adapt", "wasi_snapshot_preview1="+os.Getenv("LANG_WASI_ADAPTER"),
		embeddedPath, "-o", componentPath,
	).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools component new: %v\n%s", err, out)
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
	return so.String(), se.String(), cmd.ProcessState.ExitCode()
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

// Cross-module struct types end-to-end on WASM: same shape as the
// arm32 e2e check, run through wasmtime.
func TestWASMCrossModuleStructType(t *testing.T) {
	got := runWasmMultiFile(t, "main.lang", map[string]string{
		"point.lang": `pub struct Point { x: i32, y: i32 }
pub function make(x: i32, y: i32): Point {
	return Point { x: x, y: y };
}`,
		"main.lang": `import "./point";
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
// `wasmtime run --invoke main mod.wat alpha beta` therefore yields
// argv = [mod.wat, alpha, beta] and `len(args()) == 3`.
func TestWASMArgsBuiltin(t *testing.T) {
	src := `function main(): i32 {
		var a: string[] = args();
		return len(a);
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
			Some(line) => { write(line); return len(line); },
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

// Sub-i32 widths (u8 / i8 / u16 / i16). Storage lives in i32
// locals (wasm has no narrower locals); the bookkeeping is in
// the casts: narrowing masks to dw bits, signed widening emits
// `i32.extend8_s` / `i32.extend16_s`. Arithmetic is at i32
// precision.
func TestWASMSubI32Widths(t *testing.T) {
	src := `function main(): i32 {
    var a: u8 = 200;
    var b: u8 = 50;
    var sum: u8 = (a + b) as u8;
    if (sum != 250) { return 1; }
    var s: i8 = -7;
    var widened: i32 = s as i32;
    if (widened != -7) { return 2; }
    var u: u16 = 65000;
    var u32_form: u32 = u as u32;
    if (u32_form != 65000) { return 3; }
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
    if (len(bytes) != 3) { return 4; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (u8 array)", got)
	}
}

// Owned i8 arrays. Same shape as the u8 test but the load is
// sign-extending (`i32.load8_s`), so -1 reads back as -1.
func TestWASMI8Array(t *testing.T) {
	src := `function main(): i32 {
    var v: i8[] = [-1, 1, 127];
    if ((v[0] as i32) != -1) { return 1; }
    if ((v[1] as i32) != 1) { return 2; }
    if ((v[2] as i32) != 127) { return 3; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (i8 array)", got)
	}
}

// Owned u16 arrays. 2-byte stride, zero-extending load.
func TestWASMU16Array(t *testing.T) {
	src := `function main(): i32 {
    var v: u16[] = [65000, 1, 32768];
    if (v[0] != 65000) { return 1; }
    if (v[1] != 1) { return 2; }
    if (v[2] != 32768) { return 3; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (u16 array)", got)
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
// `m.set(k, v)`, `m.get(k)`, `m.has(k)`, `m.len()`. This is
// the first cut from PR 4 in docs/LANGUAGE-DIRECTION.md;
// generic K / V and the IndexMap fingerprint-table layout
// land in follow-ups.
func TestWASMMapBasics(t *testing.T) {
	src := `function main(): i32 {
    var m: Map = map_new(8);
    if (m.len() != 0) { return 1; }
    if (m.has(7)) { return 2; }
    m.set(7, 42);
    m.set(11, 99);
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
    m.set(7, 100);
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

// Map dynamic resize: insert past the initial capacity and
// verify all entries remain reachable. Without resize this
// would trap on `unreachable`; with resize the buffer doubles
// (2 → 4 → 8 → 16) and the wrapper's data pointer follows
// along.
func TestWASMMapResize(t *testing.T) {
	src := `function main(): i32 {
    var m: Map = map_new(2);
    var i: i32 = 0;
    while (i < 12) {
        m.set(i, i * 10);
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

// Slice views (`[T]`) — `arr[a:b]` produces a non-owning view
// over the parent's storage. Verifies len + indexing on a slice
// over an owned array AND sub-slicing (slice-of-slice).
// Exercises the new $__slice_make + $__slice_idx runtime helpers,
// plus the `len(slice)` codegen path that loads length from
// `slice + 4` instead of `arr - 4`.
func TestWASMSliceViews(t *testing.T) {
	src := `function main(): i32 {
    var arr: i32[] = [10, 20, 30, 40, 50];
    var s: [i32] = arr[1:4];          // [20, 30, 40]
    if (len(s) != 3) { return 1; }
    if (s[0] != 20) { return 2; }
    if (s[1] != 30) { return 3; }
    if (s[2] != 40) { return 4; }
    var t = s[1:3];                   // [30, 40]
    if (len(t) != 2) { return 5; }
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
    if (len(head) != 3 || head[0] != 1 || head[2] != 3) { return 1; }
    var tail: [i32] = arr[2:];
    if (len(tail) != 3 || tail[0] != 3 || tail[2] != 5) { return 2; }
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

// Mixing i32 and i64 without an `as` cast is a checker error —
// implicit widening is rejected per docs/LANGUAGE-DIRECTION.md.
// We can't run this through runWasm (it'd fail compilation), so
// just verify the checker rejects it.
func TestI64ImplicitWideningRejected(t *testing.T) {
	src := `function bad(): i64 { var x: i32 = 1; var y: i64 = 2 as i64; return x + y; }`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err == nil {
		t.Fatalf("expected checker error for mixed i32/i64 add, got none")
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

// In legacy mode (no closures), function values are bare table
// indices. If the AST emitter ever pushed funcIndex (position in
// prog.Funcs) instead of tableIndex (position in the funcref
// table), call_indirect would either trap or dispatch to the wrong
// function. This program declares two non-table functions before
// `target` so funcIndex["target"] = 2 but tableIndex["target"] = 0;
// dispatching through `apply(target, 4)` must hit `target` (which
// returns 40), not trap or hit a different entry.
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
	src := `function f(x: float): float { return -x; }
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
		a[2] = 100;
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

func TestWASMSwitchBasic(t *testing.T) {
	src := `function classify(n: i32): i32 {
		switch (n) {
			case 0: return 100;
			case 1, 2, 3: return 200;
			case 7: return 700;
			default: return 0;
		}
		return -1;
	}
	function main(): i32 {
		return classify(0) + classify(2) + classify(7) + classify(99);
	}`
	// 100 + 200 + 700 + 0 = 1000
	if got := runWasm(t, src); got != 1000 {
		t.Errorf("got %d, want 1000", got)
	}
}

func TestWASMSwitchBreakInLoop(t *testing.T) {
	// `break` inside a case exits the switch, not the enclosing loop.
	src := `function main(): i32 {
		var sum: i32 = 0;
		for (var i: i32 = 0; i < 5; i = i + 1) {
			switch (i) {
				case 2: break;
				default: sum = sum + i;
			}
		}
		return sum;
	}`
	// 0 + 1 + 3 + 4 = 8 (i=2 breaks the switch but the loop continues)
	if got := runWasm(t, src); got != 8 {
		t.Errorf("got %d, want 8", got)
	}
}

func TestWASMTernary(t *testing.T) {
	src := `function abs(n: i32): i32 { return n < 0 ? 0 - n : n; }
		function main(): i32 { return abs(-7); }`
	if got := runWasm(t, src); got != 7 {
		t.Errorf("got %d, want 7", got)
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
	src := `function main(): i32 { return len("hello"); }`
	if got := runWasm(t, src); got != 5 {
		t.Errorf("got %d, want 5", got)
	}
}

func TestWASMStringIndexAndCompare(t *testing.T) {
	src := `function main(): i32 {
		var s: string = "abc";
		var byte: i32 = s[1];
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
	src := `struct Point { x: i32, y: i32 }
		function main(): i32 {
			var p: Point = Point { x: 10, y: 32 };
			p.x = p.x + 5;
			return p.x + p.y;
		}`
	// (10+5) + 32 = 47
	if got := runWasm(t, src); got != 47 {
		t.Errorf("got %d, want 47", got)
	}
}

func TestWASMStructPassByReference(t *testing.T) {
	src := `struct Box { v: i32 }
		function bump(b: Box): void { b.v = b.v + 100; }
		function main(): i32 {
			var b: Box = Box { v: 5 };
			bump(b);
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
		return len(c);
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
				Bad(msg) => { return len(msg); }
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
	src := `enum Option[T] { Some(T), None }
		function find(): Option[i32] { return Some(42); }
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
	src := `enum Option[T] { Some(T), None }
		function find(): Option[i32] { return None; }
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
				Err(msg) => { return len(msg); }
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
// arena_save / arena_restore work the same way on the WASM
// backend (memory[40] is the bump cursor). Same test shape as
// TestArenaResetReclaimsAllocations but routed through wasmtime.
func TestWASMArenaReset(t *testing.T) {
	src := `function main(): i32 {
		var saved: i32 = arena_save();
		var a: i32[] = [1, 2, 3, 4, 5];
		var afterAlloc: i32 = arena_save();
		arena_restore(saved);
		var afterRestore: i32 = arena_save();
		if (afterAlloc <= saved) { return 1; }
		if (afterRestore != saved) { return 2; }
		if (len(a) != 5) { return 3; }
		return 0;
	}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("WASM arena_save/restore: exit = %d, want 0", got)
	}
}

// random_bytes(n) on WASM goes through `wasi_snapshot_preview1.
// random_get`. Same length / non-equality assertions as the
// arm32 version.
func TestWASMRandomBytes(t *testing.T) {
	src := `function main(): i32 {
		var a: string = random_bytes(16);
		var b: string = random_bytes(16);
		if (len(a) != 16) { return 1; }
		if (len(b) != 16) { return 2; }
		if (a == b) { return 3; }
		return 0;
	}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("WASM random_bytes: exit = %d, want 0", got)
	}
}

func TestWASMOptionFloatPayload(t *testing.T) {
	src := `function pick(): Option[float] { return Some(3.14); }
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

// Result[float, string] — Ok carries an f32 payload through the
// heap. Same fix path as Option[float], but with a
// non-payload-less variant on each arm so the test covers a
// realistic two-typed-parameter shape.
func TestWASMResultFloatOk(t *testing.T) {
	src := `function check(x: float): Result[float, string] {
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
			Some(_) => { return 1; },
			None => { return 0; }
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
			Some(_) => { return 1; },
			None => {}
		}
		match (read_file("rt.txt")) {
			Ok(s) => { return len(s); },
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
