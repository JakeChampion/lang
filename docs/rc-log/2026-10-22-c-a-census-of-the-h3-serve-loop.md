# 2026-10-22 — a census of the HTTP/3 serve loop by allocation site

`TestSelfHostServeAllocsCensusH3`. Refs #12887.

## The tool

`TestSelfHostServeAllocsPerRequestH3` gates the total. It does not say where
the boxes come from. The census test builds the same server with
`FERN_RC_TRACE=1` and serves two runs, of 1,000 and of 3,000 requests. It
names each `rctrace a` line by its site and the frame above it. It then
subtracts the short run from the long run and divides by 2,000. The result is
the cost of one request without the start-up cost.

```
FERN_SERVE_ALLOC_CENSUS=/tmp/census.txt go test ./internal/testing/e2ecompiler \
  -run '^TestSelfHostServeAllocsCensusH3$' -count=1 -v
```

The test writes two tables to the file, one for the serial client and one for
the multiplexed client. Each table gives the count by module and then by site.
The module is that of the Fern function that asks for the box. Where the site
is a runtime helper, the caller gives the module. A run takes about two
minutes on x86-64.

## The census

x86-64, main at `cbdfe6d5`.

| Module | Serial | Multiplexed, 64 in flight |
|---|---:|---:|
| `streams` | 37.94 | 26.21 |
| `h3` | 27.97 | 24.06 |
| `serve` | 27.01 | 7.39 |
| `qpack` | 25.00 | 24.00 |
| `conn` | 24.00 | 4.41 |
| `h3q` | 17.00 | 14.03 |
| `packet` | 15.00 | 0.51 |
| `recovery` | 7.00 | 0.08 |
| `frames` | 5.00 | 3.06 |
| `aes_gcm` | 4.00 | 0.10 |
| the rest | 9.49 | 7.83 |
| **Total** | **200.41** | **111.68** |

The totals agree with `TestSelfHostServeAllocsPerRequestH3` (200.92) and
`TestSelfHostServeAllocsPerRequestH3Mux` (112.91). The serial client sends
one request a packet, so each request pays for a packet, its protection and
its acknowledgement: `packet`, `conn`, `recovery` and `aes_gcm`. With 64
requests in flight those costs spread over many requests. The multiplexed
table is the cost of a request itself.

The largest sites in the multiplexed table:

| Per request | Site | Caller |
|---:|---|---|
| 6.00 | `qpack.__int` | `qpack.__sec_int` |
| 4.00 | `h3q.Conn.__ended` | `h3q.Conn.__applied` |
| 4.00 | `qpack.field` | `qpack.Decoder.__indexed` |
| 3.12 | `__fern_arr_push` | `__fern_arr_push_owned` |
| 3.03 | `h3.Session.taken` | `h3q.Conn.__applied` |
| 3.00 | `__fern_arr_push` | `h3.Session.__write` |
| 3.00 | `qpack.field` | `h3q.Conn.respond_head` |
| 3.00 | `streams.Streams.__new_stream` | `streams.Streams.__peer_opens` |

## Two findings

- **A `Result` with a tuple payload is a box.** `qpack.__int` returns
  `Result[(i64, i32), i32]`. `sempair` pairs `__sec_int`, so the variant's
  position comes back in the result register. The payload goes in the one
  word beside it. A tuple of two scalars does not fit one word, so each
  `Ok((value, i))` builds a 40-byte tuple box. The six calls a request makes
  to decode its header section cost six boxes.

- **A payload of one `i64` is a box too.** `word` refuses an `i64`, because
  it is wider than a pointer on wasm32. A probe with both shapes allocates
  three boxes for each call to a function that decodes two integers.
  `semscalarreturn` already returns up to three more words for a scalar
  record. It does not do that for a variant payload.

- **A new stream is three records.** `Streams.__new_stream` builds a
  `Stream` with its `Send` and `Recv` halves. Each is a record of its own,
  so each is a box. A nested record that stays with its parent for the
  parent's life can live inside the parent. That is a larger change than the
  first two.

## Next

Return a variant's scalar payload in the words `semscalarreturn` already
uses, so that `Ok((value, i))` and `Ok(n)` build no box. Then measure it with
this census.
