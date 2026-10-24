package e2ecompiler

import (
	"strings"
	"testing"
)

func TestSelfHostTcpClientIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	port := startTcpPongServer(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	src := []byte(tcpClientIRProgram(port) + "\n")
	asm := runCapture(t, x86gcc, x86runner, driverBin, src, "-target", "arm64-linux")
	if len(asm) == 0 {
		t.Fatal("self-host arm64 compiler emitted 0 bytes")
	}
	if strings.Contains(string(asm), "__fern_tcp_pollable") {
		t.Fatal("native pollable identity still calls a runtime helper")
	}
	for _, sym := range []string{"bl __fn___fern_tcp_connect", "bl __fn___fern_tcp_send", "bl __fn___fern_tcp_recv", "bl __fn___fern_tcp_close"} {
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
