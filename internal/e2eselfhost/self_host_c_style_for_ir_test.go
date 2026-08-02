package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostCStyleForIR covers the C-style `for (var i = INIT; COND; STEP)`
// loop through the self-hosted x86-64 compiler on the IR path. The self-host
// Stmt union has no C-style-for node and previously misparsed `for (` as the
// `for (k, v) in m` map form, segfaulting (#2820). The parser now desugars a
// `var`-init C-style for to a scoped `while (true)` with a first-iteration
// flag, so `continue` re-runs STEP (matching C semantics) instead of skipping
// it — reusing if / while / break (no new node).
func TestSelfHostCStyleForIR(t *testing.T) {
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
		{"reproducer-sum", `function main(): i32 { var s: i32 = 0; for (var i: i32 = 1; i <= 10; i = i + 1) { s = s + i; } return s; }`, 55},
		// continue must run STEP (sum 0+1+3+4 = 8, skipping 2) — proves the
		// first-iteration-flag desugar, not a naive while-rewrite (which would
		// infinite-loop or give 10).
		{"continue-runs-step", `function main(): i32 { var s: i32 = 0; for (var i: i32 = 0; i < 5; i = i + 1) { if (i == 2) { continue; } s = s + i; } return s; }`, 8},
		{"break", `function main(): i32 { var s: i32 = 0; for (var i: i32 = 0; i < 100; i = i + 1) { if (i == 4) { break; } s = s + i; } return s; }`, 6},
		{"nested", `function main(): i32 { var s: i32 = 0; for (var i: i32 = 0; i < 3; i = i + 1) { for (var j: i32 = 0; j < 3; j = j + 1) { s = s + 1; } } return s; }`, 9},
		{"decrement", `function main(): i32 { var s: i32 = 0; for (var i: i32 = 10; i > 0; i = i - 1) { s = s + 1; } return s; }`, 10},
		{"step-by-two", `function main(): i32 { var s: i32 = 0; for (var i: i32 = 0; i < 6; i = i + 2) { s = s + i; } return s; }`, 6},
		{"compound-step", `function main(): i32 { var s: i32 = 0; for (var i: i32 = 0; i < 5; i += 1) { s += i; } return s; }`, 10},
		// zero-iteration (condition false at entry).
		{"zero-iter", `function main(): i32 { var s: i32 = 7; for (var i: i32 = 5; i < 5; i = i + 1) { s = s + 1; } return s; }`, 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := emitAndRunIR(t, tc.src); got != tc.want {
				t.Errorf("self-host IR %q: exit = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}
