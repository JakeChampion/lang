package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostU32RetGenericDynWasmIR extends the u32-return-call unsigned-ness
// tracking (#5245, first landed for concrete free functions + methods in #5255)
// to the three producers that fix left uncovered — exactly the cases the issue's
// fix shape called for ("generic type-var returns, and dyn dispatch"):
//
//   - an ERASED-GENERIC call whose return mirrors a u32 argument (`idg[T](x: T): T`)
//   - a u32-valued if/match IIFE (`(if (c) { big_u32 } else { 0 }) >> k`)
//   - a `dyn Trait` method dispatch returning u32 (`d.v() >> k`)
//
// A u32 result is a clean value in [0, 2^32), but with bit 31 set it is
// signed-negative in a 32-bit slot, so wasm's signed `i32.shr_s` / `div_s` /
// `gt_s` diverge from the unsigned answer; irlower must select the `_u` opcode.
// This is wasm-only: x86-64 / arm64 keep the u32 zero-extended in a 64-bit
// register, so a signed shift already matched there.
//
// The generic arm reuses the "name|$arg<i>" argref registry the u64 sibling
// uses; the IIFE arm classifies by the first branch's returned value; the dyn arm
// keys "dyn <Trait>.<method>" in i64_ret_fns via append_dyn_i64_ret_fns (ret flag
// '3', is_u32_ret_fn). SHIFTS are the sharp probe (a shift's signedness follows
// the LEFT operand alone), so `idg(a) >> k` / `d.v() >> k` have no other u32
// signal. For DIV the call must be the SOLE u32 operand (two u32-returning calls,
// no `as u32` literal) — otherwise a u32 literal operand already selects the
// unsigned op and the case would pass even unfixed. Every value here has bit 31
// set; expected values are the interpreter-oracle answers (kept <= 126 for
// wasmtime's exit-code range).
func TestSelfHostU32RetGenericDynWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host u32-return generic/dyn wasm IR e2e")
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
		// ERASED-GENERIC return mirroring a u32 argument, chained in a shift:
		// `idg(a) >> 25`. is_u32_ret_fn misses (the generic fn isn't u32-typed),
		// so the ExprCall(ident) arm falls through to str_ret_argref + the u32-ness
		// of the actual argument. 0x80000001 >> 25 == 64 unsigned.
		{"generic-shr", `function idg[T](x: T): T { return x; } function main(): i32 { var a: u32 = 2147483649 as u32; return (idg(a) >> 25) as i32; }`, 64},
		// TWO generic-call operands in a DIVISION: `idg(a) / idg(b)` — the argref
		// u32-ness is the sole signal (no u32 literal operand). 4000000000 / 7 ==
		// 571428571; % 100 == 71.
		{"generic-twocall-div", `function idg[T](x: T): T { return x; } function main(): i32 { var a: u32 = 4000000000 as u32; var b: u32 = 7 as u32; return ((idg(a) / idg(b)) % (100 as u32)) as i32; }`, 71},
		// u32-valued if/match IIFE chained in a shift: the 0-arg IIFE lambda the
		// desugar emits is classified by its first branch's returned value (`a`, a
		// u32). `(if (…) { a } else { 0 }) >> 25`.
		{"iife-shr", `function main(): i32 { var a: u32 = 2147483649 as u32; var r: u32 = (if (a > (0 as u32)) { a } else { 0 as u32 }) >> 25; return r as i32; }`, 64},
		// dyn Trait method dispatch returning u32, chained in a shift: `d.v() >> 25`.
		// The "dyn Val.v" key is populated by append_dyn_i64_ret_fns with ret flag
		// '3'; the ExprCall(method) arm resolves the receiver to "dyn Val".
		{"dyn-shr", `trait Val { function v(self: Self): u32; } struct B { n: u32 } impl Val for B { function v(self: Self): u32 { return self.n; } } function main(): i32 { var b: B = B { n: 2147483649 as u32 }; var d: dyn Val = b; return (d.v() >> 25) as i32; }`, 64},
		// TWO dyn-method-call operands in a DIVISION: `d.v() / d.d7()` — the dyn
		// u32-return is the sole signal. Same 71 arithmetic as generic-twocall-div.
		{"dyn-twocall-div", `trait Val { function v(self: Self): u32; function d7(self: Self): u32; } struct B { n: u32, d: u32 } impl Val for B { function v(self: Self): u32 { return self.n; } function d7(self: Self): u32 { return self.d; } } function main(): i32 { var b: B = B { n: 4000000000 as u32, d: 7 as u32 }; var d: dyn Val = b; return ((d.v() / d.d7()) % (100 as u32)) as i32; }`, 71},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runIR(t, tc.src); got != tc.expected {
				t.Errorf("u32-return generic/dyn wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
