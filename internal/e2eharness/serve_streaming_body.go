package e2eharness

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// StreamingBodyServerSource is a server on `port` whose handler answers
// the file at `path` on /big, five chunks from a producer on /chunks, and
// a producer whose first chunk is empty on /sparse: the bodies the serve
// loop produces as the socket takes them, a file under its length and
// chunks under chunked transfer coding.
func StreamingBodyServerSource(port int, path string) string {
	return fmt.Sprintf(`import "std/http";
import "std/tcp";
import "core/int";

function chunk(i: i32): Option[u8[]] {
    if (i >= 5) { return None; }
    return Some(("chunk " + int.int_to_string(i) + "\n").bytes());
}

function sparse(i: i32): Option[u8[]] {
    if (i == 0) { var none: u8[] = []; return Some(none); }
    if (i == 1) { return Some("after an empty chunk\n".bytes()); }
    return None;
}

function handle(req: HttpRequest, plat: Platform): HttpResponse {
    if (req.path == "/big") { return http.file(%q).with_content_type("application/octet-stream"); }
    if (req.path == "/chunks") { return http.chunks(200, chunk).with_content_type("text/plain"); }
    if (req.path == "/sparse") { return http.chunks(200, sparse); }
    return http.ok("ok");
}
function main(): i32 {
    return tcp.tcp_serve(%d, handle);
}
`, path, port)
}

// StreamingBodyContent is what the file StreamingBodyServerSource serves
// holds: three million bytes, more than a socket buffer takes in one
// write, so the loop produces the body over several waits.
func StreamingBodyContent() []byte {
	out := make([]byte, 3_000_000)
	for i := range out {
		out[i] = byte('a' + (i*7+i/251)%26)
	}
	return out
}

// StreamingBodyChunks is the body /chunks answers, the producer's five
// chunks joined.
const StreamingBodyChunks = "chunk 0\nchunk 1\nchunk 2\nchunk 3\nchunk 4\n"

// CheckStreamingBody drives StreamingBodyServerSource on one keep-alive
// connection: /big answers the file whole under its length, /chunks
// answers the producer's chunks under chunked transfer coding, /sparse
// skips the empty chunk, a HEAD of /chunks answers no body, and /ok
// still answers after them; then, over HTTP/1.0, /chunks is
// close-delimited with no transfer coding and the connection closed
// behind it.
func CheckStreamingBody(t *testing.T, addr string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	conn := rawRequest(t, addr, "/big", "")
	defer conn.Close()
	reader := bufio.NewReader(conn)
	want := StreamingBodyContent()
	resp := readResponseFrom(t, reader, "/big")
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || resp.ContentLength != int64(len(want)) || string(body) != string(want) {
		t.Fatalf("/big: status=%d length=%d got %d bytes (equal: %v)", resp.StatusCode, resp.ContentLength, len(body), string(body) == string(want))
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("/big: Content-Type=%q", ct)
	}
	if _, err := io.WriteString(conn, "GET /chunks HTTP/1.1\r\nHost: localhost\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	resp = readResponseFrom(t, reader, "/chunks")
	body, _ = io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || len(resp.TransferEncoding) != 1 || resp.TransferEncoding[0] != "chunked" || resp.ContentLength != -1 {
		t.Fatalf("/chunks: status=%d transfer-encoding=%v length=%d, want chunked", resp.StatusCode, resp.TransferEncoding, resp.ContentLength)
	}
	if string(body) != StreamingBodyChunks {
		t.Fatalf("/chunks: body %q", body)
	}
	if _, err := io.WriteString(conn, "GET /sparse HTTP/1.1\r\nHost: localhost\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	resp = readResponseFrom(t, reader, "/sparse")
	body, _ = io.ReadAll(resp.Body)
	if string(body) != "after an empty chunk\n" {
		t.Fatalf("/sparse: body %q", body)
	}
	if _, err := io.WriteString(conn, "HEAD /chunks HTTP/1.1\r\nHost: localhost\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	head, err := http.ReadResponse(reader, &http.Request{Method: "HEAD"})
	if err != nil {
		t.Fatalf("HEAD /chunks: %v", err)
	}
	head.Body.Close()
	if head.StatusCode != 200 || len(head.TransferEncoding) != 0 {
		t.Fatalf("HEAD /chunks: status=%d transfer-encoding=%v, want a bare head", head.StatusCode, head.TransferEncoding)
	}
	if _, err := io.WriteString(conn, "GET /ok HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	resp = readResponseFrom(t, reader, "/ok")
	body, _ = io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "ok" {
		t.Fatalf("/ok after the streamed bodies: status=%d body=%q", resp.StatusCode, body)
	}

	old := rawRequestVersion(t, addr, "/chunks", "HTTP/1.0")
	defer old.Close()
	raw, err := io.ReadAll(old)
	if err != nil {
		t.Fatalf("HTTP/1.0 /chunks: %v", err)
	}
	text := string(raw)
	sep := strings.Index(text, "\r\n\r\n")
	if sep < 0 {
		t.Fatalf("HTTP/1.0 /chunks: no header block in %q", text)
	}
	headBlock, tail := strings.ToLower(text[:sep]), text[sep+4:]
	if !strings.HasPrefix(text, "HTTP/1.1 200") || strings.Contains(headBlock, "transfer-encoding") || strings.Contains(headBlock, "content-length") || !strings.Contains(headBlock, "connection: close") {
		t.Fatalf("HTTP/1.0 /chunks: want a close-delimited 200, got head\n%s", text[:sep])
	}
	if tail != StreamingBodyChunks {
		t.Fatalf("HTTP/1.0 /chunks: body %q", tail)
	}
}

// readResponseFrom reads one response off a reader that persists across
// the requests of a connection.
func readResponseFrom(t *testing.T, reader *bufio.Reader, label string) *http.Response {
	t.Helper()
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatalf("%s: %v", label, err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("%s: body: %v", label, err)
	}
	resp.Body = io.NopCloser(strings.NewReader(string(body)))
	return resp
}

// rawRequestVersion is rawRequest with the request's HTTP version chosen.
func rawRequestVersion(t *testing.T, addr, path, version string) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	if _, err := io.WriteString(conn, "GET "+path+" "+version+"\r\nHost: localhost\r\n\r\n"); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	return conn
}
