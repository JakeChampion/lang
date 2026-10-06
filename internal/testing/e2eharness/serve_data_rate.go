package e2eharness

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The minimum data rate on the write side (#9854): a response the peer
// drains below `response_min_data_rate` after `response_data_rate_grace`
// is cut off, and one drained above it goes out whole however long that
// takes. The request body's rate is left at its default, so a drain judged
// by it instead would get its 5 s grace and the stalled reader its body.

// DataRateResponseBytes is the response body's length: larger than the
// loopback socket buffers can hold between them, so the write is short
// and the drain is the peer's to pace.
const DataRateResponseBytes = 8 << 20

// DataRateServerSource is a server answering
// DataRateResponseBytes of body under a 100 KB/s minimum response data
// rate with a 300 ms grace.
func DataRateServerSource() string {
	return fmt.Sprintf(`import "std/http";
import "std/string";
import "std/serve";
import "std/time";
import "std/platform";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    return http.ok("x".repeat(%d));
}
function main(): i32 {
    let opts: serve.Config = serve.Config { ...serve.config(), response_min_data_rate: 100000, response_data_rate_grace: time.duration_millis(300 as i64) };
    return serve.run(0, opts, handle);
}
`, DataRateResponseBytes)
}

// dialSmallWindow connects with a 64 KiB receive buffer, so the peer's
// send is short and what the client reads paces the drain.
func dialSmallWindow(t *testing.T, addr string) net.Conn {
	t.Helper()
	d := net.Dialer{Timeout: 5 * time.Second, Control: func(network, address string, c syscall.RawConn) error {
		var serr error
		err := c.Control(func(fd uintptr) {
			serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF, 64<<10)
		})
		if err != nil {
			return err
		}
		return serr
	}}
	conn, err := d.DialContext(context.Background(), "tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(conn, "GET /big HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	return conn
}

// CheckResponseRateCutsStalledReader asks for the response and reads
// nothing for 2 s: the server, owed progress within the grace, closes
// the connection, so the read that follows ends short of the body.
func CheckResponseRateCutsStalledReader(t *testing.T, addr string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	conn := dialSmallWindow(t, addr)
	defer conn.Close()
	time.Sleep(2 * time.Second)
	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(conn)
	if err != nil && !strings.Contains(err.Error(), "reset") {
		t.Fatalf("reading the cut-off response: %v after %d bytes", err, len(got))
	}
	if len(got) >= DataRateResponseBytes {
		t.Fatalf("the stalled reader was given the whole %d-byte response; want it cut off after the grace", len(got))
	}
}

// CheckResponseRateKeepsSteadyReader reads the response 64 KiB at a
// time with a 40 ms pause between reads, about 1.6 MB/s against the
// 100 KB/s minimum, and must get every byte: a drain above the rate is
// never cut, however long the whole takes.
func CheckResponseRateKeepsSteadyReader(t *testing.T, addr string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	conn := dialSmallWindow(t, addr)
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(60 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64<<10)
	total := 0
	for {
		n, err := conn.Read(buf)
		total += n
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("reading the paced response: %v after %d bytes", err, total)
		}
		time.Sleep(40 * time.Millisecond)
	}
	// The headers are the rest of the bytes; the body must be whole.
	if total < DataRateResponseBytes {
		t.Fatalf("the steady reader got %d bytes, want the whole %d-byte body and its headers", total, DataRateResponseBytes)
	}
}
