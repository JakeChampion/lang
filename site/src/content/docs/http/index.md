---
title: HTTP in Fern
description: A map of Fern's HTTP stack — from a one-function handler to HTTP/2, TLS, gRPC and deterministic simulation — and the order to read it in.
sidebar:
  order: 0
  label: Overview
---

Fern's standard library has a complete HTTP stack written in Fern: a
server loop with a crash-only supervisor, a client, a router, the
middleware you need in production, TLS 1.3, HTTP/2, WebSockets, gRPC and
a simulated network to test all of it. There is no framework to install
and no runtime underneath; a server is one static binary, or one
WebAssembly component.

This guide goes from the smallest handler to the parts you reach for
once a service carries real traffic. Each page builds on the one before,
and every complete program on these pages is compiled by the site's
build.

## The pages

| Page | What it covers |
|---|---|
| [Your first server](./first-server/) | `handle`, responses, requests, building for native and for `wasi-http` |
| [Requests and responses](./requests-responses/) | Headers, query strings, typed JSON bodies, forms, cookies, errors that answer for themselves |
| [Routing and middleware](./routing-middleware/) | `std/http/router`, writing middleware with `use`, CORS, authentication, request ids, access logs, compression, static files, caching |
| [State and the server](./server/) | `init`, per-worker state, `serve.Config`, the supervisor, deadlines, graceful shutdown |
| [Calling other services](./client/) | `std/fetch`, `plat.http`, timeouts, allow lists, connection pooling, HTTP/2 upstreams |
| [Streaming](./streaming/) | Streamed request bodies, uploads, produced responses, trailers, files, Server-Sent Events, WebSockets |
| [HTTPS, HTTP/2 and gRPC](./protocols/) | TLS 1.3 serving, ALPN, kTLS, io_uring, HTTP/2, a gRPC service |
| [Running in production](./production/) | Metrics, tracing, proxies and client addresses, health checks, deploying |
| [Testing HTTP code](./testing/) | Calling a handler directly, the mock platform, the simulated network, cassettes |

## The modules

Everything is imported by path; the qualifier is the last path segment,
so `import "std/http/router";` gives you `router.new()`.

| Module | Role |
|---|---|
| `std/http` | The message layer: response constructors, body readers, problem details, `ToResponse` |
| `std/serve` | The accept loop, its `Config`, the supervisor and graceful shutdown |
| `std/platform` | `Platform`, the host effects a handler may use: `http`, `log`, clocks, `config`, `sleep` |
| `std/fetch` | The HTTP client: HTTP/1.1 and HTTP/2, TLS, redirects, pooling, decompression |
| `std/headers` | Case-insensitive `HeaderMap` operations and content negotiation |
| `std/url` | URL and query-string parsing and encoding |
| `std/http/router` | Method + path patterns with parameters, 404 and 405 |
| `std/http/form`, `std/http/multipart` | URL-encoded forms and streamed `multipart/form-data` |
| `std/http/cookie` | `Set-Cookie`, the request `Cookie` header, and a client cookie jar |
| `std/http/accesslog` | One structured log line per request |
| `std/http/cors`, `std/http/auth`, `std/http/requestid` | CORS, bearer and basic authentication, request ids |
| `std/http/compress` | `gzip` / `zstd` response compression |
| `std/http/static` | Directory serving with ETags, ranges and precompressed files |
| `std/http/cache` | Validators, conditional requests and `Cache-Control` |
| `std/http/sse`, `std/http/websocket` | Server-Sent Events and WebSockets |
| `std/http/trace` | W3C Trace Context |
| `std/http/forwarded` | `Forwarded`, `X-Forwarded-*` and the PROXY protocol, under a trust list |
| `std/http/grpc`, `std/protobuf` | The gRPC wire format and schema-less protobuf |
| `std/tls/*` | TLS 1.3 client and server, certificate loading and verification |
| `std/metrics` | Counters, gauges, histograms and the Prometheus exposition |
| `std/mock_platform`, `std/sim`, `std/sim_fetch`, `std/sim_platform` | Test doubles and a deterministic simulated network |

The [standard library reference](../stdlib/) has every public function of
each, generated from the source.

## Three ideas the whole stack shares

**A handler is a function.** It takes an `HttpRequest` and a platform
value and returns an `HttpResponse`. Routers, middleware and the server
loop are all built from that one shape, so a handler can be called
directly from a test with no socket.

**Effects come through the platform.** A handler that calls an upstream,
logs, reads the clock or sleeps does so through its `plat` argument. In
production that is the real host; in a test it is a mock that records
calls or a simulated network driven by a seed. A handler that reaches
around its platform — calling the ambient `eprint`, say — is refused at
check time.

**Waiting never blocks the worker.** When a handler waits on
`plat.http`, `plat.sleep` or a streamed body, it parks and the worker
serves other connections until the answer arrives. You write straight-line
code with no `async` keyword; the loop does the scheduling.
