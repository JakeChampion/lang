package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostMatchLiteralIR covers `match` on a NON-enum scrutinee
// (i32 / string literal patterns + `_`) through the self-hosted x86-64
// compiler on the IR path. The native compiler already lowers this
// (internal/ir.emitLiteralMatch); the self-host previously could not
// parse a literal pattern at all (its Pattern grammar is variant-only),
// so a `match (n) { 1 => …, _ => … }` failed to compile.
//
// The parser now recognises a literal at the pattern position and
// desugars the whole match to an if/else-if chain (build_literal_match),
// the same shape `switch` and the native emitLiteralMatch produce — so it
// rides the existing if / while / `==` lowering with no new AST node and
// every backend (here the IR path) inherits it. The expression form
// (`var r = match (n) { 1 => 10, _ => 0 }`) routes through the same
// desugar inside the IIFE the self-host already builds for value-position
// matches.
func TestSelfHostMatchLiteralIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	emitAndRunIR := func(t *testing.T, src string) int {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, "-ir")
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		emitted, err := cmd.Output()
		if err != nil || len(emitted) == 0 {
			t.Fatalf("driver failed for %q: %v", src, err)
		}
		innerAsm := filepath.Join(dir, "ir_inner.s")
		innerBin := filepath.Join(dir, "ir_inner")
		if err := os.WriteFile(innerAsm, emitted, 0o644); err != nil {
			t.Fatalf("write inner asm: %v", err)
		}
		if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", innerAsm, "-o", innerBin).CombinedOutput(); err != nil {
			t.Fatalf("inner gcc: %v\n%s", err, out)
		}
		var inner *exec.Cmd
		if len(runner) == 0 {
			inner = exec.Command(innerBin)
		} else {
			inner = exec.Command(runner[0], append(append([]string{}, runner[1:]...), innerBin)...)
		}
		_ = inner.Run()
		if inner.ProcessState == nil || !inner.ProcessState.Exited() {
			t.Fatalf("inner did not exit normally for %q", src)
		}
		return inner.ProcessState.ExitCode()
	}

	cases := []struct {
		name string
		src  string
		want int
	}{
		// Statement form, i32 scrutinee: first arm, a middle arm, and the
		// `_` fall-through all dispatch by `==`.
		{"i32-first", `function main(): i32 { var n: i32 = 1; match (n) { 1 => { return 10; }, 2 => { return 20; }, _ => { return 0; } } }`, 10},
		{"i32-middle", `function main(): i32 { var n: i32 = 2; match (n) { 1 => { return 10; }, 2 => { return 20; }, _ => { return 0; } } }`, 20},
		{"i32-default", `function main(): i32 { var n: i32 = 9; match (n) { 1 => { return 10; }, 2 => { return 20; }, _ => { return 7; } } }`, 7},
		// Literal scrutinee (constant-folded) still routes through the
		// literal-match desugar.
		{"i32-literal-scrutinee", `function main(): i32 { match (3) { 1 => { return 1; }, 3 => { return 33; }, _ => { return 0; } } }`, 33},
		// String scrutinee: arms compare with `==` (string equality).
		{"string-match", `function main(): i32 { var s: string = "b"; match (s) { "a" => { return 1; }, "b" => { return 7; }, _ => { return 0; } } }`, 7},
		{"string-default", `function main(): i32 { var s: string = "z"; match (s) { "a" => { return 1; }, "b" => { return 7; }, _ => { return 4; } } }`, 4},
		// Guard on a literal arm: `5 when n > 3` folds to `n == 5 && n > 3`.
		{"guard-true", `function main(): i32 { var n: i32 = 5; match (n) { 1 => { return 1; }, 5 when n > 3 => { return 88; }, _ => { return 0; } } }`, 88},
		{"guard-false-falls-through", `function main(): i32 { var n: i32 = 5; match (n) { 1 => { return 1; }, 5 when n > 100 => { return 88; }, _ => { return 9; } } }`, 9},
		// Expression form (value position): desugars to an IIFE wrapping the
		// same literal-match if-chain.
		{"expr-i32", `function main(): i32 { var n: i32 = 2; var r: i32 = match (n) { 1 => 10, 2 => 20, _ => 0 }; return r; }`, 20},
		{"expr-default", `function main(): i32 { var n: i32 = 8; var r: i32 = match (n) { 1 => 10, 2 => 20, _ => 30 }; return r; }`, 30},
		// A `_`-only-after-one-literal match still works (the chain has a
		// single `if`, base else is the `_` body).
		{"single-literal-plus-default", `function main(): i32 { var n: i32 = 4; match (n) { 1 => { return 1; }, _ => { return 42; } } }`, 42},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := emitAndRunIR(t, tc.src); got != tc.want {
				t.Errorf("self-host IR %q: exit = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}
