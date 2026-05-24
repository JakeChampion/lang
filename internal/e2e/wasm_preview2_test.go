// E2E test for the WASI Preview 2 component pipeline. Builds the lang
// CLI, asks it to emit a Component Model component (preview-1 module
// wrapped via wasm-tools + the wasi-preview1-component-adapter), and
// runs it with `wasmtime run`.
//
// Skips when either wasm-tools, the adapter (LANG_WASI_ADAPTER env), or
// wasmtime is missing so `go test ./...` stays green on developer
// machines without the full preview-2 toolchain.
package e2e

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestWasmPreview2HelloWorld(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("preview-2 toolchain not exercised on windows")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH; skipping preview-2 e2e")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping preview-2 e2e")
	}
	adapter := os.Getenv("LANG_WASI_ADAPTER")
	if adapter == "" {
		t.Skip("LANG_WASI_ADAPTER not set; skipping preview-2 e2e (CI sets this)")
	}
	if _, err := os.Stat(adapter); err != nil {
		t.Skipf("adapter %q not readable: %v", adapter, err)
	}

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "hello.fern")
	// Exercises three native preview-2 paths through one program:
	//   - wasi:random/random.get-random-bytes (canonical-ABI list<u8>
	//     return + cabi_realloc, from step 2);
	//   - wasi:cli/stdout.get-stdout + wasi:io/streams output-stream
	//     blocking-write-and-flush (the print path, step 3);
	//   - wasi:cli/stderr.get-stderr + same blocking-write-and-flush
	//     (the eprint path).
	// The preview-1 adapter no longer touches stdio for us — the
	// imports above reach the host directly.
	if err := os.WriteFile(srcPath, []byte(`function main(): i32 {
    var b = random_bytes(8);
    print("hello preview2");
    eprint("err preview2");
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	// Build the lang CLI from this checkout so the test exercises
	// the in-tree post-process pipeline rather than whatever happens
	// to be on PATH.
	bin := filepath.Join(dir, "lang")
	build := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/fern")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build lang: %v\n%s", err, out)
	}

	componentPath := filepath.Join(dir, "hello.component.wasm")
	emit := exec.Command(bin,
		"-target", "wasm",
		"-wasi-adapter", adapter,
		"-o", componentPath,
		srcPath,
	)
	var obuf, ebuf bytes.Buffer
	emit.Stdout = &obuf
	emit.Stderr = &ebuf
	if err := emit.Run(); err != nil {
		t.Fatalf("lang -target wasm: %v\nstdout:\n%s\nstderr:\n%s", err, obuf.String(), ebuf.String())
	}

	// The output should be a Component Model component, recognised
	// by `wasm-tools print` as a top-level `(component …)` form.
	dump := exec.Command("wasm-tools", "print", componentPath)
	dout, err := dump.CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print: %v\n%s", err, dout)
	}
	if !strings.Contains(string(dout), "(component") {
		t.Fatalf("output is not a component (no `(component` token):\n%s", dout)
	}

	run := exec.Command("wasmtime", "run", componentPath)
	var sout, serr bytes.Buffer
	run.Stdout = &sout
	run.Stderr = &serr
	if err := run.Run(); err != nil {
		t.Fatalf("wasmtime run %s: %v\nstdout:\n%s\nstderr:\n%s",
			componentPath, err, sout.String(), serr.String())
	}
	if got, want := strings.TrimRight(sout.String(), "\n"), "hello preview2"; got != want {
		t.Fatalf("stdout = %q; want %q (stderr=%q)", got, want, serr.String())
	}
	if got, want := strings.TrimRight(serr.String(), "\n"), "err preview2"; got != want {
		t.Fatalf("stderr = %q; want %q (stdout=%q)", got, want, sout.String())
	}
}

// TestWasmPreview2StdinReadLine exercises the native preview-2
// stdin path: `wasi:cli/stdin.get-stdin` +
// `wasi:io/streams.input-stream.blocking-read`, with the host
// allocating each byte through our `cabi_realloc` export. Echoes
// each line back via the streams stdout writer (step 3a), so this
// test covers the full preview-2 stdio surface.
func TestWasmPreview2StdinReadLine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("preview-2 toolchain not exercised on windows")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH; skipping preview-2 e2e")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping preview-2 e2e")
	}
	adapter := os.Getenv("LANG_WASI_ADAPTER")
	if adapter == "" {
		t.Skip("LANG_WASI_ADAPTER not set; skipping preview-2 e2e (CI sets this)")
	}
	if _, err := os.Stat(adapter); err != nil {
		t.Skipf("adapter %q not readable: %v", adapter, err)
	}

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "echo.fern")
	// Loop until EOF, writing each line back. write() doesn't add
	// a newline; read_line preserves the trailing '\n', so we get
	// byte-for-byte echo.
	if err := os.WriteFile(srcPath, []byte(`function main(): i32 {
    while (true) {
        match (stdin().read_line()) {
            Some(line) => { write(line); },
            None => { return 0; }
        }
    }
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	bin := filepath.Join(dir, "lang")
	build := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/fern")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build lang: %v\n%s", err, out)
	}

	componentPath := filepath.Join(dir, "echo.component.wasm")
	emit := exec.Command(bin,
		"-target", "wasm",
		"-wasi-adapter", adapter,
		"-o", componentPath,
		srcPath,
	)
	var emitOut, emitErr bytes.Buffer
	emit.Stdout = &emitOut
	emit.Stderr = &emitErr
	if err := emit.Run(); err != nil {
		t.Fatalf("lang -target wasm: %v\nstdout:\n%s\nstderr:\n%s", err, emitOut.String(), emitErr.String())
	}

	input := "alpha\nbeta\ngamma\n"
	run := exec.Command("wasmtime", "run", componentPath)
	run.Stdin = strings.NewReader(input)
	var sout, serr bytes.Buffer
	run.Stdout = &sout
	run.Stderr = &serr
	if err := run.Run(); err != nil {
		t.Fatalf("wasmtime run %s: %v\nstdout:\n%s\nstderr:\n%s",
			componentPath, err, sout.String(), serr.String())
	}
	if got := sout.String(); got != input {
		t.Fatalf("stdout = %q; want %q (stderr=%q)", got, input, serr.String())
	}
}

// TestWasmPreview2FileRoundtrip exercises the native preview-2
// file I/O path: open_writer + Writer.write + Writer.close, then
// open_reader + Reader.read_line + Reader.close. Both go through
// `wasi:filesystem/preopens.get-directories`,
// `wasi:filesystem/types.descriptor.open-at`, and the appropriate
// `*-via-stream` to materialise an `input-stream` /
// `output-stream` resource the Reader/Writer struct holds. No
// preview-1 path_open / fd_read / fd_write involved on either
// side.
func TestWasmPreview2FileRoundtrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("preview-2 toolchain not exercised on windows")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH; skipping preview-2 e2e")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping preview-2 e2e")
	}
	adapter := os.Getenv("LANG_WASI_ADAPTER")
	if adapter == "" {
		t.Skip("LANG_WASI_ADAPTER not set; skipping preview-2 e2e (CI sets this)")
	}
	if _, err := os.Stat(adapter); err != nil {
		t.Skipf("adapter %q not readable: %v", adapter, err)
	}

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "fs.fern")
	if err := os.WriteFile(srcPath, []byte(`function main(): i32 {
    match (open_writer("out.txt")) {
        Ok(w) => {
            match (w.write("line 1\n")) { Some(_) => { return 1; }, None => {} }
            match (w.write("line 2\n")) { Some(_) => { return 2; }, None => {} }
            match (w.close()) { Some(_) => { return 3; }, None => {} }
        },
        Err(_) => { return 4; }
    }
    match (open_reader("out.txt")) {
        Ok(r) => {
            match (r.read_line()) { Some(line) => { write(line); }, None => { return 5; } }
            match (r.read_line()) { Some(line) => { write(line); }, None => { return 6; } }
            match (r.read_line()) { Some(_) => { return 7; }, None => {} }
            match (r.close()) { Some(_) => { return 8; }, None => {} }
        },
        Err(_) => { return 9; }
    }
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	bin := filepath.Join(dir, "lang")
	build := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/fern")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build lang: %v\n%s", err, out)
	}

	componentPath := filepath.Join(dir, "fs.component.wasm")
	emit := exec.Command(bin,
		"-target", "wasm",
		"-wasi-adapter", adapter,
		"-o", componentPath,
		srcPath,
	)
	var emitOut, emitErr bytes.Buffer
	emit.Stdout = &emitOut
	emit.Stderr = &emitErr
	if err := emit.Run(); err != nil {
		t.Fatalf("lang -target wasm: %v\nstdout:\n%s\nstderr:\n%s", err, emitOut.String(), emitErr.String())
	}

	// `wasmtime run --dir DIR` preopens DIR as the working
	// directory, which `get-directories` returns to us as the
	// first preopen descriptor — that's where `open-at` resolves
	// "out.txt" against.
	run := exec.Command("wasmtime", "run", "--dir", dir, componentPath)
	var sout, serr bytes.Buffer
	run.Stdout = &sout
	run.Stderr = &serr
	if err := run.Run(); err != nil {
		t.Fatalf("wasmtime run: %v\nstdout:\n%s\nstderr:\n%s", err, sout.String(), serr.String())
	}
	want := "line 1\nline 2\n"
	if got := sout.String(); got != want {
		t.Fatalf("stdout = %q; want %q (stderr=%q)", got, want, serr.String())
	}
	// Verify the file actually got written.
	on_disk, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatalf("read out.txt: %v", err)
	}
	if string(on_disk) != want {
		t.Fatalf("on-disk = %q; want %q", string(on_disk), want)
	}
}

// TestWasmPreview2ReadWriteFile exercises the convenience
// helpers `write_file` / `read_file` through the preview-2
// pipeline. Both delegate to the open_reader / open_writer
// helpers from step 3c, so they go through native
// `wasi:filesystem` instead of preview-1 `path_open`.
func TestWasmPreview2ReadWriteFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("preview-2 toolchain not exercised on windows")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH; skipping preview-2 e2e")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping preview-2 e2e")
	}
	adapter := os.Getenv("LANG_WASI_ADAPTER")
	if adapter == "" {
		t.Skip("LANG_WASI_ADAPTER not set; skipping preview-2 e2e (CI sets this)")
	}
	if _, err := os.Stat(adapter); err != nil {
		t.Skipf("adapter %q not readable: %v", adapter, err)
	}

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "rwf.fern")
	// Force the read_file accumulator to grow at least once so we
	// also exercise the doubling + memory.copy path. The initial
	// buffer is 4 KiB, so we write a payload past that.
	if err := os.WriteFile(srcPath, []byte(`function main(): i32 {
    var content = "";
    var i = 0;
    while (i < 600) {
        content = content + "hello world\n";
        i = i + 1;
    }
    match (write_file("rwf.txt", content)) {
        Some(_) => { return 1; },
        None => {}
    }
    match (read_file("rwf.txt")) {
        Ok(s) => {
            if (s.len() == content.len()) { print("match"); }
            else { print("mismatch"); }
        },
        Err(_) => { return 2; }
    }
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	bin := filepath.Join(dir, "lang")
	build := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/fern")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build lang: %v\n%s", err, out)
	}

	componentPath := filepath.Join(dir, "rwf.component.wasm")
	emit := exec.Command(bin,
		"-target", "wasm",
		"-wasi-adapter", adapter,
		"-o", componentPath,
		srcPath,
	)
	var emitOut, emitErr bytes.Buffer
	emit.Stdout = &emitOut
	emit.Stderr = &emitErr
	if err := emit.Run(); err != nil {
		t.Fatalf("lang -target wasm: %v\nstdout:\n%s\nstderr:\n%s", err, emitOut.String(), emitErr.String())
	}

	run := exec.Command("wasmtime", "run", "--dir", dir, componentPath)
	var sout, serr bytes.Buffer
	run.Stdout = &sout
	run.Stderr = &serr
	if err := run.Run(); err != nil {
		t.Fatalf("wasmtime run: %v\nstdout:\n%s\nstderr:\n%s", err, sout.String(), serr.String())
	}
	if got := strings.TrimRight(sout.String(), "\n"); got != "match" {
		t.Fatalf("stdout = %q; want %q (stderr=%q)", got, "match", serr.String())
	}
	on_disk, err := os.ReadFile(filepath.Join(dir, "rwf.txt"))
	if err != nil {
		t.Fatalf("read rwf.txt: %v", err)
	}
	if want := strings.Repeat("hello world\n", 600); string(on_disk) != want {
		t.Fatalf("on-disk len=%d, want %d", len(on_disk), len(want))
	}
}

// TestWasmPreview2TcpEcho exercises the wasi:sockets pipeline:
// the guest itself binds a TCP port, accepts one connection,
// echoes the bytes received, then closes. Pre-step-4 this would
// have required `wasmtime --tcp-listen=…` since preview-1 had
// no way for the guest to open a listener; now the guest is
// self-contained — the only host privilege needed is
// `-S inherit-network` for outbound socket creation.
//
// Picking a port: we open + immediately close a transient
// listener on :0 to extract a free ephemeral port i32, then
// hand it to the guest via wasmtime's positional args. Race
// window is tiny but non-zero; if the test ever flakes here,
// that's the cause.
func TestWasmPreview2TcpEcho(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("preview-2 toolchain not exercised on windows")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH; skipping preview-2 e2e")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping preview-2 e2e")
	}
	adapter := os.Getenv("LANG_WASI_ADAPTER")
	if adapter == "" {
		t.Skip("LANG_WASI_ADAPTER not set; skipping preview-2 e2e (CI sets this)")
	}
	if _, err := os.Stat(adapter); err != nil {
		t.Skipf("adapter %q not readable: %v", adapter, err)
	}

	// Pick a free port — open a listener on :0, capture the
	// kernel-assigned port, close before the guest tries to
	// bind. There's a tiny race against another process grabbing
	// the same port, but it's localhost-only and the test is
	// short-lived.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "echo.fern")
	// Hardcode the port in the source instead of plumbing
	// args() — args() comes back as `Array<string>` and the
	// language doesn't have a string-to-int builtin yet, so
	// passing the port through would need a bespoke parser.
	// Acceptable: the port is templated in via Go string
	// formatting at build time.
	src := strings.Replace(`function main(): i32 {
    var sock = tcp_listen(__PORT__);
    if (sock < 0) { return 1; }
    var conn = tcp_accept(sock);
    if (conn < 0) { return 2; }
    var msg = tcp_recv(conn, 1024);
    var sent = tcp_send(conn, msg);
    if (sent < 0) { return 3; }
    tcp_close(conn);
    tcp_close(sock);
    return 0;
}
`, "__PORT__", strings.TrimSpace(itoa(port)), 1)
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	bin := filepath.Join(dir, "lang")
	build := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/fern")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build lang: %v\n%s", err, out)
	}

	componentPath := filepath.Join(dir, "echo.component.wasm")
	emit := exec.Command(bin,
		"-target", "wasm",
		"-wasi-adapter", adapter,
		"-o", componentPath,
		srcPath,
	)
	var emitOut, emitErr bytes.Buffer
	emit.Stdout = &emitOut
	emit.Stderr = &emitErr
	if err := emit.Run(); err != nil {
		t.Fatalf("lang -target wasm: %v\nstdout:\n%s\nstderr:\n%s", err, emitOut.String(), emitErr.String())
	}

	// Spawn the server. `-S inherit-network` lets the guest
	// create + bind sockets via wasi:sockets — without it the
	// host denies tcp-create-socket / start-bind.
	run := exec.Command("wasmtime", "run", "-S", "inherit-network", componentPath)
	var sout, serr bytes.Buffer
	run.Stdout = &sout
	run.Stderr = &serr
	if err := run.Start(); err != nil {
		t.Fatalf("wasmtime run start: %v", err)
	}
	// Make sure we always reap the process even on failure.
	t.Cleanup(func() {
		if run.Process != nil {
			run.Process.Kill()
		}
		run.Wait()
	})

	// Connect with a short retry loop — wasmtime takes a moment
	// to spin up the component and reach start-listen.
	deadline := time.Now().Add(5 * time.Second)
	var conn net.Conn
	for {
		conn, err = net.Dial("tcp", net.JoinHostPort("127.0.0.1", itoa(port)))
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial: %v\nstdout:\n%s\nstderr:\n%s", err, sout.String(), serr.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	defer conn.Close()

	want := "hello from the host\n"
	if _, err := conn.Write([]byte(want)); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Half-close the write side so the guest's blocking-read
	// returns even if it asked for more bytes than we sent.
	if cw, ok := conn.(*net.TCPConn); ok {
		cw.CloseWrite()
	}

	got, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != want {
		t.Fatalf("echo = %q; want %q (stderr=%q)", string(got), want, serr.String())
	}
	if err := run.Wait(); err != nil {
		t.Fatalf("wasmtime exit: %v\nstdout:\n%s\nstderr:\n%s", err, sout.String(), serr.String())
	}
}

// itoa is a tiny strconv.Itoa shim — we don't import strconv
// elsewhere in this file and the cost of pulling it in for one
// call isn't worth it.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// TestWasmPreview2HttpHandler exercises step 5: the
// `lang -target wasi-http` build mode emits a component that
// implements `wasi:http/incoming-handler.handle`. We compile a
// tiny router (path == /hello → 200 "world"; POST any path
// echoes the body), spawn `wasmtime serve`, drive it with curl
// equivalents through Go's net/http client, and assert that
// method / path / body all flow through correctly.
//
// This is the same WIT shape Fastly Compute, Netlify Edge
// Functions, and Unikraft Cloud target — proxy-world components
// produced here run on any of those without extra glue.
func TestWasmPreview2HttpHandler(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("preview-2 toolchain not exercised on windows")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH; skipping preview-2 e2e")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping preview-2 e2e")
	}
	adapter := os.Getenv("LANG_WASI_ADAPTER")
	if adapter == "" {
		t.Skip("LANG_WASI_ADAPTER not set; skipping preview-2 e2e (CI sets this)")
	}
	if _, err := os.Stat(adapter); err != nil {
		t.Skipf("adapter %q not readable: %v", adapter, err)
	}

	// Pick a free port. wasmtime serve's --addr binds eagerly
	// without retrying, so we hand it a kernel-assigned ephemeral
	// port and hope nobody else races us into it before serve
	// starts. Same trade-off as TestWasmPreview2TcpEcho.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "router.fern")
	src := `function handle(req: HttpRequest, plat: Platform): HttpResponse {
    if (req.path == "/hello") {
        return http_response_ok("world");
    }
    if (req.method == "POST") {
        return http_response_ok(req.body_string());
    }
    return http_response_text(404, "not found");
}
`
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	bin := filepath.Join(dir, "lang")
	build := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/fern")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build lang: %v\n%s", err, out)
	}

	componentPath := filepath.Join(dir, "router.component.wasm")
	emit := exec.Command(bin,
		"-target", "wasi-http",
		"-wasi-adapter", adapter,
		"-o", componentPath,
		srcPath,
	)
	var emitOut, emitErr bytes.Buffer
	emit.Stdout = &emitOut
	emit.Stderr = &emitErr
	if err := emit.Run(); err != nil {
		t.Fatalf("lang -target wasi-http: %v\nstdout:\n%s\nstderr:\n%s", err, emitOut.String(), emitErr.String())
	}

	addr := net.JoinHostPort("127.0.0.1", itoa(port))
	run := exec.Command("wasmtime", "serve", "--addr", addr, componentPath)
	var sout, serr bytes.Buffer
	run.Stdout = &sout
	run.Stderr = &serr
	if err := run.Start(); err != nil {
		t.Fatalf("wasmtime serve start: %v", err)
	}
	t.Cleanup(func() {
		if run.Process != nil {
			run.Process.Kill()
		}
		run.Wait()
	})

	// Wait for wasmtime to start listening. wasmtime serve writes
	// "Serving HTTP on http://…" to stderr once it's bound, but
	// dialing in a retry loop is simpler and matches what other
	// component-model HTTP harnesses do.
	client := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(5 * time.Second)
	var resp *http.Response
	for {
		resp, err = client.Get("http://" + addr + "/hello")
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial /hello: %v\nstdout:\n%s\nstderr:\n%s", err, sout.String(), serr.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if resp.StatusCode != 200 {
		t.Errorf("/hello status = %d; want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "world" {
		t.Errorf("/hello body = %q; want %q", string(body), "world")
	}

	resp, err = client.Get("http://" + addr + "/missing")
	if err != nil {
		t.Fatalf("get /missing: %v", err)
	}
	if resp.StatusCode != 404 {
		t.Errorf("/missing status = %d; want 404", resp.StatusCode)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "not found" {
		t.Errorf("/missing body = %q; want %q", string(body), "not found")
	}

	// POST round-trip exercises the body-read pipeline (consume +
	// stream + bulk blocking-read). Use a short body that fits in
	// one read; the doubling accumulator is exercised separately
	// by spec-shaped programs but we keep this one fast.
	want := "echo me back"
	resp, err = client.Post("http://"+addr+"/echo", "text/plain", strings.NewReader(want))
	if err != nil {
		t.Fatalf("post /echo: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("POST status = %d; want 200", resp.StatusCode)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != want {
		t.Errorf("POST body echo = %q; want %q", string(body), want)
	}
}

