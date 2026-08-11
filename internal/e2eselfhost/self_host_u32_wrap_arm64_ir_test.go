package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostU32WrapArm64IR is the arm64 sibling of TestSelfHostU32WrapIR:
// it proves the arm64 stack-IR backend (asm_arm64_ir.fern) wraps u32 arithmetic
// at 2^32 (mask via `mov w0, w0`, logical `>>` via `lsr`), so std/crypto's
// SHA-256 is correct — the bug #2861 tracked. The oracle is the NATIVE arm64
// compiler (compileAndRunArm64), not the legacy arm64 AST path (which shares
// the u32-overflow bug); for an overflow program IR == native therefore also
// proves the program took the IR path. CI-gated arm64 (qemu).
//
// Reuses shaCoreSrc from self_host_u32_wrap_ir_test.go.
func TestSelfHostU32WrapArm64IR(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern",
		"asm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	emitAndRunIR := func(t *testing.T, src string) int {
		t.Helper()
		var cmd *exec.Cmd
		if len(x86runner) == 0 {
			cmd = exec.Command(driverBin, "-target", "arm64-linux", "-ir")
		} else {
			cmd = exec.Command(x86runner[0], append(append(append([]string{}, x86runner[1:]...), driverBin), "-target", "arm64-linux", "-ir")...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		emitted, err := cmd.Output()
		if err != nil || len(emitted) == 0 {
			t.Fatalf("driver failed for %q: %v", src, err)
		}
		bin := buildBinArm64(t, arm64gcc, dir, "ir_inner", string(emitted))
		inner := runArm64Bin(qemu, bin)
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
		{"add-wrap", `function main(): i32 { var x: u32 = 0x80000000; var s: u32 = x + x; return ((s >> 28) & 255) as i32; }`},
		{"add5-wrap", `function main(): i32 { var a: u32 = 0xffffffff; var s: u32 = a + a + a + a + a; return ((s >> 24) & 255) as i32; }`},
		{"shl-wrap", `function main(): i32 { var a: u32 = 0xff; var s: u32 = a << 28; return ((s >> 24) & 255) as i32; }`},
		{"shr-logical", `function main(): i32 { var a: u32 = 0x80000000; return ((a >> 24) & 255) as i32; }`},
		{"rotr-inline", `function __rotr(x: u32, n: u32): u32 { return (x >> n) | (x << (32 - n)); } function main(): i32 { var x: u32 = 0x7da86405; var r: u32 = __rotr(x, 17) ^ __rotr(x, 19) ^ (x >> 10); return ((r >> 24) & 255) as i32; }`},
		{"sha256-abc-b0", shaCoreSrc + `function main(): i32 { var d: u8[] = __sha256_core(__str_to_bytes("abc")); return d[0] as i32; }`},
		{"sha256-abc-b31", shaCoreSrc + `function main(): i32 { var d: u8[] = __sha256_core(__str_to_bytes("abc")); return d[31] as i32; }`},
		{"sha256-empty-b0", shaCoreSrc + `function main(): i32 { var d: u8[] = __sha256_core(__str_to_bytes("")); return d[0] as i32; }`},
		{"alloc-u8", `function main(): i32 { var m: u8[] = __alloc_u8(3); m = m.with(0, 65); m = m.with(2, 67); return (m[0] as i32) + (m[2] as i32); }`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, want := compileAndRunArm64(t, tc.src) // native arm64 = correct oracle
			if got := emitAndRunIR(t, tc.src); got != want {
				t.Errorf("self-host arm64 IR %q: exit = %d, want %d (native)", tc.name, got, want)
			}
		})
	}
}
