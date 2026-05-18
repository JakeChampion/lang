package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
)

// Cross-validation across the three execution engines in the
// lang-port: the tree-walking interpreter (interp.lang), the
// bytecode VM (vm.lang), and the native asm emitter (asm.lang).
// Every test source is piped through ALL THREE drivers
// (interp_run.lang, vm_run.lang, asm_run.lang) and the test
// asserts they all return the same exit code.
//
// This is the "every layer agrees" demo — a regression suite
// for the consistency of the lang-port's semantics across
// completely different execution strategies.
//
// Source programs use the common subset all three engines
// support: i32, arithmetic, comparisons, if/else, while,
// var/assign, function decls + recursion. No arrays / strings
// / print* because those aren't all supported by the asm
// emitter today.

func TestSelfHostCrossValidationX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	// Stage every lang source the three drivers transitively
	// depend on. (Each driver's modload pulls in only its own
	// imports, but using a shared dir means we can build all
	// three side-by-side.)
	for _, name := range []string{
		"lexer.lang", "parser.lang",
		"interp.lang", "vm.lang", "asm.lang",
		"interp_run.lang", "vm_run.lang", "asm_run.lang",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	buildDriver := func(t *testing.T, srcName string) string {
		t.Helper()
		prog, _, err := modload.Load(filepath.Join(dir, srcName))
		if err != nil {
			t.Fatalf("modload %s: %v", srcName, err)
		}
		if err := constfold.Fold(prog); err != nil {
			t.Fatalf("constfold %s: %v", srcName, err)
		}
		info, err := checker.Check(prog)
		if err != nil {
			t.Fatalf("check %s: %v", srcName, err)
		}
		asm, err := x86_64.Emit(prog, info)
		if err != nil {
			t.Fatalf("emit %s: %v", srcName, err)
		}
		asmPath := filepath.Join(dir, srcName+".s")
		binPath := filepath.Join(dir, srcName+".bin")
		if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
			t.Fatalf("write %s asm: %v", srcName, err)
		}
		if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
			t.Fatalf("%s gcc: %v\n%s", srcName, err, out)
		}
		return binPath
	}

	interpBin := buildDriver(t, "interp_run.lang")
	vmBin := buildDriver(t, "vm_run.lang")
	asmBin := buildDriver(t, "asm_run.lang")

	runDriver := func(t *testing.T, bin string, source string, captureStdout bool) (int, string) {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], bin)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(source))
		if captureStdout {
			out, _ := cmd.Output()
			return cmd.ProcessState.ExitCode(), string(out)
		}
		_, _ = cmd.CombinedOutput()
		return cmd.ProcessState.ExitCode(), ""
	}

	// runAsm: pipe `source` to asm_run, capture the emitted
	// asm on its stdout, gcc-assemble it, run the inner
	// binary, return its exit code.
	runAsm := func(t *testing.T, source string) int {
		t.Helper()
		_, emittedAsm := runDriver(t, asmBin, source, true)
		caseDir := t.TempDir()
		innerAsm := filepath.Join(caseDir, "inner.s")
		innerBin := filepath.Join(caseDir, "inner")
		if err := os.WriteFile(innerAsm, []byte(emittedAsm), 0o644); err != nil {
			t.Fatalf("write inner asm: %v", err)
		}
		if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", innerAsm, "-o", innerBin).CombinedOutput(); err != nil {
			t.Fatalf("inner gcc: %v\n%s\n--- asm ---\n%s", err, out, emittedAsm)
		}
		var inner *exec.Cmd
		if len(runner) == 0 {
			inner = exec.Command(innerBin)
		} else {
			inner = exec.Command(runner[0], append(runner[1:], innerBin)...)
		}
		_, _ = inner.CombinedOutput()
		return inner.ProcessState.ExitCode()
	}

	cases := []struct {
		name   string
		source string
		want   int
	}{
		{"return-literal", "return 42;", 42},
		{"arithmetic", "return 1 + 2 * 3;", 7},
		{"parens", "return (1 + 2) * 3;", 9},
		{"subtraction", "return 100 - 23;", 77},
		{"division", "return 84 / 2;", 42},
		{"modulo", "return 23 % 5;", 3},
		{"unary-neg", "return 0 - 5 + 10;", 5},
		{"comparison-true", "return 5 < 10;", 1},
		{"comparison-false", "return 10 < 5;", 0},
		{"equality-true", "return 7 == 7;", 1},
		{"locals", "var x = 5; var y = 10; return x + y;", 15},
		{"reassign", "var x = 5; x = x + 3; return x;", 8},
		{"compound-assign", "var x = 1; x *= 6; x += 1; return x;", 7},
		{"if-then-branch", "var x = 5; if (x < 10) { return 1; } return 2;", 1},
		{"if-else-branch", "var x = 20; if (x < 10) { return 1; } return 2;", 2},
		{"while-sum", "var i = 1; var s = 0; while (i <= 5) { s += i; i += 1; } return s;", 15},
		{"while-early-return", "var i = 0; while (i < 100) { if (i == 7) { return i; } i += 1; } return 0 - 1;", 7},
		{"func-decl-call", "function add(x: i32, y: i32): i32 { return x + y; } function main(): i32 { return add(2, 3); }", 5},
		{"recursive-factorial", "function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } function main(): i32 { return fact(5); }", 120},
		{"recursive-fib", "function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } function main(): i32 { return fib(8); }", 21},
		{"mutual-recursion", "function is_even(n: i32): i32 { if (n == 0) { return 1; } return is_odd(n - 1); } function is_odd(n: i32): i32 { if (n == 0) { return 0; } return is_even(n - 1); } function main(): i32 { return is_even(6); }", 1},
		{
			"prime-count-up-to-30",
			"function is_prime(n: i32): i32 { if (n < 2) { return 0; } var i = 2; while (i * i <= n) { if (n % i == 0) { return 0; } i = i + 1; } return 1; } " +
				"function main(): i32 { var count = 0; var i = 2; while (i <= 30) { if (is_prime(i) == 1) { count += 1; } i = i + 1; } return count; }",
			10,
		},
		// Exit codes are clamped to 0..255 by Linux; these
		// expected values are the actual computation result MOD
		// 256.
		{
			"sum-of-squares-1-to-10",
			"function main(): i32 { var i = 1; var s = 0; while (i <= 10) { s += i * i; i += 1; } return s; }",
			385 % 256, // = 129
		},
		{
			"power-of-two-recursive",
			"function pow2(n: i32): i32 { if (n == 0) { return 1; } return 2 * pow2(n - 1); } " +
				"function main(): i32 { return pow2(10); }",
			1024 % 256, // = 0
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			interpExit, _ := runDriver(t, interpBin, tc.source, false)
			vmExit, _ := runDriver(t, vmBin, tc.source, false)
			asmExit := runAsm(t, tc.source)
			if interpExit != tc.want || vmExit != tc.want || asmExit != tc.want {
				t.Errorf("disagreement: interp=%d, vm=%d, asm=%d, want=%d\n--- source ---\n%s",
					interpExit, vmExit, asmExit, tc.want, tc.source)
			}
		})
	}
}
