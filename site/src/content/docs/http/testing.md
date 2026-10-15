---
title: Testing HTTP code
description: Call handlers directly, record effects with the mock platform, and run whole services against a deterministic simulated network — latency, resets and timeouts scripted from a seed, with record-and-replay cassettes.
sidebar:
  order: 9
---

A handler is a function and its effects come through `plat`, so testing
one needs no server, no socket and no network. There are three levels,
from quick to thorough.

## Call the handler

`http.request(method, path)` builds a request, with `with_header`,
`with_body` and `with_json` to fill it in. Pass a platform and assert on
what comes back. `std/test`'s HTTP assertions name the status, header or
body they expected and what they got.

```fern
import "std/test";
import "std/http";
import "std/http/router";
import "std/mock_platform";
import "std/platform";

function routes[P: platform.Platform](): Result[router.Router[P], router.RouteError] {
    let r: router.Router[P] = router.new();
    return Ok(r.get("/users/{id}", show)?);
}

function show[P: platform.Platform](req: HttpRequest, params: router.Params, plat: P): HttpResponse {
    return http.respond(page(params));
}

function page(params: router.Params): Result[HttpResponse, http.HttpError] {
    let id: i32 = params.int("id")?;
    return Ok(http.json("{\"id\":" + id.to_string() + "}"));
}

function send(method: string, path: string): HttpResponse {
    let m: mock_platform.MockPlatform = mock_platform.mock_platform_new();
    match (routes[mock_platform.MockPlatform]()) {
        Ok(r) => { return r.handle(http.request(method, path), m); },
        Err(e) => { return http.internal_error(e.message()); }
    }
}

function main(): i32 {
    let r: test.TestRunner = test.test_new("users");
    r = r.it("finds a user", (): test.TestOutcome => test.assert_body(send("GET", "/users/7"), "{\"id\":7}"));
    r = r.it("refuses a bad id", (): test.TestOutcome => test.assert_status(send("GET", "/users/x"), 400));
    r = r.it("405s a POST", (): test.TestOutcome => test.assert_header(send("POST", "/users/7"), "allow", "GET, HEAD"));
    return r.finish();
}
```

```bash
fern -interp users_test.fern     # or compile it and run the binary
# TAP version 13
# # Suite: users
# ok 1 - finds a user
# ok 2 - refuses a bad id
# ok 3 - 405s a POST
```

The router is generic over the platform, so the test instantiates it
with the mock while `main` in production uses `platform.Host`.

## Script the platform: `MockPlatform`

`std/mock_platform`'s `MockPlatform` implements `Platform` with canned
answers and a record of every call. Can the configuration, the clock,
randomness and upstream responses; then assert on the response and on
what the handler did.

```fern
import "std/test";
import "std/http";
import "std/fetch";
import "std/mock_platform";
import "std/option";
import "std/platform";

function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    let region: string = plat.config("REGION").unwrap_or("local");
    match (plat.http(fetch.get("https://status.example.com/health"))) {
        Ok(resp) => {
            plat.log("upstream said " + resp.status.to_string());
            return http.plain(region + " " + resp.body_string());
        },
        Err(e) => { return http.text(502, region + " upstream down"); }
    }
}

function test_upstream_up(): test.TestOutcome {
    let m: mock_platform.MockPlatform = mock_platform.mock_platform_new();
    m.config_set("REGION", "eu-west");
    m.http_set("GET", "https://status.example.com/health", 200, "fine");
    let resp: HttpResponse = handle(http.request("GET", "/"), m);
    if (!m.has_call("log")) {
        return test.fail("expected a log line");
    }
    return test.assert_body(resp, "eu-west fine");
}

function test_upstream_down(): test.TestOutcome {
    // An uncanned request fails as a refused connection.
    let m: mock_platform.MockPlatform = mock_platform.mock_platform_new();
    return test.assert_status(handle(http.request("GET", "/"), m), 502);
}

function main(): i32 {
    let r: test.TestRunner = test.test_new("health");
    r = r.it("passes the upstream through", test_upstream_up);
    r = r.it("502s when the upstream is down", test_upstream_down);
    return r.finish();
}
```

`m.calls()` lists every call in order as `MockCall { name, args }` —
an HTTP call's args are `"METHOD url"` — and `find_call`, `call_count`
and `reset` query it. `now_set`, `random_set`, `peer_set`,
`deadline_set`, `trace_set`, `file_set` and `cancel()` can the rest.
`m.materialize(resp)` reads a file body into bytes so a test can look
at what `http.file` or `std/http/static` would have sent.

## Simulate the network

A mock answers instantly and exactly as told. Real networks are slow,
reset connections, stall half-way through a body and time out — and the
bugs live there. `std/sim_fetch` is a scripted network that the **real**
`std/fetch` client runs over, in virtual time from `std/sim`: the same
retry, redirect, pooling and timeout code as production, with every
latency and fault decided by the script and a seed.

```fern
import "std/test";
import "std/http";
import "std/fetch";
import "std/sim";
import "std/sim_fetch";
import "std/sim_platform";
import "std/time";
import "std/platform";

function handle(req: HttpRequest, plat: platform.Platform): Result[HttpResponse, fetch.FetchError] {
    let t: fetch.Timeouts = fetch.Timeouts { ...fetch.timeouts(), total: time.duration_millis(500 as i64) };
    let resp: HttpResponse = plat.http(fetch.get("http://api.test/price").with_timeouts(t))?;
    return Ok(http.plain("price " + resp.body_string()));
}

const API: string = "93.184.216.34";

function run(answers: sim_fetch.Answer[]): (HttpResponse, i64) {
    let d: sim.Sim = sim.new(1);
    let n: sim_fetch.Net = sim_fetch.net(d)
        .host("api.test", [API])
        .listen(API, 80, 5)
        .route(API, 80, "/price", answers);
    let resp: HttpResponse = handle(http.request("GET", "/"), sim_platform.new(d, n));
    return (resp, d.now_ns() / (1000000 as i64));
}

function test_slow_but_in_time(): test.TestOutcome {
    let got: (HttpResponse, i64) = run([sim_fetch.reply(200, "42").after(300)]);
    return test.assert_eq(got.0.status.to_string() + " at " + got.1.to_string() + "ms", "200 at 305ms");
}

function test_too_slow(): test.TestOutcome {
    let got: (HttpResponse, i64) = run([sim_fetch.reply(200, "42").after(900)]);
    return test.assert_status(got.0, 504);
}

function test_one_reset_is_retried(): test.TestOutcome {
    let got: (HttpResponse, i64) = run([sim_fetch.reset(), sim_fetch.reply(200, "42")]);
    return test.assert_body(got.0, "price 42");
}

function main(): i32 {
    let r: test.TestRunner = test.test_new("price");
    r = r.it("answers a slow upstream in time", test_slow_but_in_time);
    r = r.it("504s past the deadline", test_too_slow);
    r = r.it("retries a reset GET once", test_one_reset_is_retried);
    return r.finish();
}
```

`handle` returns a `Result`, yet the test receives an `HttpResponse`: the
compiler wraps a top-level `Result` handler so an `Err` becomes its
response, and calls in the same program see the wrapped function — what a
client of the server would see.

The 300 ms upstream takes no wall-clock time at all: the simulation's
clock moves only when every task is waiting, straight to the next event.
A test of a two-minute timeout runs in microseconds, and it runs the same
way on every machine and every target, so a failing seed is a seed you
can replay.

What a script can say:

| Answer | Behaviour |
|---|---|
| `sim_fetch.reply(status, body)` | A response |
| `sim_fetch.redirect(status, location)` | A redirect |
| `sim_fetch.raw(text)` / `raw_bytes(bs)` | Exact bytes on the wire, malformed or not |
| `sim_fetch.reset()` | The connection resets |
| `sim_fetch.silent()` | The server accepts and never answers |
| `.after(ms)` | Wait before the first byte |
| `.in_chunks(size, every_ms)` | Dribble the response out |
| `.kept()` / `.held()` | Keep the connection open for reuse, or open but idle |

A route's answers are used in order, the last repeating; an unrouted
path is a 404. `n.connects()`, `n.open()`, `n.sent_count()` and
`n.sent(i)` report what the client did — how many connections it
opened, and the exact bytes of each request.

### Serving a handler in memory

`n.serve(addr, port, handler)` puts a handler on the simulated network,
so a client — or a service under test that calls another service — talks
to it over HTTP with no socket:

```fern
let n: sim_fetch.Net = sim_fetch.net(d)
    .host("users.test", [USERS])
    .serve(USERS, 80, (req: HttpRequest): HttpResponse => users.handle(req, plat));
```

### Sweeping seeds

`sim.sweep_seeds(n, property)` runs a property for seeds `1..n` and
returns the first that fails, or 0. Combine it with answers whose timing
comes from the seed, and one test explores hundreds of interleavings.
`examples/tests/api_sim.fern` in the repository runs the [routed API
example](../../tutorial/http-server/#a-routed-api-logged-simulated)
against a directory service that resets twice.

## Record and replay

A cassette is a recording of real exchanges, replayed as a simulated
network — integration tests that hit a third-party API once, then run
offline and deterministically. `sim_fetch.send_taped(transport, tape,
req, policy)` sends a request and appends the exchange to `tape`, one
JSON object per line:

```json
{"method":"GET","url":"http://api.test/users/7","status":200,"headers":[["content-type","application/json"]],"body":"{\"id\":7}"}
```

`sim_fetch.net(sim.new(1)).replay(cassette)` builds a network that
answers those requests with those responses. Bodies that are not UTF-8
are stored as `body_hex`. The simulated network has no TLS, so an
`https` exchange replays as plain HTTP.

## End to end, with a real socket

The simulated network covers what a handler does. The server loop under
it — framing, keep-alive, TLS, HTTP/2, shutdown — is exercised by the
standard library's own suites, including h2spec, the Autobahn WebSocket
suite and the gRPC interoperability suite, run nightly. To smoke-test a
built binary anyway, start it on a test port and drive it with
`fetch.send` from a test program.

Back to the [overview](../).
