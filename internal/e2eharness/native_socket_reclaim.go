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
	// A stream socket cannot connect to a broadcast address. Measure the
	// host errno: Darwin and Linux reject it with different error classes.
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
		prelude = "tcp_close(0);"
	}
	if operation == "socket" {
		return `function main(): i32 {
    ` + prelude + `
    var sockets: i32[] = [];
    var fd: i32 = tcp_listen(0);
    while (fd >= 0) { sockets = sockets.append(fd); fd = tcp_listen(0); }
    if (fd != -24 || sockets.len() == 0) { return 91; }
    if (tcp_listen(0) != -24) { return 92; }
    var i: i32 = 0;
    while (i < sockets.len()) {
        if (tcp_local_port(sockets[i]) <= 0) { return 93; }
        if (tcp_close(sockets[i]) != 0) { return 94; }
        i = i + 1;
    }
    return 0;
}`
	}
	errno, expr := 98, "tcp_listen(port)"
	if darwin {
		errno = 48
	}
	if operation == "connect" {
		errno, expr = connectErrno, "tcp_connect(-1, 1)"
	}
	return fmt.Sprintf(`function main(): i32 {
    var listener: i32 = tcp_listen(0);
    if (listener < 0) { return 90; }
    var port: i32 = tcp_local_port(listener);
    if (port <= 0) { return 91; }
    %s
    // The kernel must reuse this lowest available descriptor after failures.
    var before: i32 = tcp_listen(0);
    if (before < 0 || tcp_close(before) != 0) { return 92; }
    var i: i32 = 0;
    while (i < 32) {
        var failed: i32 = %s;
        if (failed != -%d) { return 93; }
        var after: i32 = tcp_listen(0);
        if (after != before) { return 94; }
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
