package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostU32RetCallWasmIR gates u32-ness tracking when a u32 value comes
// from a CALL RESULT — a concrete free function or a method — chained directly
// into an unsigned-sensitive op (`>>` `/` `%` `>` …). A clean u32 in [0, 2^32)
// with bit 31 set still reads signed-negative in the 32-bit slot, so wasm's
// signed `i32.shr_s` / `div_s` / `rem_s` / `gt_s` diverge from the unsigned
// answer; irlower must select the `_u` opcode.
//
// expr_is_u32 previously did NOT treat a u32-returning call as u32 (the reasoning
// held for value WRAPPING — the callee already masked its result into [0, 2^32) —
// but NOT for SIGN INTERPRETATION). So `id32(a) >> 25`, `p.get() >> 25`,
// `id32(a) / 7`, and `p.get() > k` all lowered SIGNED. This is wasm-only: x86-64
// / arm64 keep the u32 zero-extended in a 64-bit register, so a signed shift/div
// already matched there. The fix rides the same i64_ret_fns registry as the
// i64/u64 return family (#5159), with a distinct ret flag '3' for u32 read only
// by is_u32_ret_fn. Every value here has bit 31 set, so each case fails with the
// signed opcode and passes with the unsigned one; expected values are the
// interpreter-oracle answers (kept <= 126 for the wasmtime exit-code range).
func TestSelfHostU32RetCallWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host u32-return-call wasm IR e2e")
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
		// FREE FUNCTION u32 return chained in a shift: 0x80000001 >> 25 == 64
		// unsigned; a signed shr sign-extends and diverges (and exits outside the
		// valid range). expr_is_u32 ExprCall(ExprIdent) arm → is_u32_ret_fn.
		{"free-fn-shr", `function id32(x: u32): u32 { return x; } function main(): i32 { var a: u32 = 2147483649 as u32; return (id32(a) >> 25) as i32; }`, 64},
		// METHOD u32 return chained in a shift: `p.get() >> 25`. ExprCall's
		// ExprFieldAccess callee arm resolves the receiver's struct type and looks
		// up "P.get" in the u32-return registry.
		{"method-shr", `struct P { n: u32 } impl P { function get(self: Self): u32 { return self.n; } } function main(): i32 { var p: P = P { n: 2147483649 as u32 }; return (p.get() >> 25) as i32; }`, 64},
		// FREE FUNCTION u32 return in a DIVISION / REMAINDER where the u32-ness comes
		// ONLY from the call (the other operands are plain i32, so nothing else marks
		// it): `id32(4e9) / 7 % 100` needs div_u/rem_u. 4000000000/7 == 571428571;
		// % 100 == 71.
		{"free-fn-div", `function id32(x: u32): u32 { return x; } function main(): i32 { var a: u32 = 4000000000 as u32; return ((id32(a) / 7) % 100) as i32; }`, 71},
		// METHOD u32 return in an ordering compare with a plain-i32 bound:
		// `p.get() > 2e9` is true unsigned, false signed (4e9 reads negative).
		// Selects gt_u.
		{"method-cmp", `struct P { n: u32 } impl P { function get(self: Self): u32 { return self.n; } } function main(): i32 { var p: P = P { n: 4000000000 as u32 }; if (p.get() > 2000000000) { return 12; } return 0; }`, 12},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runIR(t, tc.src); got != tc.expected {
				t.Errorf("u32-return-call wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
