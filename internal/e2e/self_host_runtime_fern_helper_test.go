package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #2649 — runtime helpers written in Fern instead of hand-written asm.
//
// __fern_i32_pow (the integer-power helper backing `n.pow(e)`) is the first
// runtime helper migrated from a hand-written asm string to a real Fern
// function (asmcore.rt_src_i32_pow), compiled through the normal function
// emitter. It therefore links as the ordinary user-function symbol
// `__fn___fern_i32_pow` and is invoked via the stack-call convention, not as a
// bare register-ABI `__fern_i32_pow`.
//
// The behavioural pow cases in TestSelfHostAsmRunX86_64 already prove the
// helper computes the right answer; they'd keep passing even if someone
// reverted to the hand-written asm. This test locks in the *migration* itself:
// the emitted symbol must be the Fern-compiled `__fn___fern_i32_pow` and the
// old hand-asm form (the bare `__fern_i32_pow:` label / its `.Lpow_loop`) must
// be gone. It shares the asm_run driver build with TestSelfHostAsmRunX86_64
// (same sources → same driver-bin cache key), so it adds no driver rebuild.
func TestSelfHostRuntimeHelperI32PowIsFern(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm.fern", "asm_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	const powSrc = "function main(): i32 { var n: i32 = 2; return n.pow(10); }"
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], driverBin)...)
	}
	cmd.Stdin = bytes.NewReader([]byte(powSrc))
	asm, err := cmd.Output()
	if err != nil {
		t.Fatalf("driver run: %v", err)
	}
	got := string(asm)

	// The migrated, Fern-compiled helper + its stack-call site must be present.
	for _, want := range []string{"__fn___fern_i32_pow:", "call __fn___fern_i32_pow"} {
		if !strings.Contains(got, want) {
			t.Errorf("emitted asm missing %q — __fern_i32_pow no longer compiled from Fern?\n", want)
		}
	}
	// The old hand-written asm form must be gone. (Match the bare label with a
	// trailing colon so it doesn't also match the `__fn___fern_i32_pow:` form.)
	for _, bad := range []string{"\n__fern_i32_pow:", ".Lpow_loop"} {
		if strings.Contains(got, bad) {
			t.Errorf("emitted asm still contains hand-written form %q — migration regressed", bad)
		}
	}
}
