package e2eselfhost

import (
	"strings"
	"testing"
)

// TestSelfHostTcpClientIRArm64 is the arm64 half of slice 4
// (docs/ASYNC-SELFHOST-IR.md): the tcp_* client family lowers to dedicated IR
// ops on the self-host arm64 IR backend, emitting `bl __fern_tcp_*`. recv/send/
// close already had arm64 runtime bodies (server side); this adds connect +
// pollable (socket #198 / connect #203 / identity) and routes all five via the
// IR. A tcp module is IR-eligible rather than falling back to the AST emitter.
//
// Same loopback round-trip as the x86-64 sibling: the qemu'd client connects to
// the host Go server (qemu user-mode forwards the socket syscalls to the host),
// sends "ping", recvs the 6-byte reply, and exits 6. An asm-content check
// confirms the client ops lowered through the IR.
func TestSelfHostTcpClientIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	port := startTcpPongServer(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	src := []byte(tcpClientIRProgram(port) + "\n")
	asm := runCapture(t, x86gcc, x86runner, driverBin, src, "-target", "arm64")
	if len(asm) == 0 {
		t.Fatal("self-host arm64 compiler emitted 0 bytes")
	}
	// Every tcp op but pollable is migrated (#2649) and so carries the
	// stack-ABI __fn_ prefix. pollable issues no syscall at all — it is the
	// identity on a native fd — so there is nothing to move.
	for _, sym := range []string{"bl __fn___fern_tcp_connect", "bl __fn___fern_tcp_send", "bl __fn___fern_tcp_recv", "bl __fn___fern_tcp_close", "bl __fern_tcp_pollable"} {
		if !strings.Contains(string(asm), sym) {
			t.Errorf("emitted arm64 asm missing %q (tcp op did not lower through the IR path)", sym)
		}
	}
	bin := buildBinArm64(t, arm64gcc, dir, "tcp_client_arm64", string(asm))
	cmd := runArm64Bin(qemu, bin)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 6 {
		t.Errorf("tcp round-trip exited %d, want 6 (len of \"pong!!\")", code)
	}
}
