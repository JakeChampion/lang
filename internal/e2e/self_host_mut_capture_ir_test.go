package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostMutCaptureIR proves the self-hosted x86-64 IR path implements
// MUTABLE SCALAR CAPTURES by reference (#2850 / SH-057): a closure that writes a
// captured outer i32/bool shares the write with the enclosing scope (closures
// as counters). The IR path captured by value, so writes were lost (repro → 8
// not 49; counter → 0 not 2). The fix boxes captured-and-mutated scalars into
// 1-element array cells (lift_lambdas' box_mutated_scalar_captures) so the
// existing array-pointer capture is by-reference.
//
// Expected values are the Go REFERENCE INTERPRETER's (`fern -interp`), which
// defines the semantics (scalar captures mutable by-ref). The native COMPILED
// backend had the SAME by-value bug (#2896) but is now fixed too
// (closureconv.BoxMutatedScalarCaptures — see TestNativeMutScalarCapture), so
// the compiled backends agree on these values. Read-only captures stay
// by-value (not boxed) — the read-only case guards that that path is unchanged.
func TestSelfHostMutCaptureIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
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
		// The SH-057 repro: a lambda WRITES a captured scalar without reading it.
		// By-value → write lost (8); by-reference → 7 + 42 = 49.
		{"write-only", `function main(): i32 { var x = 1; var f = function (): i32 { x = 42; return 7; }; var r = f(); return r + x; }`, 49},
		// Counter: read+write capture, accumulates across two calls → 2.
		{"counter", `function main(): i32 { var x = 0; var inc = function (): i32 { x = x + 1; return x; }; var a = inc(); var b = inc(); return x; }`, 2},
		// Counter taking a param, mutating across two calls → 10+5+3 = 18.
		{"counter-param", `function main(): i32 { var n = 10; var add = function (d: i32): i32 { n = n + d; return n; }; var a = add(5); var b = add(3); return n; }`, 18},
		// The lambda's own return reflects the post-write value → 5*2 = 10.
		{"returns-written", `function main(): i32 { var x = 5; var f = function (): i32 { x = x * 2; return x; }; return f(); }`, 10},
		// Read-only capture is NOT boxed — stays by-value; must still work → 6.
		{"read-only", `function main(): i32 { var x = 5; var f = function (): i32 { return x + 1; }; return f(); }`, 6},
		// A boolean captured scalar, toggled in the closure → 1.
		{"bool-capture", `function main(): i32 { var b = false; var t = function (): i32 { b = true; return 0; }; var r = t(); if (b) { return 1; } return 0; }`, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := emitAndRunIR(t, tc.src); got != tc.want {
				t.Errorf("self-host IR %q: exit = %d, want %d (reference interp)", tc.name, got, tc.want)
			}
		})
	}
}
