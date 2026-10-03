package e2eharness

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

// BinaryBodyServerSource is a server on `port` whose handler answers
// BinaryBodyContent, which is not UTF-8, as a byte body on /bytes and as a
// stream body on /stream.
func BinaryBodyServerSource(port int) string {
	return fmt.Sprintf(`import "std/http";
import "std/stream";
import "std/tcp";
function payload(): u8[] {
    return [0 as u8, 255 as u8, 128 as u8, 10 as u8, 65 as u8];
}
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    if (req.path == "/bytes") { return http.bytes(200, payload()); }
    if (req.path == "/stream") { return http.stream(200, stream.stream_from_bytes(payload())); }
    return http.ok("ok");
}
function main(): i32 {
    return tcp.tcp_serve(%d, handle);
}
`, port)
}

// BinaryBodyContent is the body BinaryBodyServerSource answers.
var BinaryBodyContent = []byte{0x00, 0xFF, 0x80, 0x0A, 0x41}

// CheckBinaryBody drives BinaryBodyServerSource: /bytes and /stream answer
// the body byte for byte under its own length, and HEAD /bytes answers that
// length with no body.
func CheckBinaryBody(t *testing.T, addr string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	for _, path := range []string{"/bytes", "/stream"} {
		conn := rawRequest(t, addr, path, "Connection: close\r\n")
		resp := readResponse(t, conn, path)
		body, _ := io.ReadAll(resp.Body)
		conn.Close()
		if resp.StatusCode != 200 || !bytes.Equal(body, BinaryBodyContent) || resp.ContentLength != int64(len(BinaryBodyContent)) {
			t.Errorf("%s: status=%d length=%d body=% x, want 200, %d and % x", path, resp.StatusCode, resp.ContentLength, body, len(BinaryBodyContent), BinaryBodyContent)
		}
	}
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(conn, "HEAD /bytes HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	head, _ := io.ReadAll(conn)
	want := fmt.Sprintf("Content-Length: %d\r\n", len(BinaryBodyContent))
	if !bytes.Contains(head, []byte(want)) || !bytes.HasSuffix(head, []byte("\r\n\r\n")) {
		t.Errorf("HEAD /bytes: got %q, want a head carrying %q and no body", head, want)
	}
}
