package e2ecompiler

import (
	"regexp"
	"strings"
	"testing"
)

func TestSelfHostPollIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	prog := `function main(): i32 {
    let fds: i32[] = [];
    return poll(fds, 0);
}`
	want := interpExit(t, interpBin, prog)

	asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(prog), "-target", "arm64-linux")
	if len(asm) == 0 {
		t.Fatal("self-host arm64 compiler emitted 0 bytes")
	}
	if !strings.Contains(string(asm), "bl __fn___fern_poll") {
		t.Error("poll not dispatched on arm64 (poll(fds,0) did not lower to the __fn___fern_poll helper call)")
	}
	if !regexp.MustCompile(`\n    mov x[0-9]+, #73\n`).MatchString(string(asm)) {
		t.Error("the ppoll(2) number (73) was not baked into the Fern helper source")
	}
	if !strings.Contains(string(asm), "ldr x0, [sp], #16\n    ldr x8, [sp], #16\n    svc #0\n") {
		t.Error("__syscall5 did not emit the arm64 argument pop + number + svc sequence ppoll needs")
	}
	for _, reg := range []string{"x1", "x2", "x3", "x4"} {
		if !strings.Contains(string(asm), "mov "+reg+", ") && !strings.Contains(string(asm), "ldr "+reg+", [sp], #16") {
			t.Errorf("a __syscall5 argument never reaches %s", reg)
		}
	}
	bin := buildBinArm64(t, arm64gcc, dir, "poll_empty_arm64", string(asm))
	cmd := runArm64Bin(qemu, bin)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != want {
		t.Errorf("poll([],0) exited %d, want %d (interp oracle)", code, want)
	}
}
