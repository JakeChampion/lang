package e2eharness

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// FetchUpstream is a loopback origin for FetchClientSource: it answers
// each request from a script keyed by the request-target, so a client
// test sees every response shape the parser must read (RFC 9112 §6.3)
// without a real server's choices in the way. Port2 is a second
// listener serving the same script: another origin for a cross-origin
// redirect to land on.
type FetchUpstream struct {
	Port  int
	Port2 int

	mu   sync.Mutex
	hits map[string]int
}

// StartFetchUpstream listens twice on the loopback interface and serves
// the script below until the test ends. A loopback listener that cannot
// be had is a failure, not a skip: every networking lane has one.
func StartFetchUpstream(t *testing.T) *FetchUpstream {
	t.Helper()
	up := &FetchUpstream{hits: map[string]int{}}
	listen := func() int {
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
				go up.serve(c)
			}
		}()
		return ln.Addr().(*net.TCPAddr).Port
	}
	up.Port = listen()
	up.Port2 = listen()
	return up
}

// hit counts a request to target and reports whether it is an odd one,
// so a target can answer every other connection.
func (up *FetchUpstream) hit(target string) (odd bool) {
	up.mu.Lock()
	defer up.mu.Unlock()
	up.hits[target]++
	return up.hits[target]%2 == 1
}

// serve reads one request head and its Content-Length body, then writes
// the scripted response for its target and closes.
func (up *FetchUpstream) serve(c net.Conn) {
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
	redirect := func(status int, location string) []byte {
		return []byte(fmt.Sprintf("HTTP/1.1 %d Elsewhere\r\nLocation: %s\r\nContent-Length: 0\r\n\r\n", status, location))
	}
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
		text := echoLine(method, target, host, body, headerValue(head, "x-trace"), headerValue(head, "authorization"), headerValue(head, "cookie"))
		resp = []byte(fmt.Sprintf("HTTP/1.1 201 Created\r\nContent-Length: %d\r\n\r\n%s", len(text), text))
	case target == "/redir":
		resp = redirect(302, "/plain")
	case target == "/redir-echo":
		resp = redirect(302, "/echo")
	case strings.HasPrefix(target, "/chain/"):
		// /chain/N is N redirects before a 200.
		n, _ := strconv.Atoi(strings.TrimPrefix(target, "/chain/"))
		if n == 0 {
			resp = []byte("HTTP/1.1 200 OK\r\nContent-Length: 3\r\n\r\nend")
		} else {
			resp = redirect(302, fmt.Sprintf("/chain/%d", n-1))
		}
	case target == "/nolocation":
		resp = []byte("HTTP/1.1 302 Found\r\nContent-Length: 0\r\n\r\n")
	case target == "/badlocation":
		resp = redirect(302, "http://[::1")
	case target == "/rel/dir/relative":
		resp = redirect(302, "../plain?q=2")
	case strings.HasPrefix(target, "/rel/plain"):
		resp = []byte(fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n%s", len(target), target))
	case target == "/303":
		resp = redirect(303, "/echo")
	case target == "/307":
		resp = redirect(307, "/echo")
	case target == "/xorigin":
		resp = redirect(302, fmt.Sprintf("http://127.0.0.1:%d/echo", up.Port2))
	case target == "/sameorigin":
		resp = redirect(302, "/echo")
	case target == "/reset":
		// Every odd connection is closed without a byte of response; the
		// client's one retry lands on the even one.
		if up.hit(target) {
			return
		}
		resp = []byte("HTTP/1.1 200 OK\r\nContent-Length: 7\r\n\r\nretried")
	case target == "/reset-post":
		return
	default:
		resp = []byte("HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\n\r\n")
	}
	_, _ = c.Write(resp)
}

// echoLine is what the /echo target answers: the request as the origin
// saw it.
func echoLine(method, target, host, body, trace, auth, cookie string) string {
	return fmt.Sprintf("%s %s host=%s len=%d body=%s trace=%s auth=%s cookie=%s", method, target, host, len(body), body, trace, auth, cookie)
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
// that is not a token, a file body) never reach the socket. The redirect
// cases pin each hop rule: a 302 followed, a 302 kept as data, ten hops
// allowed and eleven refused, a missing or unparseable Location, a
// relative Location resolved, a 303 and a 302-after-POST rewritten to
// GET, a 307 keeping the POST, credentials dropped across origins and
// kept within one; the reset cases pin the one retry of an idempotent
// request and none of a POST. `closedPort` is a port nothing listens on.
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
    show("redir", fetch.send(fetch.get(base() + "/redir")));
    show("noredir", fetch.send(fetch.get(base() + "/redir").with_redirects(0)));
    show("chain10", fetch.send(fetch.get(base() + "/chain/10")));
    show("chain11", fetch.send(fetch.get(base() + "/chain/11")));
    show("nolocation", fetch.send(fetch.get(base() + "/nolocation")));
    show("badlocation", fetch.send(fetch.get(base() + "/badlocation")));
    show("relative", fetch.send(fetch.get(base() + "/rel/dir/relative")));
    show("post303", fetch.send(fetch.request("POST", base() + "/303").with_header("Content-Type", "text/plain").with_text("payload")));
    show("post302", fetch.send(fetch.request("POST", base() + "/redir-echo").with_text("payload")));
    show("post307", fetch.send(fetch.request("POST", base() + "/307").with_text("payload")));
    show("xorigin", fetch.send(fetch.get(base() + "/xorigin").with_header("Authorization", "Bearer t").with_header("Cookie", "a=1").with_header("X-Trace", "kept")));
    show("sameorigin", fetch.send(fetch.get(base() + "/sameorigin").with_header("Authorization", "Bearer t").with_header("Cookie", "a=1")));
    show("reset", fetch.send(fetch.get(base() + "/reset")));
    show("resetpost", fetch.send(fetch.request("POST", base() + "/reset-post").with_text("x")));
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
echo: 201 [ECHO] headers: content-length=ECHOLEN trailers:
put: 201 [PUT] headers: content-length=PUTLEN trailers:
plat: 201 [PLAT] headers: content-length=PLATLEN trailers:
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
redir: 200 [hello] headers: content-length=5 x-up=1 trailers:
noredir: 302 [] headers: location=/plain content-length=0 trailers:
chain10: 200 [end] headers: content-length=3 trailers:
chain11: error redirect: more than 10 redirects
nolocation: error redirect: a 302 without a Location
badlocation: error redirect: a Location that is not a URL
relative: 200 [/rel/plain?q=2] headers: content-length=14 trailers:
post303: 201 [POST303] headers: content-length=POST303LEN trailers:
post302: 201 [POST302] headers: content-length=POST302LEN trailers:
post307: 201 [POST307] headers: content-length=POST307LEN trailers:
xorigin: 201 [XORIGIN] headers: content-length=XORIGINLEN trailers:
sameorigin: 201 [SAMEORIGIN] headers: content-length=SAMEORIGINLEN trailers:
reset: 200 [retried] headers: content-length=7 trailers:
resetpost: error i/o: Connection reset by peer
status: 404
`

// CheckFetchClient compares the client's output against FetchClientWant
// with the upstream's ports and the echo lines filled in.
func CheckFetchClient(t *testing.T, up *FetchUpstream, stdout string, exit int) {
	t.Helper()
	host := "127.0.0.1:" + strconv.Itoa(up.Port)
	host2 := "127.0.0.1:" + strconv.Itoa(up.Port2)
	want := FetchClientWant
	for name, line := range map[string]string{
		"ECHO":       echoLine("POST", "/echo?q=1", host, "payload", "t1", "", ""),
		"PUT":        echoLine("PUT", "/echo", host, "raw", "", "", ""),
		"PLAT":       echoLine("GET", "/echo", host, "", "", "", ""),
		"POST303":    echoLine("GET", "/echo", host, "", "", "", ""),
		"POST302":    echoLine("GET", "/echo", host, "", "", "", ""),
		"POST307":    echoLine("POST", "/echo", host, "payload", "", "", ""),
		"XORIGIN":    echoLine("GET", "/echo", host2, "", "kept", "", ""),
		"SAMEORIGIN": echoLine("GET", "/echo", host, "", "", "Bearer t", "a=1"),
	} {
		want = strings.ReplaceAll(want, "["+name+"]", "["+line+"]")
		want = strings.ReplaceAll(want, name+"LEN", strconv.Itoa(len(line)))
	}
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
