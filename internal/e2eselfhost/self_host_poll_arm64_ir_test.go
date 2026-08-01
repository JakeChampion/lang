package e2eselfhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostPollIRArm64 is the arm64 half of slice 2 of putting async on the
// self-hosted compiler's IR path (docs/ASYNC-SELFHOST-IR.md): the `poll`
// readiness builtin now lowers to the dedicated IR op (`op_poll`) on the
// self-host ARM64 IR backend too, emitting `bl __fern_poll` — the ppoll(2)
// (syscall #73; arm64 has no bare `poll`) mirror of the x86-64 helper, reading
// the SELF-HOST array layout (nfds at [ptr+0], element i at [ptr+(i+1)*8]).
// Because it's a real op — not a `call_direct` to an unknown `poll` symbol — a
// `poll`-using module is IR-ELIGIBLE on arm64 rather than falling back to the
// AST emitter (which can't emit `poll`).
//
// The case polls an EMPTY fd set, which returns -1 without a syscall —
// deterministic — pinning that the helper is dispatched and runs to the interp
// oracle's value under qemu. An asm-content check confirms the owned dispatch
// (`bl __fern_poll`) and the real ppoll body (`mov x8, #73`) are emitted.
func TestSelfHostPollIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	// poll([], 0) → -1 (no fd ready); -1 truncates to exit code 255.
	prog := `function main(): i32 {
    var fds: i32[] = [];
    return poll(fds, 0);
}`
	want := interpExit(t, interpBin, prog) // interp builtinPoll → -1 → 255

	asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(prog), "-target", "arm64")
	if len(asm) == 0 {
		t.Fatal("self-host arm64 compiler emitted 0 bytes")
	}
	if !strings.Contains(string(asm), "bl __fern_poll") {
		t.Error("poll not dispatched on arm64 (poll(fds,0) did not lower to the __fern_poll helper call)")
	}
	if !strings.Contains(string(asm), "mov x8, #73") {
		t.Error("real __fern_poll ppoll(2) body (mov x8, #73) not found in arm64 asm")
	}
	bin := buildBinArm64(t, arm64gcc, dir, "poll_empty_arm64", string(asm))
	cmd := runArm64Bin(qemu, bin)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != want {
		t.Errorf("poll([],0) exited %d, want %d (interp oracle)", code, want)
	}
}
