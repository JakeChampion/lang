package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// labeledBreakIRCases widen the self-host IR subset to labeled break/continue —
// `outer: while/for { … break outer; … continue outer; … }`. The parser records a
// loop label on StmtWhile/StmtFor and a target label on break/continue; a
// resolve_labels pass (run at the shared parse entry, before any desugar) bakes
// each labeled break/continue's RELATIVE loop depth into its `tag`; and irlower's
// break/continue lowering targets loop_blk[len-1-tag] (tag 0 = innermost =
// unchanged behaviour for unlabeled). Verified to route IR and match the
// interpreter on x86-64 + wasm (and arm64 via qemu).
//
// Each case is routing-pinned to "ir", oracle-checked against the interpreter, and
// returns a value <= 120 (cf. the wasmtime exit-code gap #2908).
var labeledBreakIRCases = []struct {
	name string
	main string
}{
	// break to the outer loop from a nested loop.
	{"break-outer", `function main(): i32 { var i = 0; var c = 0; outer: while (i < 5) { i = i + 1; var j = 0; while (j < 5) { j = j + 1; c = c + 1; if (j == 2) { break outer; } } } return c; }`},
	// continue the outer loop, skipping the rest of the inner body.
	{"continue-outer", `function main(): i32 { var i = 0; var c = 0; outer: while (i < 3) { i = i + 1; var j = 0; while (j < 3) { j = j + 1; if (j == 1) { continue outer; } c = c + 1; } } return c; }`},
	// labeled for loops.
	{"labeled-for-break", `function main(): i32 { var c = 0; outer: for i in 0..3 { for j in 0..3 { c = c + 1; if (j == 1) { break outer; } } } return c; }`},
	// A labeled C-style `for`. The Stmt union has no node for it — the parser
	// desugars it to `if (true) { INIT; var __forc_L_C = true; while (true)
	// { … } }` — so the label has to land on the while inside that scoping if,
	// not on the if. It did not, so the name named no loop and `break outer`
	// left the INNER loop instead: the same program, one exit level short.
	{"labeled-c-for-break", `function main(): i32 { var c = 0; outer: for (var i = 0; i < 3; i = i + 1) { for j in 0..3 { c = c + 1; if (j == 1) { break outer; } } } return c; }`},
	{"labeled-c-for-continue", `function main(): i32 { var c = 0; outer: for (var i = 0; i < 3; i = i + 1) { for j in 0..3 { c = c + 1; if (j == 0) { continue outer; } } } return c; }`},
	// triple nesting, continue the MIDDLE loop (depth 1 from the innermost).
	{"triple-continue-mid", `function main(): i32 { var c = 0; mid: for i in 0..3 { for j in 0..3 { for k in 0..3 { c = c + 1; if (k == 0) { continue mid; } } } } return c; }`},
	// labeled break out of a match arm inside the inner loop (exercises the
	// match-arm recursion in resolve_labels).
	{"break-from-match", `function main(): i32 { var c = 0; outer: for i in 0..4 { for j in 0..4 { match (j) { 2 => { break outer; }, _ => { c = c + 1; } } } } return c; }`},
	// regression: plain (unlabeled) break still targets the innermost loop.
	{"plain-break", `function main(): i32 { var i = 0; var c = 0; while (i < 3) { i = i + 1; var j = 0; while (j < 3) { j = j + 1; c = c + 1; if (j == 1) { break; } } } return c; }`},
	// regression: plain continue.
	{"plain-continue", `function main(): i32 { var i = 0; var s = 0; while (i < 5) { i = i + 1; if (i == 3) { continue; } s = s + i; } return s; }`},
}

// TestSelfHostLabeledBreakIRX86_64 routes each case through the self-hosted x86-64
// IR driver, oracle-checked, routing pinned to "ir".
func TestSelfHostLabeledBreakIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range labeledBreakIRCases {
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

// TestSelfHostLabeledBreakIRWasm runs the same cases through the wasm IR backend
// (whose native multi-level `br` is the part the universal resolve_labels fixed).
func TestSelfHostLabeledBreakIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host labeled-break wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range labeledBreakIRCases {
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
			watFile := filepath.Join(dir, "labeled_break_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("labeled-break wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
