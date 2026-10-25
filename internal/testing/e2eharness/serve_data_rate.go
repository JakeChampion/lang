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

const DataRateResponseBytes = 8 << 20

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

func CheckResponseRateCutsStalledReader(t *testing.T, addr string) {
	t.Helper()
	WaitServerReady(t, addr)
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

func CheckResponseRateKeepsSteadyReader(t *testing.T, addr string) {
	t.Helper()
	WaitServerReady(t, addr)
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
	if total < DataRateResponseBytes {
		t.Fatalf("the steady reader got %d bytes, want the whole %d-byte body and its headers", total, DataRateResponseBytes)
	}
}
