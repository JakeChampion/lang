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
