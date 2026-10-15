---
title: Calling other services
description: The `std/fetch` client — from a program or from inside a handler through `plat.http` — with timeouts, redirects, retries, allow lists, pooling, HTTP/2, cookies and custom trust roots.
sidebar:
  order: 5
---

`std/fetch` is Fern's HTTP client: HTTP/1.1 and HTTP/2, TLS 1.3,
redirects, decompression and connection pooling, written in Fern on the
socket and DNS primitives. You build a `fetch.Request` value and send it
one of two ways:

- **From a program**, `fetch.send(req)`.
- **From a handler**, `plat.http(req)` — the same client, scoped by the
  platform so tests can replace it and the network can be restricted.

## From a program

```fern
import "std/fetch";
import "std/http";
import "std/option";

function main(): i32 {
    match (fetch.send(fetch.get("https://example.com/"))) {
        Ok(resp) => {
            print(resp.status.to_string() + " " + resp.headers.get("content-type").unwrap_or("?"));
            print(resp.body_text().unwrap_or("<not UTF-8>"));
            return 0;
        },
        Err(e) => {
            eprint("fetch failed: " + e.message());
            return 1;
        }
    }
}
```

`resp.body_text()` is the body as checked UTF-8 (`None` when it is not);
`body_bytes()` gives the bytes. A non-2xx status is not an error — the
server answered. `resp.ok_or_status()` turns one into `Err(status)` when
you want it to be.

## Building requests

```fern
let req: fetch.Request = fetch.request("POST", "https://api.example.com/items")
    .with_header("content-type", "application/json")
    .with_header("authorization", "Bearer " + token)
    .with_text("{\"name\":\"axe\"}")
    .with_timeouts(fetch.Timeouts { ...fetch.timeouts(), total: time.duration_seconds(5 as i64) })
    .with_redirects(0);
```

| Builder | Effect |
|---|---|
| `fetch.get(url)`, `fetch.request(method, url)` | Start a request |
| `with_header(name, value)` | Add a header; `Host`, `Content-Length` and `Connection` are written for you |
| `with_text(s)`, `with_bytes(bs)`, `with_body(body)` | Set the body |
| `with_timeouts(t)` | `connect` (10 s), `inactivity` between bytes (30 s), `total` (60 s) |
| `with_redirects(n)` | Follow up to `n` redirects (default 10); `0` returns the 3xx itself |
| `with_limits(l)` | Cap the response — `l.body` is the largest body accepted (1 MiB by default) |
| `with_decoding(d)` | How much `Content-Encoding` to undo; depth `0` asks for none |
| `with_ktls()` | Hand TLS records to the Linux kernel after the handshake |

What the client does without being asked:

- **Redirects** are followed, and credentials are dropped on a hop that
  leaves the origin.
- **Retries**: an idempotent request whose connection was reset before
  any byte of the response is retried once, on a fresh connection.
- **Compression**: it asks for `zstd`, `gzip` and `br` and decodes the
  answer, with a ratio guard against decompression bombs — unless you
  set `Accept-Encoding` yourself.
- **Framing**: chunked bodies are decoded, and trailers land in
  `resp.trailers`.
- **Proxies**: `http_proxy`, `https_proxy` and `no_proxy` are honoured.
- **TLS**: `https` is TLS 1.3, verified against the system's roots
  (`SSL_CERT_FILE` overrides them). ALPN offers `h2`, and an origin that
  picks it is spoken to over HTTP/2.

## Errors

Every failure is a `fetch.FetchError` case: `InvalidUrl`, `Dns`,
`Connect`, `Tls`, `Timeout(phase)` (which of connect, inactivity or
total ran out), `Protocol`, `Redirect`, `BodyLimit`, `Decode`,
`Blocked` (the policy refused the host), `Cancelled` and a few more.
`e.message()` describes it.

`FetchError` implements `http.ToResponse`, so a handler can `?` an
upstream failure straight into its own response: 504 for a timeout, 502
for the rest. The message is not sent to the client, since it would name
your upstream.

## From a handler: `plat.http`

```fern
import "std/http";
import "std/serve";
import "std/fetch";
import "std/platform";

function handle(req: HttpRequest, plat: platform.Platform): Result[HttpResponse, fetch.FetchError] {
    let upstream: HttpResponse = plat.http(fetch.get("https://api.github.com/zen")
        .with_header("user-agent", "fern-example"))?;
    return Ok(http.plain("GitHub says: " + upstream.body_string() + "\n"));
}
```

While the handler waits for the upstream it is parked, and the worker
serves other requests. Compared with `fetch.send`, `plat.http`:

- **reaches only global addresses.** Loopback, private ranges,
  link-local, cloud metadata endpoints and IPv4-mapped tricks are refused
  as `Blocked`, checked against the addresses actually dialled, so a DNS
  name that rebinds to `127.0.0.1` is caught too. This is the
  server-side request forgery guard a handler fetching user-supplied URLs
  needs. A service reached on a private address goes through a proxy
  named by `http_proxy`.
- **keeps a connection pool** for the worker's lifetime, and HTTP/2
  origins are multiplexed: concurrent handlers share one connection per
  origin, up to 100 streams.
- **carries the trace**: with tracing on, the upstream request gets a
  child `traceparent`.
- **is cancelled with the handler**: past `handler_deadline`, or once the
  client has gone, it returns `Err(Cancelled)`.
- **is replaceable**: a test's mock or simulated platform answers it
  instead of the network.

### Restricting where handlers can go

An allow list narrows `plat.http` further: every host not on it is
`Blocked` before anything is resolved, on every redirect hop.

```fern
import "std/http";
import "std/serve";
import "std/fetch";
import "std/platform";

function init(): serve.Config {
    match (fetch.allow(["api.github.com", "*.stripe.com", "hooks.example.com:8443"])) {
        Ok(a) => { return serve.Config { ...serve.config(), http_allow: Some(a) }; },
        Err(e) => {
            eprint(e);
            return serve.config();
        }
    }
}

function handle(req: HttpRequest, plat: platform.Platform): Result[HttpResponse, fetch.FetchError] {
    let r: HttpResponse = plat.http(fetch.get("https://example.com/"))?;
    return Ok(http.ok("unreachable\n"));
}
```

Here the handler answers 502: `example.com` is not on the list.

## Cookies

`plat.http` and `fetch.send` keep no cookies unless given a jar. Turn one
on for a server's handlers with `serve.Config { cookie_jar: true }`. In a
program, put a jar in the policy:

```fern
import "std/fetch";
import "std/http";
import "std/http/cookie";

function main(): i32 {
    let jar: cookie.Jar = cookie.jar();
    let policy: fetch.Policy = fetch.Policy { ...fetch.public_policy(), jar: Some(jar) };
    let tr: fetch.Sockets = fetch.sockets();
    match (fetch.send_on(tr, fetch.get("https://example.com/login"), policy)) {
        Ok(first) => {
            // Cookies the first response set ride along on this one.
            match (fetch.send_on(tr, fetch.get("https://example.com/account"), policy)) {
                Ok(second) => { print(second.status.to_string()); },
                Err(e) => { eprint(e.message()); }
            }
        },
        Err(e) => { eprint(e.message()); }
    }
    fetch.close_idle(tr);
    return 0;
}
```

The jar follows RFC 6265bis: it refuses cookies for a public suffix or a
foreign domain, keeps `Secure` cookies to secure origins, honours the
`__Host-` and `__Secure-` prefixes, and caps how many it holds.

## Transports, policies and pools

`fetch.send` is a convenience over `fetch.send_on(transport, req,
policy)`, and the two arguments are where the client is configured:

- **The transport** is how bytes move. `fetch.sockets()` is the real
  network with a fresh pool; `fetch.sockets_with(fetch.idle_limited(16,
  time.duration_seconds(30 as i64)))` sizes the pool. Keep one transport
  across calls to reuse connections, and `fetch.close_idle(tr)` when
  done. `sim_fetch.Net` is a transport too — a scripted network for
  tests.
- **The policy** is what the client may do: `public_only` (what
  `plat.http` enforces), `allow`, `jar`, `proxies`, and `roots`.

### Trust roots

The default roots are the machine's. Where a machine has none — a slim
container, or a WASI program without the certificate directory — use the
bundled Mozilla set, or your own CA:

```fern
import "std/fetch";
import "std/http";
import "std/tls/mozilla";
import "std/tls/verify";

function main(): i32 {
    let mozilla_policy: fetch.Policy = fetch.Policy { ...fetch.public_policy(), roots: Some(mozilla.roots()) };
    match (verify.roots_from_file("/etc/my-ca.pem")) {
        Ok(internal) => {
            let internal_policy: fetch.Policy = fetch.Policy { ...fetch.public_policy(), public_only: false, roots: Some(internal) };
            match (fetch.send_on(fetch.sockets(), fetch.get("https://billing.internal/"), internal_policy)) {
                Ok(r) => { print(r.status.to_string()); },
                Err(e) => { eprint(e.message()); }
            }
        },
        Err(e) => { eprint("no CA file"); }
    }
    match (fetch.send_on(fetch.sockets(), fetch.get("https://example.com/"), mozilla_policy)) {
        Ok(r) => { print(r.status.to_string()); },
        Err(e) => { eprint(e.message()); }
    }
    return 0;
}
```

For mutual TLS and other connection-level control, `std/tls/client`
connects directly: `client.config(name, roots).with_identity(id)` gives a
client certificate, and `client.connect(host, port, cfg, timeout)` a TLS
connection.

## Under `wasm32-wasi-http`

A component dials nothing itself: `fetch` hands the request to the
host's `wasi:http/outgoing-handler`, which resolves, connects and speaks
TLS. The host's outbound policy then decides what is reachable, and the
pool, proxy variables and kTLS do not apply. Handler code is unchanged.

Next: [Streaming →](../streaming/)
