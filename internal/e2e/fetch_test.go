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
import "std/utf8";

function main(): i32 {
    var h: i32 = fetch.ipv4(127, 0, 0, 1);
    var resp: u8[] = fetch.fetch_get(h, %d, "/");
    var body: u8[] = fetch.http_body(resp);
    match (utf8.from_bytes(body)) {
        Some(text) => { if (text == "hello-world") { return 42; } },
        None => { return 2; },
    }
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

// A response body is arbitrary bytes, which is why the fetch path is
// byte-domain (#5714, D9). The upstream here serves a body that is NOT
// valid UTF-8 (NUL, 0xFF, a lone 0x80 continuation byte); the guest
// asserts every byte survives http_body unmodified AND that decoding it
// as text is rejected. Under the old string-typed fetch this body was a
// `string` holding malformed UTF-8 — the exact value the invariant
// forbids.
func TestFetchBinaryBody(t *testing.T) {
	bin := buildFernCLI(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("no free TCP port: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	body := []byte{0x00, 0xFF, 0x80, 0x0A, 0x41}
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
				fmt.Fprintf(c, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n", len(body))
				_, _ = c.Write(body)
			}(conn)
		}
	}()

	src := fmt.Sprintf(`import "std/fetch";
import "std/utf8";

function main(): i32 {
    var h: i32 = fetch.ipv4(127, 0, 0, 1);
    var resp: u8[] = fetch.fetch_get(h, %d, "/");
    if (fetch.http_status(resp) != 200) { return 1; }
    var body: u8[] = fetch.http_body(resp);
    if (body.len() != 5) { return 2; }
    if ((body[0] as i32) != 0) { return 3; }
    if ((body[1] as i32) != 255) { return 4; }
    if ((body[2] as i32) != 128) { return 5; }
    if ((body[3] as i32) != 10) { return 6; }
    if ((body[4] as i32) != 65) { return 7; }
    // The same bytes must be rejected as text rather than reinterpreted.
    match (utf8.from_bytes(body)) {
        Some(t) => { return 8; },
        None => { return 42; },
    }
    return 9;
}`, port)

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "fetch_binary.fern")
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
			out := filepath.Join(dir, be.target+"_fetch_binary.bin")
			if o, err := exec.Command(bin, "-target", be.target, "-o", out, srcPath).CombinedOutput(); err != nil {
				t.Fatalf("build failed: %v\n%s", err, o)
			}
			cmd := be.run(qemu, out)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 42 {
				t.Errorf("%s: binary-body fetch exit = %d, want 42", be.target, code)
			}
		})
	}
}

// Both fetch read paths collect `recv` buffers into a chunk list and join
// once, because concatenating onto a growing accumulator is quadratic in
// the response size whenever that accumulator has a second reference. The
// rc==1 cliff counters are what make the difference observable, so pin
// them against a 1 MiB body: the blocking path's list is a plain local and
// must never cross the cliff at all, and the drain's list is captured by
// `resume` so it does cross — but what it copies is pointer array, kept
// three orders of magnitude below the ~137 MB a byte-wise accumulator
// would have copied for the same body.
func TestFetchAccumulatorStaysLinear(t *testing.T) {
	bin := buildFernCLI(t)

	const bodyLen = 1 << 20
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("no free TCP port: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	body := make([]byte, bodyLen)
	for i := range body {
		body[i] = byte('a' + i%26)
	}
	go func() {
		for {
			conn, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(10 * time.Second))
				b := make([]byte, 512)
				_, _ = c.Read(b)
				fmt.Fprintf(c, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n", len(body))
				_, _ = c.Write(body)
			}(conn)
		}
	}()

	src := fmt.Sprintf(`import "std/async";
import "std/fetch";

function main(): i32 {
    var h: i32 = fetch.ipv4(127, 0, 0, 1);
    var raw: u8[] = fetch.fetch_get(h, %[1]d, "/");
    if (fetch.http_body(raw).len() != %[2]d) { return 1; }
    // fetch_raw's chunk list is a plain local, so it grows in place.
    if (__arr_push_shared_count() != 0) { return 2; }
    var none: u8[] = [];
    var fs: async.Future[u8[]][] = [fetch.fetch_future(h, %[1]d, "/")];
    var bodies: u8[][] = async.gather(fs, none);
    if (bodies[0].len() != %[2]d) { return 3; }
    // __fetch_drain's list IS captured, so its appends cross the cliff —
    // pointer-sized, which is the whole point of carrying chunks.
    if (__arr_push_shared_bytes() > (8388608 as i64)) { return 4; }
    return 42;
}`, port, bodyLen)

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "fetch_linear.fern")
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
			out := filepath.Join(dir, be.target+"_fetch_linear.bin")
			if o, err := exec.Command(bin, "-target", be.target, "-o", out, srcPath).CombinedOutput(); err != nil {
				t.Fatalf("build failed: %v\n%s", err, o)
			}
			cmd := be.run(qemu, out)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 42 {
				t.Errorf("%s: accumulator shape exit = %d, want 42", be.target, code)
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
import "std/platform";

function main(): i32 {
    var p: Platform = platform.platform_new();
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
import "std/utf8";

function main(): i32 {
    var resp: u8[] = fetch.get_url("http://127.0.0.1:%d/path");
    var body: u8[] = fetch.http_body(resp);
    match (utf8.from_bytes(body)) {
        Some(text) => { if (text == "hello-world") { return 42; } },
        None => { return 2; },
    }
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

// get_url must reject a URL whose scheme bytes are not exactly
// "http://" — including a multibyte look-alike lead byte — before any
// network I/O happens. No upstream server: every case must come back
// empty.
func TestFetchGetURLBadScheme(t *testing.T) {
	bin := buildFernCLI(t)

	src := `import "std/fetch";

function main(): i32 {
    // Cyrillic н look-alike scheme, a too-short string, a near-miss.
    if (fetch.get_url("нttp://1.2.3.4/").len() != 0) { return 1; }
    if (fetch.get_url("htt").len() != 0) { return 2; }
    if (fetch.get_url("http:/1.2.3.4/").len() != 0) { return 3; }
    return 42;
}`

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "geturl_badscheme.fern")
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
			out := filepath.Join(dir, be.target+"_geturl_badscheme.bin")
			if o, err := exec.Command(bin, "-target", be.target, "-o", out, srcPath).CombinedOutput(); err != nil {
				t.Fatalf("build failed: %v\n%s", err, o)
			}
			cmd := be.run(qemu, out)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 42 {
				t.Errorf("%s: bad-scheme exit = %d, want 42", be.target, code)
			}
		})
	}
}
