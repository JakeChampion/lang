# Raw Reader input

`Reader.read_chunk_bytes(n): Result[u8[], IoError]` reads directly into
owned byte storage. It preserves arbitrary bytes, including NUL and split
UTF-8 encodings. EOF is an empty successful result; negative counts and
closed handles report errors. Zero-length reads preserve host errors and
do not advance the cursor. Retained arrays survive subsequent reads and
closing the reader.

The shared fixture covers all byte values, short reads, EOF, invalid
counts, closed handles and aliases. Interpreter host-outcome tests cover
EOF with bytes and interrupted reads. Bootstrap native, SSA, interpreter
and WASM paths pass, including Preview 2 components. Primary execution
passes on Darwin, x86-64 Linux, ARM64 Linux and core WASM. Strict semantic
IR runs include balanced allocation checks. The IR registry test passes.
The full unit suite and `make lint-all` pass.

The official pinned Darwin bootstrap passes the compiler and `tr` smoke
tests. Stage2 and stage3 are identical at 14,361,697 bytes, SHA-256
`9f8252149936f3ff58925032f5e764737fabe88395bdd99e1cf39701dc78a35e`.

Two limitations remain explicit. The Fern interpreter needs a later host
bridge and bootstrap refresh. Primary WASM components lack stdin Reader
imports even for the existing text API; a regression checks that raw reads
also produce a clear unsupported-target error. This change does not claim
execution coverage for those paths or completion of the string invariant.

## Measurement

The same primary compiler builds both versions. The baseline reads text
chunks then converts them to bytes; the new version reads bytes directly.
Both read the same regular-file stdin in 64 KiB chunks and produce matching
byte counts and endpoint checksums. A four-round pilot passed before
scaling only the round count to 64, with 1 MiB per round. There are two
warmups and seven alternating measurements per version, without sanitizer
or allocation instrumentation.

| Version | Median | Range |
| --- | ---: | ---: |
| Text read followed by byte conversion | 13.759 ms | 11.136-16.393 ms |
| Direct raw read | 14.550 ms | 12.374-24.183 ms |

The timing ranges overlap; this run establishes no speed difference.
Native text decreases from 22,972 to 21,920 bytes. Object text decreases
by the same 1,052 bytes, with unchanged data and constant sections. The raw
read helper adds 408 bytes while the old read helper removes 624 bytes.
Removed conversion and unused close helpers save another 804 bytes; enum
cleanup changes add 68 bytes, and the caller saves 104 bytes. A four-byte
layout change accounts for the remainder. Native unwind data decreases by
160 bytes. No size baseline is raised.
