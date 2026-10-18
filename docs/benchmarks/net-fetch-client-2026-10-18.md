# The HTTP/1.1 client per request, 2026-10-18

What one `std/fetch` request costs on a keep-alive connection, measured
the way the server's hello path is, and what this change cut. The
server side is `docs/benchmarks/net-twofetch-2026-10-17.md` and the
`http1-hot-path` commits; this is the client half of the same standard.

## How it is measured

A Fern program holds one `fetch.Sockets` transport and sends `GET /` to
a local Fern hello server through `fetch.send_on` 4,000 times, so the
connection is taken from the pool and parked again on every request.
Two routes: direct (`http://127.0.0.1:port/`), and through a forward
proxy with credentials (`http://u@127.0.0.1:port` as `http_proxy`,
`http://8.8.8.8/` as the URL), the route `plat.http` takes in the
two-fetch profile. Three figures per route:

- instructions per request: callgrind over the whole program at 1,000
  and 5,000 requests, the difference over 4,000, on x86-64;
- allocations per request: `__heap_alloc_count()` around 3,000 requests
  after 1,000 warm ones;
- syscalls per request: `strace -c`.

Outside a serve loop a request is three syscalls, as the server's hello
request is: `sendto`, `poll`, `read`. Inside a handler's task it is four:
`sendto`, `epoll_pwait` (the park), `read`, and the `epoll_ctl` that
re-arms the pooled socket with ONESHOT for the next fetch. The two-fetch
server's requests under strace: 3 `sendto`, 3 `read`, 3 `epoll_pwait`,
2 `epoll_ctl` each.

## The gate's 393

`TestSelfHostServeAllocsPerRequestTwoFetch` read 393.01 allocations per
request on this container against its ceiling of 171, while the
2026-10-17 record says 170. A compiler and stdlib from 1ca3056b8, the
commit that measured 170, read 394 here too, so main had not regressed:
the container's environment sets a 26-entry `no_proxy`, and the client
split and re-read the list on every request, 112 allocations per fetch.
With `no_proxy` unset the same build read 169.02.

## Per commit

Instructions and allocations per request, direct and through the proxy,
and the two-fetch gate's allocations per request as this container reads
it (its environment's `no_proxy` list included).

| change | direct Ir | direct allocs | proxied Ir | proxied allocs | two-fetch gate |
|---|--:|--:|--:|--:|--:|
| main (5a196db51) | 24,627 | 65 | 31,450 | 86 | 393.01 |
| `no_proxy` and the proxy specs read once | 24,627 | 65 | 27,017 | 76 | 149.01 |
| the head written into one buffer; `http_end_to_end` hands back a map that drops nothing | 21,669 | 52 | 24,439 | 63 | |
| a target and a pool row without the strings a request never reads | 20,047 | 41 | 22,749 | 51 | 107.01 |

The gate's ceiling follows the measured 107.01 to 108.

## What a request still pays

From `FERN_RC_TRACE=1` on the direct route, the 41 allocations per
request are:

- the caller's `fetch.get(url)`: the `Request` and its default
  `Timeouts`, `HttpLimits`, `Decoding`, empty header map and empty
  body, about nine;
- the URL parsed into owned `scheme`, `host` and `path` strings and a
  `Url`, four, and the `__Target` and `__Place` built from them;
- the response parsed: a name and a value string per field, the two
  arrays of the map grown twice each, the body copied out of the read
  buffer, and the map rebuilt once more because the Fern server then sent
  `Connection: keep-alive`, a hop-by-hop field the client strips (it no
  longer does, below);
- the read: the 64 KiB `tcp_recv` buffer, the `[fd, 1]` wait set and the
  `poll` buffer behind `async.wait_any`, the `Chunk`;
- the pool: the `__Conn` taken, the row parked and the array it goes in.

The instructions go mostly to the response head (the CRLF scans and the
case-folded name lookups `http1-hot-path` replaces on the server side,
which the response parser shares), `url_parse`, UTF-8 validation of each
field value, and the allocator and reference counting under all of it.

## The three follow-ups, later the same day

The same harness, direct route, against a hello server built from the
same compiler as the client (`bin/fern-selfhost` from the tree under
test, so the base row is re-measured rather than copied from above; the
server is the `std/serve` hello of `e2eharness.ServeAllocsServerSource`).
Server instructions are callgrind over the server for 1,000 and 5,000
client requests, the difference over 4,000.

| change | response bytes | client Ir | client allocs | server Ir |
|---|--:|--:|--:|--:|
| main (00d1533e0) | 104 | 19,391 | 41 | 8,253 |
| no `Connection: keep-alive` on a persisting HTTP/1.1 response | 80 | 15,380 | 36 | 8,271 |
| a `Request`'s defaults are static boxes | 80 | 15,285 | 32 | 8,271 |

- **The `Connection` field** goes out only where it carries meaning:
  `close` when the response ends the connection, `keep-alive` to an
  HTTP/1.0 request whose connection persists. A persisting HTTP/1.1
  response carries none, so the client no longer parses a field, strips
  it and rebuilds the header map: 24 bytes and 4,011 instructions per
  request off the client, four allocations with it. The server pays 18
  instructions for the request's version threaded to the serializer and
  tested for HTTP/1.0 by its minor digit (`__http10`).
- **The request's defaults.** `Timeouts` cost four boxes per `fetch.get`
  (three `Duration`s and the record) because the compiler placed a record
  in static data only when every field was a narrow scalar, and
  `duration_seconds(10 as i64)` was a cast of a literal rather than a
  literal, so the constructor leaf was never spliced. Both are compiler
  fixes: an i64 or f64 constant field is a static box's word on every
  backend, and an unsuffixed integer literal cast to an integer type is
  that literal at the type's width. `fetch.get(url)` is now the one
  `Request` box; with a literal URL it is none.
- **One `epoll_ctl` per fetch in a handler.** The loop's pool keeps its
  HTTP/1.1 connections watched level-triggered between a task's reads
  (`async.KEEP` on the first read, `async.WATCHED` after), so a request
  on a kept connection is `sendto`, the park's `epoll_pwait` and `read`:
  three syscalls, as outside a loop. A readiness on a kept connection no
  task is reading is an idle connection the peer closed or is writing to
  unasked; the loop unwatches it and the pool closes it. The two-fetch
  server (`TestSelfHostServeSyscallsPerRequestTwoFetch`) goes from 11 to
  9 syscalls per request, and its allocations from 107.01 to 97.01 per
  request (the two requests' defaults and the per-flight watch
  bookkeeping); the gates pin 9 and a ceiling of 98.

## What is left

- **The response parser's head scan.** `__head_shape`, `__fields_of` and
  the length-first lookups of `http1-hot-path` apply to the response
  parser as they do to the request parser; the client gets them when
  that branch lands.
- **A header map shared with the last response.** The request parser
  takes the previous request's map and hands it back when the block
  repeats it; the response parser takes none. A kept connection could
  carry its last response's map. Against a Fern server it gains little
  because `Date` comes first and changes every second; against one that
  sends `Date` later it saves the strings and arrays above.
- **The URL.** `url_parse` still produces owned `scheme`, `host` and
  `path` strings and a `Url` per request, four of the 32 allocations.
