package e2eselfhost

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostWasmIRTcpStream pins tcp_send / tcp_recv on the self-host wasm-IR
// path — slice 2 of #4318, against a real echo server the test runs itself.
//
// The interesting part is that these are NOT transcriptions of native's
// buildTcpSendBody / buildTcpRecvBody, because the string representations
// differ. A self-host string block is `[len@+0][bytes@+4]` — ONE pointer, with
// no SSO form to normalise — where native passes a (ptr, len) pair. Reading it
// native-style would send from the wrong offset with the wrong length.
//
// Two host-side constraints shape the helpers:
//
//   - wasmtime caps blocking-write-and-flush at 4 KiB per call, so tcp_send
//     drains through a chunked loop. The large-payload case below is what
//     actually exercises it: 10000 bytes is three chunks, and an off-by-one in
//     the loop shows up as a short write rather than a crash.
//   - blocking-read's returned list lives in host memory that is only valid
//     until the next canonical-ABI call, so tcp_recv COPIES into a fresh u8[]
//     block. It also needs the exported cabi_realloc (the canonical ABI
//     materialises the list<u8> in guest memory) — shared with wasm_poll's
//     list<u32> through a single gate, since a second definition is a
//     duplicate-export error.
func TestSelfHostWasmIRTcpStream(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm-IR tcp-stream e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping wasm-IR tcp-stream e2e")
	}
	adapter := os.Getenv("FERN_WASI_ADAPTER")
	if adapter == "" {
		t.Skip("FERN_WASI_ADAPTER unset; skipping wasm-IR tcp-stream e2e")
	}
	if _, err := os.Stat(adapter); err != nil {
		t.Skipf("adapter %s not found; skipping", adapter)
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	witDir, err := filepath.Abs("../../cmd/fern/wit")
	if err != nil {
		t.Fatalf("abs wit dir: %v", err)
	}

	// serve accepts one connection, reads until it has `want` bytes, then
	// replies with reply(received). Returns the port.
	serve := func(t *testing.T, want int, reply func(got []byte) string) int {
		t.Helper()
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		t.Cleanup(func() { ln.Close() })
		go func() {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
			var got []byte
			buf := make([]byte, 65536)
			for len(got) < want {
				n, err := c.Read(buf)
				if n > 0 {
					got = append(got, buf[:n]...)
				}
				if err != nil {
					if err != io.EOF {
						return
					}
					break
				}
			}
			_, _ = c.Write([]byte(reply(got)))
		}()
		return ln.Addr().(*net.TCPAddr).Port
	}

	runWith := func(t *testing.T, name, src string, wantSyms []string) string {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin)
		} else {
			cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		wat, err := cmd.Output()
		if err != nil || len(wat) == 0 {
			t.Fatalf("driver failed: %v", err)
		}
		for _, sym := range wantSyms {
			if !strings.Contains(string(wat), sym) {
				t.Fatalf("%s: emitted WAT has no `%s` (module bailed off the IR path?)", name, sym)
			}
		}
		watPath := filepath.Join(dir, name+".wat")
		if err := os.WriteFile(watPath, wat, 0o644); err != nil {
			t.Fatalf("write wat: %v", err)
		}
		corePath := filepath.Join(dir, name+".core.wasm")
		if out, err := exec.Command(wasmtools, "parse", watPath, "-o", corePath).CombinedOutput(); err != nil {
			t.Fatalf("wasm-tools parse: %v\n%s", err, out)
		}
		embedPath := filepath.Join(dir, name+".embed.wasm")
		if out, err := exec.Command(wasmtools, "component", "embed", witDir,
			"-w", "fern", corePath, "-o", embedPath).CombinedOutput(); err != nil {
			t.Fatalf("wasm-tools component embed: %v\n%s", err, out)
		}
		compPath := filepath.Join(dir, name+".component.wasm")
		if out, err := exec.Command(wasmtools, "component", "new", embedPath,
			"--adapt", "wasi_snapshot_preview1="+adapter, "-o", compPath).CombinedOutput(); err != nil {
			t.Fatalf("wasm-tools component new: %v\n%s", err, out)
		}
		out, err := exec.Command(wasmtime, "run", "-S", "inherit-network", compPath).Output()
		if err != nil {
			t.Fatalf("wasmtime run: %v", err)
		}
		return strings.TrimSpace(string(out))
	}

	run := func(t *testing.T, name, src string) string {
		return runWith(t, name, src, []string{"call $__fern_tcp_send", "call $__fern_tcp_recv"})
	}
	// runSendOnly asserts only the send symbol — the send-only module has no
	// tcp_recv in it, which is the whole point of that case.
	runSendOnly := func(t *testing.T, name, src string) string {
		return runWith(t, name, src, []string{"call $__fern_tcp_send"})
	}

	const localhostBE = "127 + (0 * 256) + (0 * 65536) + (1 * 16777216)"

	// Round-trip: send a short payload, read the echo back. Pins that the
	// string block is read from the right offset (bytes at +4, not +0) and that
	// the reply is copied out of the host buffer intact.
	t.Run("send_recv_roundtrip", func(t *testing.T) {
		port := serve(t, 5, func(got []byte) string { return "ECHO:" + string(got) })
		src := fmt.Sprintf(`function main(): i32 {
    var host: i32 = %s;
    var c: i32 = tcp_connect(host, %d);
    if (c < 0) { write("connect-failed\n"); return 1; }
    var n: i32 = tcp_send(c, "hello");
    write("sent="); print_int(n);
    var r: string = string_from_bytes_unchecked(tcp_recv(c, 64));
    write(" got="); write(r); write("\n");
    tcp_close(c);
    return 0;
}`, localhostBE, port)
		if got := run(t, "tcp_roundtrip", src); got != "sent=5 got=ECHO:hello" {
			t.Errorf("stdout = %q, want %q", got, "sent=5 got=ECHO:hello")
		}
	})

	// tcp_send ALONE, with no tcp_recv in the module. This is the case the
	// original stream test missed: it exercised send and recv together, and
	// recv's cabi_realloc gate satisfied the requirement that send also has
	// (its blocking-write-and-flush takes a list<u8>), so a send-only program
	// failed to compose with "module does not export a function named
	// cabi_realloc" while the paired test stayed green. Asserting the compose
	// succeeds is the point; the byte count confirms it still works.
	t.Run("send_only_composes_without_recv", func(t *testing.T) {
		port := serve(t, 4, func(got []byte) string { return "" })
		src := fmt.Sprintf(`function main(): i32 {
    var host: i32 = %s;
    var c: i32 = tcp_connect(host, %d);
    if (c < 0) { write("connect-failed\n"); return 1; }
    var n: i32 = tcp_send(c, "ping");
    write("sent="); print_int(n); write("\n");
    tcp_close(c);
    return 0;
}`, localhostBE, port)
		if got := runSendOnly(t, "tcp_send_only", src); got != "sent=4" {
			t.Errorf("stdout = %q, want %q", got, "sent=4")
		}
	})

	// 10000 bytes is three 4 KiB chunks, so this is the case that actually
	// exercises tcp_send's drain loop. The server reports the byte count it
	// received, so a short write shows up as a mismatch rather than silently
	// passing.
	t.Run("send_spans_multiple_4k_chunks", func(t *testing.T) {
		const payloadLen = 10000
		port := serve(t, payloadLen, func(got []byte) string { return fmt.Sprintf("%d", len(got)) })
		src := fmt.Sprintf(`function main(): i32 {
    var host: i32 = %s;
    var c: i32 = tcp_connect(host, %d);
    if (c < 0) { write("connect-failed\n"); return 1; }
    var payload: string = "x".repeat(%d);
    var n: i32 = tcp_send(c, payload);
    write("sent="); print_int(n);
    var r: string = string_from_bytes_unchecked(tcp_recv(c, 64));
    write(" server-received="); write(r); write("\n");
    tcp_close(c);
    return 0;
}`, localhostBE, port, payloadLen)
		want := fmt.Sprintf("sent=%d server-received=%d", payloadLen, payloadLen)
		if got := run(t, "tcp_chunked", src); got != want {
			t.Errorf("stdout = %q, want %q", got, want)
		}
	})
}
