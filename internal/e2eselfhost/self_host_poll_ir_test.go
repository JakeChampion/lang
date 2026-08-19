package e2eselfhost

import (
	"os/exec"
	"strings"
	"testing"
)

// TestSelfHostPollIRX86_64 is the first slice of putting async on the
// self-hosted compiler's IR path (docs/ASYNC-SELFHOST-IR.md): the `poll`
// readiness builtin now lowers to a dedicated IR op (`op_poll`) that the
// self-host x86-64 IR backend emits as a call into `__fn___fern_poll` (poll(2)
// over the fd set), reading the SELF-HOST array layout (len at [ptr+0],
// element i at [ptr+(i+1)*8]). Because it's a real op — not a
// `call_direct` to an unknown `poll` symbol — a `poll`-using module is
// now IR-ELIGIBLE rather than bailing (the AST emitter it used to fall to
// can't emit `poll` at all).
//
// The case polls an EMPTY fd set, which returns -1 without a syscall —
// deterministic, and enough to pin (1) the module routes the "ir" path
// and (2) it runs to the interp oracle's value. The syscall/marshalling
// body mirrors the native `__fern_poll` (separately tested; the self-host one
// is compiled Fern since #2649), adapted to
// the self-host array ABI. Real-fd polling on the self-host arrives with
// the timer/socket builtins (later slices).
func TestSelfHostPollIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	// poll([], 0) → -1 (no fd ready); -1 truncates to exit code 255.
	main := `function main(): i32 {
    var fds: i32[] = [];
    return poll(fds, 0);
}`
	src := []byte(main + "\n")
	want := interpExit(t, interpBin, string(src)) // interp builtinPoll → -1 → 255

	path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
	if path != "ir" {
		t.Fatalf("poll([],0) routed through %q path, want \"ir\"", path)
	}
	asm := runCapture(t, gcc, runner, driverBin, src)
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes")
	}
	progBin := buildBin(t, gcc, dir, "poll_empty", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != want {
		t.Errorf("poll([],0) exited %d, want %d (interp oracle)", code, want)
	}
}
