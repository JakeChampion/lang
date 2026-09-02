// `tcp_serve_with` (#2679) — e2e for the state-threading serve loop on
// native x86-64.
//
// The state is a `Map[string, i32]`, deliberately: that is the case a
// closure-captured `Cell[T]` cannot cover, since E057 restricts a cell's
// element to cycle-free scalars and strings. The test pins the two
// properties threading is supposed to give — a value survives from one
// request to the next, and per-key state stays independent — plus the
// negative that makes them meaningful: a server that rebuilt its state
// each request would answer 1 every time.
package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

const serveWithStateSrc = `
import "std/http";
import "std/tcp";
import "core/int";

function handle(hits: Map[string, i32], req: HttpRequest, plat: Platform): (Map[string, i32], HttpResponse) {
    var n: i32 = 1;
    match (hits.get(req.path)) {
        Some(prev) => { n = prev + 1; },
        None => {}
    }
    return (hits.insert(req.path, n),
            http.http_response_ok(req.path + "=" + int.int_to_string(n)));
}

function main(): i32 {
    var init: Map[string, i32] = map_new(8);
    return tcp.tcp_serve_with(%d, init, handle);
}`

func TestServeWithThreadedStateX86_64(t *testing.T) {
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, fmt.Sprintf(serveWithStateSrc, port))
	_, _ = startSupervisedServer(t, bin, runner)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	waitServerReady(t, addr, 10*time.Second)

	// Same path three times: the counter must climb, which it can only
	// do if the Map returned by one request reached the next one.
	for want := 1; want <= 3; want++ {
		resp := httpRoundTrip(t, addr, "/a", 5*time.Second)
		if got := responseBodyTail(resp); got != fmt.Sprintf("/a=%d", want) {
			t.Fatalf("request %d to /a: body %q, want %q (state did not survive the request boundary)",
				want, got, fmt.Sprintf("/a=%d", want))
		}
	}

	// A different key starts at 1 — the threaded value is a real map,
	// not one shared counter.
	if got := responseBodyTail(httpRoundTrip(t, addr, "/b", 5*time.Second)); got != "/b=1" {
		t.Fatalf("first request to /b: body %q, want \"/b=1\"", got)
	}

	// ...and the original key keeps counting from where it was, so the
	// second key's insert did not replace the accumulated state.
	if got := responseBodyTail(httpRoundTrip(t, addr, "/a", 5*time.Second)); got != "/a=4" {
		t.Fatalf("fourth request to /a: body %q, want \"/a=4\"", got)
	}
}

// responseBodyTail returns the body of an HTTP/1.1 response, or "" when
// the response has no header/body separator (a closed connection).
func responseBodyTail(resp string) string {
	i := strings.Index(resp, "\r\n\r\n")
	if i < 0 {
		return ""
	}
	return resp[i+4:]
}

// The same threading, with no `main` written at all: `init(): S` and a
// state-taking `handle` are the two-phase lifecycle the auto-main
// synthesis recognises (docs/PLATFORM-RESEARCH.md Rec §3), so the
// program is the two entry points and nothing else. What this pins over
// the test above is the WIRING — that the synthesised main routes
// through tcp_serve_with and hands it init()'s value, rather than
// building the state per request or dropping it.
const serveInitStateSrc = `
import "std/http";
import "std/tcp";
import "core/int";

function init(): Map[string, i32] {
    return map_new(8);
}

function handle(hits: Map[string, i32], req: HttpRequest, plat: Platform): (Map[string, i32], HttpResponse) {
    var n: i32 = 1;
    match (hits.get(req.path)) {
        Some(prev) => { n = prev + 1; },
        None => {}
    }
    return (hits.insert(req.path, n),
            http.http_response_ok(req.path + "=" + int.int_to_string(n)));
}`

func TestServeInitProvidedStateX86_64(t *testing.T) {
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, serveInitStateSrc)
	_, _ = startSupervisedServer(t, bin, runner, fmt.Sprintf("PORT=%d", port))
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	waitServerReady(t, addr, 10*time.Second)

	for want := 1; want <= 3; want++ {
		resp := httpRoundTrip(t, addr, "/a", 5*time.Second)
		if got := responseBodyTail(resp); got != fmt.Sprintf("/a=%d", want) {
			t.Fatalf("request %d to /a: body %q, want %q (init's state did not reach the next request)",
				want, got, fmt.Sprintf("/a=%d", want))
		}
	}
	if got := responseBodyTail(httpRoundTrip(t, addr, "/b", 5*time.Second)); got != "/b=1" {
		t.Fatalf("first request to /b: body %q, want \"/b=1\"", got)
	}
}
