package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostBareBlockIR covers a bare block statement `{ ... }` (its own
// scope) through the self-hosted x86-64 compiler on both the AST path and
// the IR path. The self-host Stmt union has no StmtBlock, so the parser
// desugars a statement-position `{` to `if (true) { ... }` (the same trick
// `loop` uses for `while (true)`). Parsing it as StmtUnknown silently drops
// its inner statements (issue #2821).
func TestSelfHostBareBlockIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	emitAndRun := func(t *testing.T, src string, ir bool) int {
		t.Helper()
		args := []string{}
		if ir {
			args = append(args, "-ir")
		}
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, args...)
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), args...)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		emitted, err := cmd.Output()
		if err != nil || len(emitted) == 0 {
			t.Fatalf("driver failed (ir=%v) for %q: %v", ir, src, err)
		}
		tag := "ast"
		if ir {
			tag = "ir"
		}
		innerAsm := filepath.Join(dir, tag+"_inner.s")
		innerBin := filepath.Join(dir, tag+"_inner")
		if err := os.WriteFile(innerAsm, emitted, 0o644); err != nil {
			t.Fatalf("write inner asm: %v", err)
		}
		if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", innerAsm, "-o", innerBin).CombinedOutput(); err != nil {
			t.Fatalf("inner gcc (ir=%v): %v\n%s", ir, err, out)
		}
		var inner *exec.Cmd
		if len(runner) == 0 {
			inner = exec.Command(innerBin)
		} else {
			inner = exec.Command(runner[0], append(append([]string{}, runner[1:]...), innerBin)...)
		}
		_ = inner.Run()
		if inner.ProcessState == nil || !inner.ProcessState.Exited() {
			t.Fatalf("inner did not exit normally (ir=%v) for %q", ir, src)
		}
		return inner.ProcessState.ExitCode()
	}

	cases := []struct {
		name string
		src  string
		want int
	}{
		{"reproducer", `function main(): i32 { var b: i32 = 1; { var inner: i32 = 40; b = b + inner; } return b; }`, 41},
		{"two-blocks", `function main(): i32 { var s: i32 = 0; { s = s + 5; } { s = s + 10; } return s; }`, 15},
		{"block-in-if", `function main(): i32 { var x: i32 = 3; if (x > 0) { { x = x * 7; } } return x; }`, 21},
		{"block-in-loop", `function main(): i32 { var s: i32 = 0; var i: i32 = 0; while (i < 3) { { s = s + i; } i = i + 1; } return s; }`, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			astCode := emitAndRun(t, tc.src, false)
			irCode := emitAndRun(t, tc.src, true)
			if astCode != irCode {
				t.Errorf("AST-path vs IR-path mismatch for %q: AST=%d IR=%d", tc.name, astCode, irCode)
			}
			if irCode != tc.want {
				t.Errorf("self-host %q: exit = %d, want %d", tc.name, irCode, tc.want)
			}
		})
	}
}
