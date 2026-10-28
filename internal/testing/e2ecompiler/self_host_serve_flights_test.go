package e2ecompiler

import (
	"bufio"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const serveFlightsSource = `import "std/http";
import "std/serve";
import "std/platform";
import "std/fetch";
function handle(req: HttpRequest, plat: platform.Platform): Result[HttpResponse, fetch.FetchError] {
    let first: HttpResponse = plat.http(fetch.get("http://8.8.8.8" + req.path + "a"))?;
    let second: HttpResponse = plat.http(fetch.get("http://8.8.8.8" + req.path + "b"))?;
    return Ok(http.ok(first.body_string() + second.body_string()));
}
function main(): i32 {
    return serve.exit_code(serve.run(0, serve.Config { ...serve.config().with_h2(), max_connections_per_ip: 0 }, handle));
}
`

// Each handler waits on two upstream answers that arrive in random order, so
// the flight table adds, finds and removes flights in an order no request
// controls. A flight found for the wrong key answers another request's body.
func TestSelfHostServeFlightTable(t *testing.T) {
	const conns, h2conns, h2streams = 400, 8, 16
	cli := buildSelfHostCLI(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(time.Duration(rand.Intn(15)) * time.Millisecond)
		io.WriteString(w, strings.TrimPrefix(r.URL.Path, "/")+";")
	}))
	t.Cleanup(up.Close)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(serveFlightsSource), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := runX86_64Bin(cli.runner, cli.x86Binary(t, src, "FERN_STRICT_IR=1"))
	cmd.Env = append(os.Environ(), "http_proxy=http://u@"+up.Listener.Addr().String())
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.WaitServerReady(t, addr)

	want := func(path string) string { return path[1:] + "a;" + path[1:] + "b;" }
	errs := make(chan error, conns+h2conns)
	var wg sync.WaitGroup
	for c := 0; c < conns; c++ {
		conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func(c int) {
			defer wg.Done()
			defer conn.Close()
			errs <- flightsHTTP1(conn, c, want)
		}(c)
	}
	for c := 0; c < h2conns; c++ {
		client, err := e2eharness.DialH2C(addr, 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func(c int) {
			defer wg.Done()
			defer client.Close()
			errs <- flightsH2(client, c, h2streams, want)
		}(c)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
}

func flightsHTTP1(conn net.Conn, c int, want func(string) string) error {
	conn.SetDeadline(time.Now().Add(2 * time.Minute))
	r := bufio.NewReader(conn)
	for i := 0; i < 3; i++ {
		path := fmt.Sprintf("/c%dr%d", c, i)
		if _, err := fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: localhost\r\n\r\n", path); err != nil {
			return err
		}
		resp, err := http.ReadResponse(r, nil)
		if err != nil {
			return fmt.Errorf("%s: %v", path, err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return fmt.Errorf("%s: %v", path, err)
		}
		if got := string(body); resp.StatusCode != 200 || got != want(path) {
			return fmt.Errorf("%s answered %d %q, want 200 %q", path, resp.StatusCode, got, want(path))
		}
	}
	return nil
}

func flightsH2(client *e2eharness.H2CClient, c, streams int, want func(string) string) error {
	client.SetDeadline(time.Now().Add(2 * time.Minute))
	ids := make([]uint32, streams)
	for s := range ids {
		var err error
		if ids[s], err = client.Open(fmt.Sprintf("/h%ds%d", c, s)); err != nil {
			return err
		}
	}
	for s, id := range ids {
		path := fmt.Sprintf("/h%ds%d", c, s)
		body, err := client.Await(id)
		if err != nil {
			return fmt.Errorf("%s: %v", path, err)
		}
		if body != want(path) {
			return fmt.Errorf("%s answered %q, want %q", path, body, want(path))
		}
	}
	return nil
}
