package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostWasmArityGate pins #4360: an arg-count mismatch must REJECT on
// the self-host wasm drivers (exit 1 + an E004 diagnostic on stderr, nothing
// emitted), matching the Go compiler's check-time error — it used to sail
// through the emitter and miscompile. The gate is asmcore.check_call_arity, a
// conservative free-function arity slice: the accept cases pin that its
// skip-rules (defaulted args, locals shadowing a function name, method calls)
// never reject a valid program.
func TestSelfHostWasmArityGate(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern",
		"wasm_run.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	astDriver := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	irDriver := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "wasm_ir_run")

	// run pipes src to a driver and returns (stdout, stderr, exit code).
	run := func(t *testing.T, bin string, src string, args ...string) ([]byte, []byte, int) {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin, args...)
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), bin), args...)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, _ := cmd.Output()
		return out, stderr.Bytes(), cmd.ProcessState.ExitCode()
	}

	rejects := []struct {
		name string
		src  string
	}{
		// The issue's repro shape: plain free-function call, one arg short.
		{"too-few", "function f(a: i32, b: i32): i32 { return a + b; } function main(): i32 { return f(1); }"},
		{"too-many", "function g(a: i32): i32 { return a; } function main(): i32 { return g(1, 2); }"},
		// Nested inside an expression and a while body.
		{"nested", "function h(a: i32, b: i32): i32 { return a * b; } function main(): i32 { var s = 0; while (s < 3) { s = s + h(1); } return s; }"},
	}
	accepts := []struct {
		name string
		src  string
		want int
	}{
		{"exact", "function f(a: i32, b: i32): i32 { return a + b; } function main(): i32 { return f(40, 2); }", 42},
		// Omitted defaulted arg: fill_default_args runs before the gate.
		{"default-arg", "function inc(n: i32, by: i32 = 1): i32 { return n + by; } function main(): i32 { return inc(41); }", 42},
		// A local shadowing a module function name is a fn-value call — the
		// gate must skip it (function-wide shadow set).
		{"shadowed", "function dbl(n: i32): i32 { return n * 2; } function pick(f: (i32) => i32, n: i32): i32 { return f(n); } function main(): i32 { return pick(dbl, 21); }", 42},
	}

	drivers := []struct {
		name string
		bin  string
		args []string
	}{
		{"wasm_run", astDriver, nil},
		{"wasm_ir_run", irDriver, []string{"-ir"}},
	}
	for _, d := range drivers {
		for _, tc := range rejects {
			t.Run(d.name+"/reject-"+tc.name, func(t *testing.T) {
				out, errOut, code := run(t, d.bin, tc.src, d.args...)
				if code != 1 {
					t.Errorf("driver exited %d, want 1 (reject)", code)
				}
				if !strings.Contains(string(errOut), "E004") {
					t.Errorf("stderr = %q, want an E004 diagnostic", errOut)
				}
				if len(out) != 0 {
					t.Errorf("driver emitted %d bytes for a mis-arity program, want 0", len(out))
				}
			})
		}
		for _, tc := range accepts {
			t.Run(d.name+"/accept-"+tc.name, func(t *testing.T) {
				out, errOut, code := run(t, d.bin, tc.src, d.args...)
				if code != 0 {
					t.Fatalf("driver exited %d (stderr %q), want 0 (accept)", code, errOut)
				}
				if len(out) == 0 {
					t.Fatal("driver emitted 0 bytes for a valid program")
				}
				if _, err := exec.LookPath("wasmtime"); err != nil {
					t.Skip("wasmtime not on PATH; emitted WAT not executed")
				}
				watPath := filepath.Join(dir, "arity_"+tc.name+".wat")
				if err := os.WriteFile(watPath, out, 0o644); err != nil {
					t.Fatalf("write wat: %v", err)
				}
				wt := exec.Command("wasmtime", "run", watPath)
				_ = wt.Run()
				if wt.ProcessState == nil || !wt.ProcessState.Exited() {
					t.Fatalf("wasmtime did not exit normally")
				}
				if c := wt.ProcessState.ExitCode(); c != tc.want {
					t.Errorf("program exited %d, want %d", c, tc.want)
				}
			})
		}
	}
}
