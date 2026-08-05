package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostArm64GasWriteback pins #6083: a `!` pre-index writeback on a
// mnemonic the assembler has no writeback encoder for must be REFUSED, not
// silently stripped.
//
// `__fern_eprint_str` claimed its newline scratch with
// `strb w1, [sp, #-16]!` and released it with `add sp, sp, #16`. The assembler
// implements pre-index writeback for ldr/str/ldp/stp — the operand stack's whole
// idiom — but not for the byte / half-word forms, and arm64_gas_mem drops the
// `!` because it sits outside the brackets. So sp was never decremented and the
// paired release RAISED it by 16: every eprint returned on a frame 16 bytes
// adrift. eprint's own bytes were correct, so the corruption surfaced only at
// the next call, which is why `audit_io_builtins` printed "Hi!" and "to-stderr"
// and then died somewhere else entirely.
//
// The emitter now uses __fern_putchar's explicit `sub sp` shape, so nothing
// emits this form any more; this test guards the assembler side, which is what
// keeps the next such helper from failing the same silent way.
func TestSelfHostArm64GasWriteback(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arm64 gas writeback e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	source := arm64NativeSrc(t) + "\n" + arm64GasWritebackSelfTestMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the arm64 gas writeback self-test")
	}
	watPath := filepath.Join(dir, "arm64_gas_writeback_selftest.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("arm64 gas writeback self-test failed at check %d", code)
	}
}

const arm64GasWritebackSelfTestMain = `
function main(): i32 {
    // The exact form __fern_eprint_str used: no byte-store writeback encoder,
    // so it must land in p.unknown rather than encode as a plain offset store.
    var p1: Arm64GasProg = arm64_gas_program("strb w1, [sp, #-16]!\nret\n");
    if (p1.unknown.len() == 0) { return 1; }

    // The load twin, and the half-word pair.
    var p2: Arm64GasProg = arm64_gas_program("ldrb w1, [sp, #-16]!\nret\n");
    if (p2.unknown.len() == 0) { return 2; }
    var p3: Arm64GasProg = arm64_gas_program("strh w1, [sp, #-16]!\nret\n");
    if (p3.unknown.len() == 0) { return 3; }

    // ldur/stur have only an immediate-offset encoder too.
    var p4: Arm64GasProg = arm64_gas_program("stur x1, [sp, #-16]!\nret\n");
    if (p4.unknown.len() == 0) { return 4; }

    // The forms that DO implement writeback must stay accepted — this is the
    // operand stack's own idiom, so a blanket refusal would reject every
    // function the emitter produces.
    var p5: Arm64GasProg = arm64_gas_program("str x0, [sp, #-16]!\nret\n");
    if (p5.unknown.len() != 0) { return 5; }
    var p6: Arm64GasProg = arm64_gas_program("stp x29, x30, [sp, #-16]!\nret\n");
    if (p6.unknown.len() != 0) { return 6; }
    var p7: Arm64GasProg = arm64_gas_program("ldr x0, [sp], #16\nret\n");
    if (p7.unknown.len() != 0) { return 7; }

    // A byte store with NO writeback is still fine — the refusal keys on the
    // marker, not on the mnemonic.
    var p8: Arm64GasProg = arm64_gas_program("strb w1, [sp]\nret\n");
    if (p8.unknown.len() != 0) { return 8; }
    var p9: Arm64GasProg = arm64_gas_program("ldrb w0, [x1, x2]\nret\n");
    if (p9.unknown.len() != 0) { return 9; }

    return 0;
}
`
