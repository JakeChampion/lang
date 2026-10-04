package e2eharness

import (
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// PerIPCapServerSource is a supervised server with one worker that lets a
// client hold two connections (`max_connections_per_ip: 2`).
func PerIPCapServerSource(port int) string {
	return fmt.Sprintf(`import "std/http";
import "std/serve";
import "std/platform";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    return http.ok("ok");
}
function main(): i32 {
    return serve.supervise(%d, serve.Config { ...serve.config(), workers: 1, max_connections_per_ip: 2 }, handle);
}
`, port)
}

// CheckPerIPCap drives PerIPCapServerSource: two idle connections from
// this host fill its cap, a third is closed as it is accepted with no
// response, and once one of the two closes the next connection is
// served; the other of the two still answers, since the cap counted
// rather than closed it.
func CheckPerIPCap(t *testing.T, addr string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	held := make([]net.Conn, 0, 2)
	for n := 0; n < 2; n++ {
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		held = append(held, conn)
	}
	// The loop accepts each before the next is dialled only in order;
	// a short wait lets both land in its table.
	time.Sleep(200 * time.Millisecond)

	third, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer third.Close()
	_, _ = io.WriteString(third, "GET /ok HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n")
	if err := third.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(third)
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatalf("the third connection of one client stayed open past the cap of 2: %q", b)
	}
	if strings.Contains(string(b), "HTTP/1.1") {
		t.Fatalf("the third connection of one client was answered past the cap of 2:\n%s", b)
	}

	held[0].Close()
	time.Sleep(200 * time.Millisecond)
	if resp := HTTPRoundTrip(t, addr, "/ok", 5*time.Second); !ContainsStatus200(resp) {
		t.Fatalf("a connection after one of the two closed was not served:\n%s", resp)
	}
	if _, err := io.WriteString(held[1], "GET /ok HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	if err := held[1].SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if b, _ := io.ReadAll(held[1]); !ContainsStatus200(string(b)) {
		t.Fatalf("the second of the two held connections was not answered:\n%s", b)
	}
}
