package e2eselfhost

import (
	"fmt"
	"net"
	"os/exec"
	"testing"
	"time"
)

// TestSelfHostUdpSendIRX86_64 is the x86-64 half of the udp_send coverage, and
// the leg that runs without a cross toolchain.
//
// The op had no kind_tag case on either register backend until #6917, so it fell
// through the emitter's dispatch to the binary-op fallback: the call was dropped
// and main returned whatever was in the result register. Nothing pinned that on
// x86-64 — the only native coverage was the arm64 test, which needs the cross
// toolchain and was itself skipped under qemu.
//
// The datagram is asserted, not just the exit code: a return of len(payload) is
// what a dropped call could coincidentally produce, but a delivered payload
// cannot be faked.
func TestSelfHostUdpSendIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer conn.Close()
	port := conn.LocalAddr().(*net.UDPAddr).Port

	const payload = "hello-udp-fern" // 14 bytes
	src := fmt.Sprintf(`function main(): i32 { return udp_send("127.0.0.1", %d, "%s"); }`, port, payload)

	asm := runCapture(t, gcc, runner, driverBin, []byte(src))
	if len(asm) == 0 {
		t.Fatal("self-host x86-64 compiler emitted 0 bytes for the udp sender")
	}
	senderBin := buildBin(t, gcc, dir, "udpsender", string(asm))

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(senderBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], senderBin)...)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sender: %v", err)
	}

	buf := make([]byte, 2048)
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	n, _, rerr := conn.ReadFromUDP(buf)
	if rerr != nil {
		t.Errorf("did not receive datagram: %v", rerr)
	} else if got := string(buf[:n]); got != payload {
		t.Errorf("datagram payload = %q, want %q", got, payload)
	}

	// Wait reports a non-zero exit as an error, and a non-zero exit is the
	// SUCCESS condition here, so the exit code is what gets asserted.
	_ = cmd.Wait()
	// udp_send returns the bytes accepted == len(payload) on success.
	if code := cmd.ProcessState.ExitCode(); code != len(payload) {
		t.Errorf("udp sender exited %d, want %d (udp_send byte count)", code, len(payload))
	}
}
