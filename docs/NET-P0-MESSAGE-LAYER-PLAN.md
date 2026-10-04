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

The native column has one cause of its own. Every read of a `const` array
allocates a fresh copy of it on native: 100 reads of a four-element `u8[]`
constant cost 100 allocations, where the self-host costs 0. `std/http`'s
`NOT_TCHAR` and `FIELD_CTL` scans pay it per header.

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
3. **One copy per kept string.** `string_from_bytes_range_unchecked` copies a
   byte range straight into a string, so the method, path, version and each
   header name and value cost one allocation instead of two. The self-host
   parse fell from 69 to 59. The other nine double copies of the 19 are the
   lowercasing in slice 5 and the path decode. Native keeps two copies, since
   its string constructor takes an array.
4. **Lookups that do not allocate.** The framing rules ask `HeaderMap` for
   one value or a count. They stop building an array to answer. `append`
   grows by doubling, not by one.
5. **Names compared, not lowercased.** `HeaderMap` keeps a name as it came
   off the wire and compares names ASCII-case-insensitively. That removes the
   lowercasing copy per header. HTTP/1.1 field names are case-insensitive, so
   only iteration order and spelling are observable, and both keep what the
   client sent.
6. **A bodiless request carries no stream.** The empty body is one shared
   value, not a stream built over zero bytes.
7. **The head serializes into the builder.** The status line and fields are
   pushed into the one builder the body goes into, with no intermediate
   `string`. The framing half of serialize drops from 13 to 2: the builder
   and its result.
8. **The donor boundary.** What is left after slices 3 to 7 is the
   `HttpRequest` box, its strings, the response box and the wire buffer.
   Those are exactly what #9851 §3.3's handler boundary with a reuse donor
   exists for. It gets its own plan once the gate shows that residue, since
   the residue sets what the boundary has to donate.

## 4. Exit

Criterion B of #9853 is met when the gate reads 0 for the framing path on
both compilers with the hello handler, which needs slice 8. Slices 3 to 7
are each worth landing alone, because every server pays them per request
whatever its handler does.
