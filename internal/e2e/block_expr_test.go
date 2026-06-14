// Block-expressions (slice 1, interpreter-only): `if`/`match`
// expression branches accept a `{ stmts; tail }` body whose statements
// run in a fresh child scope and whose trailing expression (no `;`) is
// the branch's value. These end-to-end tests run real programs on the
// interpreter (the source of truth for slice 1) and assert main()'s exit
// code, plus confirm the compiled backends reject a BlockExpr cleanly.
// See docs/BLOCK-EXPRESSIONS.md.
package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// blockExprInterpExit runs src on the interpreter and returns (exitCode, stdout,
// stderr) without failing the test — callers assert on the values
// (slice-1 reject paths expect a non-zero exit, the happy paths expect a
// specific code).
func blockExprInterpExit(t *testing.T, src string) (int, string, string) {
	t.Helper()
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", p)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	return cmd.ProcessState.ExitCode(), out.String(), errb.String()
}

func TestBlockExprInterp(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{
			// `if`-branch block: a leading statement then a trailing
			// value. The block evaluates to `e + 1` = 6.
			"if-block-tail",
			`function main(): i32 {
				var e = 5;
				var x: i32 = if (e > 0) { var k = e + 1; k } else { 0 };
				return x;
			}`,
			6,
		},
		{
			// The other branch is taken — value-less branch isn't
			// reached, the single-expr `else` value flows.
			"if-block-else-taken",
			`function main(): i32 {
				var e = 0;
				var x: i32 = if (e > 0) { var k = e + 1; k } else { var z = 9; z };
				return x;
			}`,
			9,
		},
		{
			// `match`-arm block-expression: arm 0 runs `var s = tag +
			// 5; s` → 5; the wildcard arm is a bare expr.
			"match-arm-block",
			`function main(): i32 {
				var tag = 0;
				var r: i32 = match (tag) { 0 => { var s = tag + 5; s }, _ => 99 };
				return r;
			}`,
			5,
		},
		{
			// Multiple leading statements; the tail sees them all.
			"multi-leading-statements",
			`function main(): i32 {
				var x: i32 = if (true) { var a = 2; var b = a * 3; var c = b + 1; c } else { 0 };
				return x;
			}`,
			7,
		},
		{
			// Composition: a block-expr `if` nested inside a `match`
			// scrutinee. `match (if (c) { var k = 7; k } else { 0 }) {
			// ... }` → matches 7.
			"compose-through-match",
			`function main(): i32 {
				var c = true;
				var r: i32 = match (if (c) { var k = 7; k } else { 0 }) {
					7 => { var hit = 1; hit },
					_ => 0
				};
				return r;
			}`,
			1,
		},
		{
			// Block locals don't leak: the same name `k` is rebound in
			// a later block-expr without collision, proving each block
			// gets its own scope.
			"locals-confined-to-block",
			`function main(): i32 {
				var a: i32 = if (true) { var k = 10; k } else { 0 };
				var b: i32 = if (true) { var k = 20; k } else { 0 };
				return a + b;
			}`,
			30,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, out, errb := blockExprInterpExit(t, c.src)
			if code != c.want {
				t.Errorf("exit = %d, want %d\nstdout: %s\nstderr: %s", code, c.want, out, errb)
			}
		})
	}
}

// All compiled backends reject a BlockExpr cleanly (a clear error, no
// panic) — slice 1 is interpreter-only. Driven through the CLI compile
// path so the whole pipeline is exercised, not just the IR unit.
func TestBlockExprCompiledRejectCLI(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := `function main(): i32 {
		var x: i32 = if (true) { var k = 1; k } else { 0 };
		return x;
	}`
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	for _, target := range []string{"wasm", "arm64", "x86-64"} {
		t.Run(target, func(t *testing.T) {
			outBin := filepath.Join(dir, "out-"+target)
			cmd := exec.Command(bin, "-target", target, "-o", outBin, p)
			var out, errb bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &errb
			err := cmd.Run()
			if err == nil {
				t.Fatalf("compile to %s unexpectedly succeeded (BlockExpr should reject in slice 1)", target)
			}
			combined := out.String() + errb.String()
			if strings.Contains(combined, "panic") {
				t.Errorf("compile to %s PANICKED instead of clean reject:\n%s", target, combined)
			}
			if !strings.Contains(combined, "block-expression") {
				t.Errorf("compile to %s error should mention block-expression, got:\n%s", target, combined)
			}
		})
	}
}
