package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #2649 — IR-path runtime helpers written in Fern.
//
// __fern_str_to_i32 is the first runtime helper hosted on the self-hosted IR
// path as a Fern function (asmcore.rt_src_str_to_i32, lowered through the IR
// pipeline by asm_ir.emit_ir_runtime_fern_fn) rather than the hand-written
// stack-arg wrapper that used to live in emit_ir_runtime. It links as the
// ordinary user-function symbol __fn___fern_str_to_i32, which the IR call site
// (op_call_direct("__fern_str_to_i32") → ir_helper_symbol) already targets.
//
// TestSelfHostAsmIRPath/str2i32-* already prove the IR-compiled helper computes
// correctly (incl. the roundtrip case, which feeds a freshly-allocated string
// in — exercising the borrowed-param path with no use-after-free). This test
// locks in the *migration*: the IR driver's emitted asm must define the
// Fern-compiled __fn___fern_str_to_i32 and must NOT contain the old hand-asm
// wrapper's local labels (.Lirs2i_*), so a silent revert to the wrapper fails.
func TestSelfHostRuntimeHelperStrToI32IsFernIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile(filepath.Join("../../examples/self_host", "asm_ir_run.fern"))
	if err != nil {
		t.Fatalf("read asm_ir_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_ir_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_ir_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "airun_s2i")

	const prog = `function main(): i32 { return str_to_i32("42"); }`
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], driverBin)...)
	}
	cmd.Stdin = bytes.NewReader([]byte(prog))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("driver run: %v", err)
	}
	got := string(out)

	if !strings.Contains(got, "__fn___fern_str_to_i32:") {
		t.Errorf("IR asm missing __fn___fern_str_to_i32: definition — helper no longer compiled from Fern on the IR path?")
	}
	if !strings.Contains(got, "call __fn___fern_str_to_i32") {
		t.Errorf("IR asm missing call __fn___fern_str_to_i32 — call site not resolving to the Fern helper")
	}
	if strings.Contains(got, ".Lirs2i_") {
		t.Errorf("IR asm still contains the hand-written wrapper's labels (.Lirs2i_*) — IR migration regressed")
	}
}
