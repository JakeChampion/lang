package e2eharness

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

// BinaryBodyServerSource is a server whose handler answers
// BinaryBodyContent, which is not UTF-8, as a byte body on /bytes, as a
// stream body on /stream, and in two chunks from a producer on /chunks.
func BinaryBodyServerSource() string {
	return fmt.Sprintf(`import "std/http";
import "std/stream";
import "std/serve";
import "std/platform";
function payload(): u8[] {
    return [0 as u8, 255 as u8, 128 as u8, 10 as u8, 65 as u8];
}
function half(i: i32): Option[u8[]] {
    let p: u8[] = payload();
    if (i == 0) { return Some([p[0], p[1]]); }
    if (i == 1) { return Some([p[2], p[3], p[4]]); }
    return None;
}
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    if (req.path == "/bytes") { return http.bytes(200, payload()); }
    if (req.path == "/stream") { return http.stream(200, stream.stream_from_bytes(payload())); }
    if (req.path == "/chunks") { return http.chunks(200, half); }
    return http.ok("ok");
}
function main(): i32 {
    return serve.run(0, serve.config(), handle);
}
`)
}

// BinaryBodyContent is the body BinaryBodyServerSource answers.
var BinaryBodyContent = []byte{0x00, 0xFF, 0x80, 0x0A, 0x41}

// CheckBinaryBody drives BinaryBodyServerSource: /bytes and /stream answer
// the body byte for byte under its own length, /chunks answers it byte for
// byte under chunked coding, and HEAD /bytes answers that length with no
// body.
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
	conn := rawRequest(t, addr, "/chunks", "")
	resp := readResponse(t, conn, "/chunks")
	body, _ := io.ReadAll(resp.Body)
	conn.Close()
	if resp.StatusCode != 200 || !bytes.Equal(body, BinaryBodyContent) || len(resp.TransferEncoding) != 1 || resp.TransferEncoding[0] != "chunked" {
		t.Errorf("/chunks: status=%d transfer-encoding=%v body=% x, want 200, chunked and % x", resp.StatusCode, resp.TransferEncoding, body, BinaryBodyContent)
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
