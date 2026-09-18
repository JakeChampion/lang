package e2e

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// tcp_local_port(sock) answers the port a socket is bound to. It exists for
// tcp_listen(0): the kernel picks the port there and, until this builtin, no
// part of the language could say which one — so a program that wanted a free
// port had to name one and hope, and the tests around it had to probe the host
// for a port, hand it to the guest, and retry the bind when something else took
// it in between.
//
// The round-trip below runs entirely inside the guest, which is the point. It
// listens on 0, asks for the port, and connects to it over loopback; connect(2)
// completes in the kernel's backlog before accept(2) runs, so one process plays
// both ends with no host coordination and nothing to race. A wrong port cannot
// pass: connecting to one nothing is listening on is refused.
//
// Exit codes localise a failed step — 9x is setup, 42 is every step holding.
const tcpLocalPortRoundTrip = `function main(): i32 {
    var l: i32 = tcp_listen(0);
    if (l < 0) { return 90; }
    var port: i32 = tcp_local_port(l);
    if (port <= 0) { return 91; }
    if (port > 65535) { return 92; }
    var loopback: i32 = 127 + (1 << 24);
    var c: i32 = tcp_connect(loopback, port);
    if (c < 0) { return 93; }
    var s: i32 = tcp_accept(l);
    if (s < 0) { return 94; }
    // The accepted end is bound to the same listening port.
    if (tcp_local_port(s) != port) { return 95; }
    var sent: i32 = tcp_send(c, "ping!");
    var got: u8[] = tcp_recv(s, 16);
    if (sent != 5) { return 96; }
    if (got.len() != 5) { return 97; }
    if (tcp_close(c) + tcp_close(s) + tcp_close(l) != 0) { return 98; }
    return 42;
}`

// TestTcpLocalPortRoundTrip runs the round-trip on every native backend: the
// two ISAs times the flat and SSA emitters, which each write their own
// getsockname helper.
func TestTcpLocalPortRoundTrip(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "roundtrip.fern")
	if err := os.WriteFile(srcPath, []byte(tcpLocalPortRoundTrip), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	cases := []struct {
		name    string
		target  string
		backend string
	}{
		{"x86-64_flat", "x86-64-linux", ""},
		{"x86-64_ssa", "x86-64-linux", "ssa"},
		{"arm64_flat", "arm64-linux", ""},
		{"arm64_ssa", "arm64-linux", "ssa"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var run func(string) *exec.Cmd
			if c.target == "arm64-linux" {
				qemu := arm64QemuOrEmpty(t)
				run = func(out string) *exec.Cmd { return runArm64Bin(qemu, out) }
			} else {
				if qemu := x86QemuOrEmpty(t); qemu != "" {
					t.Skip("tcp_local_port round-trip runs host-native only (loopback under qemu-user)")
				}
				run = func(out string) *exec.Cmd { return exec.Command(out) }
			}

			out := filepath.Join(dir, c.name+".bin")
			args := []string{"-target", c.target}
			if c.backend != "" {
				args = append(args, "-backend", c.backend)
			}
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

// TestTcpLocalPortInterp pins the same answer from the interpreter, whose
// socket handles are indices into Go maps rather than fds — so it has to reach
// for net.Listener.Addr() where the AOT backends call getsockname(2). Without
// this the interp could return its handle id and nobody would notice.
func TestTcpLocalPortInterp(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	// The interp has no tcp_connect, so it gets the half of the contract it
	// can run: an ephemeral listener reports a plausible port, and a handle
	// that names no socket reports failure rather than a stale number.
	src := `function main(): i32 {
    var l: i32 = tcp_listen(0);
    if (l < 0) { return 90; }
    var port: i32 = tcp_local_port(l);
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

// TestTcpLocalPortErrno pins the failure arm on both x86-64 emitters: a
// descriptor that is not a socket must come back as a negative errno, not as a
// port read out of whatever the sockaddr buffer happened to hold.
//
// The descriptor is fd 0, and what makes that a non-socket is leaving
// cmd.Stdin nil: os/exec then hands the child the null device. Do not wire this
// to the test process's own stdin — a socket there (some CI runners and agent
// shells do exactly that) makes getsockname SUCCEED and report port 0, and the
// case passes or fails on the harness rather than on the emitter.
func TestTcpLocalPortErrno(t *testing.T) {
	if qemu := x86QemuOrEmpty(t); qemu != "" {
		t.Skip("tcp_local_port errno test runs host-native only")
	}
	bin := buildFernCLI(t)
	dir := t.TempDir()
	src := `function main(): i32 {
    var p: i32 = tcp_local_port(0);
    if (p >= 0) { return 90; }
    return 42;
}`
	srcPath := filepath.Join(dir, "errno.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	for _, backend := range []string{"", "ssa"} {
		name := "flat"
		if backend != "" {
			name = backend
		}
		t.Run(name, func(t *testing.T) {
			out := filepath.Join(dir, "errno-"+name+".bin")
			args := []string{"-target", "x86-64-linux"}
			if backend != "" {
				args = append(args, "-backend", backend)
			}
			args = append(args, "-o", out, srcPath)
			if o, err := exec.Command(bin, args...).CombinedOutput(); err != nil {
				t.Fatalf("build failed: %v\n%s", err, o)
			}
			code := 0
			run := exec.Command(out)
			run.Stdin = nil // the null device; see the comment above
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
