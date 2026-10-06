package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostBareBlockIR covers a bare block statement `{ ... }` (its own
// scope) through the self-hosted x86-64 compiler. The self-host Stmt union
// has no StmtBlock, so the parser
// desugars a statement-position `{` to `if (true) { ... }` (the same trick
// `loop` uses for `while (true)`). Parsing it as StmtUnknown silently drops
// its inner statements (issue #2821).
func TestSelfHostBareBlockIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	emitAndRun := func(t *testing.T, src string) int {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin)
		} else {
			cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		emitted, err := cmd.Output()
		if err != nil || len(emitted) == 0 {
			t.Fatalf("driver failed for %q: %v", src, err)
		}
		innerAsm := filepath.Join(dir, "inner.s")
		innerBin := filepath.Join(dir, "inner")
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
		{"reproducer", `function main(): i32 { let b: i32 = 1; { let inner: i32 = 40; b = b + inner; } return b; }`, 41},
		{"two-blocks", `function main(): i32 { let s: i32 = 0; { s = s + 5; } { s = s + 10; } return s; }`, 15},
		{"block-in-if", `function main(): i32 { let x: i32 = 3; if (x > 0) { { x = x * 7; } } return x; }`, 21},
		{"block-in-loop", `function main(): i32 { let s: i32 = 0; let i: i32 = 0; while (i < 3) { { s = s + i; } i = i + 1; } return s; }`, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := emitAndRun(t, tc.src); got != tc.want {
				t.Errorf("self-host %q: exit = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}
