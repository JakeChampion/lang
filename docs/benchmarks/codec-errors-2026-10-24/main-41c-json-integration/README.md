# Codec contracts on the updated JSON and bootstrap base

Source: `882fece963f63b5d8de536903ebd9a9ad1593df1`.
Base: the JSON constructor branch at `8cf6eac8b6f809b97be11088e353667712a31d82`.
This source includes the current main corrections and the published bootstrap lock.

The family supplies borrowed byte inputs, `u8` single-byte values, and `char` fills.
It removes the duplicate byte-update names.
Strict Hex, Base32 and Base64 decoders return typed errors and reject nonzero unused bits.
SemVer parsing checks identifiers and leading zeros.
UUID helpers use checked parsing and provide the canonical nil value.
The formatter preserves tuple comments, and the interpreter accepts the minimum i32 CRC seed.

The new source passes three matrices:

- 131 commands for codecs, SemVer, UUID, ECDSA, RSA, AEAD and compression.
- 44 commands for byte views, byte decoders, the minimum CRC seed and multibyte fills.
- 22 commands for JSON objects and the shared collection vocabulary, including `json.from`.

Both checkers and interpreters cover the contract programs.
The large cryptographic suites use compiled execution.
Every program builds for all four targets.
Native x86, ARM64 under qemu and WASI executions pass all 57 allocation checks with zero live bytes.
Darwin is a compile check only.

The family tests pass 36 cases.
The compiler integration tests pass 13 cases.
The byte-contract checker tests pass 12 cases.
None of these suites has a failure or a skip.
Go vet, Go format and the format check for all 1200 Fern source files pass.

The actual published pin produces stage 1 with SHA-256
`624e448b8b3df5cb02194f0710324a80ed8c4d635322ca7a6575e37b1360bdc3`.
Stages 2 and 3 are byte identical:
`f6ed9d1264993ef6d231ce452a17eda29ab048e411c428131956e7886423f880`.
All stages have 14387840 bytes.
The final image matches the earlier completed codec image.

All 11 strict driver size gates pass against the unchanged main baselines.
The largest increase is 0.1 percent.

The manifest records 496 source files and 28 evidence artifacts.
The archive contains all 394 matrix output and error logs.
The complete patch applies in reverse to the source revision.
Earlier records retain their original source scope, including the prior full program sweep.
This record covers the affected programs after the JSON merge.
The final stages, oracle and published pin remain in the workspace.
