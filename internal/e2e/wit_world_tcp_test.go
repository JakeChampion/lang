package e2e

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/wasm/component"
	"github.com/jakechampion/lang/internal/wasm/componenttype"
)

// TestComposeTcpFromWorld proves the world-driven composer
// (ComposeFromWorldAuto) handles the TCP server shape — the last and most
// complex built-in capability not yet exercised through the world path
// (TestComposeStdoutFromWorld covers stdout, TestComposeFsFromWorld the
// filesystem, and the wit_extern_builtin_{env,udp} tests env/args + UDP).
// TCP is the hard one: tcp-socket.accept returns
// `result<tuple<tcp-socket, input-stream, output-stream>, error-code>`, a
// multi-handle indirect result (KindMem), alongside the listen/bind chain,
// the io/streams read+write methods, and four resource-drops (tcp-socket
// ×2, input-stream, output-stream).
//
// This is the gate that clears the way to retiring the bespoke native
// registry: once the world path composes a working TCP server, every
// non-extern shape the CLI builds is world-composable. The core module is
// the one the real CLI emits (extracted from the registry-composed
// component); we recompose it through the world path and run it as a real
// echo server a Go client connects to.
func TestComposeTcpFromWorld(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH")
	}

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()

	dir := t.TempDir()
	fernBin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", fernBin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("build fern: %v\n%s", err, out)
	}

	src := strings.Replace(`function main(): i32 {
    var sock = tcp_listen(__PORT__);
    if (sock < 0) { return 1; }
    var conn = tcp_accept(sock);
    if (conn < 0) { return 2; }
    var msg: string = string_from_bytes_unchecked(tcp_recv(conn, 1024));
    var sent = tcp_send(conn, msg);
    if (sent < 0) { return 3; }
    tcp_close(conn);
    tcp_close(sock);
    return 0;
}
`, "__PORT__", strconv.Itoa(port), 1)
	progPath := filepath.Join(dir, "echo.fern")
	if err := os.WriteFile(progPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}
	refPath := filepath.Join(dir, "ref.wasm")
	if out, err := exec.Command(fernBin, "-target", "wasm32-wasi", "-o", refPath, progPath).CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm: %v\n%s", err, out)
	}
	ref, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatalf("read ref: %v", err)
	}
	// Recompose the CLI's own core through the world-driven path.
	core := componentCoreSection(t, ref)
	w, err := componenttype.DecodeWorld("fern")
	if err != nil {
		t.Fatalf("DecodeWorld: %v", err)
	}
	comp, err := component.ComposeFromWorldAuto(core, w)
	if err != nil {
		t.Fatalf("ComposeFromWorldAuto: %v", err)
	}
	mine := filepath.Join(dir, "world-tcp.wasm")
	if err := os.WriteFile(mine, comp, 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}
	if out, err := exec.Command(wasmtools, "validate", mine).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, out)
	}

	run := exec.Command(wasmtime, "run", "-S", "inherit-network", mine)
	if err := run.Start(); err != nil {
		t.Fatalf("wasmtime run start: %v", err)
	}
	t.Cleanup(func() {
		if run.Process != nil {
			run.Process.Kill()
		}
		run.Wait()
	})

	deadline := time.Now().Add(5 * time.Second)
	var conn net.Conn
	for {
		conn, err = net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	defer conn.Close()

	const msg = "world-tcp-echo"
	if _, err := conn.Write([]byte(msg)); err != nil {
		t.Fatalf("client write: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, len(msg))
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	if got := string(buf[:n]); got != msg {
		t.Fatalf("echo = %q, want %q", got, msg)
	}
}
