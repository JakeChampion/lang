package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
)

// TestSelfHostI32WrapIR proves the self-hosted x86-64 IR path computes plain
// i32 arithmetic with 32-bit (2^32) wrapping — matching the native compiler,
// which keeps i32 values in 32-bit registers. The register backends keep
// operand-stack slots in 64-bit registers, so an i32 op (add / sub / mul / shl)
// whose result can exceed 32 bits must be truncated + sign-extended back to the
// i32 range; this is the signed sibling of op_u32_wrap, driven from irlower's
// binary-arithmetic lowering. Without it `2147483647 + 1` stayed a wide
// positive 2147483648 instead of wrapping to INT_MIN, so an overflow check read
// differently than native and the interp (#3581).
//
// The oracle is the NATIVE compiler (compileAndRunX86_64), NOT the AST path:
// the legacy self-host AST backend has the SAME i32-non-wrap behaviour, so the
// IR path now intentionally diverges from (is more correct than) it. For an
// overflow program the AST path's answer differs, so IR == native also proves
// the program took the IR path rather than the AST fallback.
func TestSelfHostI32WrapIR(t *testing.T) {
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
	prog, _, err := modload.Load(filepath.Join(dir, "asm_ir_run.fern"))
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	driverAsm := filepath.Join(dir, "driver.s")
	driverBin := filepath.Join(dir, "driver")
	if err := os.WriteFile(driverAsm, []byte(asm), 0o644); err != nil {
		t.Fatalf("write driver asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", driverAsm, "-o", driverBin).CombinedOutput(); err != nil {
		t.Fatalf("driver gcc: %v\n%s", err, out)
	}

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
	}{
		// The canonical repro: i32 max + 1 wraps to INT_MIN, so the < 0 check
		// reads true (exit 1). A runtime `x + 1` (x a local) is NOT constant-
		// folded, so only the runtime wrap closes the gap.
		{"add-overflow-neg", `function main(): i32 { var x = 2147483647; var y = x + 1; if (y < 0) { return 1; } return 0; }`},
		// Overflowed value feeding a later op: (INT_MIN) / 1000000 % 100 = -47,
		// which the exit-code byte reports as 209 (256-47).
		{"add-overflow-divrem", `function main(): i32 { var x = 2147483647; var y = (x + 1) / 1000000; return y % 100; }`},
		// i32 multiply overflow: 100000 * 100000 = 10^10 wraps into the i32 range.
		// The low byte witnesses the wrap (the wide value's low byte differs).
		{"mul-overflow-byte", `function main(): i32 { var a = 100000; var b = 100000; var c = a * b; return c & 255; }`},
		// Multiply overflow whose wrapped result is negative.
		{"mul-overflow-neg", `function main(): i32 { var a = 100000; var b = 100000; var c = a * b; if (c < 0) { return 0; } return 7; }`},
		// Left shift past bit 31: 1 << 31 = INT_MIN (negative) once wrapped.
		{"shl-overflow-neg", `function main(): i32 { var a = 1; var s = a << 31; if (s < 0) { return 1; } return 0; }`},
		// Subtraction underflow: INT_MIN - 1 wraps to INT_MAX (positive).
		{"sub-underflow-pos", `function main(): i32 { var lo = 0 - 2147483647; var x = lo - 2; if (x > 0) { return 1; } return 0; }`},
		// Chained adds that each overflow — the wrap must apply per op so the
		// running value stays in range, matching native's 32-bit accumulation.
		{"chained-add", `function main(): i32 { var a = 2000000000; var s = a + a + a; return (s >> 24) & 255; }`},
		// No-overflow sanity: ordinary arithmetic is unaffected by the wrap.
		{"no-overflow", `function main(): i32 { var a = 12; var b = 30; return a + b; }`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, want := compileAndRunX86_64(t, tc.src) // native = the correct oracle
			if got := emitAndRunIR(t, tc.src); got != want {
				t.Errorf("self-host IR %q: exit = %d, want %d (native)", tc.name, got, want)
			}
		})
	}
}
