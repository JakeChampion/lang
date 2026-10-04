package e2eharness

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"math/rand"
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

	mu      sync.Mutex
	hits    map[string]int
	held    int
	release chan struct{}
}

// StartFetchUpstream listens twice on the loopback interface and serves
// the script below until the test ends. A loopback listener that cannot
// be had is a failure, not a skip: every networking lane has one.
func StartFetchUpstream(t *testing.T) *FetchUpstream {
	t.Helper()
	up := &FetchUpstream{hits: map[string]int{}, release: make(chan struct{})}
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
	if p := keptPath(target); p != "" {
		serveKept(c, p)
		return
	}
	if originPath(target) == "/hold" {
		up.hold(c)
		return
	}
	if originPath(target) == "/slow" {
		// The upstream a blocking-handler conformance case waits on: the
		// answer arrives SlowUpstreamDelay after the request.
		time.Sleep(SlowUpstreamDelay)
		_, _ = c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 4\r\n\r\nslow"))
		return
	}
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
	case target == "/gzip":
		z := gzipped([]byte("hello gzip"))
		resp = append([]byte(fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Encoding: gzip\r\nContent-Length: %d\r\nX-Up: 1\r\n\r\n", len(z))), z...)
	case target == "/gzip-body-chunked":
		z := gzipped([]byte("hello gzip"))
		resp = append([]byte(fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Encoding: gzip\r\nTransfer-Encoding: chunked\r\n\r\n%x\r\n", len(z))), z...)
		resp = append(resp, "\r\n0\r\n\r\n"...)
	case target == "/gzip-identity-coding":
		z := gzipped([]byte("hello gzip"))
		resp = append([]byte(fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Encoding: identity, gzip\r\nContent-Length: %d\r\n\r\n", len(z))), z...)
	case target == "/x-gzip":
		z := gzipped([]byte("hello gzip"))
		resp = append([]byte(fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Encoding: x-gzip\r\nContent-Length: %d\r\n\r\n", len(z))), z...)
	case target == "/gzip-floor":
		// 60 000 zero bytes: a few dozen bytes encoded, far past a
		// hundredfold growth, under the 64 KiB the ratio never refuses.
		z := gzipped(make([]byte, 60000))
		resp = append([]byte(fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Encoding: gzip\r\nContent-Length: %d\r\n\r\n", len(z))), z...)
	case target == "/gzip-wide":
		// 100 000 bytes past that floor, from 2 000 seeded random bytes
		// repeated: the first copy is incompressible, so the growth stays
		// well under a hundredfold and the ratio admits the body.
		z := gzipped(bytes.Repeat(wideBlock(), 50))
		resp = append([]byte(fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Encoding: gzip\r\nContent-Length: %d\r\n\r\n", len(z))), z...)
	case target == "/gzip-bad":
		resp = []byte("HTTP/1.1 200 OK\r\nContent-Encoding: gzip\r\nContent-Length: 15\r\n\r\nnot gzip at all")
	case target == "/gzip-bomb":
		// 200 000 zero bytes: a few hundred bytes encoded, past a
		// hundredfold growth, within the 1 MiB body cap.
		z := gzipped(make([]byte, 200000))
		resp = append([]byte(fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Encoding: gzip\r\nContent-Length: %d\r\n\r\n", len(z))), z...)
	case target == "/gzip-double":
		z := gzipped(gzipped([]byte("twice")))
		resp = append([]byte(fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Encoding: gzip, gzip\r\nContent-Length: %d\r\n\r\n", len(z))), z...)
	case target == "/gzip-double-wide":
		// The /gzip-wide body under two codings: past the floor, within
		// the ratio, so depth 2 is judged at a size the floor does not
		// decide.
		z := gzipped(gzipped(bytes.Repeat(wideBlock(), 50)))
		resp = append([]byte(fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Encoding: gzip, gzip\r\nContent-Length: %d\r\n\r\n", len(z))), z...)
	case target == "/gzip-double-bomb":
		// 100 000 zero bytes under two codings: past the floor and the
		// ratio, so the whole-response verdict shows at depth 2.
		z := gzipped(gzipped(make([]byte, 100000)))
		resp = append([]byte(fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Encoding: gzip, gzip\r\nContent-Length: %d\r\n\r\n", len(z))), z...)
	case target == "/br":
		resp = []byte("HTTP/1.1 200 OK\r\nContent-Encoding: br\r\nContent-Length: 3\r\n\r\nraw")
	case target == "/truncated":
		resp = []byte("HTTP/1.1 200 OK\r\nContent-Length: 50\r\n\r\nonly a little")
	case strings.HasPrefix(target, "http://"):
		// An absolute-form target is what a client writes to a forward
		// proxy: answer as the proxy saw it.
		text := proxyLine(method, target, host, body, headerValue(head, "x-trace"), headerValue(head, "proxy-authorization"))
		resp = []byte(fmt.Sprintf("HTTP/1.1 201 Created\r\nContent-Length: %d\r\n\r\n%s", len(text), text))
	case strings.HasPrefix(target, "/echo"):
		text := echoLine(method, target, host, body, headerValue(head, "x-trace"), headerValue(head, "authorization"), headerValue(head, "cookie"), headerValue(head, "accept-encoding"))
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

// hold parks the connection until Release, then answers it. It is the
// upstream a handler stays suspended on for as long as a test needs.
func (up *FetchUpstream) hold(c net.Conn) {
	up.mu.Lock()
	up.held++
	release := up.release
	up.mu.Unlock()
	_ = c.SetDeadline(time.Time{})
	<-release
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	_, _ = c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 4\r\n\r\nheld"))
}

// Held is how many /hold connections are parked since the last Release.
func (up *FetchUpstream) Held() int {
	up.mu.Lock()
	defer up.mu.Unlock()
	return up.held
}

// Release answers every parked /hold connection.
func (up *FetchUpstream) Release() {
	up.mu.Lock()
	close(up.release)
	up.release = make(chan struct{})
	up.held = 0
	up.mu.Unlock()
}

// SlowUpstreamDelay is how long the upstream's /slow target waits before
// answering.
const SlowUpstreamDelay = 100 * time.Millisecond

// originPath is the path of `target`, written in origin form or in the
// absolute form a client writes to a forward proxy.
func originPath(target string) string {
	if rest, ok := strings.CutPrefix(target, "http://"); ok {
		if i := strings.Index(rest, "/"); i >= 0 {
			return rest[i:]
		}
	}
	return target
}

// keptPath is the kept target `target` names, in either form, or "".
func keptPath(target string) string {
	if path := originPath(target); path == "/kept" || path == "/kept-redir" {
		return path
	}
	return ""
}

// serveKept answers /kept and /kept-redir on one connection for as long
// as the client sends them, never asking it to close: /kept-redir
// redirects to /kept, and /kept answers how many requests the connection
// has carried.
func serveKept(c net.Conn, target string) {
	for served := 1; ; served++ {
		resp := "HTTP/1.1 302 Found\r\nLocation: /kept\r\nContent-Length: 0\r\n\r\n"
		if target == "/kept" {
			text := fmt.Sprintf("req=%d", served)
			resp = fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n%s", len(text), text)
		}
		if _, err := c.Write([]byte(resp)); err != nil {
			return
		}
		head, _, ok := readRequest(c)
		if !ok {
			return
		}
		parts := strings.Split(strings.SplitN(head, "\r\n", 2)[0], " ")
		if len(parts) < 3 || keptPath(parts[1]) == "" {
			return
		}
		target = keptPath(parts[1])
	}
}

// echoLine is what the /echo target answers: the request as the origin
// saw it.
func echoLine(method, target, host, body, trace, auth, cookie, accept string) string {
	return fmt.Sprintf("%s %s host=%s len=%d body=%s trace=%s auth=%s cookie=%s accept=%s", method, target, host, len(body), body, trace, auth, cookie, accept)
}

// wideBlock is 2 000 seeded random bytes: incompressible on its own,
// so a body of repeats grows far less than a hundredfold when decoded.
func wideBlock() []byte {
	block := make([]byte, 2000)
	rand.New(rand.NewSource(7)).Read(block)
	return block
}

// gzipped is data as one gzip member.
func gzipped(data []byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, _ = w.Write(data)
	_ = w.Close()
	return buf.Bytes()
}

// proxyLine is what an absolute-form target answers: the request as the
// proxy saw it.
func proxyLine(method, target, host, body, trace, proxyAuth string) string {
	return fmt.Sprintf("%s %s host=%s len=%d body=%s trace=%s proxy-auth=%s", method, target, host, len(body), body, trace, proxyAuth)
}

// SetFetchProxy names the upstream as the forward proxy for the test
// process and every program it runs (`http_proxy`, with a user and no
// password, the credential form that needs its colon supplied), so a
// request for a host other than loopback goes to the upstream as an
// absolute-form target. Loopback is never proxied, so every other case
// of FetchClientSource is unaffected.
func SetFetchProxy(t *testing.T, up *FetchUpstream) {
	t.Helper()
	t.Setenv("http_proxy", "http://u@127.0.0.1:"+strconv.Itoa(up.Port))
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
		// A TLS record (a handshake's first byte) is a client speaking
		// `https` to this plain origin: close at once, so a host doing
		// the handshake reports it instead of waiting for a server hello.
		if buf[0] == 0x16 {
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
// request and none of a POST. The pool cases pin a held `Sockets`
// carrying two requests on one connection, a one-shot `send` following a
// same-origin redirect on the connection that carried it, and a host
// platform carrying two `plat.http` requests on one connection through the
// proxy while a fresh platform dials its own.
// The policy cases pin `plat.http` refusing
// a loopback origin that `send` reaches, the numeric host forms refused
// before any lookup, and the proxy SetFetchProxy names taking every
// request for a host that is not loopback (through `send` and through
// `plat.http`, whose block list does not apply to the proxy), with the
// absolute-form target, the origin's `Host` and the proxy credentials; the
// `plat.http` request for a global literal is proxied, the one for the
// metadata address is refused before the proxy sees it.
// The gzip cases pin a `Content-Encoding: gzip` body decoded (with a
// length, chunked, beside an `identity` coding, and named `x-gzip`) and
// its encoding and length fields dropped, a HEAD answer naming a coding
// left alone, a body that is not gzip, a body past a hundredfold growth
// refused with its allowance named and two admitted (one under the
// 64 KiB the ratio never refuses, one past it but within the ratio), the
// body cap applied to the decoded bytes, two codings refused at the
// default depth and undone at depth 2 (a few bytes, then a body past the
// floor admitted and one past the ratio refused), a
// caller's own `Accept-Encoding` and `Decoding { depth: 0 }` each leaving
// the body as it came, a coding the client never asks for left alone,
// and the `Accept-Encoding: gzip` the client writes (every echo line
// carries it; `noencoding` carries the caller's).
// `closedPort` is a port nothing listens on.
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
            let text: string = "<not utf-8>";
            match (resp.body_text()) {
                Some(s) => { text = s; },
                None => {}
            }
            let fields: string = "";
            let i: i32 = 0;
            while (i < resp.headers.names.len()) {
                fields = fields + " " + resp.headers.names[i] + "=" + resp.headers.values[i];
                i = i + 1;
            }
            let trailers: string = "";
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
    let raw: u8[] = "raw".bytes();
    show("put", fetch.send(fetch.request("PUT", base() + "/echo").with_bytes(raw)));
    show("blocked", platform.host().http(fetch.get(base() + "/echo")));
    show("numeric", fetch.send(fetch.get("http://2130706433/")));
    show("octal", fetch.send(fetch.get("http://0177.0.0.1/")));
    show("short", fetch.send(fetch.get("http://127.1/")));
    show("proxied", fetch.send(fetch.request("POST", "http://origin.invalid:81/via?x=1").with_header("X-Trace", "p1").with_text("body")));
    show("platproxied", platform.host().http(fetch.get("http://8.8.8.8/via")));
    show("platproxiedblocked", platform.host().http(fetch.get("http://169.254.169.254/via")));
    let plat: platform.Host = platform.host();
    show("platkept1", plat.http(fetch.get("http://8.8.8.8/kept")));
    show("platkept2", plat.http(fetch.get("http://8.8.8.8/kept")));
    show("platkeptfresh", platform.host().http(fetch.get("http://8.8.8.8/kept")));
    match (fetch.send(fetch.get(base() + "/binary"))) {
        Ok(resp) => {
            let bs: u8[] = resp.body_bytes();
            let text: string = "<not utf-8>";
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
    let small: http.HttpLimits = http.http_limits();
    show("limit", fetch.send(fetch.get(base() + "/big").with_limits(http.HttpLimits { ...small, body: 1024 })));
    show("garbage", fetch.send(fetch.get(base() + "/garbage")));
    show("bighead", fetch.send(fetch.get(base() + "/bighead")));
    show("gzipchunked", fetch.send(fetch.get(base() + "/gzipchunked")));
    show("h2", fetch.send(fetch.get(base() + "/h2")));
    show("flood", fetch.send(fetch.get(base() + "/flood")));
    show("switch", fetch.send(fetch.get(base() + "/switch")));
    show("truncated", fetch.send(fetch.get(base() + "/truncated")));
    show("gzip", fetch.send(fetch.get(base() + "/gzip")));
    show("gzipbodychunked", fetch.send(fetch.get(base() + "/gzip-body-chunked")));
    show("gzipidentitycoding", fetch.send(fetch.get(base() + "/gzip-identity-coding")));
    show("gzipx", fetch.send(fetch.get(base() + "/x-gzip")));
    match (fetch.send(fetch.get(base() + "/gzip-floor"))) {
        Ok(resp) => { print("gzipfloor: " + resp.body_bytes().len().to_string()); },
        Err(e) => { print("gzipfloor: error " + e.message()); }
    }
    match (fetch.send(fetch.get(base() + "/gzip-wide"))) {
        Ok(resp) => { print("gzipwide: " + resp.body_bytes().len().to_string()); },
        Err(e) => { print("gzipwide: error " + e.message()); }
    }
    show("gziphead", fetch.send(fetch.request("HEAD", base() + "/gzip")));
    show("gzipbad", fetch.send(fetch.get(base() + "/gzip-bad")));
    show("gzipbomb", fetch.send(fetch.get(base() + "/gzip-bomb")));
    show("gzipcap", fetch.send(fetch.get(base() + "/gzip-bomb").with_limits(http.HttpLimits { ...small, body: 4096 })));
    show("gzipdouble", fetch.send(fetch.get(base() + "/gzip-double")));
    show("gzipdeep", fetch.send(fetch.get(base() + "/gzip-double").with_decoding(fetch.Decoding { depth: 2, ratio: 100 })));
    match (fetch.send(fetch.get(base() + "/gzip-double-wide").with_decoding(fetch.Decoding { depth: 2, ratio: 100 }))) {
        Ok(resp) => { print("gzipdeepwide: " + resp.body_bytes().len().to_string()); },
        Err(e) => { print("gzipdeepwide: error " + e.message()); }
    }
    show("gzipdeepbomb", fetch.send(fetch.get(base() + "/gzip-double-bomb").with_decoding(fetch.Decoding { depth: 2, ratio: 100 })));
    show("gzipidentity", fetch.send(fetch.get(base() + "/gzip").with_header("Accept-Encoding", "identity")));
    show("gzipoff", fetch.send(fetch.get(base() + "/gzip").with_decoding(fetch.Decoding { depth: 0, ratio: 100 })));
    show("br", fetch.send(fetch.get(base() + "/br")));
    show("noencoding", fetch.send(fetch.request("POST", base() + "/echo").with_header("Accept-Encoding", "identity").with_text("x")));
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
    let kept: fetch.Sockets = fetch.sockets();
    let open: fetch.Policy = fetch.Policy { public_only: false, proxies: fetch.proxy_env() };
    show("kept1", fetch.send_on(kept, fetch.get(base() + "/kept"), open));
    show("kept2", fetch.send_on(kept, fetch.get(base() + "/kept"), open));
    fetch.close_idle(kept);
    show("keptredir", fetch.send(fetch.get(base() + "/kept-redir")));
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

// FetchHostedSource is FetchClientSource's twin for a `wasm32-wasi-http`
// handler, where std/fetch sends through the host's outgoing-handler: the
// handler answers `/run` with one line per case, the same lines the
// program prints, for the cases the hosted route shares. It has no proxy
// cases (the host dials, so the environment's proxy is the host's own),
// no block-list cases (the host owns the network and its outbound policy
// is the rule there, so a loopback upstream is reached), and no
// `Connection: close` or interim-response cases (the host owns the
// connection and steps over 1xx itself). The host's own refusals are
// pinned where this client turns them into its errors: a response that
// is not HTTP, one cut off before it ended, a connection refused, a TLS
// handshake with an origin that speaks none, and a connection closed
// before any response byte, which the host reports as its protocol error
// (so the hosted route has no reset to retry on: `reset` and `resetpost`
// answer alike).
func FetchHostedSource(port, closedPort int) string {
	return fmt.Sprintf(`import "std/fetch";
import "std/http";
import "std/platform";

function base(): string {
    return "http://127.0.0.1:%d";
}

function line(name: string, answer: Result[HttpResponse, fetch.FetchError]): string {
    match (answer) {
        Ok(resp) => {
            let text: string = "<not utf-8>";
            match (resp.body_text()) {
                Some(s) => { text = s; },
                None => {}
            }
            let fields: string = "";
            let i: i32 = 0;
            while (i < resp.headers.names.len()) {
                fields = fields + " " + resp.headers.names[i] + "=" + resp.headers.values[i];
                i = i + 1;
            }
            let trailers: string = "";
            i = 0;
            while (i < resp.trailers.names.len()) {
                trailers = trailers + " " + resp.trailers.names[i] + "=" + resp.trailers.values[i];
                i = i + 1;
            }
            return name + ": " + resp.status.to_string() + " [" + text + "] headers:" + fields + " trailers:" + trailers + "\n";
        },
        Err(e) => { return name + ": error " + e.message() + "\n"; }
    }
    return "";
}

function size(name: string, answer: Result[HttpResponse, fetch.FetchError]): string {
    match (answer) {
        Ok(resp) => { return name + ": " + resp.body_bytes().len().to_string() + "\n"; },
        Err(e) => { return name + ": error " + e.message() + "\n"; }
    }
    return "";
}

function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    let small: http.HttpLimits = http.http_limits();
    let raw: u8[] = "raw".bytes();
    let out: string = "";
    out = out + line("plain", plat.http(fetch.get(base() + "/plain")));
    out = out + line("chunked", plat.http(fetch.get(base() + "/chunked")));
    out = out + line("nobody", plat.http(fetch.get(base() + "/nobody")));
    out = out + line("head", plat.http(fetch.request("HEAD", base() + "/plain")));
    out = out + line("echo", plat.http(fetch.request("POST", base() + "/echo?q=1").with_header("X-Trace", "t1").with_text("payload")));
    out = out + line("put", plat.http(fetch.request("PUT", base() + "/echo").with_bytes(raw)));
    out = out + line("numeric", plat.http(fetch.get("http://2130706433/")));
    out = out + line("octal", plat.http(fetch.get("http://0177.0.0.1/")));
    out = out + line("short", plat.http(fetch.get("http://127.1/")));
    match (plat.http(fetch.get(base() + "/binary"))) {
        Ok(resp) => {
            let bs: u8[] = resp.body_bytes();
            let text: string = "<not utf-8>";
            match (resp.body_text()) {
                Some(s) => { text = s; },
                None => {}
            }
            out = out + "binary: " + bs.len().to_string() + " " + (bs[0] as i32).to_string() + " " + (bs[3] as i32).to_string() + " " + text + "\n";
        },
        Err(e) => { out = out + "binary: error " + e.message() + "\n"; }
    }
    out = out + size("big", plat.http(fetch.get(base() + "/big")));
    out = out + line("limit", plat.http(fetch.get(base() + "/big").with_limits(http.HttpLimits { ...small, body: 1024 })));
    out = out + line("garbage", plat.http(fetch.get(base() + "/garbage")));
    out = out + line("truncated", plat.http(fetch.get(base() + "/truncated")));
    out = out + line("gzip", plat.http(fetch.get(base() + "/gzip")));
    out = out + line("gzipbodychunked", plat.http(fetch.get(base() + "/gzip-body-chunked")));
    out = out + line("gzipx", plat.http(fetch.get(base() + "/x-gzip")));
    out = out + size("gzipfloor", plat.http(fetch.get(base() + "/gzip-floor")));
    out = out + size("gzipwide", plat.http(fetch.get(base() + "/gzip-wide")));
    out = out + line("gziphead", plat.http(fetch.request("HEAD", base() + "/gzip")));
    out = out + line("gzipbad", plat.http(fetch.get(base() + "/gzip-bad")));
    out = out + line("gzipbomb", plat.http(fetch.get(base() + "/gzip-bomb")));
    out = out + line("gzipcap", plat.http(fetch.get(base() + "/gzip-bomb").with_limits(http.HttpLimits { ...small, body: 4096 })));
    out = out + line("gzipdouble", plat.http(fetch.get(base() + "/gzip-double")));
    out = out + line("gzipdeep", plat.http(fetch.get(base() + "/gzip-double").with_decoding(fetch.Decoding { depth: 2, ratio: 100 })));
    out = out + line("gzipidentity", plat.http(fetch.get(base() + "/gzip").with_header("Accept-Encoding", "identity")));
    out = out + line("gzipoff", plat.http(fetch.get(base() + "/gzip").with_decoding(fetch.Decoding { depth: 0, ratio: 100 })));
    out = out + line("br", plat.http(fetch.get(base() + "/br")));
    out = out + line("noencoding", plat.http(fetch.request("POST", base() + "/echo").with_header("Accept-Encoding", "identity").with_text("x")));
    out = out + line("refused", plat.http(fetch.get("http://127.0.0.1:%d/")));
    out = out + line("badurl", plat.http(fetch.get("not a url")));
    out = out + line("noscheme", plat.http(fetch.get("ftp://127.0.0.1/")));
    out = out + line("tls", plat.http(fetch.get("https://127.0.0.1:%d/plain")));
    out = out + line("crlfurl", plat.http(fetch.get(base() + "/a\r\nX-Injected: 1\r\n\r\n")));
    out = out + line("crlfvalue", plat.http(fetch.get(base() + "/plain").with_header("X-A", "1\r\nX-Injected: 1")));
    out = out + line("badname", plat.http(fetch.get(base() + "/plain").with_header("X A", "1")));
    out = out + line("badmethod", plat.http(fetch.request("GE T", base() + "/plain")));
    out = out + line("filebody", plat.http(fetch.request("POST", base() + "/echo").with_body(BodyFile("/etc/hostname"))));
    out = out + line("redir", plat.http(fetch.get(base() + "/redir")));
    out = out + line("noredir", plat.http(fetch.get(base() + "/redir").with_redirects(0)));
    out = out + line("chain10", plat.http(fetch.get(base() + "/chain/10")));
    out = out + line("chain11", plat.http(fetch.get(base() + "/chain/11")));
    out = out + line("nolocation", plat.http(fetch.get(base() + "/nolocation")));
    out = out + line("badlocation", plat.http(fetch.get(base() + "/badlocation")));
    out = out + line("relative", plat.http(fetch.get(base() + "/rel/dir/relative")));
    out = out + line("post303", plat.http(fetch.request("POST", base() + "/303").with_text("payload")));
    out = out + line("post307", plat.http(fetch.request("POST", base() + "/307").with_text("payload")));
    out = out + line("xorigin", plat.http(fetch.get(base() + "/xorigin").with_header("Authorization", "Bearer t").with_header("Cookie", "a=1").with_header("X-Trace", "kept")));
    out = out + line("sameorigin", plat.http(fetch.get(base() + "/sameorigin").with_header("Authorization", "Bearer t").with_header("Cookie", "a=1")));
    out = out + line("reset", plat.http(fetch.get(base() + "/reset")));
    out = out + line("resetpost", plat.http(fetch.request("POST", base() + "/reset-post").with_text("once")));
    return http.ok(out);
}
`, port, closedPort, port)
}

// FetchHostedWant is what FetchHostedSource answers `/run` with. Response
// header names arrive from the wasi:http host lowercased, whatever the
// origin sent.
const FetchHostedWant = `plain: 200 [hello] headers: content-length=5 x-up=1 trailers:
chunked: 200 [abcde] headers: trailers:
nobody: 204 [] headers: content-length=99 trailers:
head: 200 [] headers: content-length=5 x-up=1 trailers:
echo: 201 [ECHO] headers: content-length=ECHOLEN trailers:
put: 201 [PUT] headers: content-length=PUTLEN trailers:
numeric: error invalid URL: an IPv4 address that is not four decimal octets
octal: error invalid URL: an IPv4 address that is not four decimal octets
short: error invalid URL: an IPv4 address that is not four decimal octets
binary: 4 255 97 <not utf-8>
big: 312000
limit: error response body past its limit
garbage: error protocol: the host read a malformed response
truncated: error protocol: the response ended before it was complete
gzip: 200 [hello gzip] headers: x-up=1 trailers:
gzipbodychunked: 200 [hello gzip] headers: trailers:
gzipx: 200 [hello gzip] headers: trailers:
gzipfloor: 60000
gzipwide: 100000
gziphead: 200 [] headers: content-encoding=gzip content-length=GZLEN x-up=1 trailers:
gzipbad: error decode: a gzip body that does not decode: unsupported compressed data: not gzip
gzipbomb: error decode: a body of 200000 bytes from ONEBOMB encoded, past the 65536 allowed
gzipcap: error response body past its limit
gzipdouble: error decode: content codings past the depth of 1
gzipdeep: 200 [twice] headers: trailers:
gzipidentity: 200 [<not utf-8>] headers: content-encoding=gzip content-length=GZLEN x-up=1 trailers:
gzipoff: 200 [<not utf-8>] headers: content-encoding=gzip content-length=GZLEN x-up=1 trailers:
br: 200 [raw] headers: content-encoding=br content-length=3 trailers:
noencoding: 201 [NOENC] headers: content-length=NOENCLEN trailers:
refused: error connect: Connection refused
badurl: error invalid URL: no scheme in not a url
noscheme: error invalid URL: scheme ftp is not http
tls: error TLS: a protocol error
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
post307: 201 [POST307] headers: content-length=POST307LEN trailers:
xorigin: 201 [XORIGIN] headers: content-length=XORIGINLEN trailers:
sameorigin: 201 [SAMEORIGIN] headers: content-length=SAMEORIGINLEN trailers:
reset: error protocol: the host read a malformed response
resetpost: error protocol: the host read a malformed response
`

// CheckFetchHosted compares the hosted handler's answer against
// FetchHostedWant with the upstream's ports and the echo lines filled in.
func CheckFetchHosted(t *testing.T, up *FetchUpstream, body string) {
	t.Helper()
	host := "127.0.0.1:" + strconv.Itoa(up.Port)
	host2 := "127.0.0.1:" + strconv.Itoa(up.Port2)
	want := FetchHostedWant
	want = strings.ReplaceAll(want, "GZLEN", strconv.Itoa(len(gzipped([]byte("hello gzip")))))
	want = strings.ReplaceAll(want, "ONEBOMB", strconv.Itoa(len(gzipped(make([]byte, 200000)))))
	for name, line := range map[string]string{
		"ECHO":       echoLine("POST", "/echo?q=1", host, "payload", "t1", "", "", "gzip"),
		"PUT":        echoLine("PUT", "/echo", host, "raw", "", "", "", "gzip"),
		"POST303":    echoLine("GET", "/echo", host, "", "", "", "", "gzip"),
		"POST307":    echoLine("POST", "/echo", host, "payload", "", "", "", "gzip"),
		"XORIGIN":    echoLine("GET", "/echo", host2, "", "kept", "", "", "gzip"),
		"SAMEORIGIN": echoLine("GET", "/echo", host, "", "", "Bearer t", "a=1", "gzip"),
		"NOENC":      echoLine("POST", "/echo", host, "x", "", "", "", "identity"),
	} {
		want = strings.ReplaceAll(want, "["+name+"]", "["+line+"]")
		want = strings.ReplaceAll(want, name+"LEN", strconv.Itoa(len(line)))
	}
	if body != want {
		t.Fatalf("--- got ---\n%s--- want ---\n%s", body, want)
	}
}

// FetchClientWant is what FetchClientSource prints.
const FetchClientWant = `plain: 200 [hello] headers: Content-Length=5 X-Up=1 trailers:
chunked: 200 [abcde] headers: trailers: X-T=tv
interim: 404 [close-delimited] headers: X-Final=yes trailers:
nobody: 204 [] headers: Content-Length=99 trailers:
head: 200 [] headers: Content-Length=5 X-Up=1 trailers:
echo: 201 [ECHO] headers: Content-Length=ECHOLEN trailers:
put: 201 [PUT] headers: Content-Length=PUTLEN trailers:
blocked: error blocked: 127.0.0.1 is not a global address
numeric: error invalid URL: an IPv4 address that is not four decimal octets
octal: error invalid URL: an IPv4 address that is not four decimal octets
short: error invalid URL: an IPv4 address that is not four decimal octets
proxied: 201 [VIAPOST] headers: Content-Length=VIAPOSTLEN trailers:
platproxied: 201 [VIAGET] headers: Content-Length=VIAGETLEN trailers:
platproxiedblocked: error blocked: 169.254.169.254 is not a global address
platkept1: 200 [req=1] headers: Content-Length=5 trailers:
platkept2: 200 [req=2] headers: Content-Length=5 trailers:
platkeptfresh: 200 [req=1] headers: Content-Length=5 trailers:
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
gzip: 200 [hello gzip] headers: X-Up=1 trailers:
gzipbodychunked: 200 [hello gzip] headers: trailers:
gzipidentitycoding: 200 [hello gzip] headers: trailers:
gzipx: 200 [hello gzip] headers: trailers:
gzipfloor: 60000
gzipwide: 100000
gziphead: 200 [] headers: Content-Encoding=gzip Content-Length=GZLEN X-Up=1 trailers:
gzipbad: error decode: a gzip body that does not decode: unsupported compressed data: not gzip
gzipbomb: error decode: a body of 200000 bytes from ONEBOMB encoded, past the 65536 allowed
gzipcap: error response body past its limit
gzipdouble: error decode: content codings past the depth of 1
gzipdeep: 200 [twice] headers: trailers:
gzipdeepwide: 100000
gzipdeepbomb: error decode: a body of 100000 bytes from TWOBOMB encoded, past the 65536 allowed
gzipidentity: 200 [<not utf-8>] headers: Content-Encoding=gzip Content-Length=GZLEN X-Up=1 trailers:
gzipoff: 200 [<not utf-8>] headers: Content-Encoding=gzip Content-Length=GZLEN X-Up=1 trailers:
br: 200 [raw] headers: Content-Encoding=br Content-Length=3 trailers:
noencoding: 201 [NOENC] headers: Content-Length=NOENCLEN trailers:
refused: error connect: Connection refused
badurl: error invalid URL: no scheme in not a url
noscheme: error invalid URL: scheme ftp is not http
tls: error TLS: https is not supported on this target
crlfurl: error invalid URL: a path or query that cannot be written on a request line
crlfvalue: error invalid request: a header value has a byte that cannot be written
badname: error invalid request: a header name is not a token
badmethod: error invalid request: the method is not a token
filebody: error invalid request: a file body cannot be sent
redir: 200 [hello] headers: Content-Length=5 X-Up=1 trailers:
noredir: 302 [] headers: Location=/plain Content-Length=0 trailers:
chain10: 200 [end] headers: Content-Length=3 trailers:
chain11: error redirect: more than 10 redirects
nolocation: error redirect: a 302 without a Location
badlocation: error redirect: a Location that is not a URL
relative: 200 [/rel/plain?q=2] headers: Content-Length=14 trailers:
post303: 201 [POST303] headers: Content-Length=POST303LEN trailers:
post302: 201 [POST302] headers: Content-Length=POST302LEN trailers:
post307: 201 [POST307] headers: Content-Length=POST307LEN trailers:
xorigin: 201 [XORIGIN] headers: Content-Length=XORIGINLEN trailers:
sameorigin: 201 [SAMEORIGIN] headers: Content-Length=SAMEORIGINLEN trailers:
reset: 200 [retried] headers: Content-Length=7 trailers:
resetpost: error i/o: Connection reset by peer
kept1: 200 [req=1] headers: Content-Length=5 trailers:
kept2: 200 [req=2] headers: Content-Length=5 trailers:
keptredir: 200 [req=2] headers: Content-Length=5 trailers:
status: 404
`

// CheckFetchClient compares the client's output against FetchClientWant
// with the upstream's ports and the echo lines filled in.
func CheckFetchClient(t *testing.T, up *FetchUpstream, stdout string, exit int) {
	t.Helper()
	host := "127.0.0.1:" + strconv.Itoa(up.Port)
	host2 := "127.0.0.1:" + strconv.Itoa(up.Port2)
	want := FetchClientWant
	want = strings.ReplaceAll(want, "GZLEN", strconv.Itoa(len(gzipped([]byte("hello gzip")))))
	want = strings.ReplaceAll(want, "ONEBOMB", strconv.Itoa(len(gzipped(make([]byte, 200000)))))
	want = strings.ReplaceAll(want, "TWOBOMB", strconv.Itoa(len(gzipped(gzipped(make([]byte, 100000))))))
	for name, line := range map[string]string{
		"ECHO":       echoLine("POST", "/echo?q=1", host, "payload", "t1", "", "", "gzip"),
		"PUT":        echoLine("PUT", "/echo", host, "raw", "", "", "", "gzip"),
		"VIAPOST":    proxyLine("POST", "http://origin.invalid:81/via?x=1", "origin.invalid:81", "body", "p1", "Basic dTo="),
		"VIAGET":     proxyLine("GET", "http://8.8.8.8/via", "8.8.8.8", "", "", "Basic dTo="),
		"POST303":    echoLine("GET", "/echo", host, "", "", "", "", "gzip"),
		"POST302":    echoLine("GET", "/echo", host, "", "", "", "", "gzip"),
		"POST307":    echoLine("POST", "/echo", host, "payload", "", "", "", "gzip"),
		"XORIGIN":    echoLine("GET", "/echo", host2, "", "kept", "", "", "gzip"),
		"SAMEORIGIN": echoLine("GET", "/echo", host, "", "", "Bearer t", "a=1", "gzip"),
		"NOENC":      echoLine("POST", "/echo", host, "x", "", "", "", "identity"),
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
