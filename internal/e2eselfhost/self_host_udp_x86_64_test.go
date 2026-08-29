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

// TestSelfHostUdpSendRejectsNonLiteralHostX86_64 pins the host contract on
// the self-host native runtime: udp_send takes a dotted-quad IPv4 literal,
// and anything else returns -3 (`invalid-argument`) without opening a
// socket.
//
// Until #7740 the parser in asmcore's rt_src_udp_send accumulated every
// non-'.' byte as (c - 48), so a hostname reported success and sent the
// datagram to whatever address its letters produced. A host with more than
// four groups also stored past the 16-byte sockaddr scratch.
//
// One program tries every rejected shape and returns which one was
// accepted, so exit 0 means all were refused; the listener then confirms
// nothing reached the wire.
func TestSelfHostUdpSendRejectsNonLiteralHostX86_64(t *testing.T) {
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

	rejected := []string{
		"localhost", // a hostname: no dots at all
		"1.2.3.x",   // a non-digit inside a group
		"1.2.3.4.5", // five groups — the store past the sockaddr
		"1.2.3.999", // an octet past 255
		"1..2.3",    // an empty group
		"1.2.3.",    // a trailing dot
		".1.2.3",    // a leading dot
	}
	src := "function main(): i32 {\n"
	for i, host := range rejected {
		src += fmt.Sprintf("    if (udp_send(%q, %d, \"x\") >= 0) { return %d; }\n", host, port, i+1)
	}
	src += "    return 0;\n}\n"

	asm := runCapture(t, gcc, runner, driverBin, []byte(src))
	if len(asm) == 0 {
		t.Fatal("self-host x86-64 compiler emitted 0 bytes for the host-validation program")
	}
	senderBin := buildBin(t, gcc, dir, "udphosts", string(asm))

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(senderBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], senderBin)...)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sender: %v", err)
	}
	_ = cmd.Wait()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("udp_send accepted %q (exit %d); every host in the list must be rejected",
			rejected[code-1], code)
	}

	// Nothing may have reached the wire: each rejected call is refused
	// before socket(2).
	_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 2048)
	if n, _, rerr := conn.ReadFromUDP(buf); rerr == nil {
		t.Errorf("a rejected host sent a datagram: %q", string(buf[:n]))
	}
}
