// Package e2e exercises the full pipeline: compile a .lang source string to
// ARM32 assembly, link it with the system's ARM cross-compiler, and run the
// resulting binary under qemu-arm.
//
// Each test SKIPS (rather than fails) when the cross-compiler or qemu-arm
// is not installed, so `go test ./...` stays green on machines without an
// ARM toolchain. CI installs both, so coverage is exercised there.
package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/parser"
)

func tooling(t *testing.T) (gcc, qemu string) {
	t.Helper()
	for _, c := range []string{"arm-linux-gnueabihf-gcc", "arm-linux-gnueabi-gcc"} {
		if p, err := exec.LookPath(c); err == nil {
			gcc = p
			break
		}
	}
	for _, c := range []string{"qemu-arm", "qemu-arm-static"} {
		if p, err := exec.LookPath(c); err == nil {
			qemu = p
			break
		}
	}
	if gcc == "" || qemu == "" {
		t.Skipf("ARM cross toolchain not available (gcc=%q qemu=%q)", gcc, qemu)
	}
	return gcc, qemu
}

func compileAndRun(t *testing.T, src string) (stdout string, exitCode int) {
	t.Helper()
	gcc, qemu := tooling(t)

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
	asm, err := codegen.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}

	dir := t.TempDir()
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s\n--- asm ---\n%s", err, out, asm)
	}
	cmd := exec.Command(qemu, binPath)
	out, _ := cmd.CombinedOutput()
	return string(out), cmd.ProcessState.ExitCode()
}

// compileMultiFileAndRun writes each entry in `files` (path → src)
// into a temp dir, loads the named entry through modload, runs the
// rest of the pipeline, and exec's the resulting ARM binary under
// qemu. Used by the cross-module e2e tests.
func compileMultiFileAndRun(t *testing.T, entry string, files map[string]string) (stdout string, exitCode int) {
	t.Helper()
	gcc, qemu := tooling(t)

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
	asm, err := codegen.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}

	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s\n--- asm ---\n%s", err, out, asm)
	}
	cmd := exec.Command(qemu, binPath)
	out, _ := cmd.CombinedOutput()
	return string(out), cmd.ProcessState.ExitCode()
}

func TestExitCode(t *testing.T) {
	_, code := compileAndRun(t, `function main(): number { return 42; }`)
	if code != 42 {
		t.Errorf("exit = %d, want 42", code)
	}
}

// A leaf function with several params exercises the register-pinned
// prologue: the body still produces the right answer despite never
// touching the stack to read a/b/c/d.
func TestLeafFunctionAllRegArgs(t *testing.T) {
	src := `
		function leaf(a: number, b: number, c: number, d: number): number {
			return (a + b) * (c + d);
		}
		function main(): number { return leaf(2, 3, 4, 5); }`
	_, code := compileAndRun(t, src)
	if code != 45 {
		t.Errorf("exit = %d, want 45 ((2+3)*(4+5))", code)
	}
}

// Function-type syntax in parameter declarations lets a function
// accept another function as a value and call it indirectly.
func TestFunctionTypeAsParameter(t *testing.T) {
	src := `
		function add(a: number, b: number): number { return a + b; }
		function apply(f: (number, number) => number, a: number, b: number): number {
			return f(a, b);
		}
		function main(): number { return apply(add, 40, 2); }`
	_, code := compileAndRun(t, src)
	if code != 42 {
		t.Errorf("exit = %d, want 42", code)
	}
}

// Deeply self-recursive function: the test would blow the call stack
// without tail-call optimization. The tail call rewrites to a branch
// so we can iterate millions of times in O(1) frames.
func TestDeepTailRecursionDoesNotOverflowStack(t *testing.T) {
	src := `
		function countdown(n: number, acc: number): number {
			if (n == 0) { return acc; }
			return countdown(n - 1, acc + 1);
		}
		function main(): number { return countdown(100000, 0); }`
	// 100000 mod 256 = 160 (the i32 result wraps when used as an
	// 8-bit exit code).
	_, code := compileAndRun(t, src)
	if code != 160 {
		t.Errorf("exit = %d, want 160 (= 100000 mod 256)", code)
	}
}

// Storing a function name in a var and calling through that var
// goes through the indirect-call path (blx r12).
func TestFunctionValueIndirectCall(t *testing.T) {
	src := `
		function add(a: number, b: number): number { return a + b; }
		function main(): number {
			var f = add;
			return f(40, 2);
		}`
	_, code := compileAndRun(t, src)
	if code != 42 {
		t.Errorf("exit = %d, want 42", code)
	}
}

func TestArithmeticAndCalls(t *testing.T) {
	src := `
		function add(a: number, b: number): number { return a + b; }
		function main(): number { return add(40, 2); }`
	_, code := compileAndRun(t, src)
	if code != 42 {
		t.Errorf("exit = %d, want 42", code)
	}
}

func TestFactorialRecursion(t *testing.T) {
	src := `
		function fact(n: number): number {
			if (n == 0) { return 1; }
			return n * fact(n - 1);
		}
		function main(): number { return fact(5); }`
	_, code := compileAndRun(t, src)
	if code != 120 {
		t.Errorf("exit = %d, want 120", code)
	}
}

func TestWhileLoop(t *testing.T) {
	src := `
		function main(): number {
			var sum: number = 0;
			var i: number = 1;
			while (i <= 10) { sum = sum + i; i = i + 1; }
			return sum;
		}`
	_, code := compileAndRun(t, src)
	if code != 55 {
		t.Errorf("exit = %d, want 55 (1+2+...+10)", code)
	}
}

func TestDivision(t *testing.T) {
	src := `function main(): number { return 100 / 7; }`
	_, code := compileAndRun(t, src)
	if code != 14 {
		t.Errorf("exit = %d, want 14", code)
	}
}

func TestComparisonsAndShortCircuit(t *testing.T) {
	src := `
		function inRange(x: number, lo: number, hi: number): boolean {
			return lo <= x && x <= hi;
		}
		function main(): number {
			if (inRange(5, 1, 10) && !inRange(20, 1, 10)) { return 1; }
			return 0;
		}`
	_, code := compileAndRun(t, src)
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
}

func TestPutcharOutput(t *testing.T) {
	src := `
		function main(): number {
			putchar(72);  // H
			putchar(73);  // I
			putchar(10);  // \n
			return 0;
		}`
	out, code := compileAndRun(t, src)
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if out != "HI\n" {
		t.Errorf("output = %q, want \"HI\\n\"", out)
	}
}

func TestBreakInWhile(t *testing.T) {
	src := `
		function main(): number {
			var i: number = 0;
			while (true) {
				if (i == 7) { break; }
				i = i + 1;
			}
			return i;
		}`
	_, code := compileAndRun(t, src)
	if code != 7 {
		t.Errorf("exit = %d, want 7", code)
	}
}

func TestContinueInForRunsStep(t *testing.T) {
	// Sum 5..9 (skip i < 5) = 5+6+7+8+9 = 35.
	// `continue` must still run the step, otherwise we'd loop forever.
	src := `
		function main(): number {
			var sum: number = 0;
			for (var i: number = 0; i < 10; i = i + 1) {
				if (i < 5) { continue; }
				sum = sum + i;
			}
			return sum;
		}`
	_, code := compileAndRun(t, src)
	if code != 35 {
		t.Errorf("exit = %d, want 35", code)
	}
}

func TestForLoop(t *testing.T) {
	src := `
		function main(): number {
			var sum: number = 0;
			for (var i: number = 1; i <= 10; i = i + 1) {
				sum = sum + i;
			}
			return sum;
		}`
	_, code := compileAndRun(t, src)
	if code != 55 {
		t.Errorf("exit = %d, want 55", code)
	}
}

func TestSixArgFunction(t *testing.T) {
	src := `
		function sum6(a: number, b: number, c: number,
		              d: number, e: number, f: number): number {
			return a + b + c + d + e + f;
		}
		function main(): number { return sum6(1, 2, 4, 8, 16, 32); }`
	_, code := compileAndRun(t, src)
	if code != 63 {
		t.Errorf("exit = %d, want 63", code)
	}
}

func TestModulo(t *testing.T) {
	src := `function main(): number { return 17 % 5; }`
	_, code := compileAndRun(t, src)
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
}

func TestBitwiseAndOr(t *testing.T) {
	// (12 & 10) | 1 = 8 | 1 = 9
	src := `function main(): number { return (12 & 10) | 1; }`
	_, code := compileAndRun(t, src)
	if code != 9 {
		t.Errorf("exit = %d, want 9", code)
	}
}

func TestShifts(t *testing.T) {
	// (1 << 5) >> 2 = 32 >> 2 = 8
	src := `function main(): number { return (1 << 5) >> 2; }`
	_, code := compileAndRun(t, src)
	if code != 8 {
		t.Errorf("exit = %d, want 8", code)
	}
}

func TestStringConcat(t *testing.T) {
	src := `function main(): number {
		var s: string = "Hello, " + "world!";
		print(s);
		return 0;
	}`
	out, code := compileAndRun(t, src)
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if out != "Hello, world!\n" {
		t.Errorf("output = %q, want %q", out, "Hello, world!\n")
	}
}

func TestStringConcatChained(t *testing.T) {
	// Three-part concat exercises a nested concat inside the helper —
	// `(a + b) + c` allocates twice.
	src := `function main(): number {
		print("foo" + "-" + "bar");
		return 0;
	}`
	out, _ := compileAndRun(t, src)
	if out != "foo-bar\n" {
		t.Errorf("output = %q", out)
	}
}

func TestStringPrint(t *testing.T) {
	src := `function main(): number {
		print("Hello, world!");
		return 0;
	}`
	out, code := compileAndRun(t, src)
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	// `print` lowers to puts, which appends a newline.
	if out != "Hello, world!\n" {
		t.Errorf("output = %q, want %q", out, "Hello, world!\n")
	}
}

func TestStringEscapes(t *testing.T) {
	src := `function main(): number {
		print("tab:\there\nnext line");
		return 0;
	}`
	out, _ := compileAndRun(t, src)
	if out != "tab:\there\nnext line\n" {
		t.Errorf("output = %q", out)
	}
}

// `for x in arr { ... }` desugars to an index loop; e2e check that
// the desugared form actually iterates correctly under qemu-arm.
// Cross-module struct types end-to-end: an entry file declares
// `var p: point.Point = point.make(3, 4);`, the loader rewrites
// the qualified type to the mangled `point__Point`, the checker
// validates the field access, and the linked binary returns 7.
func TestCrossModuleStructTypeArm(t *testing.T) {
	_, code := compileMultiFileAndRun(t, "main.lang", map[string]string{
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
	if code != 7 {
		t.Errorf("exit = %d, want 7 (3 + 4)", code)
	}
}

// Top-level consts end-to-end: integer + arithmetic over an
// earlier const folds at compile time, the IR pipeline sees only
// literals, and the resulting binary returns the resolved value.
func TestConstFoldedIntoArm(t *testing.T) {
	src := `
		const BASE: number = 10;
		const TWICE: number = BASE * 2;
		function main(): number { return TWICE + BASE; }`
	_, code := compileAndRun(t, src)
	if code != 30 {
		t.Errorf("exit = %d, want 30 (10*2 + 10)", code)
	}
}

// Cross-module `pub const` reaches the binary intact: the entry
// imports a module that exports a number-typed const, and the
// folded literal travels through the rewriter and the rest of the
// pipeline without surprises.
func TestPubConstAcrossModulesArm(t *testing.T) {
	_, code := compileMultiFileAndRun(t, "main.lang", map[string]string{
		"limits.lang": `pub const MAX: number = 42;`,
		"main.lang": `import "./limits";
function main(): number { return limits.MAX; }`,
	})
	if code != 42 {
		t.Errorf("exit = %d, want 42", code)
	}
}

// compileAndCaptureStreams is compileAndRun split-streams. The
// `eprint` and `write` e2e tests need to know which file
// descriptor each builtin lands on, which compileAndRun's
// CombinedOutput collapses. qemu passes stdin/stdout/stderr
// straight through, so the parent's split capture is faithful.
func compileAndCaptureStreams(t *testing.T, src string) (stdout, stderr string, exitCode int) {
	t.Helper()
	gcc, qemu := tooling(t)

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
	asm, err := codegen.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	dir := t.TempDir()
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s\n--- asm ---\n%s", err, out, asm)
	}
	cmd := exec.Command(qemu, binPath)
	var soBuf, seBuf bytes.Buffer
	cmd.Stdout = &soBuf
	cmd.Stderr = &seBuf
	_ = cmd.Run()
	return soBuf.String(), seBuf.String(), cmd.ProcessState.ExitCode()
}

// compileAndRunWithArgs is compileAndRun plus extra positional argv
// for the qemu-launched binary. argv[0] is the binary path injected
// by qemu (argv[0] is whatever execve sets). Used by the args()
// e2e tests to feed scripted argv into the running program.
func compileAndRunWithArgs(t *testing.T, src string, extraArgs ...string) int {
	t.Helper()
	gcc, qemu := tooling(t)

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
	asm, err := codegen.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	dir := t.TempDir()
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s\n--- asm ---\n%s", err, out, asm)
	}
	cmdArgs := append([]string{binPath}, extraArgs...)
	cmd := exec.Command(qemu, cmdArgs...)
	out, _ := cmd.CombinedOutput()
	if testing.Verbose() {
		t.Logf("output: %s", string(out))
	}
	return cmd.ProcessState.ExitCode()
}

// args() under qemu-arm: argv[0] is the binary path that qemu
// passes through, plus whatever extra args we supplied — so
// `len(args())` should match the count we set up.
func TestArgsBuiltinArm(t *testing.T) {
	src := `function main(): number {
		var a: string[] = args();
		return len(a);
	}`
	code := compileAndRunWithArgs(t, src, "alpha", "beta")
	if code != 3 {
		t.Errorf("got %d, want 3 (binary path + alpha + beta)", code)
	}
}

// Reading individual args produces the expected string contents —
// the runtime helper must scan each NUL-terminated argv entry,
// allocate a length-prefixed copy, and the language's `print`
// must then handle it like any other string.
func TestArgsBuiltinReadsValueArm(t *testing.T) {
	src := `function main(): number {
		var a: string[] = args();
		print(a[1]);
		return 0;
	}`
	gcc, qemu := tooling(t)
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
	asm, err := codegen.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	dir := t.TempDir()
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s\n--- asm ---\n%s", err, out, asm)
	}
	cmd := exec.Command(qemu, binPath, "hello")
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), "hello") {
		t.Errorf("expected output to contain `hello`, got %q", string(out))
	}
}

// runWithStdinEnv builds the program, then runs it under qemu
// with scripted stdin + extra env vars, returning stdout, stderr,
// and exit code separately. Used by the read_line / env / exit
// builtins' e2e tests where any one of those streams is the
// signal under test.
func runWithStdinEnv(t *testing.T, src, stdin string, extraEnv []string) (stdout, stderr string, exitCode int) {
	t.Helper()
	gcc, qemu := tooling(t)

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
	asm, err := codegen.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	dir := t.TempDir()
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s\n--- asm ---\n%s", err, out, asm)
	}
	cmd := exec.Command(qemu, binPath)
	cmd.Stdin = strings.NewReader(stdin)
	if len(extraEnv) > 0 {
		// qemu-arm forwards parent env to the guest by default;
		// append our overrides so they win the lookup.
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	var soBuf, seBuf bytes.Buffer
	cmd.Stdout = &soBuf
	cmd.Stderr = &seBuf
	_ = cmd.Run()
	return soBuf.String(), seBuf.String(), cmd.ProcessState.ExitCode()
}

// `read_line()` returns `Some(line)` for a present line and
// `None` at EOF — the post-Phase-3 typed shape replaces the
// empty-string sentinel from earlier PRs.
func TestReadLineBuiltinArm(t *testing.T) {
	src := `function main(): number {
		match (stdin().read_line()) {
			Some(line) => { write(line); return len(line); },
			None => { return -1; }
		}
		return -2;
	}`
	stdout, _, code := runWithStdinEnv(t, src, "hello\n", nil)
	if stdout != "hello\n" {
		t.Errorf("stdout = %q, want %q", stdout, "hello\n")
	}
	if code != 6 {
		t.Errorf("exit = %d, want 6 (len of \"hello\\n\")", code)
	}
}

// EOF routes to the `None` arm. A non-empty line (even just
// `"\n"`) routes to `Some`. The match arms encode the
// distinction; callers no longer have to special-case
// `len(line) == 0`.
func TestReadLineBuiltinEOFArm(t *testing.T) {
	src := `function main(): number {
		match (stdin().read_line()) {
			Some(line) => { return 1; },
			None => { return 0; }
		}
		return -1;
	}`
	_, _, code := runWithStdinEnv(t, src, "", nil)
	if code != 0 {
		t.Errorf("exit = %d, want 0 (EOF routes to None arm)", code)
	}
}

// `env(name)` returns `Some(value)` when the key is set,
// `None` when it isn't.
func TestEnvBuiltinArm(t *testing.T) {
	src := `function main(): number {
		match (env("LANG_TEST_VAR")) {
			Some(v) => { write(v); return len(v); },
			None => { return -1; }
		}
		return -2;
	}`
	stdout, _, code := runWithStdinEnv(t, src, "", []string{"LANG_TEST_VAR=hi"})
	if stdout != "hi" {
		t.Errorf("stdout = %q, want %q", stdout, "hi")
	}
	if code != 2 {
		t.Errorf("exit = %d, want 2 (len of \"hi\")", code)
	}
}

// Missing env routes to `None`, distinguishable from a present-
// but-empty value (which would be `Some("")`).
func TestEnvBuiltinMissingArm(t *testing.T) {
	src := `function main(): number {
		match (env("LANG_TEST_DEFINITELY_NOT_SET_XYZ")) {
			Some(v) => { return 1; },
			None => { return 0; }
		}
		return -1;
	}`
	_, _, code := runWithStdinEnv(t, src, "", nil)
	if code != 0 {
		t.Errorf("exit = %d, want 0 (missing env routes to None)", code)
	}
}

// `exit(code)` short-circuits whatever main was about to return.
// Pairing it with `eprint` is the canonical "fatal error" shape.
func TestExitBuiltinArm(t *testing.T) {
	src := `function main(): number {
		eprint("boom");
		exit(7);
		return 0;
	}`
	_, stderr, code := runWithStdinEnv(t, src, "", nil)
	if stderr != "boom\n" {
		t.Errorf("stderr = %q, want %q", stderr, "boom\n")
	}
	if code != 7 {
		t.Errorf("exit = %d, want 7", code)
	}
}

// `write` is `print` minus the newline. Three calls back-to-back
// land on stdout as one continuous run with no separators; the
// final `print` then closes the line.
func TestWriteBuiltinArm(t *testing.T) {
	src := `function main(): number {
		write("a");
		write("b");
		print("c");
		return 0;
	}`
	stdout, _, _ := compileAndCaptureStreams(t, src)
	if stdout != "ab" + "c\n" {
		t.Errorf("stdout = %q, want %q", stdout, "abc\n")
	}
}

// `eprint` writes to stderr with a trailing newline. Pairing it
// with `print` on stdout in the same program proves the two
// builtins land on different file descriptors and don't
// interfere with each other.
func TestEprintBuiltinArm(t *testing.T) {
	src := `function main(): number {
		print("hi");
		eprint("err");
		return 0;
	}`
	stdout, stderr, _ := compileAndCaptureStreams(t, src)
	if stdout != "hi\n" {
		t.Errorf("stdout = %q, want %q", stdout, "hi\n")
	}
	if stderr != "err\n" {
		t.Errorf("stderr = %q, want %q", stderr, "err\n")
	}
}

func TestForEachOverArray(t *testing.T) {
	src := `
		function main(): number {
			var sum: number = 0;
			for x in [10, 20, 30] {
				sum = sum + x;
			}
			return sum;
		}`
	_, code := compileAndRun(t, src)
	if code != 60 {
		t.Errorf("exit = %d, want 60 (10+20+30)", code)
	}
}

// break and continue inside a foreach body target the surrounding
// loop just like in a hand-written `for`.
func TestForEachBreakContinue(t *testing.T) {
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
	_, code := compileAndRun(t, src)
	if code != 8 {
		t.Errorf("exit = %d, want 8 (1+3+4)", code)
	}
}

func TestArraySumAndMutation(t *testing.T) {
	src := `
		function main(): number {
			var a: number[] = [10, 20, 30, 40];
			a[2] = 100;
			return a[0] + a[1] + a[2] + a[3];
		}`
	_, code := compileAndRun(t, src)
	if code != 170 {
		t.Errorf("exit = %d, want 170 (10+20+100+40)", code)
	}
}

// Sum types end-to-end on arm32: a payload-carrying variant
// is constructed, matched, and the bound payloads flow into
// the result.
func TestEnumMatchPayloadArm(t *testing.T) {
	src := `enum Pair { Two(number, number) }
		function main(): number {
			var p: Pair = Two(7, 5);
			match (p) {
				Two(a, b) => { return a + b; }
			}
			return -1;
		}`
	_, code := compileAndRun(t, src)
	if code != 12 {
		t.Errorf("exit = %d, want 12 (7 + 5)", code)
	}
}

// Multi-variant dispatch on arm32 — each arm is reachable, the
// payload-less Ok constructor and the string-carrying Err
// constructor both work.
func TestEnumMatchDispatchArm(t *testing.T) {
	// Use distinct variant names so this test doesn't collide
	// with the auto-injected `Result[T, E]` (which owns Ok/Err).
	src := `enum Status { Good, Bad(string) }
		function status(): Status { return Bad("boom"); }
		function main(): number {
			match (status()) {
				Good => { return 0; },
				Bad(msg) => { return len(msg); }
			}
			return -1;
		}`
	_, code := compileAndRun(t, src)
	if code != 4 {
		t.Errorf("exit = %d, want 4 (len of \"boom\")", code)
	}
}

// Generic enum Option end-to-end on arm32 — same scenarios as
// the WASM tests, but compiled and executed under qemu so the
// type-erased lowering is exercised on a non-WASM backend too.
func TestGenericOptionArm(t *testing.T) {
	src := `enum Option[T] { Some(T), None }
		function find(): Option[number] { return Some(42); }
		function main(): number {
			match (find()) {
				Some(v) => { return v; },
				None => { return -1; }
			}
			return 99;
		}`
	_, code := compileAndRun(t, src)
	if code != 42 {
		t.Errorf("exit = %d, want 42", code)
	}
}

// Generic Result[T, E] on arm32 — both type parameters route
// through the heap at runtime. The error arm carries a string
// payload that flows through `len` on extraction.
func TestGenericResultArm(t *testing.T) {
	// Use the auto-injected `Result[T, E]` instead of redeclaring
	// it — Phase 3 makes Result a built-in.
	src := `function check(b: boolean): Result[number, string] {
			if (b) { return Ok(7); }
			return Err("oops");
		}
		function main(): number {
			match (check(false)) {
				Ok(v) => { return v; },
				Err(msg) => { return len(msg); }
			}
			return -1;
		}`
	_, code := compileAndRun(t, src)
	if code != 4 {
		t.Errorf("exit = %d, want 4 (len of \"oops\")", code)
	}
}

// Note: there's no `TestGenericOptionFloatArm` because the arm32
// backend doesn't yet support float ops at all (any program
// using `float` errors at codegen). Float-payload generic enums
// are exercised on the WASM side via `TestWASMOptionFloatPayload`
// + `TestWASMResultFloatOk`. When arm32 grows VFP support the
// matching test goes here.

// runArmInDir is the arm32 analogue of runWasmInDir. It
// compiles the program, drops it in a temp dir alongside any
// seed files, runs it under qemu-arm with the temp dir as the
// process's cwd (so libc open() resolves relative paths
// against it), and returns stdout, exit code, and the dir for
// post-run inspection.
func runArmInDir(t *testing.T, src string, seed map[string]string) (stdout string, code int, dir string) {
	t.Helper()
	gcc, qemu := tooling(t)

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
	asm, err := codegen.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	dir = t.TempDir()
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s\n--- asm ---\n%s", err, out, asm)
	}
	for name, content := range seed {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	cmd := exec.Command(qemu, binPath)
	cmd.Dir = dir
	out, _ := cmd.CombinedOutput()
	return string(out), cmd.ProcessState.ExitCode(), dir
}

// `read_file` round-trips a string through libc open/read/close
// on the arm32 runtime helper.
func TestReadFileOkArm(t *testing.T) {
	src := `function main(): number {
		match (read_file("greeting.txt")) {
			Ok(s) => { write(s); return len(s); },
			Err(_) => { return -1; }
		}
		return -2;
	}`
	stdout, code, _ := runArmInDir(t, src, map[string]string{
		"greeting.txt": "hello, file",
	})
	if stdout != "hello, file" {
		t.Errorf("stdout = %q, want %q", stdout, "hello, file")
	}
	if code != 11 {
		t.Errorf("exit = %d, want 11 (len of \"hello, file\")", code)
	}
}

// Missing files surface as `IoError.NotFound(path)`. The path
// is carried in the variant payload so callers don't need
// secondary context plumbing.
func TestReadFileNotFoundArm(t *testing.T) {
	src := `function main(): number {
		match (read_file("does_not_exist.txt")) {
			Ok(_) => { return 0; },
			Err(err) => {
				match (err) {
					NotFound(p) => { write(p); return 1; },
					_ => { return 99; }
				}
			}
		}
		return -1;
	}`
	stdout, code, _ := runArmInDir(t, src, nil)
	if !strings.Contains(stdout, "does_not_exist.txt") {
		t.Errorf("stdout should echo the path; got %q", stdout)
	}
	if code != 1 {
		t.Errorf("exit = %d, want 1 (NotFound arm)", code)
	}
}

// `write_file` truncates the target and writes the content via
// libc open(O_CREAT|O_TRUNC|O_WRONLY) + write + close.
func TestWriteFileOkArm(t *testing.T) {
	src := `function main(): number {
		match (write_file("out.txt", "from arm")) {
			Some(_) => { return 1; },
			None => { return 0; }
		}
		return -1;
	}`
	_, code, dir := runArmInDir(t, src, nil)
	if code != 0 {
		t.Errorf("exit = %d, want 0 (None / success)", code)
	}
	got, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "from arm" {
		t.Errorf("got %q, want %q", got, "from arm")
	}
}

// arm32 streaming round-trip — same shape as the WASM
// version but compiled to native arm assembly and run under
// qemu. open_writer + Writer.write + Writer.close +
// open_reader + Reader.read_line + Reader.close.
func TestStreamingRoundtripArm(t *testing.T) {
	src := `function main(): number {
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
	stdout, code, _ := runArmInDir(t, src, nil)
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "line 1\n") || !strings.Contains(stdout, "line 2\n") {
		t.Errorf("stdout missing both lines; got %q", stdout)
	}
}

func TestReaderReadChunkArm(t *testing.T) {
	src := `function main(): number {
		match (open_writer("rc.txt")) {
			Ok(w) => {
				match (w.write("hello world")) { Some(_) => { return 1; }, None => {} }
				match (w.close()) { Some(_) => { return 2; }, None => {} }
			},
			Err(_) => { return 3; }
		}
		match (open_reader("rc.txt")) {
			Ok(r) => {
				match (r.read_chunk(5)) { Some(s) => { write(s); write(":"); }, None => { return 4; } }
				match (r.read_chunk(20)) { Some(s) => { write(s); }, None => { return 5; } }
				match (r.read_chunk(20)) { Some(_) => { return 6; }, None => { return 0; } }
			},
			Err(_) => { return 7; }
		}
		return -1;
	}`
	stdout, code, _ := runArmInDir(t, src, nil)
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "hello: world") {
		t.Errorf("stdout should contain `hello: world`; got %q", stdout)
	}
}
