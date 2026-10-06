# P0: the per-request framing path, measured and planned

What is left of #9853's byte-based message layer and its exit criterion B
(zero allocations per request in the framing path for the hello handler),
planned from a measurement of what the path allocates today. It also records
what #8635, the two-word `[u8]`, still owes P0, which turns out to be nothing.

## 1. What the framing path allocates today

A hello request, `GET /` with `Host`, `User-Agent` and `Accept`, parsed by
`http_parse_request_framed_from`, and an `http.ok("hello")` response
serialized by `http_serialize_response_to_bytes`. Allocations are
`__heap_alloc_count()` deltas over 1,000 iterations, x86-64, at `origin/main`
411e0f893.

| Compiler | Parse | Serialize |
| --- | ---: | ---: |
| Self-host | 69 | 17 |
| Native | 68 | 21 |

`FERN_RC_TRACE` attributes one self-host parse:

| Allocations | Where |
| ---: | --- |
| 38 | 19 strings, each built by two copies: `__bytes_string` copies the range into a fresh `u8[]`, then `string_from_bytes_unchecked` copies that array into the string |
| 7 | `HeaderMap.append` growing two string arrays, one push at a time |
| 3 | `utf8.from_bytes` on a field value, copying a range it was just handed as a copy |
| 2 | the empty body of a `GET`, built as a stream over no bytes |
| 19 | the head parse's helpers: `Result`, `Option` and tuple boxes between them, and the arrays `HeaderMap.get_all` builds |

The native trace names callers, and it shows five framing rules each calling
`HeaderMap.get_all`. Each call builds an array only to read its first element
or its length.

Of the serializer's 17, `http.ok` builds the response and accounts for 4;
that is the handler's cost. The other 13 are framing. The head is built as a
`string` by five concatenations and then copied into the builder.

The native column had one cause of its own, which slice 2 removed. Every
read of a `const` array allocated a fresh copy of it on native: 100 reads of
a four-element `u8[]` constant cost 100 allocations, where the self-host
costs 0. `std/http`'s `NOT_TCHAR` and `FIELD_CTL` scans paid it per header.

The `fip` codec of `docs/FIP-HTTP-CODEC.md` is the same work at 0 on both
compilers. Rerun on the self-host at the same commit, its baseline variant
went from the 86.4 allocations per request recorded there to 117.3. The
stricter parse rules since that record added steps, and each one allocates.

## 2. What #8635 owes this

Nothing. The epic's principle 2 (#9851 §3) already says that a small view
pins its whole buffer, so the parser's outputs into `HttpRequest` are owned
copies and only the streaming body borrows. The parse runs over the buffer
by offset, and its findings are scalars. So no view escapes into a request,
and the message layer needs neither a two-word `[u8]` nor a new escape rule.
The table above says the same: nothing in it is a view the layer could have
kept instead of copying.

The rest of #8635 has moved on since it was filed:

- **Native.** The slice header it would retire has been reference-counted
  since #8406, so it no longer leaks. Changing native's representation now
  is new native-only surface, which the freeze (`docs/NATIVE-FREEZE.md`)
  does not admit.
- **Self-host.** `[u8]` is already one tagged word with no header box
  (`docs/STRING-BYTE-VIEWS-2026-10-02.md`). Its escape diagnostics, E063 and
  E065, cover returns and records (`docs/STRING-VIEW-DIAGNOSTICS-2026-10-03.md`).
- **What is left.** Slicing a `[u8]` copies on the self-host. A zero-copy
  sub-range view is the one piece no shipped work covers. Nothing in P0
  needs it. P3's streaming body hands the handler owned chunks.

## 3. Slices

One PR each, in this order. Each one moves the gate in slice 1 down, never
up.

1. **The gate.** A per-request allocation census of the framing path: the
   parse and the serialize of the request in §1, counted separately. It runs
   on both compilers, in `internal/e2eselfhost` with a twin in
   `internal/e2e`, and pins today's counts as a ratchet that may only fall.
2. **Native: a `const` array is static.** A read of a `const` array reads one
   immortal copy instead of building a new one. This is a native bugfix,
   referenced on #4451, with its own issue, allocation-count test and PR.
   Done for #11471: the new `const.arr` op places each constant scalar
   array once in static data. The Go compiler's parse fell from 50 to 43
   allocations per request on x86-64, 60 to 53 on arm64 and 56 to 49 on
   wasm; the serialize stayed at 6.
3. **One copy per kept string.** `string_from_bytes_range_unchecked` copies a
   byte range straight into a string, so the method, path, version and each
   header name and value cost one allocation instead of two. The self-host
   parse fell from 69 to 59. The other nine double copies of the 19 are the
   lowercasing in slice 5 and the path decode. Native keeps two copies, since
   its string constructor takes an array.
4. **Lookups that do not allocate.** The framing rules walk the map by
   index instead of asking `get_all` for an array, which also stops them
   lowercasing a literal key on every lookup. The self-host parse fell from
   59 to 48, and native's from 68 to 54 on x86-64. What `append` costs as the
   map grows is still to measure.
5. **Names compared, not lowercased.** `HeaderMap` keeps a name as it came
   off the wire and compares names ASCII-case-insensitively. That removes the
   lowercasing copy per header. HTTP/1.1 field names are case-insensitive, so
   only iteration order and spelling are observable, and both keep what the
   client sent. The self-host parse fell from 48 to 42 on every target,
   and native's from 54 to 50 on x86-64.
6. **A bodiless request carries no stream.** The empty body is one shared
   value, not a stream built over zero bytes. Its first part is in the
   compiler: on the self-host a payloadless `Option` or `Result` (the
   stream's `None` source, an `Ok(())`) is one static block per tag, as a
   payloadless variant of a user enum already was, so the self-host parse fell
   from 42 to 41. `__alloc_u8(0)` is a static empty array too (#11507), as it
   is on the Go compiler, which takes the zero-length body copy off the
   self-host parse: 41 to 40. A shared `Stream` needs constant records with
   pointer fields.
7. **The head serializes into the builder.** The status line and fields are
   pushed into the one builder the body goes into, with no intermediate
   `string`. Serialize fell from 13 to 4 on the self-host and to 6 on the Go
   compiler: the builder, its storage and its result, and the boxes left
   between the helpers.
8. **What is left, then the donor boundary.** §5 attributes the residue
   after slices 2 to 7. Most of it is not what a donor would provide, so
   slice 8 is the series in §5.3, with the donor itself last.

## 4. Exit

Criterion B of #9853 is met when the gate reads 0 for the framing path on
both compilers with the hello handler, which needs slice 8. It reads 0
for the parse and the serialize on every target since §5.3's slice 8. The
Go compiler's backends were retired on 2026-10-05
(`docs/NATIVE-FREEZE.md`), so the self-host is the only compiler the
criterion still names. Slices 3 to 7
are each worth landing alone, because every server pays them per request
whatever its handler does.

## 5. The residue after slices 2 to 7

### 5.1 Measured

One parse and one serialize of the §1 request, traced allocation by
allocation at `origin/main` d1cfbd282 (`FERN_RC_TRACE=1 FERN_RC_TRACE_DEEP=1`
on the self-host, `FERN_RC_TRACE=1` on the Go compiler, resolved with
`addr2line`). The totals are the gate's pins.

| Compiler, target | Parse | Serialize |
| --- | ---: | ---: |
| Self-host, every target | 40 | 4 |
| Go compiler, x86-64 | 43 | 6 |
| Go compiler, arm64 | 53 | 6 |
| Go compiler, wasm | 49 | 6 |

Each allocation falls in one of three classes:

- **Kept.** What the handler sees: the request box, its strings, the header
  map and its arrays, the body. A reuse donor is what removes these.
- **Wrapper.** The parse's own result, `HttpFramed` and the `Framed` case
  around it, which the serve loop unpacks at once.
- **Scratch.** A temporary nothing keeps. A compiler or stdlib fix removes
  it, with no donor.

The self-host parse is 13 kept, 2 wrapper and 25 scratch:

| Allocations | Scratch | Where |
| ---: | --- | --- |
| 3 | `HeaderMap.append` takes a fresh box for `HeaderMap { ...h, … }` and frees the old one: the spread does not reuse the box it consumes | `headers.fern` `append` |
| 14 | `Option`, `Result`, tuple and `__Head` boxes returned between the head parse's helpers: `Some(v)` per field value, `Ok(h)`, `Ok(p)`, `Ok(joined)`, `Length(n)` and its `Ok`, `__Head` and its `Ok`, the request-line and `__framed_body` tuples, the `line` default tuple, the `Some(0)` length | `__field_value`, `__request_line`, `__head`, `__framed_body`, `__http_content_length` |
| 2 | `header_map_new()` for a default overwritten before it is read, and for the trailers of a request that has none | `__head`, `__framed_body` |
| 6 | request-target scratch: the method and path strings built only to pass to `http_request_target`, the decoded `u8[]`, a string copy made only to check its UTF-8, the empty first segment, the segments array | `__request_line`, `__decoded_path`, `__joined_segments` |

The serialize's 4 are the builder's two blocks, the copy `buf_take_bytes`
makes of its contents, and the `phrases` `string[]` literal in
`http_status_text`, rebuilt per call because the static constant pool holds
scalar arrays but not arrays of strings.

The Go compiler's 43 on x86-64 are 8 kept, 2 wrapper and 33 scratch. It
reuses the `append` box and does not box `Some(v)`, but:

- every `string_from_bytes_range` goes through a temporary `u8[]`, 9 in
  all;
- every empty `[]` allocates, 8 in all;
- the default `Length(0)` is boxed;
- `http_status_text`'s `codes` literal is not static.

arm64 adds 10 and wasm adds 6, from strings the x86-64 backend keeps inline
and from per-target boxing of `Some(v)`.

### 5.2 What a donor buys

At most the 13 kept allocations on the self-host, and 8 on the Go
compiler. The 25 scratch allocations would still be there under a donor, so
the scratch goes first. Each piece is a fix every program gets, not only a
server, which is what slice 8 was meant to buy.

### 5.3 Slices

In this order, one PR each. Compiler slices are self-host first. A Go-compiler
twin is a needless-allocation bugfix under `docs/NATIVE-FREEZE.md`, referenced
on #4451, as slice 2 was.

1. **A record field may hold a static box.** `[]` was already a static box,
   but the self-host's static-box plan admitted only scalar fields, so
   `HeaderMap { names: [], values: [] }` was built per call. A field now
   holds another static box's address, so every `header_map_new()` is one
   static box. Done, after slice 2: parse 37 to 35. The trailers' map and
   the default map are static, and the parse's own map starts from the
   static box, which its first append copies.
2. **`h = h.append(…)` reuses `h`'s box.** At every call in the parse the
   caller's `h` dies at the call, and the Go compiler already writes the
   spread `HeaderMap { ...h, … }` into it; the self-host took a fresh box.
   The self-host's ownership inference counted a parameter only when
   something anchored to it was carried out, and an update that replaces
   every reference field carries out nothing of its base. Done: a record
   update now carries its base, so `h` is counted and the update pairs with
   its box (`docs/rc-log/2026-10-04-b-an-update-carries-its-base.md`).
   Parse 40 to 37, every append reusing the fresh box the map starts from;
   once slice 1 makes that box static the first append copies it, and the
   two together reach 35.
3. **An array of string literals is static.** A string literal is a static
   box, so a field or element holding one is a constant word. `phrases` and
   any record or array of literals join the constant pool. Done: serialize
   4 to 3.
4. **The request target decodes without scratch.** A target with no `%` and
   no dot segment is the range itself, one string. One that needs decoding
   checks its UTF-8 over the bytes, not over a copy. The two strings handed to
   `http_request_target` are read as ranges of the buffer. Done: the request
   line hands `__target_path` its range of the buffer, and a path that needs
   work is decoded and has its dot segments removed in one byte array, with
   UTF-8 checked only when an escape decoded past ASCII. Parse 35 to 28 on
   the self-host.
5. **The head parse returns by writing, not by boxing.** The helpers that
   return a tuple, `Option` or `Result` once per request are the 14 rows
   above. Which fix applies is decided by the first measurement of this
   slice: a niche `Option` of a pointer, which the Go compiler already has,
   removes `Some(v)` for every program; the rest is either an unboxed
   return of a small tuple or enum through the caller's frame, or the
   `docs/FIP-PACKET-PROTOCOL.md` shape of the parse writing its findings
   into the state it threads. The compiler fix is preferred where it
   covers the case, since every caller gains. Removes 14. Parse 29 to 15.
   Done, by neither of the two return conventions. The measurement put
   the 14 in helpers each called from one place, whose box the caller
   takes apart at once, so the semantic inliner (`seminline`) now splices
   a function called from one place whatever it computes, emits no body
   for it once nothing else names it, and reads a tuple, record or variant
   built and only read off its construction, through the phis that join
   its returns, instead of allocating it. That is a fix every program
   gets on every target, wasm included, with no change to how a call
   returns. Parse 28 to 15 on the self-host. The
   gate's probe hands the request to a function that is not spliced, as a
   server hands it to its handler, so it counts what a server keeps.
6. **The donor boundary.** What is left is the kept and wrapper rows and the
   serialize's builder and copy. The connection record owns a request
   record and a write buffer. The parse fills the request in place, the
   handler borrows it, and the response is serialized into the connection's
   buffer and sent from there. Designed against the residue slices 1 to 5
   leave, which sets what has to be donated.

   The residue, traced at `origin/main` 612791ce2: the self-host parse's 15
   are 13 kept (8 strings, the method, the path and three names and
   values; the two field arrays; the field map; the body's stream; the
   request), the `HttpFramed` wrapper, and the `Ok` box
   `__parse_header_bytes` returns, which has four callers, so the inliner
   leaves it a call. The serialize's 3 are the builder's two blocks and the
   copy `buf_take_bytes` makes.

   Done for the parse, by sharing instead of filling in place. Filling in
   place needs strings and arrays a parse may rewrite, which Fern does not
   have, and a handler that keeps its request, or parks holding it, would
   make the donor shared anyway. The serve loop keeps the request it took
   last, on any connection, less any body (`http_framed_kept`), and every
   parse takes it as `prev`: a string it would copy that holds the same text as `prev`'s at
   the same place is `prev`'s, a field block repeating `prev`'s is `prev`'s
   map, and a bodiless request repeating `prev` is `prev`, framing
   included. A value is immutable, so sharing an equal one is the same
   whoever else holds it: no uniqueness check, no new language surface and
   no compiler work, the same on both compilers and every target. Parse 15
   to 1 on the self-host, and 43, 53 and 49 to 1 on the Go compiler, for a
   client repeating its request. A request that differs pays for what
   differs: a new path costs the path, the request and its framing. The
   donor is one per loop rather than one per connection because a donor
   per connection is a head held by every idle keep-alive connection: 3.2
   KiB each in the held-connection gate, against its 1 KiB bound. A parse
   with no request before it (`http_parse_request_framed`) builds its
   request fresh, so a caller it is spliced into still keeps the request in
   registers; the checks against an empty donor cost the `http_hello`
   bench, which parses that way, 2% more instructions on x86-64, and the
   same rounds parsed with the donor cost 10% fewer than before.

   Left after it: the `Ok` box, slice 7, and the serialize's 3, slice 8.
7. **A small variant returns without a box.** `Ok(h)` of a function with
   several callers, and `Framed(f)` of the parse itself, are built only for
   each caller to take apart at once. Done on the self-host: a variant whose
   payloads fit a word, from a function whose every caller takes it apart,
   is returned as its position with the payload in a second register
   (`docs/SELFHOST-SSA-BACKEND.md`, "A variant returned in two words"), and
   is never built. Parse 1 to 0 on all three targets; the gate's probe now
   parses from two call sites, so the `Framed` of a parse that is not
   spliced into its caller is counted too, and it was 2 before. `std/serve`
   gets the `Ok` but not the `Framed`: it keeps the framing whole, holding
   the tail it parsed past a request in its tuples and in-flight records, so
   its parse stays unpaired and builds the box it hands on.
8. **The response is serialized into a buffer the connection keeps.** A
   builder cleared instead of freed, with the reply sent from it, removes
   its two blocks and the copy. Done: the serve loop keeps one builder,
   per loop rather than per connection for the reason the donor is, and
   `http_serialize_response_into` writes each reply into it. Two builtins
   carry it: `tcp_send_buf(fd, b, from)` sends the builder's bytes in
   place, and `buf_clear(b)` empties it and keeps its storage. A short
   write still copies what the kernel did not take into the connection's
   pending bytes. The burst's corked replies are what the builder holds,
   so corking no longer concatenates arrays. The loop's `Date` is a field
   line formatted once per second and written beside the handler's
   fields, where it had been added to the response with `with_header`
   after a `get_all` lookup, both of which allocated per request.
   Serialize 3 to 0 on all three targets.
