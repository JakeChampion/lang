package e2eharness

import (
	"context"
	"fmt"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func NativeSocketConnectError(t *testing.T) int {
	t.Helper()
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	syscall.CloseOnExec(fd)
	defer syscall.Close(fd)
	err = syscall.Connect(fd, &syscall.SockaddrInet4{Addr: [4]byte{255, 255, 255, 255}, Port: 1})
	errno, ok := err.(syscall.Errno)
	if !ok || errno == 0 {
		t.Fatalf("broadcast TCP connect must fail: %v", err)
	}
	return int(errno)
}

func NativeSocketFailureProbe(operation string, darwin, zero bool, connectErrno int) string {
	prelude := ""
	if zero {
		prelude = "unsafe { __fd_close(0); }"
	}
	if operation == "socket" {
		return `function main(): i32 {
    ` + prelude + `
    let sockets: Socket[] = [];
    let fd: Socket = tcp_listen(0);
    while (fd.fd >= 0) { sockets = sockets.append(fd); fd = tcp_listen(0); }
    if (fd.fd != -24 || sockets.len() == 0) { return 91; }
    let refused: Socket = tcp_listen(0);
    if (refused.fd != -24) { return 92; }
    for s in sockets {
        if (tcp_local_port(s) <= 0) { return 93; }
        if (tcp_close(s) != 0) { return 94; }
    }
    return 0;
}`
	}
	errno, expr := 98, "tcp_listen(port)"
	if darwin {
		errno = 48
	}
	if operation == "connect" {
		errno, expr = connectErrno, "tcp_connect_with([255u8, 255u8, 255u8, 255u8], 1, false)"
	}
	return fmt.Sprintf(`function main(): i32 {
    let listener: Socket = tcp_listen(0);
    if (listener.fd < 0) { return 90; }
    let port: i32 = tcp_local_port(listener);
    if (port <= 0) { return 91; }
    %s
    // The kernel must reuse this lowest available descriptor after failures.
    let before: Socket = tcp_listen(0);
    let lowest: i32 = before.fd;
    if (lowest < 0 || tcp_close(before) != 0) { return 92; }
    let i: i32 = 0;
    while (i < 32) {
        let failed: Socket = %s;
        if (failed.fd != -%d) { return 93; }
        let after: Socket = tcp_listen(0);
        if (after.fd != lowest) { return 94; }
        if (tcp_close(after) != 0) { return 95; }
        i = i + 1;
    }
    if (tcp_close(listener) != 0) { return 96; }
    return 0;
}`, prelude, expr, errno)
}

func CheckNativeSocketFailure(t *testing.T, command *exec.Cmd) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	args := append([]string{"-c", `ulimit -n 64; exec "$@"`, "socket-probe"}, command.Args...)
	cmd := exec.CommandContext(ctx, "sh", args...)
	cmd.Env, cmd.Dir = command.Env, command.Dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("socket failure must preserve errno and descriptors: %v\n%s", err, out)
	}
}
