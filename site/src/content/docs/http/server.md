---
title: State and the server
description: Start-up with `init`, state that outlives a request, every `serve.Config` setting, the crash-only supervisor, deadlines, background work and graceful shutdown.
sidebar:
  order: 4
---

## Start-up with `init`

A handler-only program can also define `init`, which runs once before
the first request. What it returns decides what it supplies:

| `init` returns | Effect |
|---|---|
| nothing | Runs first: a start-up log line, a configuration check |
| `serve.Config` | The server's configuration |
| `S` | State handed to every request |
| `(serve.Config, S)` | Both |

`init` takes no parameters, or the platform. A `shutdown` function, if
present, runs when the server stops — [see below](#graceful-shutdown).

```fern
import "std/http";
import "std/serve";
import "std/time";
import "std/platform";

function init(plat: platform.Platform): serve.Config {
    plat.log("starting");
    return serve.Config {
        ...serve.config(),
        handler_deadline: time.duration_seconds(5 as i64),
        max_connections: 4096,
    };
}

function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    return http.ok("ok\n");
}

function shutdown(reason: string): void {
    eprint("stopped: " + reason);
}
```

## State

When `init` returns a state, `handle` takes it first and returns the
next state beside the response:

```fern
import "std/http";
import "std/serve";
import "core/map";
import "std/option";
import "std/platform";

struct Counts { hits: Map[string, i32], total: i32 }

function init(): (serve.Config, Counts) {
    return (serve.Config { ...serve.config(), workers: 1 },
            Counts { hits: map_new(16), total: 0 });
}

function handle(c: Counts, req: HttpRequest, plat: platform.Platform): (Counts, HttpResponse) {
    let n: i32 = c.hits.get(req.path).unwrap_or(0) + 1;
    let next: Counts = Counts { hits: c.hits.insert(req.path, n), total: c.total + 1 };
    return (next, http.plain(n.to_string() + " of " + next.total.to_string() + "\n"));
}

function shutdown(reason: string, c: Counts): void {
    eprint("served " + c.total.to_string() + " requests before " + reason);
}
```

Values in Fern are immutable and reference-counted, so "updating" the
map builds a new one that shares structure with the old; the old one is
freed as soon as nothing refers to it.

Three things to know about state:

- **It is per worker.** Each worker process starts from `init`'s value
  and sees only the requests that reach it, which is why the example asks
  for one worker. A worker that crashes is replaced by one starting from
  `init` again. Anything that must be shared across workers or survive a
  crash belongs in a store the handler reaches through `plat`.
- **A stateful handler runs to completion.** Requests are applied to the
  state one at a time, in order, so a stateful handler does not park and
  reads bodies whole.
- **`wasm32-wasi-http` has no state.** That world creates a fresh
  instance per request; build stateful handlers for a native target.

The pairings are checked: a stateful `handle` with no `init`, an `init`
whose state nothing takes, or a `shutdown` that does not match is a
compile error (E075), not a server that misbehaves.

## The entry points

`init` and `handle` are sugar. The same servers are reachable from a
`main` you write:

| Function | Handler shape | Process model |
|---|---|---|
| `serve.run(port, cfg, h)` | `(req, plat) => resp` | This process |
| `serve.supervise(port, cfg, h)` | `(req, plat) => resp` | A supervisor and `cfg.workers` workers |
| `serve.run_with(port, cfg, s, h)` | `(s, req, plat) => (s, resp)` | This process, with state |
| `serve.supervise_with(port, cfg, s, h)` | `(s, req, plat) => (s, resp)` | Supervised, state per worker |

Each has a `_shutdown` variant that takes the shutdown hook as its last
argument. The generated `main` uses `supervise` natively, `run` on
`wasm32-wasi`, and nothing on `wasm32-wasi-http`.

All of them return `Result[i32, serve.ListenError]`: `Ok` with an exit
code, `0` after a clean drain or `1` if a request had to be cut off, and
`Err` when the server's port or its `metrics_port` could not be bound,
with the port and the `net.NetError`. A `main` that only reports the
failure returns `serve.exit_code(...)`, which prints it and answers `98`;
the generated `main` does that. Port `0`
asks the OS for a free port. When `LISTEN_FDS` is set (systemd socket
activation, or a zero-downtime restart that hands the listener over),
the server takes fd 3 instead of binding.

## The supervisor

`serve.supervise` binds the listener once, then forks `cfg.workers`
workers (default: one per CPU) that share it. The supervisor itself
serves nothing. When a worker dies — a crash, an out-of-memory kill, a
bug — it is re-forked with a bounded backoff while the others keep
answering, so a fault costs the requests that worker had in flight, not
the service. Eight workers in a row that die within 100 ms of starting
make the supervisor give up rather than spin. `SIGTERM` and `SIGINT` are
forwarded to every worker, and the supervisor exits with the worst code
any worker returned.

`reuse_port: true` gives each worker its own listener with
`SO_REUSEPORT`, so the kernel spreads connections across workers instead
of letting them race for one accept queue.

## Concurrency inside a worker

A worker runs one handler at a time, but handlers that wait do not hold
it. When a stateless handler calls `plat.http`, `plat.sleep` or reads a
streamed body, it **parks**: the worker goes on accepting and serving
other connections, then resumes the handler where it left off when its
answer arrives. There is no `async` or `await` in the code; the compiler
makes every function that can reach a wait resumable. A worker can hold
thousands of parked handlers, up to `max_in_flight`.

A handler that computes does hold its worker until it returns, so add
workers for CPU-bound work.

## Configuration

`serve.config()` gives the defaults; override fields with a spread.

| Field | Default | What it bounds |
|---|---|---|
| `workers` | `0` = one per CPU | Worker processes under `supervise` |
| `backlog` | 128 | The listen queue |
| `reuse_port` | `false` | One listener per worker |
| `max_connections` | 1024 | Open connections per worker |
| `max_connections_per_ip` | 100 | Open connections from one client address |
| `max_in_flight` | 1024 | Parked handlers and streaming producers |
| `recv_deadline` | 10 s | Time to receive a request's head — the slow-loris guard |
| `min_data_rate` / `data_rate_grace` | 240 B/s after 5 s | A request body that trickles in |
| `response_min_data_rate` / `response_data_rate_grace` | 240 B/s after 5 s | A client that reads its response too slowly |
| `keep_alive_idle` | 130 s | An idle keep-alive connection |
| `keep_alive_requests` | 1000 | Requests on one connection |
| `limits` | `http.http_limits()` | Request line 8 KiB, header block 32 KiB / 100 fields, body 1 MiB |
| `handler_deadline` | `0` = none | Time a handler may take before it is answered 503 and cancelled |
| `background_deadline` | 30 s | Work scheduled with `with_background` |
| `shutdown_grace` | 2 s | Time still accepting after `SIGTERM` |
| `drain_deadline` | 30 s | Time for in-flight requests to finish after that |
| `readiness_path` | `""` | A path that answers 503 once shutdown begins |
| `stop_with_parent` | `false` | Begin shutdown when the parent process exits |
| `stream_bodies` | `false` | Start handlers before the body has arrived — [Streaming](../streaming/) |
| `tls` | `None` | Serve HTTPS — use `with_tls` ([HTTPS, HTTP/2 and gRPC](../protocols/)) |
| `h2` | `false` | Serve HTTP/2 too — use `with_h2()` |
| `ktls` / `io_uring` | `false` | Kernel TLS and the io_uring event loop, on Linux — `with_ktls()`, `with_io_uring()` |
| `http_allow` | `None` | An allow list for `plat.http` |
| `cookie_jar` | `false` | Give `plat.http` a cookie jar |
| `trace` | `false` | W3C trace context for every request |
| `proxy_protocol` | `false` | Read a PROXY protocol header first |
| `metrics` / `metrics_port` | empty / `0` | Prometheus metrics — [Running in production](../production/) |

A malformed or oversized request never reaches the handler: the server
answers it with 400, 413, 414, 417, 431 or 501 itself and closes the
connection.

## Deadlines and cancellation

With `handler_deadline` set, a handler still running when it passes is
answered `503` in its place. If it is parked, it is cancelled: its
pending `plat.http` returns `Err(Cancelled)`, and `plat.cancelled()`
turns true, which a long loop can check to stop early. The same happens
when the client goes away. `plat.deadline()` gives the deadline itself,
to pass on to an upstream.

```fern
import "std/http";
import "std/serve";
import "std/time";
import "std/platform";

function init(): serve.Config {
    return serve.Config { ...serve.config(), handler_deadline: time.duration_millis(1500 as i64) };
}

function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    let i: i32 = 0;
    while (i < 100) {
        if (plat.cancelled()) {
            return http.text(503, "gave up\n");
        }
        plat.sleep(time.duration_millis(50 as i64));
        i = i + 1;
    }
    return http.ok("finished\n");
}
```

## Work after the response

`with_background(work)` attaches a closure that runs after the response
has been written, so the client is not kept waiting for it — the same
idea as `waitUntil` on edge platforms. Several attach in order. The
server bounds the work by `background_deadline`, and a graceful shutdown
waits for it.

```fern
import "std/http";
import "std/serve";
import "std/platform";

function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    let path: string = req.path;
    return http.ok("accepted\n")
        .with_background(() => plat.log("audit: " + path));
}
```

## Graceful shutdown

On `SIGTERM` or `SIGINT` the server:

1. keeps accepting for `shutdown_grace`, while `readiness_path` answers
   503 so a load balancer stops sending traffic;
2. closes the listener and turns keep-alive off;
3. closes idle connections;
4. gives in-flight requests and background work until `drain_deadline`;
5. calls the shutdown hook with the reason — `"sigterm"`, `"sigint"` or
   `"drain-deadline"` — and the final state, if any;
6. exits `0`, or `1` if anything was cut off.

```fern
import "std/http";
import "std/serve";
import "std/time";
import "std/platform";

function init(): serve.Config {
    return serve.Config {
        ...serve.config(),
        readiness_path: "/readyz",
        shutdown_grace: time.duration_seconds(5 as i64),
        drain_deadline: time.duration_seconds(20 as i64),
    };
}

function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    return http.ok("ok\n");
}

function shutdown(reason: string): void {
    eprint("drained after " + reason);
}
```

Under the supervisor each worker drains on its own, so the hook runs once
per worker, each with its own state. The defaults suit a container
orchestrator: Kubernetes sends `SIGTERM`
and waits 30 s before killing a pod.

Next: [Calling other services →](../client/)
