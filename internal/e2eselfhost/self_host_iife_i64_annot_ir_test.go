package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// iifeI64AnnotIRCases pin a small-literal-branch i64/u64 if/match-EXPRESSION bound
// to an i64/u64-annotated local to the self-host IR path on x86-64 + wasm. An
// if/match-expression desugars to a 0-arg IIFE; the IR lowering already inlined it
// into an i64 temp when SOME branch carried an i64 value (e.g. `{ 5000000000 }`),
// but a fully-small-literal i64 expression — where i64-ness comes ONLY from the
// binding annotation (`var x: i64 = if (c) { 5 } else { 9 }`) — failed the
// branch-value width classifier and bailed the whole module to the legacy AST
// emitter. #2691 threads a force_i64 flag from lower_i64 (the binding context is
// definitionally i64/u64) through lower_iife / lower_iife_match so the inline temp
// is marked i64 and each small-literal branch is widened into it. Each case is
// oracle-checked against the interpreter and returns <= 126. Mirrors
// self_host_structarray_call_field_ir_test.go.
//
// (Constant-condition forms like `if (1 < 2) { 5 } else { 9 }` take a separate
// const-fold path and stay on the AST emitter — correct, just not widened here.)
//
// The later cases pin the other half of the same decision: the per-branch tag
// if_expr_rt computes, which wider_rt then combines. A branch that COMPUTES its
// value (binary / unary / cast) rather than naming a literal used to fall through
// to "i32" and lose the width the same way.
var iifeI64AnnotIRCases = []struct {
	name string
	main string
}{
	// if-expression, runtime condition, both branches small i64 literals. 5.
	{"ifexpr-i64-runtime", `function main(): i32 { var n = 5; var x: i64 = if (n > 3) { 5 } else { 9 }; return x as i32; }`},
	// u64 annotation rides the same path. 5.
	{"u64-ifexpr-runtime", `function main(): i32 { var n = 5; var x: u64 = if (n > 3) { 5 } else { 9 }; return x as i32; }`},
	// match-expression, small i64 literal arms. 5.
	{"matchexpr-i64", `function main(): i32 { var n = 1; var x: i64 = match (n) { 1 => 5, _ => 9 }; return x as i32; }`},
	// match-expression, three arms (non-default branch taken). 5.
	{"matchexpr-i64-wild", `function main(): i32 { var n = 2; var x: i64 = match (n) { 1 => 7, 2 => 5, _ => 9 }; return x as i32; }`},
	// if-expression result used in subsequent i64 arithmetic. 5 + 1 = 6.
	{"ifexpr-i64-arith", `function main(): i32 { var n = 5; var x: i64 = if (n > 3) { 5 } else { 9 }; var y: i64 = x + 1; return y as i32; }`},
	// nested else-if, small i64 literals (middle branch taken). 7.
	{"nested-elseif-i64", `function main(): i32 { var n = 1; var x: i64 = if (n > 5) { 1 } else if (n > 0) { 7 } else { 9 }; return x as i32; }`},
	// Regression: a branch with a real i64 (big-literal) value was already on the IR
	// path via the branch-value classifier — it must stay there. 5000000000 % 7 = 2.
	{"ifexpr-i64-bigbranch", `function main(): i32 { var n = 5; var x: i64 = if (n > 3) { 5000000000 } else { 1 }; return (x % 7) as i32; }`},

	// A COMPUTED branch — if_expr_rt classified literals, bools, strings and nested
	// IIFEs and sent everything else to "i32", so an arm that computed its value
	// instead of naming it lost the width: wider_rt had nothing to widen with, the
	// IIFE was labelled i32, and the i64 `return` bailed the module. Magnitude was
	// never the trigger — the bare literal `2147483967i64` lowered fine while
	// `2147483967i64 * 2i64` did not. 604 /? 478 = Some(1), so 1.
	{"matchexpr-i64-binary-arm", `function main(): i32 { var x: i64 = match ((604i64) /? (478i64)) { Some(w) => w, None => (3i64 * 5i64) }; return x as i32; }`},
	// Same gap with the computed arm TAKEN, so the exit code is a direct oracle on
	// the width: 5000000000 / 1000000000 = 5 at 64-bit, but a 32-bit truncation
	// (705032704) / 1000000000 = 0. 1 /? 0 is None, so the None arm is the value.
	{"matchexpr-i64-binary-arm-taken", `function main(): i32 { var x: i64 = match ((1i64) /? (0i64)) { Some(w) => w, None => (5000000000i64 / 1000000000i64) }; return x as i32; }`},
	// A SHIFT is typed by the value being shifted, not by the count — the count is
	// an independent operand and may legitimately be narrower. 5000000000 >> 30 = 4;
	// truncated to 32 bits it would be 705032704 >> 30 = 0.
	{"matchexpr-i64-shift-arm", `function main(): i32 { var x: i64 = match ((1i64) /? (0i64)) { Some(w) => w, None => (5000000000i64 >> 30i64) }; return x as i32; }`},
	// An `as <type>` CAST is decisive on its TARGET, not its operand: reading the
	// operand inverts the one construct whose job is to change the width, so
	// `((5000000000i64 >> 30i64) as i32) & 7i32` reads i64 and bails a module that
	// lowered before. The un-annotated `return` position is what exposes it — with a
	// `var x: i32 =` annotation the binding supplies the type and the inversion hides.
	// 5000000000 >> 30 = 4, & 7 = 4.
	{"matchexpr-cast-arm-narrows", `function main(): i32 { return (match ((1i64) /? (0i64)) { Some(w) => (w as i32), None => (((5000000000i64 >> 30i64) as i32) & 7i32) } & 255i32); }`},
	// A `!` branch is a boolean whatever its operand is. 5.
	{"ifexpr-bool-not-arm", `function main(): i32 { var n = 5; var b: boolean = if (n > 3) { !(n > 100) } else { false }; var x: i32 = 0; if (b) { x = 5; } return x; }`},
	// A COMPARISON branch is likewise a boolean, not the width of its operands. 7.
	{"ifexpr-bool-cmp-arm", `function main(): i32 { var n = 5; var b: boolean = if (n > 3) { n > 1 } else { false }; var x: i32 = 0; if (b) { x = 7; } return x; }`},
}

// TestSelfHostIifeI64AnnotIRX86_64 routes each case through the self-hosted x86-64
// IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostIifeI64AnnotIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range iifeI64AnnotIRCases {
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

// TestSelfHostIifeI64AnnotIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostIifeI64AnnotIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host iife-i64-annot wasm IR e2e")
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

	for _, tc := range iifeI64AnnotIRCases {
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
			watFile := filepath.Join(dir, "iife_i64_annot_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("iife-i64-annot wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
