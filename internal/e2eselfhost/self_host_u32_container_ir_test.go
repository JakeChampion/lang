package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostU32ContainerWasmIR gates u32-ness tracking when a u32 value is
// read out of a CONTAINER (struct field, tuple element, array literal / slice
// element, or an enum-variant payload binding) and then chained directly into
// an unsigned-sensitive op (`>>` `/` `%` `>` …). A u32 value with bit 31 set is
// signed-negative in a 32-bit slot, so wasm's signed `i32.shr_s` / `div_s` /
// `gt_s` diverge from the unsigned answer; irlower must select the `_u` opcode.
//
// expr_is_u32 previously only recognised a u32 IDENT slot and a u32[] ident
// element — so `p.n >> k`, `t.0 >> k`, `[big][0] >> k`, `a[lo:hi][0] >> k`, and a
// `U(u32) => x` match binding all lowered SIGNED. This is wasm-only: x86-64 /
// arm64 keep the u32 zero-extended in a 64-bit register, so a signed shift/div
// already matched there. Every value here has bit 31 set, so each case fails
// with the signed opcode and passes with the unsigned one; expected values are
// the interpreter-oracle answers (kept <= 126 for the wasmtime exit-code range).
func TestSelfHostU32ContainerWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host u32-container wasm IR e2e")
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
		// STRUCT FIELD chained in a shift: 0x80000001 >> 25 == 64 unsigned; a
		// signed shr sign-extends and diverges. #expr_is_u32 ExprFieldAccess arm.
		{"struct-field-shr", `struct SU { n: u32 } function main(): i32 { var p: SU = SU { n: 2147483649 as u32 }; return (p.n >> 25) as i32; }`, 64},
		// ENUM u32-PAYLOAD binding chained in a shift: the match arm marks the bound
		// slot u32 (mark_u32) so `x >> 25` selects shr_u. 4-byte read (no width
		// change vs the u64 payload's 8-byte).
		{"enum-payload-shr", `enum EU { U(u32), N } function main(): i32 { var e: EU = EU.U(2147483649); match (e) { EU.U(x) => { return (x >> 25) as i32; }, EU.N => { return 99; } } }`, 64},
		// TUPLE ELEMENT chained in a shift: `t.0 >> 25`. expr_is_u32's
		// ExprFieldAccess arm resolves the digit field via expr_tuple_elem_tag.
		{"tuple-elem-shr", `function main(): i32 { var t: (u32, i32) = (2147483649, 1); return (t.0 >> 25) as i32; }`, 64},
		// ARRAY LITERAL element chained in a shift: `[big, …][0] >> 25`. ExprIndex
		// arm gained an ExprArray case.
		{"array-literal-shr", `function main(): i32 { return ([2147483649 as u32, 1 as u32][0] >> 25) as i32; }`, 64},
		// ARRAY SLICE element chained in a shift: `a[lo:hi][0] >> 25`. ExprIndex arm
		// gained an ExprSlice case (via expr_is_u32arr).
		{"array-slice-shr", `function main(): i32 { var a: u32[] = [2147483649 as u32, 1 as u32, 2 as u32]; return (a[0:2][0] >> 25) as i32; }`, 64},
		// STRUCT FIELD chained in a DIVISION: `p.n / 7` needs div_u (a numerator >=
		// 2^31 reads signed-negative). 4000000000 / 7 == 571428571; % 100 == 71.
		{"struct-field-div", `struct SU { n: u32 } function main(): i32 { var p: SU = SU { n: 4000000000 as u32 }; return ((p.n / (7 as u32)) % (100 as u32)) as i32; }`, 71},
		// STRUCT FIELD in an ordering compare: `p.n > 2e9` is true unsigned, false
		// signed (4e9 reads negative). Selects gt_u.
		{"struct-field-cmp", `struct SU { n: u32 } function main(): i32 { var p: SU = SU { n: 4000000000 as u32 }; if (p.n > (2000000000 as u32)) { return 11; } return 0; }`, 11},
		// TUPLE ELEMENT in an ordering compare: `t.0 > 2e9`.
		{"tuple-elem-cmp", `function main(): i32 { var t: (u32, i32) = (4000000000, 1); if (t.0 > (2000000000 as u32)) { return 12; } return 0; }`, 12},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runIR(t, tc.src); got != tc.expected {
				t.Errorf("u32-container wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
