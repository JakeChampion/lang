package e2eharness

import (
	"context"
	"fmt"
	"io"
	"net"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// NativeSocketSendProbe checks accepted-byte counts and errno after shutdown.
func NativeSocketSendProbe(payload string, shut bool) string {
	result, rounds := len(payload), 1
	if shut {
		result, rounds = -int(syscall.EPIPE), 32
	}
	return fmt.Sprintf(`function main(): i32 {
    var sig: i32 = %d;
    signal_default(sig);
    if (signal_disposition(sig) != 0) { return 92; }
    var data: string = %q;
    if (tcp_send(-1, data) != -%d) { return 90; }
    var i: i32 = 0;
    while (i < %d) {
        if (tcp_send(3, data) != %d) { return 91; }
        i = i + 1;
    }
    if (signal_disposition(sig) != 0) { return 93; }
    return 0;
}`, int(syscall.SIGPIPE), payload, int(syscall.EBADF), rounds, result)
}

// CheckNativeSocketSend inherits a real TCP socket as descriptor 3. Shutting
// down its write half makes EPIPE deterministic without a peer-close race.
func CheckNativeSocketSend(t *testing.T, command *exec.Cmd, payload string, shut bool) {
	t.Helper()
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	client, err := net.DialTCP("tcp4", nil, listener.Addr().(*net.TCPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	peer, err := listener.AcceptTCP()
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	file, err := client.File()
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if shut {
		if err := syscall.Shutdown(int(file.Fd()), syscall.SHUT_WR); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, command.Path, command.Args[1:]...)
	cmd.Env, cmd.Dir = command.Env, command.Dir
	cmd.ExtraFiles = append(cmd.ExtraFiles, file)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tcp_send must return without SIGPIPE: %v\n%s", err, out)
	}
	if !shut {
		if err := peer.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, len(payload))
		if _, err := io.ReadFull(peer, got); err != nil {
			t.Fatal(err)
		}
		if string(got) != payload {
			t.Fatal("tcp_send changed payload bytes")
		}
	}
}
