package e2eselfhost

import (
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
)

// udpSenderProgram is a one-shot datagram sender built from the self-hosted
// ARM64 emitter's udp_send primitive: socket(AF_INET, SOCK_DGRAM) → parse
// the dotted-quad host into sin_addr → sendto → close. It returns the byte
// count accepted (== payload length on success), which the test reads as
// the process exit code.
const udpSenderProgram = `function main(): i32 {
    return udp_send("127.0.0.1", %d, "%s");
}`

// TestSelfHostUdpSendArm64 exercises the self-hosted ARM64 emitter's
// udp_send primitive end-to-end: a Go UDP socket listens, the self-host
// emitter compiles a one-shot sender, it runs under qemu-aarch64 (the
// socket/sendto syscalls pass through to the host), and the test asserts
// the datagram arrives with the exact payload and the sender exits with
// the byte count. CI-gated; skips cleanly without the cross toolchain.
func TestSelfHostUdpSendArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	// This ran unskipped for the first time once udp_send acquired a backend at
	// all (#6917). It had been skipped under qemu on the reading that qemu-user's
	// socket emulation was at fault, with the cause recorded as not established —
	// the sender delivered nothing and exited 225 / 248 rather than 14.
	//
	// The cause was not the emulation. `udp_send` had no kind_tag case on either
	// register backend, so the op fell through to the emitter's comment fallback:
	// the call was dropped and main returned whatever was in the result register,
	// which is what those exit codes are. A datagram was never sent to time out
	// waiting for.
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	// Build the self-host arm64 emitter driver as an x86-64 host binary.
	prog, _, err := modload.Load(filepath.Join(dir, "asm_ir_run.fern"))
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	driverBin := buildBin(t, x86gcc, dir, "driver", asm)

	// Hold the receiving UDP socket open for the whole test so the
	// datagram is delivered (and to learn a free port).
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer conn.Close()
	port := conn.LocalAddr().(*net.UDPAddr).Port

	const payload = "hello-udp-fern" // 14 bytes
	senderSrc := fmt.Sprintf(udpSenderProgram, port, payload)
	senderAsm := runCapture(t, x86gcc, x86runner, driverBin, []byte(senderSrc), "-target", "arm64-linux")
	if len(senderAsm) == 0 {
		t.Fatal("self-host arm64 compiler emitted 0 bytes for the udp sender")
	}
	senderBin := buildBin(t, arm64gcc, dir, "udpsender", string(senderAsm))

	cmd := runArm64Bin(qemu, senderBin)
	var forceKilled bool
	wd := time.AfterFunc(30*time.Second, func() {
		forceKilled = true
		_ = cmd.Process.Kill()
	})
	if err := cmd.Start(); err != nil {
		wd.Stop()
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

	waitErr := cmd.Wait()
	wd.Stop()
	if forceKilled {
		t.Fatalf("udp sender had to be force-killed (hung): %v", waitErr)
	}
	// udp_send returns the bytes accepted == len(payload) on success.
	if code := cmd.ProcessState.ExitCode(); code != len(payload) {
		t.Errorf("udp sender exited %d, want %d (udp_send byte count)", code, len(payload))
	}
}
