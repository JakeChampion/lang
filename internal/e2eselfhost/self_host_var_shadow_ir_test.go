package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// varShadowIRCases pin lexical variable SHADOWING in a nested block on the
// self-host IR path. A `var x` inside an if/while/for body shadows an outer `x`
// for the block's extent; after the block the outer binding is restored. The
// self-host lowerer resolved a name to its FIRST slot (slot_of) while `add_local`
// appended a fresh slot for the inner `var x`, so a shadowing `var x = 10` wrote
// to the OUTER slot and clobbered it — `return x` after the block read 10 instead
// of the outer 1 (interp + native were correct). The fix resolves a name to its
// MOST-RECENT binding (slot_of scans from the end); `lower_block` already retires
// the inner names on exit, so the outer is found again afterward. Each case is
// routing-pinned to "ir" and value-pinned against the native interpreter oracle.
var varShadowIRCases = []struct {
	name string
	src  string
	want int
}{
	// shadow in an if-block; the outer x must survive (1, not 10).
	{"if_block", `function main(): i32 { var x = 1; if (true) { var x = 10; } return x; }`, 1},
	// shadow in a while-body.
	{"while_body", `function main(): i32 { var x = 1; var i = 0; while (i < 1) { var x = 10; i = i + 1; } return x; }`, 1},
	// shadow in a for-body.
	{"for_body", `function main(): i32 { var x = 5; for i in [1, 2] { var x = 99; } return x; }`, 5},
	// the inner shadow IS used inside the block (reads the inner 10), then the
	// outer is read after (1) — proves both bindings resolve to the right slot.
	{"inner_then_outer", `function main(): i32 { var x = 1; var r = 0; if (true) { var x = 10; r = x; } return r * 10 + x; }`, 101},
	// outer read in an expression after the block.
	{"outer_after_in_expr", `function main(): i32 { var x = 1; if (true) { var x = 10; } var y = x + 1; return y; }`, 2},
	// a string outer shadowed by an i32 inner — the outer string survives and its
	// .len() is read after the block (slot type must not be clobbered either).
	{"typed_shadow", `function main(): i32 { var x = "hello"; if (true) { var x = 3; } return x.len(); }`, 5},
	// nested shadows two deep; each scope restores on exit (1 -> 2 -> 3 -> 2 -> 1).
	{"two_deep", `function main(): i32 { var x = 1; var a = 0; if (true) { var x = 2; a = a + x; if (true) { var x = 3; a = a + x; } a = a + x; } a = a + x; return a; }`, 8},
	// a FOR-loop variable shadowing an outer local: the loop var is scoped to the
	// loop, so after it the outer `i` (99) is restored. The loop var is bound
	// OUTSIDE the body block, so this exercises the for/match scope-retire (not
	// just slot_of) — at function-body top level (no enclosing block).
	{"for_var_shadow", `function main(): i32 { var i = 99; for i in [1, 2, 3] { } return i; }`, 99},
	// for-loop var shadowing, iterating a bare-ident array (the snapshot path).
	{"for_ident_shadow", `function main(): i32 { var a = [1, 2, 3]; var x = 50; for x in a { } return x; }`, 50},
	// a MATCH arm's payload binding shadowing an outer local: scoped to the arm,
	// so the outer `n` (5) survives — `r` got the inner 10, `n` stays 5 → 15.
	{"match_bind_shadow", `enum E { A(i32) }
function main(): i32 { var n = 5; var e: E = A(10); var r = 0; match (e) { A(n) => { r = n; } } return r + n; }`, 15},
	// a for-loop var leaking would collide with a later same-name local; here the
	// loop sums to 6, then a fresh `i = 10` → 16. (Guards the scope-retire doesn't
	// strand the slot.)
	{"for_then_redeclare", `function main(): i32 { var s = 0; for i in [1, 2, 3] { s = s + i; } var i = 10; return s + i; }`, 16},
}

// TestSelfHostVarShadowIRX86_64 routes each case through the self-host x86-64 IR
// driver (pinned to "ir") and asserts the native-oracle exit code.
func TestSelfHostVarShadowIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
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

	for _, tc := range varShadowIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "var_shadow_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("var-shadow %q exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostVarShadowWasmIR runs the same cases through the wasm IR backend.
func TestSelfHostVarShadowWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host var-shadow wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
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

	for _, tc := range varShadowIRCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "var_shadow_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("var-shadow wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
