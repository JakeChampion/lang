package e2eharness

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// FetchUpstream is a loopback origin for FetchClientSource: it answers
// each request from a script keyed by the request-target, so a client
// test sees every response shape the parser must read (RFC 9112 §6.3)
// without a real server's choices in the way.
type FetchUpstream struct {
	Port int
}

// StartFetchUpstream listens on the loopback interface and serves the
// script below until the test ends. A loopback listener that cannot be
// had is a failure, not a skip: every networking lane has one.
func StartFetchUpstream(t *testing.T) *FetchUpstream {
	t.Helper()
	ln, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go serveFetchScript(c)
		}
	}()
	return &FetchUpstream{Port: ln.Addr().(*net.TCPAddr).Port}
}

// serveFetchScript reads one request head and its Content-Length body,
// then writes the scripted response for its target and closes.
func serveFetchScript(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	head, body, ok := readRequest(c)
	if !ok {
		return
	}
	line := strings.SplitN(head, "\r\n", 2)[0]
	parts := strings.Split(line, " ")
	if len(parts) < 3 {
		return
	}
	method, target := parts[0], parts[1]
	host := headerValue(head, "Host")
	var resp []byte
	switch {
	case target == "/plain":
		resp = []byte("HTTP/1.1 200 OK\r\nContent-Length: 5\r\nConnection: keep-alive\r\nKeep-Alive: timeout=5\r\nX-Up: 1\r\n\r\nhello")
	case target == "/chunked":
		resp = []byte("HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\nTrailer: X-T\r\n\r\n3\r\nabc\r\n2\r\nde\r\n0\r\nX-T: tv\r\n\r\n")
	case target == "/interim":
		resp = []byte("HTTP/1.1 100 Continue\r\n\r\nHTTP/1.1 404 Not Found\r\nX-Final: yes\r\n\r\nclose-delimited")
	case target == "/nobody":
		resp = []byte("HTTP/1.1 204 No Content\r\nContent-Length: 99\r\n\r\n")
	case target == "/binary":
		resp = append([]byte("HTTP/1.1 200 OK\r\nContent-Length: 4\r\n\r\n"), 0xff, 0xfe, 0x00, 'a')
	case target == "/big":
		big := bytes.Repeat([]byte("abcdefghijklmnopqrstuvwxyz"), 12000)
		resp = append([]byte(fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n", len(big))), big...)
	case target == "/garbage":
		resp = []byte("NOT-HTTP\r\n\r\n")
	case target == "/bighead":
		// A header block past the 32 KiB budget: 431 before its end.
		resp = []byte("HTTP/1.1 200 OK\r\nX-Pad: " + strings.Repeat("a", 40000) + "\r\nContent-Length: 0\r\n\r\n")
	case target == "/gzipchunked":
		resp = []byte("HTTP/1.1 200 OK\r\nTransfer-Encoding: gzip, chunked\r\n\r\n0\r\n\r\n")
	case target == "/h2":
		resp = []byte("HTTP/2.0 200 OK\r\nContent-Length: 0\r\n\r\n")
	case target == "/switch":
		resp = []byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n\x81\x05hello")
	case target == "/flood":
		// Interim responses past the header budget before any final one:
		// 2000 of 25 bytes each is 50 000 bytes, past 32 KiB.
		resp = append(bytes.Repeat([]byte("HTTP/1.1 100 Continue\r\n\r\n"), 2000), "HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n"...)
	case target == "/truncated":
		resp = []byte("HTTP/1.1 200 OK\r\nContent-Length: 50\r\n\r\nonly a little")
	case strings.HasPrefix(target, "/echo"):
		text := fmt.Sprintf("%s %s host=%s len=%d body=%s trace=%s", method, target, host, len(body), body, headerValue(head, "x-trace"))
		resp = []byte(fmt.Sprintf("HTTP/1.1 201 Created\r\nContent-Length: %d\r\n\r\n%s", len(text), text))
	default:
		resp = []byte("HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\n\r\n")
	}
	_, _ = c.Write(resp)
}

// readRequest reads a request head and the body its Content-Length
// declares.
func readRequest(c net.Conn) (head, body string, ok bool) {
	var buf []byte
	tmp := make([]byte, 4096)
	for !bytes.Contains(buf, []byte("\r\n\r\n")) {
		n, err := c.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			return "", "", false
		}
	}
	sep := bytes.Index(buf, []byte("\r\n\r\n"))
	head = string(buf[:sep])
	rest := buf[sep+4:]
	want, _ := strconv.Atoi(headerValue(head, "Content-Length"))
	for len(rest) < want {
		n, err := c.Read(tmp)
		rest = append(rest, tmp[:n]...)
		if err != nil && err != io.EOF {
			return "", "", false
		}
		if err == io.EOF {
			break
		}
	}
	return head, string(rest[:min(want, len(rest))]), true
}

func headerValue(head, name string) string {
	for _, line := range strings.Split(head, "\r\n")[1:] {
		if i := strings.Index(line, ":"); i > 0 && strings.EqualFold(line[:i], name) {
			return strings.TrimSpace(line[i+1:])
		}
	}
	return ""
}

// FetchClientSource is a program that sends one request per scripted
// response shape through std/fetch and prints what came back, one line
// per case, exiting 0 once every line is printed; the request-side
// refusals (a CRLF in the URL or a header value, a header name or method
// that is not a token, a file body) never reach the socket. `closedPort`
// is a port nothing listens on.
func FetchClientSource(port, closedPort int) string {
	return fmt.Sprintf(`import "std/fetch";
import "std/http";
import "std/platform";
import "std/net";

function base(): string {
    return "http://127.0.0.1:%d";
}

function show(name: string, answer: Result[HttpResponse, fetch.FetchError]): void {
    match (answer) {
        Ok(resp) => {
            var text: string = "<not utf-8>";
            match (resp.body_text()) {
                Some(s) => { text = s; },
                None => {}
            }
            var fields: string = "";
            var i: i32 = 0;
            while (i < resp.headers.names.len()) {
                fields = fields + " " + resp.headers.names[i] + "=" + resp.headers.values[i];
                i = i + 1;
            }
            var trailers: string = "";
            i = 0;
            while (i < resp.trailers.names.len()) {
                trailers = trailers + " " + resp.trailers.names[i] + "=" + resp.trailers.values[i];
                i = i + 1;
            }
            print(name + ": " + resp.status.to_string() + " [" + text + "] headers:" + fields + " trailers:" + trailers);
        },
        Err(e) => { print(name + ": error " + e.message()); }
    }
}

function main(): i32 {
    show("plain", fetch.send(fetch.get(base() + "/plain")));
    show("chunked", fetch.send(fetch.get(base() + "/chunked")));
    show("interim", fetch.send(fetch.get(base() + "/interim")));
    show("nobody", fetch.send(fetch.get(base() + "/nobody")));
    show("head", fetch.send(fetch.request("HEAD", base() + "/plain")));
    show("echo", fetch.send(fetch.request("POST", base() + "/echo?q=1").with_header("X-Trace", "t1").with_text("payload")));
    var raw: u8[] = "raw".bytes();
    show("put", fetch.send(fetch.request("PUT", base() + "/echo").with_bytes(raw)));
    show("plat", platform.platform_new().http(fetch.get(base() + "/echo")));
    match (fetch.send(fetch.get(base() + "/binary"))) {
        Ok(resp) => {
            var bs: u8[] = resp.body_bytes();
            var text: string = "<not utf-8>";
            match (resp.body_text()) {
                Some(s) => { text = s; },
                None => {}
            }
            print("binary: " + bs.len().to_string() + " " + (bs[0] as i32).to_string() + " " + (bs[3] as i32).to_string() + " " + text);
        },
        Err(e) => { print("binary: error " + e.message()); }
    }
    match (fetch.send(fetch.get(base() + "/big"))) {
        Ok(resp) => { print("big: " + resp.body_bytes().len().to_string()); },
        Err(e) => { print("big: error " + e.message()); }
    }
    var small: http.HttpLimits = http.http_limits();
    show("limit", fetch.send(fetch.get(base() + "/big").with_limits(http.HttpLimits { ...small, body: 1024 })));
    show("garbage", fetch.send(fetch.get(base() + "/garbage")));
    show("bighead", fetch.send(fetch.get(base() + "/bighead")));
    show("gzipchunked", fetch.send(fetch.get(base() + "/gzipchunked")));
    show("h2", fetch.send(fetch.get(base() + "/h2")));
    show("flood", fetch.send(fetch.get(base() + "/flood")));
    show("switch", fetch.send(fetch.get(base() + "/switch")));
    show("truncated", fetch.send(fetch.get(base() + "/truncated")));
    show("refused", fetch.send(fetch.get("http://127.0.0.1:%d/")));
    show("badurl", fetch.send(fetch.get("not a url")));
    show("noscheme", fetch.send(fetch.get("ftp://127.0.0.1/")));
    show("tls", fetch.send(fetch.get("https://127.0.0.1/")));
    show("crlfurl", fetch.send(fetch.get(base() + "/a\r\nX-Injected: 1\r\n\r\n")));
    show("crlfvalue", fetch.send(fetch.get(base() + "/plain").with_header("X-A", "1\r\nX-Injected: 1")));
    show("badname", fetch.send(fetch.get(base() + "/plain").with_header("X A", "1")));
    show("badmethod", fetch.send(fetch.request("GE T", base() + "/plain")));
    show("filebody", fetch.send(fetch.request("POST", base() + "/echo").with_body(BodyFile("/etc/hostname"))));
    match (fetch.send(fetch.get(base() + "/nothing"))) {
        Ok(resp) => {
            match (resp.ok_or_status()) {
                Ok(r) => { print("status: ok"); },
                Err(status) => { print("status: " + status.to_string()); }
            }
        },
        Err(e) => { print("status: error " + e.message()); }
    }
    return 0;
}
`, port, closedPort)
}

// FetchClientWant is what FetchClientSource prints.
const FetchClientWant = `plain: 200 [hello] headers: content-length=5 x-up=1 trailers:
chunked: 200 [abcde] headers: trailers: x-t=tv
interim: 404 [close-delimited] headers: x-final=yes trailers:
nobody: 204 [] headers: content-length=99 trailers:
head: 200 [] headers: content-length=5 x-up=1 trailers:
echo: 201 [POST /echo?q=1 host=127.0.0.1:PORT len=7 body=payload trace=t1] headers: content-length=ECHO trailers:
put: 201 [PUT /echo host=127.0.0.1:PORT len=3 body=raw trace=] headers: content-length=PUTLEN trailers:
plat: 201 [GET /echo host=127.0.0.1:PORT len=0 body= trace=] headers: content-length=PLAT trailers:
binary: 4 255 97 <not utf-8>
big: 312000
limit: error response body past its limit
garbage: error protocol: a malformed response
bighead: error protocol: the header block or chunk framing is past its limit
gzipchunked: error protocol: a transfer coding other than chunked
h2: error protocol: an HTTP version other than 1
flood: error protocol: interim responses past the header budget
switch: error protocol: a protocol switch the client did not ask for
truncated: error protocol: the connection closed before the response ended
refused: error connect: Connection refused
badurl: error invalid URL: no scheme in not a url
noscheme: error invalid URL: scheme ftp is not http
tls: error TLS is not supported
crlfurl: error invalid URL: a path or query that cannot be written on a request line
crlfvalue: error invalid request: a header value has a byte that cannot be written
badname: error invalid request: a header name is not a token
badmethod: error invalid request: the method is not a token
filebody: error invalid request: a file body cannot be sent
status: 404
`

// CheckFetchClient compares the client's output against FetchClientWant
// with the upstream's port filled in.
func CheckFetchClient(t *testing.T, port int, stdout string, exit int) {
	t.Helper()
	host := "127.0.0.1:" + strconv.Itoa(port)
	want := strings.ReplaceAll(FetchClientWant, "PORT", strconv.Itoa(port))
	want = strings.ReplaceAll(want, "ECHO", strconv.Itoa(len("POST /echo?q=1 host="+host+" len=7 body=payload trace=t1")))
	want = strings.ReplaceAll(want, "PLAT", strconv.Itoa(len("GET /echo host="+host+" len=0 body= trace=")))
	want = strings.ReplaceAll(want, "PUTLEN", strconv.Itoa(len("PUT /echo host="+host+" len=3 body=raw trace=")))
	if exit != 0 || stdout != want {
		t.Fatalf("exit %d\n--- got ---\n%s--- want ---\n%s", exit, stdout, want)
	}
}

// ClosedLoopbackPort is a loopback port nothing listens on: one that was
// just released.
func ClosedLoopbackPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}
