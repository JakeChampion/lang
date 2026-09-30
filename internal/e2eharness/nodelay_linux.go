//go:build linux

package e2eharness

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// pidfd_open and pidfd_getfd share their numbers across Linux's
// architectures.
const (
	sysPidfdOpen  = 434
	sysPidfdGetfd = 438
)

// ConnectedTCPNoDelay reads TCP_NODELAY on every connected TCP socket the
// process `pid` holds, through copies of its descriptors taken with
// pidfd_getfd: the count of sockets with the option on, and the count
// with it off. A listener and anything that is not a TCP socket are not
// counted.
func ConnectedTCPNoDelay(t *testing.T, pid int) (on, off int) {
	t.Helper()
	pidfd, _, errno := syscall.Syscall(sysPidfdOpen, uintptr(pid), 0, 0)
	if errno != 0 {
		t.Fatalf("pidfd_open(%d): %v", pid, errno)
	}
	defer syscall.Close(int(pidfd))
	entries, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid))
	if err != nil {
		t.Fatalf("read the server's descriptors: %v", err)
	}
	for _, e := range entries {
		target, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		link, err := os.Readlink(fmt.Sprintf("/proc/%d/fd/%d", pid, target))
		if err != nil || len(link) < 7 || link[:7] != "socket:" {
			continue
		}
		fd, _, errno := syscall.Syscall(sysPidfdGetfd, pidfd, uintptr(target), 0)
		if errno != 0 {
			t.Fatalf("pidfd_getfd(%d): %v", target, errno)
		}
		nodelay, counted := tcpNoDelay(int(fd))
		syscall.Close(int(fd))
		if !counted {
			continue
		}
		if nodelay {
			on++
		} else {
			off++
		}
	}
	return on, off
}

// tcpNoDelay is the socket's TCP_NODELAY, and whether it is a connected
// TCP socket at all.
func tcpNoDelay(fd int) (nodelay, counted bool) {
	if ty, err := syscall.GetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_TYPE); err != nil || ty != syscall.SOCK_STREAM {
		return false, false
	}
	if proto, err := syscall.GetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_PROTOCOL); err != nil || proto != syscall.IPPROTO_TCP {
		return false, false
	}
	var sa [128]byte
	salen := uint32(len(sa))
	if _, _, errno := syscall.Syscall(syscall.SYS_GETPEERNAME, uintptr(fd), uintptr(unsafe.Pointer(&sa[0])), uintptr(unsafe.Pointer(&salen))); errno != 0 {
		return false, false
	}
	v, err := syscall.GetsockoptInt(fd, syscall.IPPROTO_TCP, syscall.TCP_NODELAY)
	if err != nil {
		return false, false
	}
	return v != 0, true
}

// NoDelayServerSource is a single-loop server answering "ok".
func NoDelayServerSource(port int) string {
	return fmt.Sprintf(`import "std/http";
import "std/tcp";
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    return http.ok("ok");
}
function main(): i32 {
    return tcp.tcp_serve_opts(%d, tcp.serve_options(), handle);
}
`, port)
}

// CheckServeNoDelay drives NoDelayServerSource: a keep-alive connection
// answered and held open is, in the server, a connected TCP socket with
// TCP_NODELAY on, and the server holds none with it off.
func CheckServeNoDelay(t *testing.T, cmd *exec.Cmd, addr string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(conn, "GET /ok HTTP/1.1\r\nHost: x\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read the response: %v", err)
	}
	resp.Body.Close()
	on, off := ConnectedTCPNoDelay(t, cmd.Process.Pid)
	if on < 1 || off != 0 {
		t.Fatalf("the server's connected sockets: %d with TCP_NODELAY, %d without; want the accepted one with it and none without", on, off)
	}
}

// NetNoDelayProbe dials itself through std/net's `connect` and takes the
// connection with `accept`, prints "ready" and waits, so both ends are
// connected sockets of one process for CheckNetNoDelay to read.
func NetNoDelayProbe() string {
	return `import "std/net";
function main(): i32 {
    var ln: i32 = 0;
    match (net.listen_with(0, net.listen_options())) { Ok(fd) => { ln = fd; }, Err(e) => { return 1; } }
    var c: i32 = 0;
    match (net.connect(net.socket_addr(net.ipv4_loopback(), tcp_local_port(ln)))) { Ok(fd) => { c = fd; }, Err(e) => { return 2; } }
    var a: i32 = 0;
    match (net.accept(ln)) { Ok(fd) => { a = fd; }, Err(e) => { return 3; } }
    print("ready");
    sleep_ms(60000 as i64);
    return 0;
}
`
}

// CheckNetNoDelay drives a started NetNoDelayProbe whose stdout is
// `out`: once it reports ready, the dialled and the accepted socket both
// have TCP_NODELAY on.
func CheckNetNoDelay(t *testing.T, cmd *exec.Cmd, out io.Reader) {
	t.Helper()
	ready := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(out).ReadString('y')
		ready <- line
	}()
	select {
	case line := <-ready:
		if line != "ready" {
			t.Fatalf("the probe printed %q, want ready", line)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the probe never reported ready")
	}
	on, off := ConnectedTCPNoDelay(t, cmd.Process.Pid)
	if on != 2 || off != 0 {
		t.Fatalf("the probe's connected sockets: %d with TCP_NODELAY, %d without; want both ends with it", on, off)
	}
}
