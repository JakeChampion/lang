package e2e

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// udp_send on the native backends (#10278). The checker accepted it and only
// wasmbin implemented it, so on x86-64 and arm64 a program calling it stopped at
// the assembler on an undefined label.
//
// The datagram is asserted as well as the exit code: a byte count is a number a
// wrong helper could produce, a delivered payload is not. The host contract is
// the self-host's: a dotted-quad IPv4 literal, and -3 without opening a socket
// for anything else. (wasm answers -28 for a bad host.)
func TestX86_64UdpSend(t *testing.T) {
	_, runner := x86_64Tooling(t)
	start := func(bin string) *exec.Cmd { return runX86_64Bin(runner, bin) }
	for _, backend := range []string{"flat", "ssa"} {
		t.Run(backend, func(t *testing.T) { runNativeUdpSend(t, "x86-64-linux", backend, start) })
	}
}

func TestArm64UdpSend(t *testing.T) {
	_, qemu := arm64Tooling(t)
	start := func(bin string) *exec.Cmd { return runArm64Bin(qemu, bin) }
	for _, backend := range []string{"flat", "ssa"} {
		t.Run(backend, func(t *testing.T) { runNativeUdpSend(t, "arm64-linux", backend, start) })
	}
}

func runNativeUdpSend(t *testing.T, target, backend string, start func(bin string) *exec.Cmd) {
	fern := buildLangBinForInterp(t)
	stdlib, err := filepath.Abs("../stdlib")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer conn.Close()
	port := conn.LocalAddr().(*net.UDPAddr).Port

	build := func(name, src string) string {
		dir := t.TempDir()
		path := filepath.Join(dir, name+".fern")
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		bin := filepath.Join(dir, name)
		out, err := exec.Command(fern, "-target", target, "-backend", backend, "-o", bin, path, stdlib).CombinedOutput()
		if err != nil {
			t.Fatalf("compile %s: %v\n%s", name, err, out)
		}
		return bin
	}
	run := func(bin string) int {
		cmd := start(bin)
		if err := cmd.Run(); err != nil {
			if _, exited := err.(*exec.ExitError); !exited {
				t.Fatalf("run %s: %v", bin, err)
			}
		}
		return cmd.ProcessState.ExitCode()
	}

	const payload = "hello-udp-fern" // 14 bytes
	sender := build("udpsend", fmt.Sprintf(`function main(): i32 { return udp_send("127.0.0.1", %d, "%s"); }`, port, payload))
	if code := run(sender); code != len(payload) {
		t.Errorf("udp_send returned %d, want %d (the bytes sent)", code, len(payload))
	}
	buf := make([]byte, 2048)
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	n, _, rerr := conn.ReadFromUDP(buf)
	if rerr != nil {
		t.Fatalf("did not receive the datagram: %v", rerr)
	}
	if got := string(buf[:n]); got != payload {
		t.Errorf("datagram payload = %q, want %q", got, payload)
	}

	rejected := []string{"localhost", "1.2.3.x", "1.2.3.4.5", "1.2.3.999", "1..2.3", "1.2.3.", ".1.2.3"}
	src := "function main(): i32 {\n"
	for i, host := range rejected {
		src += fmt.Sprintf("    if (udp_send(%q, %d, \"x\") != 0 - 3) { return %d; }\n", host, port, i+1)
	}
	src += "    return 0;\n}\n"
	if code := run(build("udphosts", src)); code != 0 {
		t.Errorf("a rejected host did not answer -3: exit %d", code)
	}
	_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	if n, _, rerr := conn.ReadFromUDP(buf); rerr == nil {
		t.Errorf("a rejected host sent a datagram: %q", string(buf[:n]))
	}
}
