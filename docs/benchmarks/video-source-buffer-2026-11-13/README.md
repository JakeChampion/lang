# Byte-source allocation control

The IVF and Annex B input readers reuse `std/audio/source.Buffer`.
This control reads the same 1 MiB file through that buffer until EOF.
Each iteration consumes the complete available window.
The next read therefore starts with an empty window.

The previous implementation copied each owned read chunk into a second array.
The new implementation transfers the chunk directly into the empty buffer.
A nonempty window retains the existing append and compaction paths.

Both versions use compiler source `569131581d609d105c7b239e8b2d88ed3b56a51a`.
The frozen x86-64 compiler has SHA-256 `188535edf2999d1221c144ba18dc6dd469c235ebddd6b08e820cde5cfe08bad2`.
Only the source-buffer implementation differs between the two runs.
Both executables use `-sanitize` and the same standard library source.
The input consists of `bytes(range(256))` repeated 4096 times.

| Read chunk | Previous allocations | Current allocations | Previous fresh bytes | Current fresh bytes |
| ---: | ---: | ---: | ---: | ---: |
| 3 | 2,097,165 | 1,398,112 | 67,108,992 | 47,535,856 |
| 31 | 202,965 | 135,312 | 9,742,304 | 5,412,504 |
| 16,384 | 392 | 263 | 2,653,512 | 1,338,112 |

The counters cover the read loop and its byte-count report.
`__heap_alloc_count` counts allocator requests.
`__heap_bump_bytes` counts fresh heap bytes during this interval.
These counters do not measure peak live memory, RSS, or throughput.

All six runs read 1,048,576 bytes and return FNV-1a checksum `2630655429`.
The sanitizer reports equal allocation and free counts, with zero live bytes.
[The machine-readable results](results.json) include the input and compiler hashes.

## Repeat the control

Preserve one compiler for both builds.
Compile `scripts/video-reference/source-buffer.fern` against each standard library source tree:

```sh
"$compiler" -stdlib "$stdlib" -target x86-64-linux -sanitize -o source-buffer scripts/video-reference/source-buffer.fern
python3 -c 'from pathlib import Path; Path("input.bin").write_bytes(bytes(range(256)) * 4096)'
./source-buffer input.bin 16384
```

Repeat the command with chunk sizes `3` and `31`.
Compare the byte counts and checksums before the allocation counters.
The original module is available at the compiler source commit above.
