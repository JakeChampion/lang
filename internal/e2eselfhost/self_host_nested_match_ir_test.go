package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// nestedMatchIRCases pin a `match` nested inside another `match` arm's body to the
// self-host IR path on x86-64 + wasm. lower_stmt's StmtMatch arm lowers each arm
// body via lower_block, which recurses through lower_stmt for every statement —
// including a nested StmtMatch — with no guard against nesting (the only bails are
// per-arm pattern/payload shapes, which apply identically at any depth). The
// self-hosted compiler emits this construct itself: @derive(Eq) codegen builds an
// inner match as an outer arm body, and @derive(Eq) already routes IR. No
// existing self-host test nests a match in an arm (the block-expr `match-arm-block`
// case only nests a block), so this routing is unguarded.
//
// Each case is routing-pinned via asm_pathprobe_run (assert path == "ir") and
// oracle-checked against the interpreter, mirroring self_host_block_expr_ir_test.go.
// Patterns stay in the lowerable subset at every level (scalar variant payloads,
// i32/string literals, trailing _/variant); every result is <= 126 (wasmtime
// exit-code truncation, cf. #2908).
var nestedMatchIRCases = []struct {
	name string
	main string
}{
	// match on a variant, nested match on another variant in the A arm.
	{"nested-variant-in-variant", `enum E { A(i32), B }
function f(x: E, y: E): i32 { match (x) { A(n) => { match (y) { A(m) => { return n + m; }, B => { return n; }, } }, B => { return 0; }, } }
function main(): i32 { return f(A(20), A(22)); }`},
	// Outer variant match, inner literal (i32) match.
	{"nested-lit-in-variant", `enum E { A(i32), B }
function f(x: E, k: i32): i32 { match (x) { A(n) => { match (k) { 0 => { return n; }, _ => { return n + k; }, } }, B => { return 99; }, } }
function main(): i32 { return f(A(5), 3); }`},
	// Outer literal match, inner variant match.
	{"nested-variant-in-lit", `enum E { A(i32), B }
function f(k: i32, y: E): i32 { match (k) { 0 => { match (y) { A(m) => { return m; }, B => { return 1; }, } }, _ => { return 7; }, } }
function main(): i32 { return f(0, A(8)); }`},
	// Three levels of nesting.
	{"nested-3deep", `enum E { A(i32), B }
function f(a: E, b: E, c: E): i32 { match (a) { A(x) => { match (b) { A(y) => { match (c) { A(z) => { return x + y + z; }, B => { return x + y; }, } }, B => { return x; }, } }, B => { return 0; }, } }
function main(): i32 { return f(A(1), A(2), A(3)); }`},
	// Inner match scrutinises a STRING.
	{"nested-string-inner", `function f(k: i32, s: string): i32 { match (k) { 0 => { match (s) { "hi" => { return 2; }, _ => { return 0; }, } }, _ => { return 9; }, } }
function main(): i32 { return f(0, "hi"); }`},
	// Nested match in EXPRESSION-value (tail) position — exercises lower_value_tail.
	{"nested-expr-value", `enum E { A(i32), B }
function f(x: E, k: i32): i32 { var r: i32 = match (x) { A(n) => match (k) { 0 => n, _ => n + k }, B => 0 }; return r; }
function main(): i32 { return f(A(7), 3); }`},
	// Nested match inside a while-loop body, composing with surrounding control flow.
	{"nested-in-while", `enum E { A(i32), B }
function classify(x: E): i32 { match (x) { A(n) => { match (n) { 0 => { return 1; }, _ => { return 2; }, } }, B => { return 0; }, } }
function main(): i32 { var s: i32 = 0; var i: i32 = 0; while (i < 5) { s = s + classify(A(i)); i = i + 1; } return s; }`},
	// Regression guard: a flat single-level match still routes "ir".
	{"single-match-regress", `function f(k: i32): i32 { match (k) { 0 => { return 5; }, _ => { return 9; }, } }
function main(): i32 { return f(0); }`},
}

// TestSelfHostNestedMatchIRX86_64 routes each nested-match case through the
// self-hosted x86-64 IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostNestedMatchIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range nestedMatchIRCases {
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

// TestSelfHostNestedMatchIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostNestedMatchIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host nested-match wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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

	for _, tc := range nestedMatchIRCases {
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
			watFile := filepath.Join(dir, "nested_match_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("nested-match wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
