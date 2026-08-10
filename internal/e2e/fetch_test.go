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

// std/fetch.fetch_get is the outbound HTTP/1.1 client (the
// upstream-fetch half of the edge-handler use case): tcp_connect +
// send the request + recv until close. This drives it against a real
// Go upstream that returns "hello-world", and checks http_body peels
// the body off the response. Both native backends (arm64 binary under
// qemu connects to the host upstream).
func TestFetchGet(t *testing.T) {
	bin := buildFernCLI(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("no free TCP port: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	go func() {
		for {
			conn, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close() // close → fetch_get's recv loop hits EOF
				_ = c.SetDeadline(time.Now().Add(3 * time.Second))
				b := make([]byte, 512)
				_, _ = c.Read(b)
				fmt.Fprint(c, "HTTP/1.1 200 OK\r\nContent-Length: 11\r\n\r\nhello-world")
			}(conn)
		}
	}()

	// fetch_get the upstream, peel the body, exit 42 iff it's the
	// expected "hello-world".
	src := fmt.Sprintf(`import "std/fetch";
import "std/string";

function main(): i32 {
    var h: i32 = fetch.ipv4(127, 0, 0, 1);
    var resp: string = fetch.fetch_get(h, %d, "/");
    var body: str = fetch.http_body(resp);
    if (body == "hello-world") { return 42; }
    return 1;
}`, port)

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "fetch.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	backends := []struct {
		target string
		qemu   func(*testing.T) string
		run    func(qemu, bin string, args ...string) *exec.Cmd
	}{
		{"x86-64-linux", x86QemuOrEmpty, runX86Bin},
		{"arm64-linux", arm64QemuOrEmpty, runArm64Bin},
	}
	for _, be := range backends {
		be := be
		t.Run(be.target, func(t *testing.T) {
			qemu := be.qemu(t)
			out := filepath.Join(dir, be.target+"_fetch.bin")
			if o, err := exec.Command(bin, "-target", be.target, "-o", out, srcPath).CombinedOutput(); err != nil {
				t.Fatalf("build failed: %v\n%s", err, o)
			}
			cmd := be.run(qemu, out)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 42 {
				t.Errorf("%s: fetch_get exit = %d, want 42", be.target, code)
			}
		})
	}
}

// plat.fetch is the capability-scoped form a handler uses:
// `plat.fetch("a.b.c.d", port, path)` (parses the literal IPv4, routes
// through the Platform bag). It returns the HTTP STATUS CODE as an i32
// (the i32 result that flows through the std/task runtime); the body is
// read via the lower-level fetch_get + http_body. Same upstream round-trip.
func TestPlatformFetch(t *testing.T) {
	bin := buildFernCLI(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("no free TCP port: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	go func() {
		for {
			conn, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(3 * time.Second))
				b := make([]byte, 512)
				_, _ = c.Read(b)
				fmt.Fprint(c, "HTTP/1.1 200 OK\r\nContent-Length: 11\r\n\r\nhello-world")
			}(conn)
		}
	}()

	src := fmt.Sprintf(`import "std/fetch";
import "std/string";

function main(): i32 {
    var p: Platform = Platform { version: 1 };
    var status: i32 = p.fetch("127.0.0.1", %d, "/");   // i32 status code
    if (status == 200) { return 42; }
    return status;
}`, port)

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "platfetch.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	backends := []struct {
		target string
		qemu   func(*testing.T) string
		run    func(qemu, bin string, args ...string) *exec.Cmd
	}{
		{"x86-64-linux", x86QemuOrEmpty, runX86Bin},
		{"arm64-linux", arm64QemuOrEmpty, runArm64Bin},
	}
	for _, be := range backends {
		be := be
		t.Run(be.target, func(t *testing.T) {
			qemu := be.qemu(t)
			out := filepath.Join(dir, be.target+"_platfetch.bin")
			if o, err := exec.Command(bin, "-target", be.target, "-o", out, srcPath).CombinedOutput(); err != nil {
				t.Fatalf("build failed: %v\n%s", err, o)
			}
			cmd := be.run(qemu, out)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 42 {
				t.Errorf("%s: plat.fetch exit = %d, want 42", be.target, code)
			}
		})
	}
}

// fetch.get_url fetches a full http://HOST[:PORT]/PATH URL in one
// call (splits scheme/host/port/path, IPv4 host). Same upstream
// round-trip via the URL form.
func TestFetchGetURL(t *testing.T) {
	bin := buildFernCLI(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("no free TCP port: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	go func() {
		for {
			conn, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(3 * time.Second))
				b := make([]byte, 512)
				_, _ = c.Read(b)
				fmt.Fprint(c, "HTTP/1.1 200 OK\r\nContent-Length: 11\r\n\r\nhello-world")
			}(conn)
		}
	}()

	src := fmt.Sprintf(`import "std/fetch";
import "std/string";

function main(): i32 {
    var resp: string = fetch.get_url("http://127.0.0.1:%d/path");
    var body: str = fetch.http_body(resp);
    if (body == "hello-world") { return 42; }
    return 1;
}`, port)

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "geturl.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	backends := []struct {
		target string
		qemu   func(*testing.T) string
		run    func(qemu, bin string, args ...string) *exec.Cmd
	}{
		{"x86-64-linux", x86QemuOrEmpty, runX86Bin},
		{"arm64-linux", arm64QemuOrEmpty, runArm64Bin},
	}
	for _, be := range backends {
		be := be
		t.Run(be.target, func(t *testing.T) {
			qemu := be.qemu(t)
			out := filepath.Join(dir, be.target+"_geturl.bin")
			if o, err := exec.Command(bin, "-target", be.target, "-o", out, srcPath).CombinedOutput(); err != nil {
				t.Fatalf("build failed: %v\n%s", err, o)
			}
			cmd := be.run(qemu, out)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 42 {
				t.Errorf("%s: get_url exit = %d, want 42", be.target, code)
			}
		})
	}
}
