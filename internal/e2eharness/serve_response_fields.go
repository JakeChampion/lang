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

// ResponseFieldsServerSource is a server on `port` whose handler writes the
// request's decoded path into a response header on /echo..., sets a header
// whose name is not a token on /badname, and answers "ok" otherwise.
func ResponseFieldsServerSource(port int) string {
	return fmt.Sprintf(`import "std/http";
import "std/string";
import "std/tcp";
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    if (req.path.starts_with("/echo")) { return http.ok("echoed").with_header("X-Echo", req.path); }
    if (req.path == "/badname") { return http.ok("named").with_header("Bad Name", "v"); }
    return http.ok("ok");
}
function main(): i32 {
    return tcp.tcp_serve(%d, handle);
}
`, port)
}

// CheckResponseFields drives ResponseFieldsServerSource: a path whose decoded
// bytes hold CR LF, echoed into a header, and a header name that is not a
// token are each answered 500 with neither the handler's body nor any field
// it set, and the connection serves the next request.
func CheckResponseFields(t *testing.T, addr string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(conn)
	for _, path := range []string{"/echo%0D%0ASet-Cookie:%20evil=1", "/badname"} {
		if _, err := io.WriteString(conn, "GET "+path+" HTTP/1.1\r\nHost: localhost\r\n\r\n"); err != nil {
			t.Fatal(err)
		}
		resp, err := http.ReadResponse(r, nil)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 500 || len(body) != 0 {
			t.Errorf("%s: status=%d body=%q, want 500 with no body", path, resp.StatusCode, body)
		}
		for name := range resp.Header {
			if n := strings.ToLower(name); n == "set-cookie" || n == "x-echo" || n == "bad name" {
				t.Errorf("%s: the response carries the handler's %q field", path, name)
			}
		}
	}
	if _, err := io.WriteString(conn, "GET /ok HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(r, nil)
	if err != nil {
		t.Fatalf("/ok: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "ok" {
		t.Errorf("/ok after the refused responses: status=%d body=%q", resp.StatusCode, body)
	}
}
