# Experiment 4: an HTTP-like application pipeline in three memory disciplines

What #9586 asked, measured: a request parse, a route, an update to the
application's bounded state and a response, written three ways over one
workload. The programs are `examples/fip/httpapp_baseline.fern`,
`httpapp_fbip.fern` and `httpapp_fip.fern`, and
`internal/e2e/fip_httpapp_test.go` keeps their claims from rotting. The codec
is experiment 3's (`docs/FIP-HTTP-CODEC.md`); what this adds is the
application behind the routes, the explicit limits, and the behaviour at
capacity. Read `docs/ALLOCATION-OBSERVABLE.md` for what the allocation
numbers mean.

## The service

Five routes over a counter table of 256 slots:

| request | answer |
| --- | --- |
| `GET /health` | 200 `ok` |
| `GET /users/<id>` | 200, the id |
| `POST /counter/<id>` | 200, the new count; 503 `counter table full` when the id is new and all 256 slots are live |
| `GET /counter/<id>` | 200, the count; 404 when the id has none |
| `GET /metrics` | 200, six `name value` lines: requests, served, refused, full, counters, counter_total |
| any other path | 404 `not found` |
| a known path with the wrong method | 405 `method not allowed` |

Two limits sit between the parse and the route: more than 8 header fields
is 431 `too many headers`, more than 240 body bytes is 413 `body too
large`. Both are checked only once the request is well formed, so a
malformed request is the codec's 400 whatever else is wrong with it, and
both carry the request id back, since the request was parsed. The table
never grows: the 503 is the overload made explicit, and `/metrics` counts
how often it was given.

Every served response carries the request's `X-Request-Id` back as a
header and says `Connection: close`, as `std/http` does.

## The workload

128,000 requests, 64 per round, generated from a mixing function so every
request is different and every run is the same:

- the path cycles `/health`, `/users/<id>`, `/counter/<id>`, `/metrics`,
  `/missing/<id>`; the id is one of 320 values drawn by the hash, so the
  256-slot table fills during the run and about a fifth of the ids can
  never be created;
- on `/counter/<id>` 70% of requests are the POST, 20% the GET and 10% a
  method the route refuses; elsewhere 95% are the GET and 5% a POST;
- a POST or PUT carries a body of 0 to 240 bytes with a `Content-Length`;
- four headers on every request: `Host`, a `User-Agent` whose length sweeps
  0 to 60, `Accept`, and the `X-Request-Id`.

Every 17th request is malformed, cycling through the six refusals the codec
makes; every 23rd carries six extra header fields; every 29th that has a
body carries 300 bytes. Over the run: 81,976 served, 7,530 refused as 400
(1,255 per class), 23,947 as 404, 5,610 as 405, 5,238 as 431, 702 as 413,
and 2,997 as 503. The table ends with 256 live counters holding 12,662
increments between them. The average response is 101 bytes.

## The three programs

| variant | the request | the state | the response | annotation |
| --- | --- | --- | --- | --- |
| `httpapp_baseline.fern` | the wire bytes become a `string`; `http_parse_request` gives an `HttpRequest` with owned copies of everything | a `Map[string, i64]` keyed by the id's digits, beside a `Tallies` struct | an `HttpResponse` with a string body; `http_serialize_response` concatenates the reply | none |
| `httpapp_fbip.fern` | read in place out of the wire buffer | one `App` struct: the three table arrays, the tallies, the decision, AND the response buffer | written byte by byte through a rebuild of the whole `App` | `fbip` |
| `httpapp_fip.fern` | read in place out of the wire buffer | one `App` struct: the three table arrays, the tallies and the decision | framed into a separate owned buffer by a second call | `fip` |

The baseline is written in the one arrangement the native analysis takes in
place, the shape the key/value core found (`docs/FIP-KV-CORE.md`): the map
is threaded beside the tallies rather than inside a struct, because a map
mutator on a field of an owned struct copies the whole map (#11119), and
each mutator sits in a function of its own. It is still the idiomatic
program — string paths, a `Map`, `http.text`, `starts_with` — and the
allocations it reports are the price of that idiom, not of a bad shape.

The two disciplined variants share every line of parsing and routing. They
differ in where the response goes. A `fip` function returns one owned
container, so the `fip` plane is two calls: `apply` consumes the state,
parses, routes, updates the table and records its DECISION (status, class,
and where the body comes from) in scalar fields of the state; `frame` then
consumes the response buffer and writes that decision out, reading the
state and the wire by reference. The `fbip` plane is one call, `serve(own
app, wire, len): App`, with the response buffer a field of the state and
every byte written as `App { ...app, out: app.out.with(at, b) }`, which the
compiler pairs with the box the call consumed and writes in place.

## What it measured

arm64-darwin (Apple Silicon), medians of five runs; run-to-run spread of
`ns/req` was within 6% for every row. The same source was built by both
compilers and run on the same machine.

| variant | built by | allocs/req | ns/req | req/s | round p50 | round p99 | round p99.9 | max |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `baseline` | native | 144.2 | 5,516 | 181,285 | 351,959 | 375,709 | 386,458 | 392,667 |
| `fbip` | native | 0 | 2,892 | 345,772 | 184,333 | 202,125 | 208,667 | 218,709 |
| `fip` | native | 0 | 2,438 | 410,163 | 155,167 | 172,083 | 178,166 | 185,750 |
| `baseline` | self-host | 116.8 | 2,849 | 350,968 | 181,500 | 197,167 | 205,083 | 213,958 |
| `fbip` | self-host | 0 | 1,937 | 516,005 | 123,333 | 137,333 | 142,542 | 146,375 |
| `fip` | self-host | 0 | 1,246 | 802,191 | 79,333 | 90,500 | 95,917 | 99,667 |

Round latencies are one batch of 64 requests end to end, in nanoseconds.
The baseline's fresh bytes (`__heap_bump_bytes()` growth) over the whole run
were 28,736 built by native and 44,240 built by the self-host: both recycle
nearly everything they allocate, so its cost is the allocator calls, not
the heap.

### The disciplined planes allocate nothing, including at capacity

Both `fip` and `fbip` are flat at zero over all 128,000 requests, with the
table full for most of them and 2,997 requests answered 503. The baseline
allocates 144 times per request built by native and 117 built by the
self-host: the codec's 86 to 113 (`docs/FIP-HTTP-CODEC.md`) plus the
application's own — the id's digits sliced out of the path and copied, the
`Map` key built from the parsed id, the count formatted into a string, the
`Decision`, the metrics body's concatenations, and `http.text`'s
`HttpResponse` with two empty header maps.

### Throughput: 2.3x over the baseline on both compilers, 1.2 to 1.6x
between the two planes

The `fip` plane serves a request in 2,438 ns built by native and 1,246 ns
built by the self-host, 2.3x the baseline on each. The `fbip` plane is 19%
slower than `fip` built by native and 55% slower built by the self-host.
That gap is the per-byte rebuild: a 101-byte response is 101 reconstructions
of a 19-field struct, each allocation-free and each a copy of every field
the constructor restates. The packet codec found the same shape at the same
cost (`docs/FIP-PACKET-PROTOCOL.md`); the two-call `fip` plane is how to
have named state AND a buffer that is written in place.

The self-host build is the faster one in every row, by about 2x on the
baseline and on the `fip` plane alike. The plane is byte loads, byte stores
through `.with` on an owned buffer, comparisons and the hash probe, and the
self-host SSA backend's code for exactly that path is better than the native
stack backend's.

### Tail latency: the absolute tail falls, the spread does not

| variant | compiler | p99.9 / p50 |
| --- | --- | ---: |
| `baseline` | native | 1.10 |
| `fbip` | native | 1.13 |
| `fip` | native | 1.15 |
| `baseline` | self-host | 1.13 |
| `fbip` | self-host | 1.16 |
| `fip` | self-host | 1.21 |

The `fip` plane's p99.9 round is 2.1x lower than the baseline's on both
compilers, because every percentile moved with the mean. The spread above
the median did not narrow: the ratio is slightly wider for the disciplined
planes, because their median fell further than their worst round, which the
OS still owns. This is the finding of experiment 1 (`docs/FIP-EVENT-LOOP.md`)
again, on a workload with 144 allocations per request to remove: on this
machine, with this allocator, allocation is a cost paid evenly, not a source
of jitter. What the architecture guide (#9594) can claim for `fip` is a
faster tail in absolute terms and a steady state the compiler proves; it
cannot claim a tighter one.

### At capacity

The table fills after about 4,300 requests and stays full. From
then on a POST naming one of the 64 ids that never found a slot is a 503,
and the plane's cost does not move: the probe runs up to 256 slots in the
worst case, and nothing is allocated, retried or evicted. `/metrics`
reports `full 2997` at the end of the run on every variant, so a caller can
see the overload rather than infer it from a growing process.

## What the language forced

Four things, in the order they were hit. All are design facts; the last is
a documentation gap.

### A consumed `own` argument cannot be read by a later argument of the same
call

`decide(app, ..., app.counts[slot] as i32, ...)` is E050: the first argument
moves `app`, and the seventh reads it after the move. The arguments are
evaluated in order, and the checker says so at the use. The remedy is a
binding first — `let have: i32 = app.counts[slot] as i32;` — which is the
right spelling anyway, but the diagnostic names the symptom ("use of owned
parameter after it was consumed") rather than the fix. A hint that the
later argument should be hoisted would save the one guess every writer of
this shape makes.

### One owned return, so the plane is two calls or the buffer is a field

The codec's finding (`docs/FIP-HTTP-CODEC.md`, "the plane returns one
buffer") met application state here, and the two shapes it leaves are the
two disciplined variants. Recording the decision in the state and framing
from it in a second call costs nothing measurable and keeps the buffer
written in place; keeping the buffer in the state costs 19% to 55%. Neither
is wrong; the first is the one to recommend, and the guide should say so.

### The baseline's map has one in-place arrangement

As in the key/value core: the map beside the struct, one mutator per
function (#11119). Written as an application would write it first — the
map a field of the state, `insert` and the tallies updated in one rebuild —
the baseline copies the whole table on every increment.

### `std/http` has no phrase for 507

The natural status for "the counter table is full" is 507 Insufficient
Storage; `http_status_text` does not know it and `__http_reason` would
serialise `Status`. The service answers 503, which it does know. A table
that covers every registered code is the fix; it is not this experiment's.

## String and text APIs that want FIP-friendly variants

Everything the `fip` plane does with text it does over a `u8[]` and a pair
of bounds, by hand, because the stdlib's string functions take and return
`string` and therefore allocate. The hand-written versions are about 290
lines of the 1,200 in `httpapp_fip.fern`, and every one of them has a
`string` counterpart a handler would reach for first:

| the plane wrote | the stdlib has | what a `fip` variant would take |
| --- | --- | --- |
| `parse_int(buf, from, end)` | `string.parse_int()` | a byte range |
| `name_is(buf, from, end, lit)` | `string.eq_ignore_case` / `to_lower` | a byte range against a literal, folding one side |
| `bytes_are`, `starts_with` over a range | `string.starts_with` | a byte range |
| `trim_start`, `trim_end` returning bounds | `string.trim()` | bounds in, bounds out |
| `put_int(own out, at, n)` and `digits(n)` | `i32.to_string()` | a write into an owned buffer at an offset, and the width without writing |
| `put_str(own out, at, lit)`, `put_bytes` | `+` | a copy into an owned buffer at an offset |
| `put_line(own out, at, name, n)` | an f-string | the same, for a `name value\n` line |
| `find_crlf`, `find_byte` | `string.index_of` | a byte range and a byte |

Three of these are the same request: a `(buf: u8[], from: i32, end: i32)`
view that `std/string`'s predicates and parsers accept, so the stdlib's
acceptance rules (what `parse_int` admits, how `trim` defines a space) are
written once rather than copied into every plane that must agree with a
`string`-based baseline. The other half is the writing side: `put_int` is
the one every variant of every experiment so far has carried, and
`std/i32` is where it belongs.

## Answers to the questions #9586 asked

- **Representative requests without steady-state allocation**: the `fip`
  and `fbip` planes report zero allocations over 128,000 requests covering
  every route, both limits, every refusal class and the table at capacity,
  under both compilers; the gate pins it.
- **Explicit request/header/body limits**: 1,024 bytes of wire, 8 header
  fields, 240 body bytes, 256 counters, each a named constant, each
  refused with its own status, each exercised by the workload.
- **Baseline/FBIP/FIP throughput and tail latency**: the two tables above.
  2.3x on throughput, 2.1x on the p99.9 round, no change in the spread.
- **String/text APIs that need FIP-friendly variants**: the table above;
  the byte-range view and the write-at-offset formatters are the two
  families.
