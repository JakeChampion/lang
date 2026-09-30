package e2eharness

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// FileBodyServerSource is a server on `port` whose handler answers the
// file at `path` on /file and a file that does not exist on /missing,
// both through `file`: the handler names the file and the
// serve loop reads it as it writes the response, or answers 404 in its
// place.
func FileBodyServerSource(port int, path string) string {
	return fmt.Sprintf(`import "std/http";
import "std/tcp";
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    if (req.path == "/file") { return http.file(%q).with_content_type("text/plain"); }
    if (req.path == "/missing") { return http.file(%q + ".missing"); }
    return http.ok("ok");
}
function main(): i32 {
    return tcp.tcp_serve(%d, handle);
}
`, path, path, port)
}

// FileBodyContent is what the file FileBodyServerSource serves holds.
const FileBodyContent = "served from a file\nline two\n"

// CheckFileBody drives FileBodyServerSource: /file answers 200 with the
// file's bytes under their length and the handler's content type,
// /missing answers 404, and /ok still answers on the same connection
// after a file body, since the loop read the file and moved on.
func CheckFileBody(t *testing.T, addr string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	conn := rawRequest(t, addr, "/file", "")
	defer conn.Close()
	resp := readResponse(t, conn, "/file")
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != FileBodyContent || resp.ContentLength != int64(len(FileBodyContent)) {
		t.Fatalf("/file: status=%d length=%d body=%q", resp.StatusCode, resp.ContentLength, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/plain" {
		t.Fatalf("/file: Content-Type=%q, want text/plain", ct)
	}
	if _, err := io.WriteString(conn, "GET /missing HTTP/1.1\r\nHost: localhost\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	resp = readResponse(t, conn, "/missing")
	body, _ = io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(string(body), "Not Found") {
		t.Fatalf("/missing: status=%d body=%q, want 404", resp.StatusCode, body)
	}
	if _, err := io.WriteString(conn, "GET /ok HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	resp = readResponse(t, conn, "/ok")
	body, _ = io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "ok" {
		t.Fatalf("/ok after a file body: status=%d body=%q", resp.StatusCode, body)
	}
}
