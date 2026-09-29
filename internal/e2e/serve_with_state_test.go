// `tcp_serve_with` (#2679) — e2e for the state-threading serve loop on
// native x86-64; the scenario is e2eharness's, shared with the self-host
// twin.
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
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestServeWithThreadedStateX86_64(t *testing.T) {
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, e2eharness.ThreadedStateServerSource(port))
	startSupervisedServer(t, bin, runner)
	e2eharness.CheckThreadedState(t, fmt.Sprintf("127.0.0.1:%d", port))
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
	e2eharness.WaitServerReady(t, addr, 10*time.Second)

	for want := 1; want <= 3; want++ {
		resp := e2eharness.HTTPRoundTrip(t, addr, "/a", 5*time.Second)
		if got := e2eharness.ResponseBodyTail(resp); got != fmt.Sprintf("/a=%d", want) {
			t.Fatalf("request %d to /a: body %q, want %q (init's state did not reach the next request)",
				want, got, fmt.Sprintf("/a=%d", want))
		}
	}
	if got := e2eharness.ResponseBodyTail(e2eharness.HTTPRoundTrip(t, addr, "/b", 5*time.Second)); got != "/b=1" {
		t.Fatalf("first request to /b: body %q, want \"/b=1\"", got)
	}
}
