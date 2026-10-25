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

	"github.com/jakechampion/lang/internal/tools/literate"
)

func HTTPHandlerCensusSource(t *testing.T, root string, rounds int) string {
	t.Helper()
	data, err := literate.ReadFile(filepath.Join(root, "internal/stdlib/std/serve.fern"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	const async = "import \"std/async\";"
	if strings.Count(src, async) != 1 {
		t.Fatal("std/serve's imports changed; update its bounded census fixture")
	}
	src = strings.Replace(src, async, "import \"std/platform\";\n"+async, 1)
	start := strings.Index(src, "function __serve_loop(")
	if start < 0 {
		t.Fatal("production accept loop not found")
	}
	burstStart := strings.Index(src[start:], "\nfunction __serve_burst[")
	if burstStart < 0 {
		t.Fatal("production accept loop boundary not found")
	}
	burstStart += start
	end := strings.Index(src[burstStart:], "\nfunction __serve_loop_with[")
	if end < 0 {
		t.Fatal("production request burst boundary not found")
	}
	end += burstStart
	body, burst := src[start:burstStart], src[burstStart:end]
	const answered = "more = answered.1;"
	const burstCall = "let burst: (__Conns, __Flight[]) = __serve_burst(drv, conns, flights, at, ev.2, handler, start, opts, stop, clk);"
	const burstSignature = "stop: __Stop, clk: __Clock): (__Conns, __Flight[]) {"
	const burstReturn = "return (conns, flights);"
	if strings.Count(body, "while (true)") != 1 || strings.Count(burst, answered) != 1 ||
		strings.Count(body, burstCall) != 1 || strings.Count(body, "flights = burst.1;") != 1 ||
		strings.Count(burst, burstSignature) != 1 || strings.Count(burst, burstReturn) != 1 {
		t.Fatal("accept loop changed; update its bounded census fixture")
	}
	body = strings.Replace(body, "while (true)", fmt.Sprintf("let completed: i32 = 0;\n    while (completed < %d || conns.open > 0)", rounds), 1)
	body = strings.Replace(body, burstCall, "let burst: (__Conns, __Flight[], i32) = __serve_burst(drv, conns, flights, at, ev.2, handler, start, opts, stop, clk, completed);", 1)
	body = strings.Replace(body, "flights = burst.1;", "flights = burst.1;\n      completed = burst.2;", 1)
	burst = strings.Replace(burst, burstSignature, "stop: __Stop, clk: __Clock, census_completed: i32): (__Conns, __Flight[], i32) {\n  let completed: i32 = census_completed;", 1)
	burst = strings.Replace(burst, answered, answered+"\n                        completed = completed + 1;", 1)
	burst = strings.Replace(burst, burstReturn, "return (conns, flights, completed);", 1)
	return src[:start] + body + burst + src[end:] + `
function census_burn(n: i32, plat: platform.Platform): i32 {
    let started: i64 = plat.elapsed_ns();
    let x: i32 = 12345;
    while (plat.elapsed_ns() - started < n as i64) {
        let i: i32 = 0;
        while (i < 1024) {
            x = (x * 1103515245 + 12345) & 2147483647;
            i = i + 1;
        }
    }
    return x;
}
function census_handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    if (req.path == "/chunked" || req.path == "/expect") {
        match (req.body_string()) {
            Ok(text) => { return http.ok(text); },
            Err(e) => { return e.to_response(); }
        }
    }
    if (req.path == "/nocontent") { return http.no_content(); }
    // None needs no work; Some holds the slow/reset delay. This exercises
    // the i32 Option fallback as well as the timing and leak checks.
    let work: Option[i32] = None;
    if (req.path.starts_with("/slow")) { work = Some(20000000); }
    if (req.path.starts_with("/gone")) { work = Some(100000000); }
    if (req.path == "/behind-a-failed-write") { plat.log("answered /behind-a-failed-write"); }
    if (census_burn(work.unwrap_or(0), plat) == 0 - 1) { return http.ok("never"); }
    return http.ok("ok");
}
function main(): i32 {
    return exit_code(__serve_loop(3, (req: HttpRequest, plat: platform.Host): HttpResponse => census_handle(req, plat), Config { ...config(), recv_deadline: time.duration_millis(300 as i64), data_rate_grace: time.duration_millis(100 as i64), keep_alive_requests: 200, max_connections: 2 }, (reason: string): void => {}, None));
}
`
}

const KeepAliveCycle = 315

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
			t.Fatalf("%s: status=%d Content-Length=%q body=%q close=%v error=%v, want %d, %q, empty, persisting", label, resp.StatusCode, resp.Header.Get("Content-Length"), body, resp.Close, err, wantStatus, wantLength)
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
		conn := dial()
		r := bufio.NewReader(conn)
		var pipeline strings.Builder
		for i := 0; i < 200; i++ {
			fmt.Fprintf(&pipeline, "GET /p%d HTTP/1.1\r\nHost: localhost\r\n\r\n", i)
		}
		write(conn, pipeline.String())
		for i := 0; i < 199; i++ {
			read(conn, r, fmt.Sprintf("pipelined %d", i), "")
		}
		read(conn, r, "the 200th, at the cap", "close")
		eof(conn, r, "after the cap")
		conn = dial()
		r = bufio.NewReader(conn)
		write(conn, "GET /e HTTP/1.0\r\nHost: localhost\r\n\r\n")
		read(conn, r, "HTTP/1.0", "close")
		eof(conn, r, "after HTTP/1.0")
		conn = dial()
		r = bufio.NewReader(conn)
		write(conn, "GET /f HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n")
		read(conn, r, "Connection: close", "close")
		eof(conn, r, "after Connection: close")
		conn = dial()
		r = bufio.NewReader(conn)
		write(conn, "GET /u HTTP/1.1\r\nHost: localhost\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		read(conn, r, "Upgrade", "")
		conn.Close()
		conn = dial()
		r = bufio.NewReader(conn)
		pipeline.Reset()
		for i := 0; i < 33; i++ {
			fmt.Fprintf(&pipeline, "GET /q%d HTTP/1.1\r\nHost: localhost\r\n\r\n", i)
		}
		write(conn, pipeline.String())
		halfClose(conn)
		for i := 0; i < 32; i++ {
			read(conn, r, fmt.Sprintf("half-closed pipeline %d", i), "")
		}
		read(conn, r, "half-closed pipeline, last", "close", "")
		eof(conn, r, "after the half-closed pipeline")
		conn = dial()
		r = bufio.NewReader(conn)
		write(conn, "GET /j HTTP/1.1\r\nHost: localhost\r\n\r\nGET /k HT")
		read(conn, r, "before a partial request", "")
		started := time.Now()
		eof(conn, r, "the partial request's deadline")
		if waited := time.Since(started); waited > 4*time.Second {
			t.Fatalf("the partial request was closed after %v, not by the 300 ms read deadline", waited)
		}
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
		slowStarted := time.Now()
		write(conn, pipeline.String())
		halfClose(conn)
		for i := 0; i < 32; i++ {
			read(conn, r, fmt.Sprintf("slow pipeline %d", i), "")
		}
		read(conn, r, "slow pipeline, last", "close")
		eof(conn, r, "after the slow pipeline")
		if elapsed := time.Since(slowStarted); elapsed < 300*time.Millisecond {
			t.Fatalf("slow pipeline completed in %v; it must cross the unchanged 300 ms read deadline", elapsed)
		}
		conn = dial()
		write(conn, strings.Repeat("GET /n HTTP/1.1\r\nHost: localhost\r\n\r\n", 28)+strings.Repeat("GET /gone HTTP/1.1\r\nHost: localhost\r\n\r\n", 4)+"GET /behind-a-failed-write HTTP/1.1\r\nHost: localhost\r\n\r\n")
		time.Sleep(50 * time.Millisecond)
		if err := conn.(*net.TCPConn).SetLinger(0); err != nil {
			conn.Close()
			t.Fatal(err)
		}
		conn.Close()
		conn = dial()
		r = bufio.NewReader(conn)
		write(conn, "GET /o HTTP/1.1\r\nHost: localhost\r\n\r\nGET /bare HTTP/1.1\nHost: localhost\r\n\r\n")
		read(conn, r, "before a malformed request", "close")
		eof(conn, r, "after a malformed request")
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
		conn = dial()
		r = bufio.NewReader(conn)
		write(conn, "GET /nohost HTTP/1.1\r\n\r\n")
		refused(conn, r, "a request without Host", 400)
		eof(conn, r, "after a request without Host")
		conn = dial()
		r = bufio.NewReader(conn)
		write(conn, "POST /big HTTP/1.1\r\nHost: localhost\r\nContent-Length: 1048577\r\n\r\n")
		refused(conn, r, "a body past the cap", 413)
		eof(conn, r, "after a body past the cap")
		conn = dial()
		r = bufio.NewReader(conn)
		write(conn, "POST /chunked HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\n\r\n5;x=y\r\nhello\r\n6\r\n world\r\n0\r\nX-Sum: 11\r\n\r\nGET /after-chunked HTTP/1.1\r\nHost: localhost\r\n\r\n")
		readBody(conn, r, "chunked", "hello world", "")
		read(conn, r, "behind the chunked request", "")
		conn.Close()
		conn = dial()
		r = bufio.NewReader(conn)
		write(conn, "HEAD /h HTTP/1.1\r\nHost: localhost\r\n\r\nGET /after-head HTTP/1.1\r\nHost: localhost\r\n\r\n")
		bodiless(conn, r, "HEAD", "HEAD", 200, "2")
		read(conn, r, "behind the HEAD", "")
		write(conn, "GET /nocontent HTTP/1.1\r\nHost: localhost\r\n\r\n")
		bodiless(conn, r, "204", "GET", 204, "")
		conn.Close()
		conn = dial()
		r = bufio.NewReader(conn)
		write(conn, "POST /expect HTTP/1.1\r\nHost: localhost\r\nExpect: 100-continue\r\nContent-Length: 5\r\n\r\n")
		interim(conn, r, "100 Continue")
		write(conn, "hello")
		readBody(conn, r, "after 100 Continue", "hello", "")
		conn.Close()
		conn = dial()
		r = bufio.NewReader(conn)
		write(conn, "POST /expect HTTP/1.1\r\nHost: localhost\r\nExpect: nope\r\nContent-Length: 5\r\n\r\n")
		refused(conn, r, "an expectation the server cannot meet", 417)
		eof(conn, r, "after 417")
		conn = dial()
		r = bufio.NewReader(conn)
		write(conn, "GET /l HTTP/1.0\r\nHost: localhost\r\nConnection: keep-alive\r\n\r\n")
		read(conn, r, "HTTP/1.0 keep-alive", "keep-alive")
		write(conn, "GET /m HTTP/1.1\r\nHost: localhost\r\n\r\n")
		read(conn, r, "after HTTP/1.0 keep-alive", "")
		conn.Close()
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
		readBody(conn, r, "a body flowing above the minimum rate", flowing, "")
		conn.Close()
		first := dial()
		firstReader := bufio.NewReader(first)
		write(first, "GET /cap1 HTTP/1.1\r\nHost: localhost\r\n\r\n")
		read(first, firstReader, "the first of two held connections", "")
		second := dial()
		secondReader := bufio.NewReader(second)
		write(second, "GET /cap2 HTTP/1.1\r\nHost: localhost\r\n\r\n")
		read(second, secondReader, "the second of two held connections", "")
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
		read(conn, r, "the third connection once one closed", "")
		second.Close()
		conn.Close()
	}
}

func RunHTTPKeepAlive(t *testing.T, command *exec.Cmd, rounds int) string {
	t.Helper()
	return keepAliveOutput(t, runBoundedHTTPServer(t, command, func(addr string) { HTTPKeepAliveRequests(t, addr, rounds) }))
}

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
	const original = "    return exit_code(__serve_loop(3, (req: HttpRequest, plat: platform.Host): HttpResponse => census_handle(req, plat), Config { ...config(), recv_deadline: time.duration_millis(300 as i64), data_rate_grace: time.duration_millis(100 as i64), keep_alive_requests: 200, max_connections: 2 }, (reason: string): void => {}, None));"
	if strings.Count(src, original) != 1 {
		t.Fatal("bounded HTTP entry changed")
	}
	const entry = `    let listener: i32 = tcp_listen(0);
    if (listener < 0) { return 90; }
    let port: i32 = tcp_local_port(listener);
    if (port <= 0) { return 91; }
    print(int.int_to_string(port));
    let result: i32 = exit_code(__serve_loop(listener, (req: HttpRequest, plat: platform.Host): HttpResponse => census_handle(req, plat), Config { ...config(), recv_deadline: time.duration_millis(300 as i64), data_rate_grace: time.duration_millis(100 as i64), keep_alive_requests: 200, max_connections: 2 }, (reason: string): void => {}, None));
    if (tcp_close(listener) != 0) { return 92; }
    return result;`
	return strings.Replace(src, original, entry, 1)
}

func RunWasiHTTPHandlerCensus(t *testing.T, component string, rounds int) string {
	t.Helper()
	return runBoundedWasiHTTPServer(t, component, func(addr string) { httpHandlerCensusRequests(t, addr, rounds) })
}

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
