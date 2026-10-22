# Combined compiler memory changes: 2026-10-22

Base: 7fccb64b3226a526152fd7daf94fff7b3f98fc4e.
Source: 9bdaf74606d10e97f1ca7ed1cdcfcc1c055867e8.

The combined tree keeps byte constants packed and uses native constant word
indexes and array allocator tail calls. The container classification separates
construction from mutation. The complexity baseline is unchanged.

The focused suite passes in 50.215 seconds. Native tests cover constant word
indexes, allocator tail calls, large donor release, ARM64 hardening, and the lane
ABI. Byte-array tests cover bounded graphs, ordinary large literals, binary
assets, both interpreters, aliases, and raw writable copies. The census,
registries, and complexity gate also pass.

The native-memory-codegen and packed-byte-constants archives retain their
separate source scopes, profiles, output hashes, and complete validation records.

The combined bootstrap's stages two and three match at 14,159,904 bytes, SHA-256
4cc1b892988e595ae827f952ed4701b4c5a3d48bbf33315ec6df14d31c4fd4f2.
The broad runtime, profiler, constant-data, and call-ABI checks pass in 143.701
seconds. All eleven strict size rows pass with the recorded baselines. The
report combines the bootstrap size and ten stock-driver builds. The workspace
restart interrupted the stock-driver run after seven rows. The last three builds
pass in 27.690 seconds.

The combined profiling compiler uses the same frozen producer as the byte-array
control. A 1 MB byte asset uses 46,480 KiB peak RSS and takes 0.194 seconds.
The text asset uses 29,824 KiB and takes 0.135 seconds. All four assembly outputs
match the byte-array control exactly. The profile directory contains the raw
profiles, commands, measurements, and hashes.

The `.fern` files are formatted copies. Their `.fern.input.gz` neighbors retain
the exact measured inputs. Decompress those inputs over the copies to reproduce
a run. `measured-inputs.json` records the SHA-256 of each version.
