# Checked regex text and explicit byte output

`std/regex` matches bytes. For example, `.` consumes one byte of `é`,
and replacing that match with `X` leaves `0xa9` behind. The previous
implementation returned those malformed bytes as a string. Captures and
splits could also expose a partial scalar as text.

Matching syntax, byte offsets, match selection and zero-width progression
are unchanged. Text-producing APIs now return `Option`: `Some` contains
valid UTF-8, and `None` means the result would be invalid. Explicit byte
variants preserve every output byte. Both families still accept string
inputs; this does not add a matcher over arbitrary byte arrays.

| Checked text API | Raw output API |
| --- | --- |
| `regex_replace` -> `Option[string]` | `regex_replace_bytes` -> `u8[]` |
| `regex_replace_all` -> `Option[string]` | `regex_replace_all_bytes` -> `u8[]` |
| `regex_replace_groups` -> `Option[string]` | `regex_replace_groups_bytes` -> `u8[]` |
| `regex_replace_all_groups` -> `Option[string]` | `regex_replace_all_groups_bytes` -> `u8[]` |
| `regex_split` -> `Option[string[]]` | `regex_split_bytes` -> `u8[][]` |
| `RCaps.group` -> `Option[string]` | `RCaps.group_bytes` -> `u8[]` |
| `RCaps.group_named` -> `Option[string]` | `RCaps.group_named_bytes` -> `u8[]` |

Missing or nonparticipating captures still produce empty output:
`Some("")` for text and `[]` for bytes. Use `has_group` to distinguish an
absent capture from a participating empty one. Empty spans inside a scalar
are valid empty strings. A split is rejected only when a returned piece
contains incomplete UTF-8, not merely because a separator split a scalar.

Replacement templates append source spans directly into a byte buffer and
validate the complete result once. Thus `regex_replace_all_groups("(.)",
"é", "$1")` returns `Some("é")`, even though each capture alone returns
`None` as text. Literal template spans also preserve Unicode. No-match text
replacements return the original string without copying or rescanning it.

Callers must handle the new `Option`, or select the byte variant if raw
output is intentional. Repository examples and fixtures are migrated.
VCL `regsub` and `regsuball` report an evaluation error when output would
be malformed text. Their existing valid replacements keep their behavior.

## Current validation

The integration includes main `cede3aaf3` and uses its production typed-IR
lowering. Refreshed bootstrap and primary Linux/WASM target matrices pass,
including regex conformance, seeded random bytes, ASCII conversion, VCL
callers, GNU tr parity, opcode inventories and all lint gates.

The pinned seed `stage0-20261001-c891ebc` produces identical stage-2 and
stage-3 binaries of 12,411,809 bytes, SHA-256
`1ceb7758111680f0138ba6f346c3f113ee35bb61c7b8c2e0c76938c8f582599f`.
Stage 1 differs because the seed predates generator changes.

The actual stage-2 compiler passes the regex, RNG and ASCII fixtures on
Darwin, core WASM and Preview 2. The public APIs also pass in the primary
interpreter. Legacy `chr` is a compiled-runtime entry rather than an
interpreter API, so that fixture runs only on compiled targets.

| Fixture | Native allocations/frees | Core-WASM allocations/frees |
| --- | ---: | ---: |
| Regex UTF-8 | 5130 / 5130 | 5182 / 5182 |
| Seeded random bytes | 354 / 354 | 354 / 354 |
| ASCII byte method | 512 / 512 | 512 / 512 |
| Legacy ASCII constructor | 262 / 262 | 262 / 262 |

Every instrumented fixture ends with zero live bytes. Preview 2 checks
behavior only.

The shared regex fixture covers two-, three- and four-byte scalars, combining
text, partial captures, named and numbered templates, capture recombination,
zero-width matches, empty output, missing captures and raw output ownership.
The primary conformance runner compares each fixture's expected result
inside the program, so WASI's exit-code limit cannot hide a failing bitmask.

After the target pass, duplicate runs under retired lowering-mode flags
were replaced with one production-path run per target. Assertions and target
coverage remain. The final Darwin bootstrap and primary matrices, revised
Linux tests, full unit suite and all lint gates pass.

## Native measurements

Measured on arm64 macOS on 2026-10-02. The same final stage-2 compiler builds
both versions; the baseline uses main `cede3aaf3`'s stdlib. Each process
checks the exact result of 20 replacements. A 16-repeat pilot precedes
4096 repeats, changing only the repeat count. Two warmups precede seven
samples, alternating version order. Task-owned compiler and test jobs had
stopped; desktop activity remained.

| Workload | Before median | After median | Before range | After range |
| --- | ---: | ---: | ---: | ---: |
| ASCII literal replacement | 10.544 ms | 8.379 ms | 9.828-11.634 ms | 7.831-9.004 ms |
| Unicode literal replacement | 17.157 ms | 11.126 ms | 16.739-17.421 ms | 10.622-11.269 ms |
| Unicode capture templates | 92.687 ms | 77.729 ms | 91.742-107.696 ms | 75.024-79.788 ms |
| No match | 8.183 ms | 8.430 ms | 8.035-8.676 ms | 8.156-8.771 ms |

The three replacement workloads have separated sample ranges in this run.
No-match ranges overlap. These measurements are specific to these workloads
and this host.

Both fixture executables occupy 99,505 bytes. Native code falls from
59,160 to 58,504 bytes; unwind data grows from 5732 to 6124 bytes, and
data stays at 2440 bytes. Direct span assembly removes temporary captures,
replacement arrays and the old join helper while adding checked output.

Building both compiler sources with the same final compiler yields
12,411,809-byte executables on both sides. Code grows by 80 bytes, unwind
data by 72 and data by 256, all within the existing file segments. The
integration includes ASCII validation and the ARM64 byte-alignment fix;
no size baseline changed. These figures supersede older integration results.
