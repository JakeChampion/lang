package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostU32CallRetWasmIR gates u32-ness tracking when the value chained
// into an unsigned-sensitive op (`>>` `/` `%` `>` …) is the RESULT OF A CALL — a
// concrete u32-returning free function, a u32-returning method, or an
// erased-generic fn whose return mirrors a u32 argument. A u32 value with bit 31
// set is signed-negative in a 32-bit slot, so wasm's signed `i32.shr_s` /
// `div_s` / `gt_s` diverge from the unsigned answer.
//
// expr_is_u32 intentionally does NOT treat a call as u32 (the callee already
// wrapped its own u32 result, so the value arrives width-clean and needs no
// u32-wrap). But its SIGN still matters at a directly-chained shift / div /
// compare, so irlower recovers it via a separate `expr_call_is_u32_ret` signal
// (backed by the `'3'` return flag `i64_ret_fns_of` now records for a u32
// return) OR'd into the sign selection only — not into the wrap. This is the
// u32 sibling of #5159 (concrete u64-returning call). wasm-only: x86-64 / arm64
// keep the u32 zero-extended in a 64-bit register, so a signed shift/div/compare
// already matched there. Expected values are the interpreter-oracle answers
// (kept <= 126 for the wasmtime exit-code range). #5245.
func TestSelfHostU32CallRetWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host u32-call-ret wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
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

	runIR := func(t *testing.T, src string) int {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, "-ir")
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		wat, err := cmd.Output()
		if err != nil || len(wat) == 0 {
			t.Fatalf("driver failed for %q: %v", src, err)
		}
		watFile := filepath.Join(dir, "ir_prog.wat")
		if err := os.WriteFile(watFile, wat, 0o644); err != nil {
			t.Fatalf("write wat: %v", err)
		}
		run := exec.Command("wasmtime", "run", watFile)
		_ = run.Run()
		if run.ProcessState == nil || !run.ProcessState.Exited() {
			t.Fatalf("wasmtime did not exit normally for %q:\n%s", src, wat)
		}
		return run.ProcessState.ExitCode()
	}

	cases := []struct {
		name     string
		src      string
		expected int
	}{
		// CONCRETE u32-returning free function chained in a shift: 0x80000001 >> 25
		// == 64 unsigned; a signed shr sign-extends and diverges. is_u32_ret_fn.
		{"concrete-fn-shr", `function id32(x: u32): u32 { return x; } function main(): i32 { var a: u32 = 2147483649 as u32; return (id32(a) >> 25) as i32; }`, 64},
		// CONCRETE u32-returning free function chained in a DIVISION: div_u.
		// 4000000000 / 7 == 571428571; % 100 == 71.
		{"concrete-fn-div", `function id32(x: u32): u32 { return x; } function main(): i32 { var a: u32 = 4000000000 as u32; return ((id32(a) / (7 as u32)) % (100 as u32)) as i32; }`, 71},
		// CONCRETE u32-returning free function in an ordering compare: gt_u.
		{"concrete-fn-cmp", `function id32(x: u32): u32 { return x; } function main(): i32 { var a: u32 = 4000000000 as u32; if (id32(a) > (2000000000 as u32)) { return 44; } return 0; }`, 44},
		// u32-returning METHOD `p.get()` chained in a shift: method key "P.get" in
		// the '3'-flagged registry, resolved via the receiver's struct type.
		{"method-shr", `struct P { n: u32 } impl P { function get(self: Self): u32 { return self.n; } } function main(): i32 { var p: P = P { n: 2147483649 as u32 }; return (p.get() >> 25) as i32; }`, 64},
		// ERASED-GENERIC fn whose return mirrors a u32 argument: `pick[T](x: T): T`
		// called with a u32 arg is u32, so `pick(a) >> 25` selects shr_u
		// (str_ret_argref + expr_is_u32 on the mirrored argument).
		{"generic-argref-shr", `function pick[T](x: T): T { return x; } function main(): i32 { var a: u32 = 2147483649 as u32; return (pick(a) >> 25) as i32; }`, 64},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runIR(t, tc.src); got != tc.expected {
				t.Errorf("u32-call-ret wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
