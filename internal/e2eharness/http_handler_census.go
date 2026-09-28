package e2eharness

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// HTTPHandlerCensusSource bounds the production accept loop without replacing
// its read, parse, handler or serialization path. The parent owns fd 3.
func HTTPHandlerCensusSource(t *testing.T, root string, rounds int) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "internal/stdlib/std/tcp.fern"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	start := strings.Index(src, "function __serve_loop(")
	if start < 0 {
		t.Fatal("production accept loop not found")
	}
	end := strings.Index(src[start:], "\n// __serve_loop_with")
	if end < 0 {
		t.Fatal("production accept loop boundary not found")
	}
	end += start
	body := src[start:end]
	const answered = "more = answered.1;"
	if strings.Count(body, "while (true)") != 1 || strings.Count(body, answered) != 1 {
		t.Fatal("accept loop changed; update its bounded census fixture")
	}
	// The loop runs until the bound is met AND every connection is gone: a
	// persistent connection outlives its response until the client's close
	// arrives, and on wasm an open connection owns a heap record, so a
	// loop that stopped on the count alone left the last one to the exit
	// sweep and the census unbalanced.
	body = strings.Replace(body, "while (true)", fmt.Sprintf("var completed: i32 = 0;\n    while (completed < %d || conns.fds.len() > 0)", rounds), 1)
	body = strings.Replace(body, answered, answered+"\n                        completed = completed + 1;", 1)
	return src[:start] + body + src[end:] + `
function census_handle(req: HttpRequest, plat: Platform): HttpResponse {
    return http.http_response_ok("ok");
}
function main(): i32 {
    return __serve_loop(3, (req: HttpRequest, plat: Platform): HttpResponse => census_handle(req, plat), ServeOptions { ...serve_options(), keep_alive_requests: 4 });
}
`
}

// HTTPKeepAliveRequests drives `rounds` requests through one bounded serve
// loop over as few connections as its per-connection cap of 4 allows:
// pipelined pairs on a connection that persists, HTTP/1.0 and
// `Connection: close` requests that end theirs, and the cap's fourth
// response saying close. Every response is checked, and every close the
// server owes is read as EOF.
func HTTPKeepAliveRequests(t *testing.T, addr string, rounds int) {
	t.Helper()
	dial := func() net.Conn {
		conn, err := net.DialTimeout("tcp4", addr, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
			conn.Close()
			t.Fatal(err)
		}
		return conn
	}
	// Go's reader folds a `Connection: close` header into resp.Close and
	// removes it, so the close case is read from the flag and the
	// keep-alive case from the header it leaves in place.
	read := func(conn net.Conn, r *bufio.Reader, label string, wantConnection string) {
		t.Helper()
		resp, err := http.ReadResponse(r, nil)
		if err != nil {
			conn.Close()
			t.Fatalf("%s: %v", label, err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != 200 || resp.ContentLength != 2 || string(body) != "ok" {
			conn.Close()
			t.Fatalf("%s: status=%d length=%d body=%q error=%v", label, resp.StatusCode, resp.ContentLength, body, err)
		}
		got := resp.Header.Get("Connection")
		if resp.Close {
			got = "close"
		}
		if got != wantConnection {
			conn.Close()
			t.Fatalf("%s: Connection=%q, want %q", label, got, wantConnection)
		}
	}
	eof := func(conn net.Conn, r *bufio.Reader, label string) {
		t.Helper()
		if _, err := r.ReadByte(); err != io.EOF {
			conn.Close()
			t.Fatalf("%s: the server did not close the connection: %v", label, err)
		}
		conn.Close()
	}
	sent := 0
	for sent < rounds {
		// Four requests on one connection: a pipelined pair, a third, and a
		// fourth that reaches the cap and is answered with close.
		conn := dial()
		r := bufio.NewReader(conn)
		if _, err := io.WriteString(conn, "GET /a HTTP/1.1\r\nHost: localhost\r\n\r\nGET /b HTTP/1.1\r\nHost: localhost\r\n\r\n"); err != nil {
			conn.Close()
			t.Fatal(err)
		}
		read(conn, r, "pipelined first", "keep-alive")
		read(conn, r, "pipelined second", "keep-alive")
		if _, err := io.WriteString(conn, "GET /c HTTP/1.0\r\nHost: localhost\r\nConnection: keep-alive\r\n\r\n"); err != nil {
			conn.Close()
			t.Fatal(err)
		}
		read(conn, r, "HTTP/1.0 keep-alive", "keep-alive")
		if _, err := io.WriteString(conn, "GET /d HTTP/1.1\r\nHost: localhost\r\n\r\n"); err != nil {
			conn.Close()
			t.Fatal(err)
		}
		read(conn, r, "the fourth, at the cap", "close")
		eof(conn, r, "after the cap")
		sent += 4
		if sent >= rounds {
			break
		}
		// One HTTP/1.0 request with no Connection header, closed after it.
		conn = dial()
		r = bufio.NewReader(conn)
		if _, err := io.WriteString(conn, "GET /e HTTP/1.0\r\nHost: localhost\r\n\r\n"); err != nil {
			conn.Close()
			t.Fatal(err)
		}
		read(conn, r, "HTTP/1.0", "close")
		eof(conn, r, "after HTTP/1.0")
		sent++
		if sent >= rounds {
			break
		}
		// One HTTP/1.1 request asking for close, closed after it.
		conn = dial()
		r = bufio.NewReader(conn)
		if _, err := io.WriteString(conn, "GET /f HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"); err != nil {
			conn.Close()
			t.Fatal(err)
		}
		read(conn, r, "Connection: close", "close")
		eof(conn, r, "after Connection: close")
		sent++
	}
}

// RunHTTPKeepAlive is RunHTTPHandlerCensus with HTTPKeepAliveRequests as
// the client, over a loop bounded to a multiple of six requests.
func RunHTTPKeepAlive(t *testing.T, command *exec.Cmd, rounds int) string {
	t.Helper()
	return runBoundedHTTPServer(t, command, func(addr string) { HTTPKeepAliveRequests(t, addr, rounds) })
}

func RunHTTPHandlerCensus(t *testing.T, command *exec.Cmd, rounds int) string {
	t.Helper()
	return runBoundedHTTPServer(t, command, func(addr string) { httpHandlerCensusRequests(t, addr, rounds) })
}

// runBoundedHTTPServer runs a bounded serve loop over a listener the parent
// owns as fd 3, drives `client` against it and returns the server's output
// once the loop has answered its bound and exited.
func runBoundedHTTPServer(t *testing.T, command *exec.Cmd, client func(addr string)) string {
	t.Helper()
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	file, err := listener.File()
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, command.Path, command.Args[1:]...)
	cmd.Env, cmd.Dir = command.Env, command.Dir
	cmd.ExtraFiles = append(cmd.ExtraFiles, file)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			cancel()
			_ = cmd.Wait()
			t.Logf("server output: %s", &out)
		}
	}()
	client(listener.Addr().String())
	err = cmd.Wait()
	waited = true
	if err != nil {
		t.Fatalf("bounded HTTP server: %v\n%s", err, &out)
	}
	return out.String()
}

func httpHandlerCensusRequests(t *testing.T, addr string, rounds int) {
	t.Helper()
	for i := 0; i < rounds; i++ {
		conn, err := net.DialTimeout("tcp4", addr, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		err = conn.SetDeadline(time.Now().Add(5 * time.Second))
		if err == nil {
			_, err = io.WriteString(conn, "GET /hello HTTP/1.1\r\nHost: localhost\r\n\r\n")
		}
		if err != nil {
			conn.Close()
			t.Fatal(err)
		}
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			conn.Close()
			t.Fatalf("request %d: %v", i, err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		conn.Close()
		if err != nil || resp.StatusCode != 200 || resp.Proto != "HTTP/1.1" || resp.ContentLength != 2 || string(body) != "ok" {
			t.Fatalf("request %d: status=%d proto=%s length=%d body=%q error=%v", i, resp.StatusCode, resp.Proto, resp.ContentLength, body, err)
		}
	}
}

func WasiHTTPHandlerCensusSource(t *testing.T, root string, rounds int) string {
	t.Helper()
	src := HTTPHandlerCensusSource(t, root, rounds)
	const original = "    return __serve_loop(3, (req: HttpRequest, plat: Platform): HttpResponse => census_handle(req, plat), ServeOptions { ...serve_options(), keep_alive_requests: 4 });"
	if strings.Count(src, original) != 1 {
		t.Fatal("bounded HTTP entry changed")
	}
	const entry = `    var listener: i32 = tcp_listen(0);
    if (listener < 0) { return 90; }
    var port: i32 = tcp_local_port(listener);
    if (port <= 0) { return 91; }
    print(int.int_to_string(port));
    var result: i32 = __serve_loop(listener, (req: HttpRequest, plat: Platform): HttpResponse => census_handle(req, plat), ServeOptions { ...serve_options(), keep_alive_requests: 4 });
    if (tcp_close(listener) != 0) { return 92; }
    return result;`
	return strings.Replace(src, original, entry, 1)
}

func RunWasiHTTPHandlerCensus(t *testing.T, component string, rounds int) string {
	t.Helper()
	return runBoundedWasiHTTPServer(t, component, func(addr string) { httpHandlerCensusRequests(t, addr, rounds) })
}

// RunWasiHTTPKeepAlive is RunWasiHTTPHandlerCensus with HTTPKeepAliveRequests
// as the client.
func RunWasiHTTPKeepAlive(t *testing.T, component string, rounds int) string {
	t.Helper()
	return runBoundedWasiHTTPServer(t, component, func(addr string) { HTTPKeepAliveRequests(t, addr, rounds) })
}

func runBoundedWasiHTTPServer(t *testing.T, component string, client func(addr string)) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "wasmtime", "run", "-S", "inherit-network", component)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			cancel()
			_ = cmd.Wait()
			t.Logf("server stderr: %s", &stderr)
		}
	}()
	reader := bufio.NewReader(stdout)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("server port: %v", err)
	}
	port, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || port <= 0 || port > 65535 {
		t.Fatalf("invalid server port %q: %v", line, err)
	}
	client(fmt.Sprintf("127.0.0.1:%d", port))
	rest, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(bytes.TrimSpace(rest)) != 0 {
		t.Fatalf("unexpected server stdout: %s", rest)
	}
	err = cmd.Wait()
	waited = true
	if err != nil {
		t.Fatalf("bounded WASI server: %v\n%s", err, &stderr)
	}
	return stderr.String()
}
