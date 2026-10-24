package e2e

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const tcpLocalPortRoundTrip = `function main(): i32 {
    let l: i32 = tcp_listen(0);
    if (l < 0) { return 90; }
    let port: i32 = tcp_local_port(l);
    if (port <= 0) { return 91; }
    if (port > 65535) { return 92; }
    let c: i32 = tcp_connect_with([127u8, 0u8, 0u8, 1u8], port, false);
    if (c < 0) { return 93; }
    let s: i32 = tcp_accept(l);
    if (s < 0) { return 94; }
    // The accepted end is bound to the same listening port.
    if (tcp_local_port(s) != port) { return 95; }
    let sent: i32 = tcp_send_bytes(c, [112u8, 105u8, 110u8, 103u8, 33u8]);
    let got: u8[] = __alloc_u8(16);
    if (sent != 5) { return 96; }
    if (tcp_recv_into(s, got) != 5) { return 97; }
    if (tcp_close(c) + tcp_close(s) + tcp_close(l) != 0) { return 98; }
    return 42;
}`

func TestTcpLocalPortRoundTrip(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "roundtrip.fern")
	if err := os.WriteFile(srcPath, []byte(tcpLocalPortRoundTrip), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	cases := []struct {
		name   string
		target string
	}{
		{"x86-64", "x86-64-linux"},
		{"arm64", "arm64-linux"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var run func(string) *exec.Cmd
			if c.target == "arm64-linux" {
				qemu := arm64QemuOrEmpty(t)
				run = func(out string) *exec.Cmd { return runArm64Bin(qemu, out) }
			} else {
				qemu := x86QemuOrEmpty(t)
				run = func(out string) *exec.Cmd { return runX86Bin(qemu, out) }
			}

			out := filepath.Join(dir, c.name+".bin")
			args := []string{"-target", c.target}
			args = append(args, "-o", out, srcPath)
			if o, err := exec.Command(bin, args...).CombinedOutput(); err != nil {
				t.Fatalf("build failed: %v\n%s", err, o)
			}
			code := 0
			if err := run(out).Run(); err != nil {
				var ee *exec.ExitError
				if !errors.As(err, &ee) {
					t.Fatalf("run %s: %v", out, err)
				}
				code = ee.ExitCode()
			}
			if code != 42 {
				t.Errorf("exit = %d, want 42 (listen on 0, read the port back, connect to it)", code)
			}
		})
	}
}

func TestTcpLocalPortInterp(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	src := `function main(): i32 {
    let l: i32 = tcp_listen(0);
    if (l < 0) { return 90; }
    let port: i32 = tcp_local_port(l);
    if (port <= 0) { return 91; }
    if (port > 65535) { return 92; }
    if (tcp_local_port(4242) >= 0) { return 93; }
    tcp_close(l);
    return 42;
}`
	srcPath := filepath.Join(dir, "interp.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", srcPath)
	code := 0
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("run: %v", err)
		}
		code = ee.ExitCode()
	}
	if code != 42 {
		t.Errorf("exit = %d, want 42 (interp reports the listener's port)", code)
	}
}

func TestTcpLocalPortErrno(t *testing.T) {
	qemu := x86QemuOrEmpty(t)
	bin := buildFernCLI(t)
	dir := t.TempDir()
	src := `function main(): i32 {
    let p: i32 = tcp_local_port(0);
    if (p >= 0) { return 90; }
    return 42;
}`
	srcPath := filepath.Join(dir, "errno.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	{
		t.Run("x86-64", func(t *testing.T) {
			out := filepath.Join(dir, "errno.bin")
			args := []string{"-target", "x86-64-linux", "-o", out, srcPath}
			if o, err := exec.Command(bin, args...).CombinedOutput(); err != nil {
				t.Fatalf("build failed: %v\n%s", err, o)
			}
			code := 0
			run := runX86Bin(qemu, out)
			run.Stdin = nil
			if err := run.Run(); err != nil {
				var ee *exec.ExitError
				if !errors.As(err, &ee) {
					t.Fatalf("run: %v", err)
				}
				code = ee.ExitCode()
			}
			if code != 42 {
				t.Errorf("exit = %d, want 42 (getsockname on a non-socket is -errno)", code)
			}
		})
	}
}
