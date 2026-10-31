package e2ecompiler

import (
	"fmt"
	"io"
	"net"
	"os/exec"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostWasmIRTcpStream(t *testing.T) {
	wasmtime := e2eharness.Wasmtime(t)
	dir := t.TempDir()

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
		compPath := selfHostSocketComponent(t, dir, name, withPrintInt(src), wantSyms...)
		out, err := exec.Command(wasmtime, "run", "-S", "inherit-network", compPath).Output()
		if err != nil {
			t.Fatalf("wasmtime run: %v", err)
		}
		return strings.TrimSpace(string(out))
	}

	run := func(t *testing.T, name, src string) string {
		return runWith(t, name, src, []string{"call $__fern_tcp_send_bytes", "call $__fern_tcp_recv_into"})
	}
	runSendOnly := func(t *testing.T, name, src string) string {
		return runWith(t, name, src, []string{"call $__fern_tcp_send_bytes"})
	}

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

const wasmRecvHelper = `function recv(c: i32, max: i32): string {
    let buf: u8[] = __alloc_u8(max);
    let n: i32 = tcp_recv_into(c, buf);
    let out: u8[] = [];
    let i: i32 = 0;
    while (i < n) { out = out.append(buf[i]); i = i + 1; }
    return string_from_bytes_unchecked(out);
}
`
