package e2ecompiler

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

// TestSelfHostWasmIRTcpStream pins tcp_send_bytes / tcp_recv_into on the
// self-host wasm-IR path — slice 2 of #4318, against a real echo server the
// test runs itself.
//
// Two host-side constraints shape the helpers:
//
//   - wasmtime caps blocking-write-and-flush at 4 KiB per call, so
//     tcp_send_bytes drains through a chunked loop. The large-payload case
//     below is what actually exercises it: 10000 bytes is three chunks, and an
//     off-by-one in the loop shows up as a short write rather than a crash.
//   - a read lends the caller's buffer to the host through the exported
//     cabi_realloc (the canonical ABI materialises the list<u8> in guest
//     memory) — shared with wasm_poll's list<u32> through a single gate, since
//     a second definition is a duplicate-export error.
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
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")
	witDir, err := filepath.Abs("../../../cmd/fern/wit")
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
		cmd.Stdin = bytes.NewReader([]byte(withPrintInt(src)))
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
		return runWith(t, name, src, []string{"call $__fern_tcp_send_bytes", "call $__fern_tcp_recv_into"})
	}
	// runSendOnly asserts only the send symbol — the send-only module has no
	// read in it, which is the whole point of that case.
	runSendOnly := func(t *testing.T, name, src string) string {
		return runWith(t, name, src, []string{"call $__fern_tcp_send_bytes"})
	}

	// Round-trip: send a short payload, read the echo back into the caller's
	// buffer intact.
	t.Run("send_recv_roundtrip", func(t *testing.T) {
		port := serve(t, 5, func(got []byte) string { return "ECHO:" + string(got) })
		src := fmt.Sprintf(wasmRecvHelper+`function main(): i32 {
    let c: i32 = tcp_connect_with([127u8, 0u8, 0u8, 1u8], %d, false);
    if (c < 0) { write("connect-failed\n"); return 1; }
    let n: i32 = tcp_send_bytes(c, [104u8, 101u8, 108u8, 108u8, 111u8]);
    write("sent="); print_int(n);
    let r: string = recv(c, 64);
    write(" got="); write(r); write("\n");
    tcp_close(c);
    return 0;
}`, port)
		if got := run(t, "tcp_roundtrip", src); got != "sent=5 got=ECHO:hello" {
			t.Errorf("stdout = %q, want %q", got, "sent=5 got=ECHO:hello")
		}
	})

	// A send ALONE, with no read in the module. A read's cabi_realloc gate
	// would satisfy the requirement a send also has (its
	// blocking-write-and-flush takes a list<u8>), so only a send-only program
	// shows a missing "cabi_realloc" export. Asserting the compose succeeds is
	// the point; the byte count confirms it still works.
	t.Run("send_only_composes_without_recv", func(t *testing.T) {
		port := serve(t, 4, func(got []byte) string { return "" })
		src := fmt.Sprintf(`function main(): i32 {
    let c: i32 = tcp_connect_with([127u8, 0u8, 0u8, 1u8], %d, false);
    if (c < 0) { write("connect-failed\n"); return 1; }
    let n: i32 = tcp_send_bytes(c, [112u8, 105u8, 110u8, 103u8]);
    write("sent="); print_int(n); write("\n");
    tcp_close(c);
    return 0;
}`, port)
		if got := runSendOnly(t, "tcp_send_only", src); got != "sent=4" {
			t.Errorf("stdout = %q, want %q", got, "sent=4")
		}
	})

	// 10000 bytes is three 4 KiB chunks, so this is the case that actually
	// exercises tcp_send_bytes's drain loop. The server reports the byte count it
	// received, so a short write shows up as a mismatch rather than silently
	// passing.
	t.Run("send_spans_multiple_4k_chunks", func(t *testing.T) {
		const payloadLen = 10000
		port := serve(t, payloadLen, func(got []byte) string { return fmt.Sprintf("%d", len(got)) })
		src := fmt.Sprintf(wasmRecvHelper+`function main(): i32 {
    let c: i32 = tcp_connect_with([127u8, 0u8, 0u8, 1u8], %d, false);
    if (c < 0) { write("connect-failed\n"); return 1; }
    let payload: u8[] = [];
    let k: i32 = 0;
    while (k < %d) { payload = payload.append(120u8); k = k + 1; }
    let n: i32 = tcp_send_bytes(c, payload);
    write("sent="); print_int(n);
    let r: string = recv(c, 64);
    write(" server-received="); write(r); write("\n");
    tcp_close(c);
    return 0;
}`, port, payloadLen)
		want := fmt.Sprintf("sent=%d server-received=%d", payloadLen, payloadLen)
		if got := run(t, "tcp_chunked", src); got != want {
			t.Errorf("stdout = %q, want %q", got, want)
		}
	})
}

// wasmRecvHelper is a Fern recv(c, max): it waits for c to be readable,
// since a wasi:sockets read never blocks, then reads once, answering the
// bytes as text, none at the end of the stream or on an error.
const wasmRecvHelper = `function recv(c: i32, max: i32): string {
    let p: i32 = tcp_pollable(c);
    wasm_block(p);
    wasm_pollable_drop(p);
    let buf: u8[] = __alloc_u8(max);
    let n: i32 = tcp_recv_into(c, buf);
    let out: u8[] = [];
    let i: i32 = 0;
    while (i < n) { out = out.append(buf[i]); i = i + 1; }
    return string_from_bytes_unchecked(out);
}
`
