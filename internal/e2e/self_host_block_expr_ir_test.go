package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// blockExprIRCases exercise multi-statement BLOCK-EXPRESSION value branches
// through the self-host IR path on x86-64 + wasm (block-expressions slice 1 in
// the self-hosted compiler — see docs/BLOCK-EXPRESSIONS.md).
//
// The gap this closes: an `if`/`match` EXPRESSION value branch was parsed with
// a single `parse_expr`, so a multi-statement branch (`{ var k = e + 1; k }`)
// mis-parsed (the `var` landed in expression position) and bailed the module to
// the AST emitter. The parser now parses each if/match value branch as a
// block-with-tail (parse_branch_body): leading `;`-terminated statements run
// before a trailing expression — written WITHOUT a `;` — that is the branch's
// value. The result is a `Stmt[]` ending in `s_return(tail)`, which irlower's
// existing lower_value_tail lowers (leading statements + the value-producing
// terminal). A lone trailing expression with no leading statements stays
// `[s_return(expr)]`, byte-identical to the single-expr branch.
//
// Each case is oracle-checked against the interpreter, routing-pinned to "ir",
// and returns a value <= 126 (wasmtime exit-code truncation, cf. #2908).
var blockExprIRCases = []struct {
	name string
	main string
}{
	// The canonical example: a multi-statement if-EXPRESSION then-branch.
	// f(5): k = 6 -> 6.
	{"if-then-block", `function f(e: i32): i32 { var x: i32 = if (e > 0) { var k = e + 1; k } else { 0 }; return x; }
function main(): i32 { return f(5); }`},
	// A multi-statement ELSE branch (the then-branch is a single expr).
	// e = 0 -> else: j = 3 + 4 = 7.
	{"if-else-block", `function f(e: i32): i32 { var x: i32 = if (e > 0) { e } else { var j = 3 + 4; j }; return x; }
function main(): i32 { return f(0); }`},
	// Multiple leading statements in the then-branch. a=2, b=a*5=10, a+b=12.
	{"if-then-multi-stmt", `function main(): i32 { var x: i32 = if (true) { var a = 2; var b = a * 5; a + b } else { 0 }; return x; }`},
	// A while-loop statement in a branch, then the tail.
	// s = 1+2+3 = 6, tail s.
	{"if-then-while", `function main(): i32 { var x: i32 = if (true) { var s = 0; var i = 1; while (i <= 3) { s = s + i; i = i + 1; } s } else { 0 }; return x; }`},
	// A multi-statement MATCH arm body (enum scrutinee). f(A(20)): n=20, d=n+1=21 -> 42.
	{"match-arm-block", `enum E { A(i32), B } function f(e: E): i32 { var x: i32 = match (e) { A(n) => { var d = n + 1; d }, B => 9 }; return x; }
function main(): i32 { return f(A(20)) + f(B); }`},
	// A multi-statement LITERAL-match arm body (i32 scrutinee). tag=0: s=10+5=15.
	{"litmatch-arm-block", `function f(tag: i32): i32 { var x: i32 = match (tag) { 0 => { var s = 10 + 5; s }, _ => 99 }; return x; }
function main(): i32 { return f(0); }`},
	// A NESTED if-expression as the trailing tail of a branch block.
	// c=3: a=6, a>5 -> a=6.
	{"nested-if-tail", `function f(c: i32): i32 { var x: i32 = if (c > 0) { var a = c * 2; if (a > 5) { a } else { 1 } } else { 0 }; return x; }
function main(): i32 { return f(3); }`},
	// Else-if CHAIN unchanged (single-expr branches): n=5 -> middle arm 20.
	{"else-if-chain", `function f(n: i32): i32 { var x: i32 = if (n > 10) { 30 } else if (n > 0) { 20 } else { 10 }; return x; }
function main(): i32 { return f(5); }`},
	// Single-expr if-expression unchanged (regression guard, stays "ir").
	{"single-expr-regress", `function f(e: i32): i32 { var x: i32 = if (e > 0) { e + 1 } else { 0 }; return x; }
function main(): i32 { return f(5); }`},
	// String-tail block branch: a multi-statement branch yielding a string,
	// then .len(). s = "hi" -> 2.
	{"if-then-string-tail", `function f(): i32 { var s: string = if (true) { var p = "hi"; p } else { "x" }; return s.len(); }
function main(): i32 { return f(); }`},
}

// TestSelfHostBlockExprIRX86_64 routes each block-expression case through the
// self-hosted x86-64 IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostBlockExprIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asm_run.fern", "asm_pathprobe_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range blockExprIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostBlockExprIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostBlockExprIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host block-expr wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range blockExprIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "blockexpr_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("block-expr wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
