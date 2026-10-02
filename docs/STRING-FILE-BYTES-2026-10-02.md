# File byte sink for D9

`write_file_bytes(path: string, content: u8[]): Result[void, IoError]` writes
an owned byte array without first constructing a string from its contents.
It borrows the array, so callers can retain and reuse it. Empty input still
creates or truncates the file. Creation follows `write_file`: native code and
the Go interpreter use mode 0644 subject to the host umask, while WASI follows
its host's creation policy. Writing an existing file preserves its mode.

The native helpers reject an embedded NUL before opening a path. Writes
continue after short counts and retry interruption. Zero progress is an I/O
error. Every opened descriptor is closed, including after a failed write;
the first write error takes precedence over a later close error.

Primary native code lends packed array storage directly. Primary WebAssembly
packs its word-sized array slots into owned scratch and releases it on every
exit. Preview 2 writes chunks of at most 4096 bytes and drops fresh preopen
descriptors, the opened file, its output stream and owned stream errors.
The Go bootstrap uses its existing process-lifetime cached preopen handle.

Both checkers, capability inventories, IR contracts and target emitters know
the new builtin. The Go interpreter uses the host file API. Primary interpreter
file opening remains unsupported; supporting it requires a compatible host
builtin and seed update. This change does not claim interpreter parity for
file operations or accept byte views in place of owned arrays.

## Validation

The shared fixture checks exact output for 8193 bytes cycling through all byte
values, input reuse, temporary arrays, empty truncation, existing-file modes,
creation permissions matching the text API, missing parents, directory paths
and embedded NULs. It passed on primary and
bootstrap Darwin ARM64, Linux ARM64 and x86-64, core WebAssembly and Preview 2.
Linux also covers both bootstrap native backends. Primary native and core
executions require balanced allocation counts.

Seven deterministic WASI boundary cases passed for each compiler: one-byte
short writes, interruption followed by progress, zero progress, write failure,
partial progress followed by failure, close failure and open failure. The
test executes compiled modules and checks write counts, bytes and descriptor
closure. A separate Linux test performs 128 failed writes to `/dev/full`
under a 64-descriptor limit, then successfully writes another file. It passed
for both primary targets and both bootstrap native backends.

A library-only caller exposed missing per-module runtime registrations:
both Linux targets failed to link `__fn___fern_write_file_bytes_u8` before
the fix. With both byte-array runtime forms registered, the same programs
link and write the exact binary payload. The WASI fault matrix also passes
through the primary compiler's direct core-binary emitter.

After integration with main at `c987221a9`, the official pinned seed produces
identical stage-2 and stage-3 Darwin compiler binaries of 12,097,665 bytes,
SHA-256
`d08ac653f4d61d4faeea3382a06e604e76a8feb2e945635d142af9e1d9621bba`.
Stage 1 has the same size but different code: the seed predates generator
changes on main. Stages 2 and 3 establish the current compiler's fixed point.
The actual stage-2 compiler passed the shared fixture on Darwin, core WASM
and Preview 2. Native allocation counts were 34 allocated and 34 freed;
core WASM reported 28 and 28. Preview 2 does not expose that census, so its
successful execution is not evidence of whole-component allocation balance.

The integrated Linux matrix, all fault cases, per-module regression and both
IR-registry checks pass. The full unit suite and `make lint-all` pass on
`7052f1c0c`, including the per-module registration fix and main's newer CI
changes. The refreshed bootstrap has the same stage-2 and stage-3 hash
recorded above.
The Darwin Go harness is blocked by
host file-table exhaustion; the integrated Darwin coverage above uses the
actual bootstrapped compiler directly.

## Measured allocation and artifact cost

Both programs use the same stage-2 compiler and an 8192-byte retained array
containing only ASCII bytes. The text baseline constructs a valid string for
each `write_file` call; the candidate passes the array to `write_file_bytes`.
Exact file contents are checked after every run. Comparing one write with
sixteen writes separates per-call allocations from fixture setup:

| Target | Text allocations per additional write | Byte allocations per additional write |
| --- | ---: | ---: |
| Darwin ARM64 | 4 | 2 |
| Core WASM | 2 | 2 |

Every measured native/core run freed all counted allocations. WASM still
needs packing scratch because its array elements occupy word-sized slots.
These measurements do not establish file-write throughput or zero allocation.

With sanitizer and census instrumentation removed, the one-write programs
have these sizes:

| Output | Text sink | Byte sink | Difference |
| --- | ---: | ---: | ---: |
| Darwin ARM64 executable | 33,473 | 33,473 | 0 bytes |
| Core WASM binary | 7,490 | 7,514 | +24 bytes |
| Preview 2 component | 11,506 | 12,642 | +1,136 bytes |

The component helper adds explicit resource cleanup and stream-error handling
to the raw write path. The final integration must retain this size comparison;
no size baseline is increased by this change.

Compiled with the same stage-2 compiler, main's compiler executable is
12,064,241 bytes and this revision is 12,097,665 bytes, an increase of 33,424.
Mach-O section inspection attributes 14,296 additional bytes to code, 232 to
unwind data and 8192 to data. The text and data segments each cross a
16,384-byte alignment boundary, and link-edit data grows by 656 bytes.
This includes the new builtin's compiler routing and generated runtime
bodies; it is separate from the small program sizes above.

This is part of #10948 and #5714. HTTP/TCP sinks, byte views and remaining
binary consumers are separate work, so neither issue nor epic #5626 closes
with this builtin alone.
