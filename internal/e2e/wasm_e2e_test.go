// E2E tests for the WASM backend, executed under wasmtime when it's
// installed. They skip otherwise so `go test ./...` stays green on
// machines without a WASM runtime.
package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/wasm"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/parser"
)

func wasmtimePath(t *testing.T) string {
	t.Helper()
	for _, c := range []string{"wasmtime"} {
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	t.Skip("wasmtime not installed; skipping WASM e2e test")
	return ""
}

// invokeWasmtime runs `wasmtime run --invoke main` against src and
// returns stdout / stderr separately. Splitting them is important
// because wasmtime emits an `--invoke` warning on stderr that would
// otherwise be interleaved with the program's own output.
func invokeWasmtime(t *testing.T, src string) (stdout, stderr string) {
	t.Helper()
	wt := wasmtimePath(t)

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
	wat, err := wasm.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}

	dir := t.TempDir()
	watPath := filepath.Join(dir, "prog.wat")
	if err := os.WriteFile(watPath, []byte(wat), 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}

	cmd := exec.Command(wt, "run", "--invoke", "main", watPath)
	var soBuf, seBuf bytes.Buffer
	cmd.Stdout = &soBuf
	cmd.Stderr = &seBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("wasmtime: %v\nstdout:\n%s\nstderr:\n%s\n--- wat ---\n%s",
			err, soBuf.String(), seBuf.String(), wat)
	}
	return soBuf.String(), seBuf.String()
}

// invokeWasmtimeMultiFile is the multi-file analogue of
// invokeWasmtime: writes each path → src into a temp dir, loads
// the entry through modload, runs the rest of the pipeline, and
// invokes `main` under wasmtime.
func invokeWasmtimeMultiFile(t *testing.T, entry string, files map[string]string) (stdout, stderr string) {
	t.Helper()
	wt := wasmtimePath(t)

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
	wat, err := wasm.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}

	watPath := filepath.Join(dir, "prog.wat")
	if err := os.WriteFile(watPath, []byte(wat), 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command(wt, "run", "--invoke", "main", watPath)
	var soBuf, seBuf bytes.Buffer
	cmd.Stdout = &soBuf
	cmd.Stderr = &seBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("wasmtime: %v\nstdout:\n%s\nstderr:\n%s\n--- wat ---\n%s",
			err, soBuf.String(), seBuf.String(), wat)
	}
	return soBuf.String(), seBuf.String()
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
		"point.lang": `pub struct Point { x: number, y: number }
pub function make(x: number, y: number): Point {
	return Point { x: x, y: y };
}`,
		"main.lang": `import "./point";
function main(): number {
	var p: point.Point = point.make(3, 4);
	return p.x + p.y;
}`,
	})
	if got != 7 {
		t.Errorf("got %d, want 7 (3 + 4)", got)
	}
}

// invokeWasmtimeWithArgs is invokeWasmtime plus extra positional
// argv that wasmtime forwards to the wasm module via WASI.
// Splitting stdout / stderr matters here too: wasmtime puts its
// `--invoke` warning on stderr and the program's own output on
// stdout, so the args() return path can be tested without
// noise interfering.
func invokeWasmtimeWithArgs(t *testing.T, src string, extraArgs ...string) (stdout, stderr string) {
	t.Helper()
	wt := wasmtimePath(t)

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
	wat, err := wasm.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	dir := t.TempDir()
	watPath := filepath.Join(dir, "prog.wat")
	if err := os.WriteFile(watPath, []byte(wat), 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmdArgs := append([]string{"run", "--invoke", "main", watPath}, extraArgs...)
	cmd := exec.Command(wt, cmdArgs...)
	var soBuf, seBuf bytes.Buffer
	cmd.Stdout = &soBuf
	cmd.Stderr = &seBuf
	_ = cmd.Run()
	return soBuf.String(), seBuf.String()
}

// args() under wasmtime: argv[0] is the wasm module path that
// wasmtime injects, plus whatever extra positional args we pass.
// `wasmtime run --invoke main mod.wat alpha beta` therefore yields
// argv = [mod.wat, alpha, beta] and `len(args()) == 3`.
func TestWASMArgsBuiltin(t *testing.T) {
	src := `function main(): number {
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
	src := `function main(): number {
		var a: string[] = args();
		print(a[1]);
		return 0;
	}`
	stdout, _ := invokeWasmtimeWithArgs(t, src, "hello")
	if !strings.Contains(stdout, "hello") {
		t.Errorf("expected stdout to contain `hello`, got %q", stdout)
	}
}

// runWasmStdinEnv runs the compiled wasm under wasmtime with
// scripted stdin, scripted env vars, and returns stdout, stderr,
// and the exit code separately. wasmtime's `--env KEY=VAL` flag
// forwards into WASI environ_get; stdin gets piped through.
func runWasmStdinEnv(t *testing.T, src, stdin string, envs []string) (stdout, stderr string, exitCode int) {
	t.Helper()
	wt := wasmtimePath(t)

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
	wat, err := wasm.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	dir := t.TempDir()
	watPath := filepath.Join(dir, "prog.wat")
	if err := os.WriteFile(watPath, []byte(wat), 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	args := []string{"run", "--invoke", "main"}
	for _, e := range envs {
		args = append(args, "--env", e)
	}
	args = append(args, watPath)
	cmd := exec.Command(wt, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var soBuf, seBuf bytes.Buffer
	cmd.Stdout = &soBuf
	cmd.Stderr = &seBuf
	_ = cmd.Run()
	return soBuf.String(), seBuf.String(), cmd.ProcessState.ExitCode()
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
	src := `function main(): number {
		match (read_line()) {
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
	src := `function main(): number {
		match (read_line()) {
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
	src := `function main(): number {
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
	src := `function main(): number {
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

// `exit(code)` calls WASI proc_exit, which terminates the
// process with the given status. wasmtime surfaces that as the
// host process's exit code.
func TestWASMExitBuiltin(t *testing.T) {
	src := `function main(): number {
		eprint("boom");
		exit(7);
		return 0;
	}`
	_, stderr, code := runWasmStdinEnv(t, src, "", nil)
	if !strings.Contains(stderr, "boom") {
		t.Errorf("stderr missing `boom`: %q", stderr)
	}
	if code != 7 {
		t.Errorf("exit = %d, want 7", code)
	}
}

// `write` produces stdout output without a trailing newline.
// Three consecutive `write` calls concatenate into a single run;
// the final `print` adds the only newline in the output.
func TestWASMWriteBuiltin(t *testing.T) {
	src := `function main(): number {
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
	src := `function main(): number {
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
	if got := runWasm(t, `function main(): number { return 42; }`); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

func TestWASMFactorial(t *testing.T) {
	src := `function fact(n: number): number {
		if (n == 0) { return 1; }
		return n * fact(n - 1);
	}
	function main(): number { return fact(5); }`
	if got := runWasm(t, src); got != 120 {
		t.Errorf("got %d, want 120", got)
	}
}

func TestWASMForLoopWithBreakContinue(t *testing.T) {
	src := `function main(): number {
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
	src := `function unrelated_a(x: number): number { return x + 1; }
	function unrelated_b(x: number): number { return x + 2; }
	function target(x: number): number { return x * 10; }
	function apply(f: (number) => number, x: number): number {
		return f(x);
	}
	function main(): number { return apply(target, 4); }`
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
	src := `function main(): number {
		print("Hello, world!");
		return 0;
	}`
	out := runWasmCapturingStdout(t, src)
	if out != "Hello, world!" {
		t.Errorf("output = %q, want \"Hello, world!\"", out)
	}
}

// runWasmFloat parses a 32-bit float result out of wasmtime's stdout.
// wasmtime prints floats either as a bare decimal or as `f32: N`, so
// strip a leading type tag if present and parse the rest.
func runWasmFloat(t *testing.T, src string) float64 {
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
		if f, err := strconv.ParseFloat(ln, 64); err == nil {
			return f
		}
	}
	t.Fatalf("could not parse wasmtime float output:\n%s", stdout)
	return 0
}

func TestWASMFloatArithmetic(t *testing.T) {
	src := `function main(): float { return 1.5 + 2.5; }`
	if got := runWasmFloat(t, src); got != 4.0 {
		t.Errorf("got %v, want 4.0", got)
	}
}

func TestWASMFloatMultiplyAndDivide(t *testing.T) {
	src := `function main(): float { return 6.0 * 0.5 / 0.25; }`
	if got := runWasmFloat(t, src); got != 12.0 {
		t.Errorf("got %v, want 12.0", got)
	}
}

func TestWASMFloatNegate(t *testing.T) {
	src := `function f(x: float): float { return -x; }
		function main(): float { return f(3.5); }`
	if got := runWasmFloat(t, src); got != -3.5 {
		t.Errorf("got %v, want -3.5", got)
	}
}

func TestWASMPutcharWritesBytes(t *testing.T) {
	src := `function main(): number {
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
		function main(): number {
			var sum: number = 0;
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
		function main(): number {
			var sum: number = 0;
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
	src := `function main(): number {
		var a: number[] = [10, 20, 30, 40];
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
	src := `function main(): number {
		var inner: number[] = [3, 4];
		var outer: number[] = [1, 2, inner[0]];
		return outer[2] + inner[1];
	}`
	if got := runWasm(t, src); got != 7 {
		t.Errorf("got %d, want 7 (3 + 4)", got)
	}
}

func TestWASMIndirectCallApply(t *testing.T) {
	src := `
		function add(a: number, b: number): number { return a + b; }
		function apply(f: (number, number) => number, a: number, b: number): number {
			return f(a, b);
		}
		function main(): number { return apply(add, 40, 2); }`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

func TestWASMFunctionValueInVar(t *testing.T) {
	src := `
		function dbl(x: number): number { return x * 2; }
		function main(): number {
			var f = dbl;
			return f(7);
		}`
	if got := runWasm(t, src); got != 14 {
		t.Errorf("got %d, want 14", got)
	}
}

func TestWASMSwitchBasic(t *testing.T) {
	src := `function classify(n: number): number {
		switch (n) {
			case 0: return 100;
			case 1, 2, 3: return 200;
			case 7: return 700;
			default: return 0;
		}
		return -1;
	}
	function main(): number {
		return classify(0) + classify(2) + classify(7) + classify(99);
	}`
	// 100 + 200 + 700 + 0 = 1000
	if got := runWasm(t, src); got != 1000 {
		t.Errorf("got %d, want 1000", got)
	}
}

func TestWASMSwitchBreakInLoop(t *testing.T) {
	// `break` inside a case exits the switch, not the enclosing loop.
	src := `function main(): number {
		var sum: number = 0;
		for (var i: number = 0; i < 5; i = i + 1) {
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
	src := `function abs(n: number): number { return n < 0 ? 0 - n : n; }
		function main(): number { return abs(-7); }`
	if got := runWasm(t, src); got != 7 {
		t.Errorf("got %d, want 7", got)
	}
}

func TestWASMCompoundAssign(t *testing.T) {
	src := `function main(): number {
		var x: number = 1;
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
	src := `function main(): number { return len("hello"); }`
	if got := runWasm(t, src); got != 5 {
		t.Errorf("got %d, want 5", got)
	}
}

func TestWASMStringIndexAndCompare(t *testing.T) {
	src := `function main(): number {
		var s: string = "abc";
		var byte: number = s[1];
		var equal: boolean = "yes" == "yes";
		var different: boolean = "yes" == "no";
		// 'b' = 98; equal=1, different=0 → 98 + 1 - 0 = 99
		var ok: number = 0;
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
	src := `struct Point { x: number, y: number }
		function main(): number {
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
	src := `struct Box { v: number }
		function bump(b: Box): void { b.v = b.v + 100; }
		function main(): number {
			var b: Box = Box { v: 5 };
			bump(b);
			return b.v;
		}`
	if got := runWasm(t, src); got != 105 {
		t.Errorf("got %d, want 105", got)
	}
}

func TestWASMStringConcat(t *testing.T) {
	src := `function main(): number {
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

func TestWASMArrayOutOfBoundsTraps(t *testing.T) {
	src := `function main(): number {
		var a: number[] = [1, 2, 3];
		return a[10];
	}`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatal(err)
	}
	wat, err := wasm.Emit(prog, info)
	if err != nil {
		t.Fatal(err)
	}
	// Run wasmtime expecting non-zero exit (trap).
	wt := wasmtimePath(t)
	dir := t.TempDir()
	watPath := filepath.Join(dir, "prog.wat")
	if err := os.WriteFile(watPath, []byte(wat), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(wt, "run", "--invoke", "main", watPath)
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	if err := cmd.Run(); err == nil {
		t.Errorf("expected wasmtime to trap on a[10], but it succeeded\nstdout:\n%s\nstderr:\n%s", so.String(), se.String())
	}
}

func TestWASMNegativeIndexTraps(t *testing.T) {
	src := `function main(): number {
		var a: number[] = [1, 2, 3];
		return a[0 - 1];
	}`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatal(err)
	}
	wat, err := wasm.Emit(prog, info)
	if err != nil {
		t.Fatal(err)
	}
	wt := wasmtimePath(t)
	dir := t.TempDir()
	watPath := filepath.Join(dir, "prog.wat")
	if err := os.WriteFile(watPath, []byte(wat), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(wt, "run", "--invoke", "main", watPath)
	if err := cmd.Run(); err == nil {
		t.Errorf("expected wasmtime to trap on a[-1], but it succeeded")
	}
}

func TestWASMInBoundsStillWorks(t *testing.T) {
	// Make sure the bounds check doesn't break the happy path.
	src := `function main(): number {
		var a: number[] = [10, 20, 30];
		return a[1];
	}`
	if got := runWasm(t, src); got != 20 {
		t.Errorf("got %d, want 20", got)
	}
}

func TestWASMClosureFactory(t *testing.T) {
	src := `function makeAdder(n: number): (number) => number {
		function add(x: number): number { return x + n; }
		return add;
	}
	function main(): number {
		var f = makeAdder(7);
		return f(35);
	}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

func TestWASMClosureMultipleInstances(t *testing.T) {
	// Two closures over different captured values shouldn't share state.
	src := `function makeAdder(n: number): (number) => number {
		function add(x: number): number { return x + n; }
		return add;
	}
	function main(): number {
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
	src := `function outer(seed: number): number {
		var bonus: number = 100;
		function inner(x: number): number { return x + seed + bonus; }
		return inner(2);
	}
	function main(): number { return outer(40); }`
	// 2 + 40 + 100 = 142
	if got := runWasm(t, src); got != 142 {
		t.Errorf("got %d, want 142", got)
	}
}

func TestWASMMethodOnStruct(t *testing.T) {
	src := `struct Point { x: number, y: number }
		function (p: Point) sum(): number { return p.x + p.y; }
		function main(): number {
			var p: Point = Point { x: 10, y: 32 };
			return p.sum();
		}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

func TestWASMMethodWithExtraArg(t *testing.T) {
	// `b.shifted(7)` rewrites to `__method_Box_shifted(b, 7)`.
	src := `struct Box { v: number }
		function (b: Box) shifted(n: number): number { return b.v + n; }
		function main(): number {
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
	src := `enum Pair { Two(number, number) }
		function main(): number {
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
		function main(): number {
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
		function main(): number {
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
// `Option[number]`, the match arm extracts the payload, the
// caller's main returns the value. Type erasure means there's
// no per-T monomorphization: the WAT treats every payload as
// i32 regardless of T.
func TestWASMGenericOptionSome(t *testing.T) {
	src := `enum Option[T] { Some(T), None }
		function find(): Option[number] { return Some(42); }
		function main(): number {
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
		function find(): Option[number] { return None; }
		function main(): number {
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
	src := `function divide(a: number, b: number): Result[number, string] {
			if (b == 0) { return Err("zero"); }
			return Ok(a / b);
		}
		function main(): number {
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
func TestWASMOptionFloatPayload(t *testing.T) {
	src := `function pick(): Option[float] { return Some(3.14); }
		function main(): number {
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
		function main(): number {
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
	wt := wasmtimePath(t)
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
	wat, err := wasm.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	dir = t.TempDir()
	watPath := filepath.Join(dir, "prog.wat")
	if err := os.WriteFile(watPath, []byte(wat), 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	for name, content := range seed {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	cmd := exec.Command(wt, "run", "--dir="+dir, "--invoke", "main", watPath)
	var soBuf, seBuf bytes.Buffer
	cmd.Stdout = &soBuf
	cmd.Stderr = &seBuf
	_ = cmd.Run()
	return soBuf.String(), seBuf.String(), cmd.ProcessState.ExitCode(), dir
}

// `read_file` returns Ok(content) for a present file. The
// program writes the content back to stdout so we can verify
// both the read path and the type-erased Result unwrap.
func TestWASMReadFileOk(t *testing.T) {
	src := `function main(): number {
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
	src := `function main(): number {
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
	src := `function main(): number {
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
	src := `function main(): number {
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
