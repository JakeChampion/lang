# HTTP/3 multiplexed per-request cost: the header encoder, 2026-10-21

#12887 slice 1, continuing the multiplexed-cost work the stream, session and
QPACK-lookup indexes began (`docs/benchmarks/net-h3-2026-10-21.md`). With the
scans gone, the profile's largest single stdlib function is the HPACK/QPACK
Huffman encoder, which every response's literal header values pass through.

## Method

The hello server under `with_tls().with_h3()` and a client holding m requests
in flight on one connection (`e2eharness.ServeAllocsH3ServerSource` and
`ServeAllocsH3MuxClientSource`). Instructions are callgrind's count for the
server over the warm-up plus 3,000 requests at m = 64 on x86-64, a `-g` build;
the workload is identical before and after, so the deltas are exact.
Allocations per request are `__heap_alloc_count()` deltas the client reads off
`/count`.

## Allocations per request fall as requests multiplex

Allocations per hello request, by the number held in flight on one connection:

| m (in flight) | allocations/request |
|--:|--:|
| 1 (serial) | 230.9 |
| 32 | 133.9 |
| 64 | 133.3 |
| 256 offered (peak 99) | 133.8 |

A burst's flush and its stream acknowledgements are shared across the requests
answered in one pass, so the per-request share falls from the serial 231 to
about 133 once several are in flight and then holds. The multiplexed count is
already tight; the remaining per-request cost is instructions, not allocations.

## The Huffman encoder

`huffman.encode` read three bounds-checked table loads per symbol —
`HUFF_LEN[sym]` twice and `HUFF_CODE[sym]` once. The code and length are now
packed as `code << 8 | length` in one `i64` table (`HUFF`), so a symbol is one
indexed load. `scripts/gen-huffman-table.py` writes `HUFF` beside `HUFF_DEC`
from the same `HUFF_CODE` and `HUFF_LEN`, so the authored tables stay the one
source of truth.

| measure, m = 64 | before | after |
|---|--:|--:|
| `huffman.encode` instructions | 8,405,040 | 7,534,518 |
| server instructions, whole run | 210,238,968 | 209,371,568 |

The encoder is 10.4% lighter; it is the hottest stdlib function on the path, so
that is 0.41% of the server's instructions over the run, and it is a gain for
every HPACK (HTTP/2) and QPACK (HTTP/3) header value a request or a response
carries, not the HTTP/3 server alone. Output is unchanged: the HPACK and QPACK
RFC-vector suites (`http_hpack`, `http_qpack`) pass.

The live table costs about 1 KB more for it: `HUFF_CODE` (about 1 KB) plus
`HUFF_LEN` (257 B) become `HUFF` (about 2 KB), with `HUFF_CODE` now read only by
the generator. `HUFF_DEC` is 16 KB and dominates the module's data either way,
so the trade is a fair one. `gen-huffman-table.py --check`, which
`make check-sources` runs, holds `HUFF` and `HUFF_DEC` to what the authored
`HUFF_CODE` and `HUFF_LEN` generate.

## Ratchet

`TestSelfHostServeAllocsPerRequestH3Mux` pins the multiplexed allocation count
at m = 64 under a ceiling of 134 (133.33 measured on the x86-64 runner),
alongside the serial `TestSelfHostServeAllocsPerRequestH3` (231). The client
reports the peak it held in flight, and the check fails below 48, so a run that
quietly fell back to the serial path cannot pass as a multiplexed one. The
client stays below the server's 100-stream grant: it does not send
STREAMS_BLOCKED, so it would not learn credit had returned above it; the
nightly's 1x256 row offers more with h2load, which does.

## How to rerun

Build the server and the multiplexed client from
`e2eharness.ServeAllocsH3ServerSource` and `ServeAllocsH3MuxClientSource`, hold
64 requests in flight, and read the server under `valgrind --tool=callgrind`.
