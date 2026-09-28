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
	const async = "import \"std/async\";"
	if strings.Count(src, async) != 1 {
		t.Fatal("std/tcp's imports changed; update its bounded census fixture")
	}
	src = strings.Replace(src, async, "import \"std/platform\";\n"+async, 1)
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
	// A handler may not sleep around its Platform bag (E080), so the slow
	// paths burn pure work: 15M steps is 24 ms on wasm, 52 ms on x86-64
	// and 69 ms under qemu, so a burst of 32 outlasts the 300 ms read
	// deadline everywhere; /gone burns five times that, the window in
	// which the client resets the connection.
	return src[:start] + body + src[end:] + `
function census_burn(n: i32): i32 {
    var x: i32 = 12345;
    var i: i32 = 0;
    while (i < n) {
        x = (x * 1103515245 + 12345) & 2147483647;
        i = i + 1;
    }
    return x;
}
function census_handle(req: HttpRequest, plat: Platform): HttpResponse {
    var work: i32 = 0;
    if (req.path.starts_with("/slow")) { work = 15000000; }
    if (req.path.starts_with("/gone")) { work = 75000000; }
    if (req.path == "/behind-a-failed-write") { plat.log("answered /behind-a-failed-write"); }
    if (census_burn(work) == 0 - 1) { return http.http_response_ok("never"); }
    return http.http_response_ok("ok");
}
function main(): i32 {
    return __serve_loop(3, (req: HttpRequest, plat: Platform): HttpResponse => census_handle(req, plat), ServeOptions { ...serve_options(), recv_deadline: time.duration_millis(300 as i64), keep_alive_requests: 200 });
}
`
}

// KeepAliveCycle is how many requests one pass of HTTPKeepAliveRequests
// sends; a bounded loop driven by it is bounded to a multiple.
const KeepAliveCycle = 274

// HTTPKeepAliveRequests drives `rounds` requests through one bounded serve
// loop whose per-connection cap is 200 and whose request read deadline is
// 300 ms: a pipeline of 200 requests on one connection, answered in
// order across several waits and closed by the cap; HTTP/1.0 and
// `Connection: close` requests that end theirs; a pipeline of 33 requests
// whose peer half-closes behind them, one more than the loop's burst so
// the last is answered from the backlog after the end of stream has been
// read, and must say close; a complete request followed by the start of
// another, whose connection the read deadline closes; a pipeline of 33
// requests whose handlers together outlast that deadline, all of which
// must be answered; a pipelined request answered to a peer that has
// reset the connection, whose failed write must close it before the
// request behind it is answered; a request with a malformed one pipelined
// behind it, answered and then closed with no response to the second; a
// request of 101 header fields, refused before any handler sees it; and
// an HTTP/1.0 keep-alive request followed by an HTTP/1.1 one on the same
// connection. Every response is checked, and every close the server owes
// is read as EOF.
func HTTPKeepAliveRequests(t *testing.T, addr string, rounds int) {
	t.Helper()
	if rounds%KeepAliveCycle != 0 {
		t.Fatalf("rounds = %d, want a multiple of %d", rounds, KeepAliveCycle)
	}
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
	write := func(conn net.Conn, s string) {
		t.Helper()
		if _, err := io.WriteString(conn, s); err != nil {
			conn.Close()
			t.Fatal(err)
		}
	}
	halfClose := func(conn net.Conn) {
		t.Helper()
		if err := conn.(*net.TCPConn).CloseWrite(); err != nil {
			conn.Close()
			t.Fatal(err)
		}
	}
	for sent := 0; sent < rounds; sent += KeepAliveCycle {
		// 200 requests pipelined in one write: more than one event's burst,
		// so the rest are answered from the backlog on later waits, and the
		// 200th reaches the cap and is answered with close.
		conn := dial()
		r := bufio.NewReader(conn)
		var pipeline strings.Builder
		for i := 0; i < 200; i++ {
			fmt.Fprintf(&pipeline, "GET /p%d HTTP/1.1\r\nHost: localhost\r\n\r\n", i)
		}
		write(conn, pipeline.String())
		for i := 0; i < 199; i++ {
			read(conn, r, fmt.Sprintf("pipelined %d", i), "keep-alive")
		}
		read(conn, r, "the 200th, at the cap", "close")
		eof(conn, r, "after the cap")
		// One HTTP/1.0 request with no Connection header, closed after it.
		conn = dial()
		r = bufio.NewReader(conn)
		write(conn, "GET /e HTTP/1.0\r\nHost: localhost\r\n\r\n")
		read(conn, r, "HTTP/1.0", "close")
		eof(conn, r, "after HTTP/1.0")
		// One HTTP/1.1 request asking for close, closed after it.
		conn = dial()
		r = bufio.NewReader(conn)
		write(conn, "GET /f HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n")
		read(conn, r, "Connection: close", "close")
		eof(conn, r, "after Connection: close")
		// A pipeline of 33 requests whose peer half-closes behind them. The
		// server may or may not read the end of stream with the first 32
		// (that is a race on the wire), but the 33rd is answered from the
		// backlog on a later wait, once the end of stream has certainly
		// arrived, so its response must say close, since the close follows
		// it; the 32 before it persist either way.
		conn = dial()
		r = bufio.NewReader(conn)
		pipeline.Reset()
		for i := 0; i < 33; i++ {
			fmt.Fprintf(&pipeline, "GET /q%d HTTP/1.1\r\nHost: localhost\r\n\r\n", i)
		}
		write(conn, pipeline.String())
		halfClose(conn)
		for i := 0; i < 32; i++ {
			read(conn, r, fmt.Sprintf("half-closed pipeline %d", i), "keep-alive")
		}
		read(conn, r, "half-closed pipeline, last", "close")
		eof(conn, r, "after the half-closed pipeline")
		// A complete request with the start of another behind it: answered,
		// then closed by the 300 ms read deadline the partial request waits
		// under, not left waiting under the idle span.
		conn = dial()
		r = bufio.NewReader(conn)
		write(conn, "GET /j HTTP/1.1\r\nHost: localhost\r\n\r\nGET /k HT")
		read(conn, r, "before a partial request", "keep-alive")
		started := time.Now()
		eof(conn, r, "the partial request's deadline")
		if waited := time.Since(started); waited > 4*time.Second {
			t.Fatalf("the partial request was closed after %v, not by the 300 ms read deadline", waited)
		}
		// A pipeline of 33 requests whose handlers each burn tens of
		// milliseconds: the event's burst of 32 alone outlasts the 300 ms
		// read deadline their first byte armed, and the 33rd is answered
		// from the backlog on the next wait, so all 33 answered proves a
		// connection with a request still to answer is not closed by that
		// deadline.
		conn = dial()
		r = bufio.NewReader(conn)
		pipeline.Reset()
		for i := 0; i < 33; i++ {
			fmt.Fprintf(&pipeline, "GET /slow%d HTTP/1.1\r\nHost: localhost\r\n\r\n", i)
		}
		write(conn, pipeline.String())
		for i := 0; i < 33; i++ {
			read(conn, r, fmt.Sprintf("slow pipeline %d", i), "keep-alive")
		}
		conn.Close()
		// A request, a slow one and a third behind it in one write; the
		// peer resets the connection once the first is answered, so the
		// second response is written to a peer that is gone. That write
		// fails, and the loop must close the connection rather than record
		// the response as delivered and answer the third: the handler
		// reports the third on stderr, which RunHTTPKeepAlive refuses.
		conn = dial()
		r = bufio.NewReader(conn)
		write(conn, "GET /n HTTP/1.1\r\nHost: localhost\r\n\r\nGET /gone HTTP/1.1\r\nHost: localhost\r\n\r\nGET /behind-a-failed-write HTTP/1.1\r\nHost: localhost\r\n\r\n")
		read(conn, r, "before the reset", "keep-alive")
		if err := conn.(*net.TCPConn).SetLinger(0); err != nil {
			conn.Close()
			t.Fatal(err)
		}
		conn.Close()
		// A request with a malformed one pipelined behind it (a bare LF ends
		// its request line): the first is answered, then the connection is
		// closed with no response to the second, since a parser with no
		// lenient mode refuses it (RFC 9112 §2.2) rather than read on.
		conn = dial()
		r = bufio.NewReader(conn)
		write(conn, "GET /o HTTP/1.1\r\nHost: localhost\r\n\r\nGET /bare HTTP/1.1\nHost: localhost\r\n\r\n")
		read(conn, r, "before a malformed request", "keep-alive")
		started = time.Now()
		eof(conn, r, "after a malformed request")
		// The read deadline is 300 ms, so a close this soon is the parse's,
		// not the deadline's: the loop does not hold a malformed connection
		// open waiting for bytes that could never complete it.
		if waited := time.Since(started); waited > 250*time.Millisecond {
			t.Fatalf("a malformed request was closed after %v, by the read deadline rather than the parse", waited)
		}
		// 101 header fields, one past the cap: refused before any handler
		// sees the request, so the connection closes with no response.
		conn = dial()
		r = bufio.NewReader(conn)
		pipeline.Reset()
		pipeline.WriteString("GET /many HTTP/1.1\r\nHost: localhost\r\n")
		for i := 0; i < 100; i++ {
			fmt.Fprintf(&pipeline, "X-%d: %d\r\n", i, i)
		}
		pipeline.WriteString("\r\n")
		write(conn, pipeline.String())
		eof(conn, r, "a request with 101 header fields")
		// An HTTP/1.0 request asking to keep the connection, then an
		// HTTP/1.1 request on it; the client ends this one.
		conn = dial()
		r = bufio.NewReader(conn)
		write(conn, "GET /l HTTP/1.0\r\nHost: localhost\r\nConnection: keep-alive\r\n\r\n")
		read(conn, r, "HTTP/1.0 keep-alive", "keep-alive")
		write(conn, "GET /m HTTP/1.1\r\nHost: localhost\r\n\r\n")
		read(conn, r, "after HTTP/1.0 keep-alive", "keep-alive")
		conn.Close()
	}
}

// RunHTTPKeepAlive is RunHTTPHandlerCensus with HTTPKeepAliveRequests as
// the client, over a loop bounded to a multiple of KeepAliveCycle.
func RunHTTPKeepAlive(t *testing.T, command *exec.Cmd, rounds int) string {
	t.Helper()
	return keepAliveOutput(t, runBoundedHTTPServer(t, command, func(addr string) { HTTPKeepAliveRequests(t, addr, rounds) }))
}

// keepAliveOutput is the server's output once it has been checked for the
// request the fixture's handler must never see: the one pipelined behind
// a response whose write the peer's reset refused.
func keepAliveOutput(t *testing.T, out string) string {
	t.Helper()
	if strings.Contains(out, "answered /behind-a-failed-write") {
		t.Fatalf("the loop answered a request behind a failed write instead of closing the connection:\n%s", out)
	}
	return out
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
	const original = "    return __serve_loop(3, (req: HttpRequest, plat: Platform): HttpResponse => census_handle(req, plat), ServeOptions { ...serve_options(), recv_deadline: time.duration_millis(300 as i64), keep_alive_requests: 200 });"
	if strings.Count(src, original) != 1 {
		t.Fatal("bounded HTTP entry changed")
	}
	const entry = `    var listener: i32 = tcp_listen(0);
    if (listener < 0) { return 90; }
    var port: i32 = tcp_local_port(listener);
    if (port <= 0) { return 91; }
    print(int.int_to_string(port));
    var result: i32 = __serve_loop(listener, (req: HttpRequest, plat: Platform): HttpResponse => census_handle(req, plat), ServeOptions { ...serve_options(), recv_deadline: time.duration_millis(300 as i64), keep_alive_requests: 200 });
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
	return keepAliveOutput(t, runBoundedWasiHTTPServer(t, component, func(addr string) { HTTPKeepAliveRequests(t, addr, rounds) }))
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
			werr := cmd.Wait()
			t.Logf("server exit: %v; stderr: %s", werr, &stderr)
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
