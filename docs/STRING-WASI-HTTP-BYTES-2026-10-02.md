# Keep WASI HTTP response bodies as bytes

This change includes HTTP byte serialization, the file-byte sink and main
through `0d7a8d321`. Integrated target tests, bootstrap and lint pass. The full
unit suite passed before this latest main integration and is being refreshed.
The primary component size was remeasured with
the integrated stage-2 compiler and remains 60,113 bytes.

The primary HTTP adapter previously carried raw bodies through unchecked
strings. The Go bootstrap backend called the lossy `body_string()` method:
a response containing bytes 0 through 255 grew from 256 to 512 bytes.
Both adapters now use `body_bytes()` and pass bytes to the host's `list<u8>`
write interface. Text bodies retain their UTF-8 encoding.

The primary adapter reuses a bounded builder to prepare 4096-byte chunks.
It releases the builder on success and host-write errors. The Go backend
forwards its packed byte array directly and releases the owned method result.
Its hidden wrapper calls are preserved through both tree-shaking passes and
IR inlining.

Both adapters hand the response to the host before blocking body writes.
Previously the primary adapter could stall once the host's response buffer
filled. The output stream is still dropped before finishing its outgoing
body, as required by the
[WASI HTTP resource contract](https://github.com/WebAssembly/wasi-http/blob/v0.2.0/wit/types.wit).

## Validation

Actual `wasmtime serve` tests compare both compilers against exact expected
bytes for direct arrays, a stream with a nonzero cursor, captured chunk
producers and Unicode text. Binary cases cover lengths 0, 1, 4095, 4096,
4097 and 8193, including malformed UTF-8 and embedded zero bytes.

A separate test executes the adapter's actual chunk loop with a host
substitute that accepts all writes, rejects immediately, or rejects after
one successful chunk. It checks output, error counts and balanced allocation
censuses on Darwin, Linux x86-64, Linux ARM64 and core WASM. This establishes
cleanup of the chunk loop, not leak freedom of the entire HTTP reactor.

The actual integrated stage-2 compiler passes all 18 binary HTTP requests.
Its chunk-loop probe reports 32 allocations and 32 frees on Darwin, and
39 allocations and 39 frees on core WASM, with zero live bytes. Existing
reactor stream-error bindings still need a separate resource-lifetime audit;
these counts apply to the chunk loop with the deterministic host substitute.

## Component size

The same byte/stream/chunk fixture was compiled with instrumentation unset.

| Compiler | Original | Byte adapter | Difference |
| --- | ---: | ---: | ---: |
| Primary, with unused buffer helpers pruned | 59,589 B | 60,113 B | +524 B |
| Go bootstrap | 46,162 B | 44,177 B | -1,985 B |

The initial primary result was 61,279 bytes. Pruning unused buffer helpers
removed 1,166 bytes before accepting the remaining growth. The final core
code section grows by 495 bytes: the new builder lifecycle, byte-range copy,
owned byte extraction and canonical byte-list bridge replace the unchecked
string path. The remaining component growth is framing and index encoding.
No size baseline was raised.

Helper pruning is integrated. Linux target tests and actual stage-2 Darwin,
core WASM and Preview 2 probes pass. The pinned seed produces identical
stage-2 and stage-3 compiler binaries of 12,130,737 bytes, SHA-256
`9f2b730c45086688adbcea3fe8d7474d3c67ee617598cf8871581005679374c2`.
The seed predates generator changes on main, so stage 1 differs.
`make lint-all` passes in the updated Linux snapshot. The full unit suite
passed on `fd1a49d27`; its latest main integration is being refreshed.
TCP, UDP and file byte sinks remain separate work under #10948.
