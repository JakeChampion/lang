---
title: Running in production
description: Prometheus metrics from the supervisor, W3C trace context, client addresses behind proxies and the PROXY protocol, health checks, and deploying natively, under systemd, in containers and on WASI hosts.
sidebar:
  order: 8
---

## Metrics

Set `metrics_port` and the server exposes Prometheus metrics on a port
of its own: `/metrics`, plus `/healthz` and `/readyz` for probes. Under
the supervisor, each worker sends the supervisor a snapshot once a
second and the supervisor merges them and answers the scrape, so a
scrape never lands on a busy worker and the numbers cover the whole
process tree.

Without any code, the server reports:

- `http_server_request_duration_seconds` — a histogram by method and
  status, with OpenTelemetry's recommended buckets;
- `http_server_connections_accepted_total`;
- `http_server_open_connections`.

Your own counters, gauges and histograms go in the registry beside them.
A metric's value lives in a shared cell, so the copy a handler holds and
the copy the registry holds count into the same number:

```fern
import "std/http";
import "std/serve";
import "std/metrics";
import "std/platform";

function main(): i32 {
    let orders: metrics.Counter = metrics.counter("shop_orders_total", "Orders placed.");
    let basket: metrics.Histogram = metrics.histogram("shop_basket_items", "Items per order.", [1.0, 2.0, 5.0, 10.0, 20.0]);
    let reg: metrics.Registry = metrics.registry().with_counter(orders).with_histogram(basket);
    let cfg: serve.Config = serve.Config { ...serve.config(), metrics: reg, metrics_port: 9090 };
    return serve.supervise(8080, cfg, (req: HttpRequest, plat: platform.Host): HttpResponse => {
        if (req.method == "POST" && req.path == "/orders") {
            orders.inc();
            basket.observe(3.0);
            return http.created("ok\n");
        }
        return http.not_found();
    });
}
```

```bash
curl -X POST localhost:8080/orders
curl localhost:9090/metrics
# # HELP shop_orders_total Orders placed.
# # TYPE shop_orders_total counter
# shop_orders_total 1
# ...
```

Labels are attached with `with_labels(metrics.labels([metrics.label("region",
"eu"), …]))`; a gauge has `set`, `inc` and `dec`. For a program that is
not a server, `registry.render()` produces the text exposition and
`metrics.scrape(path, body, ready)` answers the three paths from a
handler of your own.

## Tracing

With `trace: true`, every request joins a W3C Trace Context trace: the
server continues the `traceparent` the request arrived with, or starts a
new trace when there is none or it is malformed. Then:

- `plat.trace()` gives the request's span context;
- every `plat.http` call sends a child span's `traceparent` upstream;
- the access log stamps each line with `traceId` and `spanId`.

```fern
import "std/http";
import "std/serve";
import "std/http/accesslog";
import "std/platform";

function init(): serve.Config {
    return serve.Config { ...serve.config(), trace: true };
}

function handle(req: HttpRequest, plat: platform.Platform): Result[HttpResponse, dyn http.ToResponse] {
    use req <- accesslog.logged(req, plat);
    match (plat.trace()) {
        Some(span) => { return Ok(http.plain("trace " + span.trace_id + "\n")); },
        None => { return Ok(http.plain("untraced\n")); }
    }
}
```

`std/http/trace` also works by hand — `trace.continue_from(headers,
random)`, `ctx.child(random)`, `ctx.inject(headers)` — for code that
propagates context through something other than `plat.http`.

The access log's lines follow the OpenTelemetry log data model in its
OTLP/JSON form, so a collector reading the server's stderr can ship them
without a parser.

## Who the client is

Behind a load balancer, `plat.peer()` is the load balancer. The client's
address is in the `Forwarded` header the proxy added — but anyone can
send that header, so it can only be believed as far as the proxies that
wrote it are trusted. `std/http/forwarded` walks the hops back from the
connection's peer and stops at the first address that is not one of
yours:

```fern
import "std/http";
import "std/serve";
import "std/http/forwarded";
import "std/net";
import "std/option";
import "std/platform";

function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    let Ok(t) = forwarded.trust(["10.0.0.0/8", "fd00::/8"]) else {
        return http.internal_error("bad trust list\n");
    };
    // Accept X-Forwarded-For too, for proxies that do not write Forwarded.
    let c: forwarded.Client = forwarded.client(req, plat, forwarded.Trust { ...t, legacy: true });
    let ip: string = "unknown";
    if let Some(addr) = c.ip() {
        ip = addr.to_string();
    }
    return http.plain("client " + ip + " over " + c.proto.unwrap_or("http") + " to " + c.host.unwrap_or("?") + "\n");
}
```

A malformed `Forwarded` field is ignored entirely, so a client cannot
forge its way past a trusted proxy. When the proxy writes a PROXY
protocol header instead (HAProxy, AWS NLB), set `proxy_protocol: true`:
the server reads the v1 or v2 header before the request, and
`plat.peer()` is then the client the proxy saw.

A proxy written in Fern adds its own hop with
`req.with_hop(forwarded.hop_of(req, plat, "https"))`.

## Health and readiness

Two separate questions:

- **Is the process alive?** `/healthz` on the metrics port answers 200
  while the supervisor runs.
- **Should it get traffic?** `readiness_path` on the main port answers
  503 as soon as shutdown begins, while requests are still being served,
  so a load balancer drains the instance before it stops accepting.
  `/readyz` on the metrics port says the same.

```fern
import "std/http";
import "std/serve";
import "std/platform";

function init(): serve.Config {
    return serve.Config { ...serve.config(), readiness_path: "/ready", metrics_port: 9090 };
}

function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    if (req.path == "/ready") {
        return http.ok("ready\n");
    }
    return http.ok("ok\n");
}
```

## Deploying

**A native binary.** `fern -target x86-64-linux` (or `arm64-linux`,
`arm64-darwin`) gives one statically linked executable with no runtime
and nothing to install, which starts in milliseconds. A container image
can be `FROM scratch` plus the binary — plus a CA bundle if the service
calls out over HTTPS, or the bundled roots from `std/tls/mozilla`.

**Under systemd** with socket activation, the server takes the listener
from `LISTEN_FDS` instead of binding, so a restart does not drop the
port:

```ini
# shop.socket
[Socket]
ListenStream=443

# shop.service
[Service]
ExecStart=/usr/local/bin/shop
```

**On Kubernetes**, the defaults already fit: `SIGTERM` starts the drain,
`readiness_path` takes the pod out of the Service while in-flight
requests finish, and the 30 s `drain_deadline` matches the default
termination grace period. Point the liveness probe at `/healthz` and
the readiness probe at your `readiness_path`.

**As a WASI component.** `fern -target wasm32-wasi-http` gives a
self-contained `wasi:http/incoming-handler` component, for `wasmtime
serve` or an edge platform that runs components. Configuration comes
through `plat.config`, which reads `wasi:config/store` there. That
interface has no released version yet: the component imports
`wasi:config/store@0.2.0-rc.1`, the release candidate `wasmtime serve
-S config` provides, so a host must offer that version.

```bash
fern -target wasm32-wasi-http -o shop.wasm shop.fern
wasmtime serve -S cli -S config -S config-var=REGION=eu-west shop.wasm
```

Each request gets a fresh instance, so there is no state, `init`
configuration or `serve.Config` on this target; the host decides
concurrency, limits and TLS. Response trailers are not sent, and
`with_background` work runs after the body is finished.

Next: [Testing HTTP code →](../testing/)
