package e2eharness

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
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
	// paths burn pure work: 12M steps is 19 ms on wasm, 42 ms on x86-64
	// and 55 ms under qemu, so a burst of 32 outlasts the 300 ms read
	// deadline everywhere, and 33 of them stay near a second on the
	// slowest backend; /gone burns five times that, the window in which
	// the client resets the connection.
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
    if (req.path == "/chunked") { return http.http_response_ok(req.body_string()); }
    if (req.path == "/nocontent") { return http.http_response_no_content(); }
    if (req.path == "/expect") { return http.http_response_ok(req.body_string()); }
    var work: i32 = 0;
    if (req.path.starts_with("/slow")) { work = 12000000; }
    if (req.path.starts_with("/gone")) { work = 60000000; }
    if (req.path == "/behind-a-failed-write") { plat.log("answered /behind-a-failed-write"); }
    if (census_burn(work) == 0 - 1) { return http.http_response_ok("never"); }
    return http.http_response_ok("ok");
}
function main(): i32 {
    return __serve_loop(3, (req: HttpRequest, plat: Platform): HttpResponse => census_handle(req, plat), ServeOptions { ...serve_options(), recv_deadline: time.duration_millis(300 as i64), data_rate_grace: time.duration_millis(100 as i64), keep_alive_requests: 200, max_connections: 2 });
}
`
}

// KeepAliveCycle is how many requests one pass of HTTPKeepAliveRequests
// sends; a bounded loop driven by it is bounded to a multiple.
const KeepAliveCycle = 284

// HTTPKeepAliveRequests drives `rounds` requests through one bounded serve
// loop whose per-connection cap is 200, whose request read deadline is
// 300 ms and which holds two connections open at most: a pipeline of 200 requests on one connection, answered in
// order across several waits and closed by the cap; HTTP/1.0 and
// `Connection: close` requests that end theirs; a pipeline of 33 requests
// whose peer half-closes behind them, one more than the loop's burst so
// the last is answered from the backlog on a later wait, all of which
// must be answered before the connection closes; a complete request
// followed by the start of another, whose connection the read deadline
// closes; a pipeline of 33 requests whose handlers together outlast that
// deadline, all of which must be answered, and whose peer half-closes
// behind them, so the last, answered after the end of stream has been
// read, must say close; a pipelined request answered to a peer that has
// reset the connection, whose failed write must close it before the
// request behind it is answered; a request with a malformed one pipelined
// behind it, answered with close and then closed with no response to the
// second; a request of 101 header fields, answered 431 and closed before
// any handler sees it, an HTTP/1.1 request without a Host, answered 400,
// and a body past the cap, answered 413 before the body arrives; a
// chunked request with a request pipelined behind it, whose decoded
// body the handler echoes and whose framing must leave exactly the second
// request to answer; a HEAD with a GET pipelined behind it, answered
// with the Content-Length and none of the body, then a 204, answered
// with neither; a request whose header block says `Expect: 100-continue`,
// answered `100 Continue` before its body is sent and then with the
// body's echo, and one whose expectation the server cannot meet,
// answered 417; an HTTP/1.0 keep-alive request followed by an HTTP/1.1
// one on the same connection; a body delivered in pieces over twice the
// read deadline but well above the minimum body rate (the loop's grace
// is 100 ms), answered with its echo, and one trickled below the rate,
// closed at the read deadline without a response; and two connections
// answered and held open, then a third whose request goes unanswered
// until one of the two closes. Every response is checked, its `Date`
// included, and every close the server owes is read as EOF.
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
	// dated checks the `Date` every response carries (RFC 9110 §6.6.1).
	dated := func(conn net.Conn, resp *http.Response, label string) {
		t.Helper()
		if _, err := http.ParseTime(resp.Header.Get("Date")); err != nil {
			conn.Close()
			t.Fatalf("%s: Date=%q: %v; headers=%v", label, resp.Header.Get("Date"), err, resp.Header)
		}
	}
	readBody := func(conn net.Conn, r *bufio.Reader, label string, wantBody string, wantConnection ...string) {
		t.Helper()
		resp, err := http.ReadResponse(r, nil)
		if err != nil {
			conn.Close()
			t.Fatalf("%s: %v", label, err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != 200 || resp.ContentLength != int64(len(wantBody)) || string(body) != wantBody {
			conn.Close()
			t.Fatalf("%s: status=%d length=%d body=%q error=%v, want %q", label, resp.StatusCode, resp.ContentLength, body, err, wantBody)
		}
		dated(conn, resp, label)
		got := resp.Header.Get("Connection")
		if resp.Close {
			got = "close"
		}
		if !slices.Contains(wantConnection, got) {
			conn.Close()
			t.Fatalf("%s: Connection=%q, want one of %q", label, got, wantConnection)
		}
	}
	read := func(conn net.Conn, r *bufio.Reader, label string, wantConnection ...string) {
		t.Helper()
		readBody(conn, r, label, "ok", wantConnection...)
	}
	// refused checks the loop's own answer to a malformed request: the
	// status, an empty body and `Connection: close`.
	refused := func(conn net.Conn, r *bufio.Reader, label string, wantStatus int) {
		t.Helper()
		resp, err := http.ReadResponse(r, nil)
		if err != nil {
			conn.Close()
			t.Fatalf("%s: %v", label, err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != wantStatus || resp.ContentLength != 0 || len(body) != 0 || !resp.Close {
			conn.Close()
			t.Fatalf("%s: status=%d length=%d body=%q close=%v error=%v, want %d, empty, close", label, resp.StatusCode, resp.ContentLength, body, resp.Close, err, wantStatus)
		}
		dated(conn, resp, label)
	}
	// interim reads the `100 Continue` the loop sends before a body.
	interim := func(conn net.Conn, r *bufio.Reader, label string) {
		t.Helper()
		line, err := r.ReadString('\n')
		if err != nil || line != "HTTP/1.1 100 Continue\r\n" {
			conn.Close()
			t.Fatalf("%s: %q, %v", label, line, err)
		}
		if line, err = r.ReadString('\n'); err != nil || line != "\r\n" {
			conn.Close()
			t.Fatalf("%s: after the status line: %q, %v", label, line, err)
		}
	}
	// bodiless checks a response that carries no body: to a HEAD, with
	// the Content-Length the body would have, or a 204, with none.
	bodiless := func(conn net.Conn, r *bufio.Reader, label string, method string, wantStatus int, wantLength string) {
		t.Helper()
		resp, err := http.ReadResponse(r, &http.Request{Method: method})
		if err != nil {
			conn.Close()
			t.Fatalf("%s: %v", label, err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != wantStatus || len(body) != 0 || resp.Header.Get("Content-Length") != wantLength || resp.Close {
			conn.Close()
			t.Fatalf("%s: status=%d Content-Length=%q body=%q close=%v error=%v, want %d, %q, empty, keep-alive", label, resp.StatusCode, resp.Header.Get("Content-Length"), body, resp.Close, err, wantStatus, wantLength)
		}
		dated(conn, resp, label)
	}
	eof := func(conn net.Conn, r *bufio.Reader, label string) {
		t.Helper()
		if _, err := r.ReadByte(); err != io.EOF {
			conn.Close()
			t.Fatalf("%s: the server did not close the connection: %v", label, err)
		}
		conn.Close()
	}
	// A close with input still unread in the server's socket is a reset
	// on Darwin, where Linux delivers EOF; either is the close.
	closed := func(conn net.Conn, r *bufio.Reader, label string) {
		t.Helper()
		if _, err := r.ReadByte(); err != io.EOF && !errors.Is(err, syscall.ECONNRESET) {
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
		// write and the half-close are separate calls, so the server may
		// answer even the 33rd before the end of stream arrives: that one
		// may say either, but the connection must end straight after it.
		// The slow pipeline below pins the close, with the race settled.
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
		read(conn, r, "half-closed pipeline, last", "close", "keep-alive")
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
		// deadline. The 33 answers are a second of the server's CPU on the
		// slowest backend, and a two-CPU runner shares that CPU with a
		// second worker, so this connection waits longer than the others.
		// The peer half-closes behind the pipeline: the 32 slow answers
		// leave the end of stream long arrived by the time the 33rd is
		// read from the backlog, so its response must say close, and the
		// close must follow it.
		conn = dial()
		if err := conn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
			conn.Close()
			t.Fatal(err)
		}
		r = bufio.NewReader(conn)
		pipeline.Reset()
		for i := 0; i < 33; i++ {
			fmt.Fprintf(&pipeline, "GET /slow%d HTTP/1.1\r\nHost: localhost\r\n\r\n", i)
		}
		write(conn, pipeline.String())
		halfClose(conn)
		for i := 0; i < 32; i++ {
			read(conn, r, fmt.Sprintf("slow pipeline %d", i), "keep-alive")
		}
		read(conn, r, "slow pipeline, last", "close")
		eof(conn, r, "after the slow pipeline")
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
		// its request line): a parser with no lenient mode refuses the
		// second (RFC 9112 §2.2) rather than read on, so the first's
		// response says close, since the close follows it, and no response
		// follows for the second. The header is what pins that the loop saw
		// the malformed tail when it answered, rather than leaving it to
		// the read deadline.
		conn = dial()
		r = bufio.NewReader(conn)
		write(conn, "GET /o HTTP/1.1\r\nHost: localhost\r\n\r\nGET /bare HTTP/1.1\nHost: localhost\r\n\r\n")
		read(conn, r, "before a malformed request", "close")
		eof(conn, r, "after a malformed request")
		// 101 header fields, one past the cap: refused before any handler
		// sees the request, answered 431 by the loop itself and closed.
		conn = dial()
		r = bufio.NewReader(conn)
		pipeline.Reset()
		pipeline.WriteString("GET /many HTTP/1.1\r\nHost: localhost\r\n")
		for i := 0; i < 100; i++ {
			fmt.Fprintf(&pipeline, "X-%d: %d\r\n", i, i)
		}
		pipeline.WriteString("\r\n")
		write(conn, pipeline.String())
		refused(conn, r, "a request with 101 header fields", 431)
		eof(conn, r, "after 101 header fields")
		// An HTTP/1.1 request without a Host (RFC 9112 §3.2): 400, closed.
		conn = dial()
		r = bufio.NewReader(conn)
		write(conn, "GET /nohost HTTP/1.1\r\n\r\n")
		refused(conn, r, "a request without Host", 400)
		eof(conn, r, "after a request without Host")
		// A body past the cap: 413 as soon as the header block declares
		// it, before any of the body arrives, then closed.
		conn = dial()
		r = bufio.NewReader(conn)
		write(conn, "POST /big HTTP/1.1\r\nHost: localhost\r\nContent-Length: 1048577\r\n\r\n")
		refused(conn, r, "a body past the cap", 413)
		eof(conn, r, "after a body past the cap")
		// A chunked request (two chunks, an extension, a trailer) with a
		// request pipelined behind it in one write: the handler echoes the
		// decoded body, and the framing must leave exactly the second
		// request to answer. The client ends this one.
		conn = dial()
		r = bufio.NewReader(conn)
		write(conn, "POST /chunked HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\n\r\n5;x=y\r\nhello\r\n6\r\n world\r\n0\r\nX-Sum: 11\r\n\r\nGET /after-chunked HTTP/1.1\r\nHost: localhost\r\n\r\n")
		readBody(conn, r, "chunked", "hello world", "keep-alive")
		read(conn, r, "behind the chunked request", "keep-alive")
		conn.Close()
		// A HEAD with a GET pipelined behind it: the HEAD is answered with
		// the Content-Length its body would have and none of the body, so
		// the GET's response must follow at once; then a 204, answered
		// with neither. The client ends this one.
		conn = dial()
		r = bufio.NewReader(conn)
		write(conn, "HEAD /h HTTP/1.1\r\nHost: localhost\r\n\r\nGET /after-head HTTP/1.1\r\nHost: localhost\r\n\r\n")
		bodiless(conn, r, "HEAD", "HEAD", 200, "2")
		read(conn, r, "behind the HEAD", "keep-alive")
		write(conn, "GET /nocontent HTTP/1.1\r\nHost: localhost\r\n\r\n")
		bodiless(conn, r, "204", "GET", 204, "")
		conn.Close()
		// `Expect: 100-continue` (RFC 9110 §10.1.1): the header block alone
		// is answered `100 Continue`, the body is sent only then, and the
		// handler's echo of it follows. The client ends this one. Then an
		// expectation the server cannot meet, answered 417 and closed.
		conn = dial()
		r = bufio.NewReader(conn)
		write(conn, "POST /expect HTTP/1.1\r\nHost: localhost\r\nExpect: 100-continue\r\nContent-Length: 5\r\n\r\n")
		interim(conn, r, "100 Continue")
		write(conn, "hello")
		readBody(conn, r, "after 100 Continue", "hello", "keep-alive")
		conn.Close()
		conn = dial()
		r = bufio.NewReader(conn)
		write(conn, "POST /expect HTTP/1.1\r\nHost: localhost\r\nExpect: nope\r\nContent-Length: 5\r\n\r\n")
		refused(conn, r, "an expectation the server cannot meet", 417)
		eof(conn, r, "after 417")
		// An HTTP/1.0 request asking to keep the connection, then an
		// HTTP/1.1 request on it; the client ends this one.
		conn = dial()
		r = bufio.NewReader(conn)
		write(conn, "GET /l HTTP/1.0\r\nHost: localhost\r\nConnection: keep-alive\r\n\r\n")
		read(conn, r, "HTTP/1.0 keep-alive", "keep-alive")
		write(conn, "GET /m HTTP/1.1\r\nHost: localhost\r\n\r\n")
		read(conn, r, "after HTTP/1.0 keep-alive", "keep-alive")
		conn.Close()
		// A body trickled a byte per 100 ms, 10 B/s: the read deadline
		// closes the connection, since what the trickle has bought at the
		// minimum rate never reaches it, and no response is owed. The
		// trickle stops at the first write the closed connection refuses.
		// It runs before the flowing body below, the cycle's last answered
		// request: the bounded loop leaves once that is answered, and a
		// request it owes no answer must not be the one it leaves on.
		conn = dial()
		r = bufio.NewReader(conn)
		write(conn, "POST /chunked HTTP/1.1\r\nHost: localhost\r\nContent-Length: 1000\r\n\r\n")
		started = time.Now()
		for i := 0; i < 40; i++ {
			time.Sleep(100 * time.Millisecond)
			if _, err := io.WriteString(conn, "t"); err != nil {
				break
			}
		}
		closed(conn, r, "a body trickled below the minimum rate")
		if waited := time.Since(started); waited > 4*time.Second {
			t.Fatalf("the trickled body was closed after %v, not by the 300 ms read deadline", waited)
		}
		// A body of 6000 bytes delivered as 20 pieces 35 ms apart: 700 ms,
		// over twice the 300 ms read deadline, at 8.5 KB/s, well above the
		// 240 B/s the loop asks once the header block is in, so the
		// request is answered with the body's echo.
		conn = dial()
		if err := conn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
			conn.Close()
			t.Fatal(err)
		}
		r = bufio.NewReader(conn)
		flowing := strings.Repeat("f", 6000)
		write(conn, "POST /chunked HTTP/1.1\r\nHost: localhost\r\nContent-Length: 6000\r\n\r\n")
		for i := 0; i < 20; i++ {
			time.Sleep(35 * time.Millisecond)
			write(conn, flowing[i*300:(i+1)*300])
		}
		readBody(conn, r, "a body flowing above the minimum rate", flowing, "keep-alive")
		conn.Close()
		// The loop holds two connections open at most: with two answered
		// and kept, a third connection's request is not read (it waits in
		// the listener's accept queue, so its bytes sit in the kernel) until
		// one of the two closes, and is answered then.
		first := dial()
		firstReader := bufio.NewReader(first)
		write(first, "GET /cap1 HTTP/1.1\r\nHost: localhost\r\n\r\n")
		read(first, firstReader, "the first of two held connections", "keep-alive")
		second := dial()
		secondReader := bufio.NewReader(second)
		write(second, "GET /cap2 HTTP/1.1\r\nHost: localhost\r\n\r\n")
		read(second, secondReader, "the second of two held connections", "keep-alive")
		conn = dial()
		r = bufio.NewReader(conn)
		write(conn, "GET /cap3 HTTP/1.1\r\nHost: localhost\r\n\r\n")
		if err := conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
			conn.Close()
			t.Fatal(err)
		}
		if b, err := r.Peek(1); err == nil || !errors.Is(err, os.ErrDeadlineExceeded) {
			conn.Close()
			t.Fatalf("a third connection at the cap was answered (%q, %v); want no bytes within 500 ms", b, err)
		}
		if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			conn.Close()
			t.Fatal(err)
		}
		first.Close()
		read(conn, r, "the third connection once one closed", "keep-alive")
		second.Close()
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
	const original = "    return __serve_loop(3, (req: HttpRequest, plat: Platform): HttpResponse => census_handle(req, plat), ServeOptions { ...serve_options(), recv_deadline: time.duration_millis(300 as i64), data_rate_grace: time.duration_millis(100 as i64), keep_alive_requests: 200, max_connections: 2 });"
	if strings.Count(src, original) != 1 {
		t.Fatal("bounded HTTP entry changed")
	}
	const entry = `    var listener: i32 = tcp_listen(0);
    if (listener < 0) { return 90; }
    var port: i32 = tcp_local_port(listener);
    if (port <= 0) { return 91; }
    print(int.int_to_string(port));
    var result: i32 = __serve_loop(listener, (req: HttpRequest, plat: Platform): HttpResponse => census_handle(req, plat), ServeOptions { ...serve_options(), recv_deadline: time.duration_millis(300 as i64), data_rate_grace: time.duration_millis(100 as i64), keep_alive_requests: 200, max_connections: 2 });
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
