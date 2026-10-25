//go:build linux

package e2eharness

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

const (
	sysPidfdOpen  = 434
	sysPidfdGetfd = 438
)

func eachConnectedTCP(t *testing.T, pid int, f func(fd int)) {
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
		if connectedTCP(int(fd)) {
			f(int(fd))
		}
		syscall.Close(int(fd))
	}
}

func connectedTCP(fd int) bool {
	if ty, err := syscall.GetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_TYPE); err != nil || ty != syscall.SOCK_STREAM {
		return false
	}
	if proto, err := syscall.GetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_PROTOCOL); err != nil || proto != syscall.IPPROTO_TCP {
		return false
	}
	var sa [128]byte
	salen := uint32(len(sa))
	_, _, errno := syscall.Syscall(syscall.SYS_GETPEERNAME, uintptr(fd), uintptr(unsafe.Pointer(&sa[0])), uintptr(unsafe.Pointer(&salen)))
	return errno == 0
}

func ConnectedTCPNoDelay(t *testing.T, pid int) (on, off int) {
	t.Helper()
	eachConnectedTCP(t, pid, func(fd int) {
		v, err := syscall.GetsockoptInt(fd, syscall.IPPROTO_TCP, syscall.TCP_NODELAY)
		if err != nil {
			t.Fatalf("read TCP_NODELAY: %v", err)
		}
		if v != 0 {
			on++
		} else {
			off++
		}
	})
	return on, off
}

func ConnectedTCPSegmentsOut(t *testing.T, pid int) uint64 {
	t.Helper()
	var total uint64
	eachConnectedTCP(t, pid, func(fd int) {
		var info [256]byte
		n := uint32(len(info))
		if _, _, errno := syscall.Syscall6(syscall.SYS_GETSOCKOPT, uintptr(fd), syscall.IPPROTO_TCP, syscall.TCP_INFO, uintptr(unsafe.Pointer(&info[0])), uintptr(unsafe.Pointer(&n)), 0); errno != 0 {
			t.Fatalf("read TCP_INFO: %v", errno)
		}
		if n < 140 {
			t.Fatalf("TCP_INFO is %d bytes, too short for tcpi_segs_out", n)
		}
		total += uint64(binary.LittleEndian.Uint32(info[136:140]))
	})
	return total
}

func NoDelayServerSource() string {
	return fmt.Sprintf(`import "std/http";
import "std/serve";
import "std/platform";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    return http.ok("ok");
}
function main(): i32 {
    return serve.run(0, serve.config(), handle);
}
`)
}

func CheckServeNoDelay(t *testing.T, cmd *exec.Cmd, addr string) {
	t.Helper()
	WaitServerReady(t, addr)
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

func NetNoDelayProbe() string {
	return `import "std/net";
function main(): i32 {
    let ln: i32 = 0;
    match (net.listen_with(0, net.listen_options())) { Ok(fd) => { ln = fd; }, Err(e) => { return 1; } }
    let c: i32 = 0;
    match (net.connect(net.socket_addr(net.ipv4_loopback(), tcp_local_port(ln)))) { Ok(fd) => { c = fd; }, Err(e) => { return 2; } }
    let a: i32 = 0;
    match (net.accept(ln)) { Ok(fd) => { a = fd; }, Err(e) => { return 3; } }
    print("ready");
    sleep_ms(60000 as i64);
    return 0;
}
`
}

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

func CheckServeCorksBurst(t *testing.T, cmd *exec.Cmd, addr string) {
	t.Helper()
	WaitServerReady(t, addr)
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	r := bufio.NewReader(conn)
	const request = "GET /ok HTTP/1.1\r\nHost: x\r\n\r\n"
	readOK := func(label string) {
		t.Helper()
		resp, err := http.ReadResponse(r, nil)
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != 200 || string(body) != "ok" {
			t.Fatalf("%s: status %d body %q (%v), want 200 ok", label, resp.StatusCode, body, err)
		}
	}
	if _, err := io.WriteString(conn, request); err != nil {
		t.Fatal(err)
	}
	readOK("the first request")
	before := ConnectedTCPSegmentsOut(t, cmd.Process.Pid)
	if _, err := io.WriteString(conn, strings.Repeat(request, 32)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 32; i++ {
		readOK(fmt.Sprintf("pipelined request %d", i))
	}
	if sent := ConnectedTCPSegmentsOut(t, cmd.Process.Pid) - before; sent > 4 {
		t.Fatalf("the server sent %d segments answering 32 pipelined requests, want the burst corked into one write", sent)
	}
}
