package e2eharness

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// WasiTCPCensusProbe exercises real socket ownership without stream I/O,
// whose buffers have separate reclamation requirements. The guest owns both
// ends and uses an ephemeral listener, avoiding a port-reservation race.
const WasiTCPCensusProbe = `function main(): i32 {
    var i: i32 = 0;
    while (i < 32) {
        var listener: i32 = tcp_listen(0);
        if (listener < 0) { return 1; }
        var port: i32 = tcp_local_port(listener);
        if (port <= 0) { return 2; }
        var client: i32 = tcp_connect(127 + 16777216, port);
        if (client < 0) { return 3; }
        var server: i32 = tcp_accept(listener);
        if (server < 0) { return 4; }
        if (tcp_close(server) != 0) { return 5; }
        if (tcp_close(client) != 0) { return 6; }
        if (tcp_close(listener) != 0) { return 7; }
        i = i + 1;
    }
    return 0;
}`

// CheckWasiSocketCensus runs the production component against real WASI sockets.
// Require the instrumented allocator's report, not just a successful exit or
// a flat high-water mark: unreclaimed blocks could satisfy both of those.
func CheckWasiSocketCensus(t *testing.T, component string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "wasmtime", "run", "-S", "inherit-network", component)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("socket lifecycle: %v\nstdout: %s\nstderr: %s", err, &stdout, &stderr)
	}
	// The bootstrap harness prints main's result; the self-host command
	// communicates it through the exit code instead.
	if got := strings.TrimSpace(stdout.String()); got != "" && got != "0" {
		t.Fatalf("socket lifecycle returned %q", got)
	}
	matches := regexp.MustCompile(`(?m)^leakcheck: allocs=([0-9]+) frees=([0-9]+) live_bytes=([0-9]+)$`).FindAllStringSubmatch(stderr.String(), -1)
	if len(matches) != 1 {
		t.Fatalf("want one allocator census, got stderr: %s", &stderr)
	}
	counts := make([]int64, 3)
	for i := range counts {
		n, err := strconv.ParseInt(matches[0][i+1], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		counts[i] = n
	}
	if counts[0] == 0 || counts[0] != counts[1] || counts[2] != 0 {
		t.Fatalf("socket lifecycle must reclaim all guest storage: %s", matches[0][0])
	}
	t.Log(matches[0][0])
}

// WasiUDPCensusProbe receives every datagram on a real loopback socket. The
// byte comparison catches freeing normalized payloads before the host reads.
func WasiUDPCensusProbe(t *testing.T, data string) (string, func()) {
	t.Helper()
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		buf := make([]byte, len(data)+1)
		for i := 0; i < 32; i++ {
			n, _, err := conn.ReadFrom(buf)
			if err != nil {
				done <- err
				return
			}
			if string(buf[:n]) != data {
				done <- fmt.Errorf("datagram %d = %q, want %q", i, buf[:n], data)
				return
			}
		}
		done <- nil
	}()
	src := fmt.Sprintf(`function main(): i32 {
    var i: i32 = 0;
    while (i < 32) {
        if (udp_send("127.0.0.1", %d, %q) != %d) { return 1; }
        i = i + 1;
    }
    return 0;
}`, conn.LocalAddr().(*net.UDPAddr).Port, data, len(data))
	return src, func() {
		t.Helper()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func WasiTCPSendCensusProbe(t *testing.T, data string) (string, func()) {
	return wasiTCPStreamCensusProbe(t, data, false)
}

func WasiTCPRecvCensusProbe(t *testing.T, data string) (string, func()) {
	return wasiTCPStreamCensusProbe(t, data, true)
}

func wasiTCPStreamCensusProbe(t *testing.T, data string, receive bool) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	if err := listener.(*net.TCPListener).SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 32; i++ {
			conn, err := listener.Accept()
			if err != nil {
				done <- err
				return
			}
			if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
				conn.Close()
				done <- err
				return
			}
			var got []byte
			if receive {
				_, err = io.WriteString(conn, data)
			} else {
				got, err = io.ReadAll(io.LimitReader(conn, int64(len(data)+1)))
			}
			conn.Close()
			if err != nil {
				done <- err
				return
			}
			if !receive && string(got) != data {
				done <- fmt.Errorf("connection %d: got %q, want %q", i, got, data)
				return
			}
		}
		done <- nil
	}()
	src := fmt.Sprintf(`function main(): i32 {
    var i: i32 = 0;
    while (i < 32) {
        var c: i32 = tcp_connect(127 + 16777216, %d);
        if (c < 0) { return 1; }
        if (tcp_send(c, %q) != %d) { return 2; }
        if (tcp_close(c) != 0) { return 3; }
        i = i + 1;
    }
    return 0;
}`, listener.Addr().(*net.TCPAddr).Port, data, len(data))
	if receive {
		src = fmt.Sprintf(`function read(c: i32): i32 {
    var bytes: u8[] = tcp_recv(c, 4096);
    var i: i32 = 0;
    while (i < bytes.len()) {
        if (bytes[i] != 120) { return -1; }
        i = i + 1;
    }
    return bytes.len();
}
function main(): i32 {
    var i: i32 = 0;
    while (i < 32) {
        var c: i32 = tcp_connect(127 + 16777216, %d);
        if (c < 0) { return 1; }
        var total: i32 = 0;
        var n: i32 = read(c);
        while (n > 0) { total = total + n; n = read(c); }
        if (n < 0 || total != %d) { return 2; }
        if (tcp_close(c) != 0) { return 3; }
        i = i + 1;
    }
    return 0;
}`, listener.Addr().(*net.TCPAddr).Port, len(data))
	}
	return src, func() {
		t.Helper()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}
