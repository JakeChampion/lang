package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/interp"
	"github.com/jakechampion/lang/internal/modload"
)

// Cross-validation across the three execution engines in the
// fern-port: the self-hosted tree-walking interpreter
// (interp.fern), the native (Go) tree-walking interpreter
// (internal/interp), and the self-hosted native asm emitter
// (asm.fern). Every test source is piped through the self-host
// interp driver (interp_run.fern) and the self-host asm driver
// (asm_run.fern), and run directly against the native interp,
// and the test asserts all three return the same exit code.
//
// This is the "every layer agrees" demo — a regression suite
// for the consistency of the fern-port's semantics across
// completely different execution strategies.
//
// Source programs use the common subset all three engines
// support: i32, arithmetic, comparisons, if/else, while,
// var/assign, function decls + recursion. No arrays / strings
// / print* because those aren't all supported by the asm
// emitter today.
//
// (A fourth engine — a bytecode VM, vm.fern — used to sit here
// too. It was retired in #4392: an unreachable fifth
// implementation of Fern semantics with no production consumer
// and known semantic drift from the other engines.)

func TestSelfHostCrossValidationX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	// Stage every lang source the two drivers transitively
	// depend on. (Each driver's modload pulls in only its own
	// imports, but using a shared dir means we can build both
	// side-by-side.)
	copySelfHostFiles(t, dir,
		"asmcore.fern", "lexer.fern", "parser.fern", "util.fern",
		"interp.fern", "astwalk.fern", "ir.fern", "irlower.fern", "asm_ir.fern",
		"interp_run.fern", "asm_run.fern")

	// Build both drivers through the shared cached path
	// (buildSelfHostBin), NOT a hand-rolled modload+emit+gcc: the
	// cached path releases the emit's dead spans back to the OS
	// (debug.FreeOSMemory) before spawning the assembler, and
	// restores a warm job's pre-linked binary from the disk cache
	// when one exists. The old inline build held the ~7 GB emit
	// residue in the test process while `as` spiked to ~8 GB on
	// asm_run's .s — over the 16 GB CI runners' RAM, so the kernel
	// OOM-killed the runner agent ("The runner has received a
	// shutdown signal", twice in a row on the same shard).
	interpBin := buildSelfHostBin(t, gcc, dir, "interp_run.fern", "interp_run.bin")
	asmBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "asm_run.bin")

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

	// runNativeInterp runs `source` directly against the native
	// (Go) tree-walking interpreter — the third leg. Skips
	// checker.Check (unlike cmd/fern's `-interp` pipeline):
	// several cases below return a boolean from an `i32`-typed
	// `main` (e.g. "comparison-true"), which the self-host legs'
	// untyped bootstrap language accepts but the native checker's
	// strict return-type rule rejects. The interpreter itself
	// evaluates the AST directly and needs no type info for this
	// program subset.
	//
	// Unlike the self-host mini-lexer/parser the other two legs
	// run on, the real Fern grammar has no implicit top-level
	// entry point — a source with no `function main` is a parse
	// error. Wrap bare-statement sources in a `main` so the same
	// test-case sources exercise all three legs.
	runNativeInterp := func(t *testing.T, source string) int {
		t.Helper()
		if !strings.Contains(source, "function main") {
			source = "function main(): i32 {\n" + source + "\n}\n"
		}
		prog, _, err := modload.LoadSource(source)
		if err != nil {
			t.Fatalf("native interp modload: %v\n--- source ---\n%s", err, source)
		}
		if err := constfold.Fold(prog); err != nil {
			t.Fatalf("native interp constfold: %v\n--- source ---\n%s", err, source)
		}
		ip := interp.New()
		for _, ed := range prog.Enums {
			ip.RegisterEnum(ed)
		}
		for _, fn := range prog.Funcs {
			ip.Register(fn)
		}
		v, err := ip.CallByName("main", nil)
		if err != nil {
			t.Fatalf("native interp run: %v\n--- source ---\n%s", err, source)
		}
		switch n := v.(type) {
		case interp.Number:
			return int(n) & 0xFF
		case interp.Bool:
			if n {
				return 1
			}
			return 0
		default:
			return 254
		}
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
		// Struct-update `P { ...base, f: v }` — exercises the new
		// has_base path through all three engines (both interp
		// evaluators copy+override the base's fields; asm copies
		// non-overridden decl fields + stores overrides). i32-only
		// fields stay within the engines' common subset.
		{
			"struct-update-single-override",
			"struct P { x: i32, y: i32, z: i32 } function main(): i32 { var p: P = P { x: 1, y: 2, z: 3 }; var q: P = P { ...p, y: 20 }; return q.x + q.y + q.z; }",
			24,
		},
		{
			"struct-update-out-of-order",
			"struct P { x: i32, y: i32, z: i32 } function main(): i32 { var p: P = P { x: 1, y: 2, z: 3 }; var q: P = P { ...p, z: 7, x: 9 }; return q.x*100 + q.y*10 + q.z; }",
			927 % 256, // = 159
		},
		{
			"struct-update-in-return",
			"struct P { a: i32, b: i32 } function bump(p: P): P { return P { ...p, b: p.b + 100 }; } function main(): i32 { var p: P = P { a: 5, b: 6 }; var q: P = bump(p); return p.b*1000 + q.a*100 + q.b; }",
			6606 % 256, // = 206
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			interpExit, _ := runDriver(t, interpBin, tc.source, false)
			nativeInterpExit := runNativeInterp(t, tc.source)
			asmExit := runAsm(t, tc.source)
			if interpExit != tc.want || nativeInterpExit != tc.want || asmExit != tc.want {
				t.Errorf("disagreement: interp=%d, native-interp=%d, asm=%d, want=%d\n--- source ---\n%s",
					interpExit, nativeInterpExit, asmExit, tc.want, tc.source)
			}
		})
	}
}
