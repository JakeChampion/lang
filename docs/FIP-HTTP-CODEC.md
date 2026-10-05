# Experiment 3: the HTTP/1.1 codec as a `fip` data plane

What #9853 asked, measured: an HTTP/1.1 request parse plus a response
serialise written as a `fip` codec over owned buffers, against the
`http_parse_request` and `http_serialize_response` a handler goes through
today. The programs are `examples/fip/http_baseline.fern` and
`examples/fip/http_fip.fern`, and `internal/e2e/fip_http_test.go` keeps their
claims from rotting. Read `docs/ALLOCATION-OBSERVABLE.md` for what the
allocation numbers mean and `docs/FIP-PACKET-PROTOCOL.md` for experiment 2,
whose shape this one keeps: one workload spelled verbatim in both programs, a
digest over every response byte so the two can only differ in cost, and
malformed input in the steady state rather than beside it.

Its numbers are what §7 of #9851 budgets the framing path against: they say
what a request costs when the server's own parse, route and serialise touch
the heap not at all, on the two compilers the server will be built with.

## The workload

128,000 requests, 64 per round, generated from a mixing function so every
request is different and every run is the same:

- the method cycles `GET`, `POST`, `PUT`, `DELETE`; only `POST` and `PUT`
  carry a body, 0 to 240 bytes of lower-case letters with a `Content-Length`;
- the path cycles `/`, `/echo`, `/users/<n>`, `/missing/<n>` and `/echo`
  again (so a `GET` reaches it and gets the 404);
- four headers on every request: `Host`, a `User-Agent` whose length sweeps
  0 to 60, `Accept`, and an `X-Request-Id` the handler copies back.

**Every 17th request is malformed**, cycling through the six refusals the
parser makes: a request line with no space, one with no version, a
`Transfer-Encoding`, a duplicate `Content-Length`, a non-numeric one, and a
body shorter than it declares. Each fires 1,255 times; 120,470 requests are
served.

The handler is the same in both: `/` answers `ok`, `/echo` on a `POST` echoes
the body, `/users/<id>` answers the id, anything else is `not found`, every
served response carries the request id back as a header, and a refused
request gets `400 bad request`. Every response says `Connection: close`, as
`std/http` does. The average response is 98 bytes.

## The two programs

| variant | the request | the response | annotation |
| --- | --- | --- | --- |
| `http_baseline.fern` | the wire bytes become a `string`; `http_parse_request` gives an `HttpRequest` holding owned copies of the method, the path, a `HeaderMap` of every header, and the body as a `Stream` | an `HttpResponse` the route builds; `http_serialize_response` concatenates the reply | none |
| `http_fip.fern` | read in place out of the wire buffer; the parse's findings (the body's bounds, the request id's bounds, the length) are scalars in the function that uses them | framed into a second owned buffer, preallocated once | `fip` |

The `fip` parser follows `std/http`'s acceptance rules on every shape the
corpus sends, because the two must agree on every request for the digest
to mean anything: a request line needs two spaces, names compare
case-insensitively, a `Content-Length` value is trimmed and must be digits
that fit an `i32`, and the header block is judged before the request line
is. It stops where the corpus stops. `std/http` has since grown rules the
corpus never exercises, and the `fip` parser keeps its old answers there: a
header line without a colon is refused by `std/http` and skipped here;
`Transfer-Encoding: chunked` frames a body in `std/http` and refuses the
request here (the corpus sends it beside a `Content-Length` on a `POST`,
which both refuse, and with no chunk behind it on a `GET`, which `std/http`
reads as incomplete, so the baseline answers 400 either way); an HTTP/1.1
request needs a `Host` in `std/http`, and every request here carries one.
A corpus that reaches into those rules has to grow the `fip` parser
first. The generator is `fip` too, and takes
the wire buffer `own`: producing the input is the harness, not the codec,
and a borrowed buffer would copy all 1,024 bytes on each store.

Both produce the same 120,470 served, 7,530 refused, the same 12,599,962
response bytes and one digest, 482417158, in the measurements below. Those
runs used lowercase response header names. The current examples preserve
the baseline handler's `X-Request-Id` spelling and compare their digests at
runtime; changing the spelling changes the digest. That agreement is the differential
test; a variant that framed a different answer is caught rather than
reporting a throughput win for doing less work.

## What it measured

x86-64 Linux, 4-core container, medians of five runs; run-to-run spread of
`ns/req` was within 12% for both. The same source was built by both compilers
and run on the same box.

| variant | built by | allocs/req | fresh bytes/req | ns/req | req/s | round p50 | round p99.9 |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `baseline` | native | 86.4 | 692 | 8,281 | 120,746 | 503,013 | 1,345,698 |
| `fip` | native | 0 | 0 | 3,748 | 266,796 | 225,951 | 559,630 |
| `baseline` | self-host | 93.4 | 1 | 6,293 | 158,887 | 360,805 | 1,366,952 |
| `fip` | self-host | 0 | 0 | 2,039 | 490,377 | 115,078 | 336,785 |

Round latencies are one batch of 64 requests end to end, in nanoseconds.
"Fresh bytes" is `__heap_bump_bytes()` growth per request: the baseline
built by native buys 692 fresh bytes per request, while the self-host build
recycles almost everything it allocates through the freelist and buys 1.

### The parse and the serialise are 86 allocations per request today

The baseline's 86.4 allocations per request are the whole price of the
current shape: the wire-to-string bridge, the request line's two owned
slices, the header block split into lines, each name lowered and each value
trimmed into the two arrays of the `HeaderMap`, the body copied into its
`Stream`, the `HttpRequest` and `HttpResponse` boxes, and the serialiser's
string concatenations, one per header and one per piece of the status line.
None of it is the handler: the route itself is three comparisons and a
`set` on the response headers.

The `fip` plane is flat at zero on both compilers over all 128,000 requests
with the malformed ones included, and it is 2.2 times faster built by native
and 3.1 times faster built by the self-host. Zero allocations is the
checked claim, and the throughput is what the claim was worth here.

### The self-host build is the faster one, on both variants

Built by the self-host compiler the `fip` plane runs in 2,039 ns per request
against native's 3,748, and the baseline in 6,293 against 8,281. The `fip`
plane is byte loads, byte stores through `.with` on an owned buffer, and
comparisons, so this is the self-host SSA backend's code for exactly that
path being better than the native stack backend's. `CLAUDE.md` says the
self-host's output wins performance ties; here it is not a tie.

When the table was taken, the self-host build of the baseline also
allocated 7 more times per request than the native build (93.4 against
86.4) for the same source and the same answers. `FERN_RC_TRACE` on both
builds attributed it to `http_serialize_response`: its `hdr_block =
hdr_block + name + ": " + value + "\r\n"` chains are fifteen `+` per
response, and native grew the uniquely-held accumulator in place through
`__fern_str_append` (#5637), allocating about eight, while the self-host
lowered every `+` to a fresh `__fern_str_concat` box. #10960 closed that:
the self-host grows the accumulator in place too (`__fern_str_grow`), and
the `str_self_append_chain` row of `testdata/selfhost-alloc-count-matrix.txt`
now pins native 4 blocks per round against self-host 6. Re-measured on
2026-10-02 at the head carrying #10960, the same program allocates 112.9
per request built by native and 113.8 built by the self-host, a gap of 0.9.
Both are above the table's figures: the source is the same, `std/http`
underneath it is not, and what moved there is not measured here.

### What this sets

The framing path a server owns is a parse, a route and a serialise. Written
as this plane is, that path costs no allocation and about 2 to 4
microseconds per request on this box for a 200-byte request and a 100-byte
response, and that is the number §7 of #9851 budgets against: the server's
own path may cost what this codec costs and allocate what it allocates,
which is nothing. What a handler allocates on top (a JSON body decoded into
a struct, a response body built from parts) is the handler's, bounded and
pinned per handler rather than folded into the server's number.

## What the language forced

Three things, in the order they were hit. All are design facts, none is a
bug.

### The plane returns one buffer, so its findings travel in it

A `fip` function cannot return a tuple of the buffer and a length: a fresh
tuple is an un-paired allocation (E068). The output buffer therefore carries
an 8-byte header ahead of the response, the response length and the refusal
class, which the driver reads back. Experiment 2 did the same with its packet
header, and it is the shape a connection record would take anyway: the bytes
to send and how many of them there are, in one place.

### A literal body is not a range in a source buffer

The response's body is either copied from the request (the echo, the user
id) or a literal (`ok`, `not found`, `bad request`). One framing function
takes a source buffer and a range; a literal has neither, so `frame_lit`
exists beside `frame`, sharing the head and the seal. That is the same
"two spellings of one operation" experiment 2 met with its in-place and
copying transforms: the plane cannot abstract over where bytes come from
without a view, and a view is what `docs/STR-VIEW-CONTRACT.md` is still
deciding.

### Reading a `string` byte by byte is admitted in `fip`

`lit[i]` on a `string` parameter is an index read, which E053 admits, and it
is how every literal here reaches the buffer: `put_str(out, at, "HTTP/1.1
")` stores one byte per statement. The alternative, spelling each literal as
byte constants, was written first and thrown away; a literal the reader can
read is worth the `string`-typed parameter.

## Answers to the questions #9853 asked

- **Allocations per request** — 86.4 through `std/http`, 0 as a `fip`
  plane, on both compilers.
- **Bytes copied per request** — the `fip` plane writes the 98 bytes of
  the response and reads the request in place; the baseline's copying is
  not a count it can report, so its 692 fresh bytes per request stand in:
  what it had to buy from the allocator to hold the copies it made.
- **ns per request** — 8,281 against 3,748 built by native, 6,293 against
  2,039 built by the self-host.
- **Malformed input** — in the steady state, all six refusals, 1,255 each.

## What this says about the message layer

The byte-based message layer (#5714) is the baseline's shape with the copies
removed: a request read into a connection-owned buffer, parsed in place, its
findings as offsets and lengths, and only what the handler keeps copied out.
This experiment is that layer with the handler inlined, and it says the
layer's own cost can be zero allocations and a few microseconds. What it
cannot say is how a handler gets at the bytes without copying them, which
is the view question `docs/STR-VIEW-CONTRACT.md` §5 answers with the
two-word `[u8]` and the checker rule that keeps it non-escaping (#8635):
that is the prerequisite the message layer waits on, and this experiment is
the number it has to keep.
