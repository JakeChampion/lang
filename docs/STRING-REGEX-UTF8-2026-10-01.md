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

## Validation

The shared fixture covers two-, three- and four-byte scalars, combining
text, partial captures, named and numbered templates, capture recombination,
zero-width matches, empty output, missing captures and raw output ownership.
Bootstrap interpreter/native/WASM and primary Darwin interpreter/native
checks pass. Primary Linux and WASM checks pass in both lowering modes;
production semantic allocation counts balance. Legacy AST runs are checked
for results separately; they do not establish leak-free ownership.

The fixture uses explicit `Option` matches because the primary interpreter
cannot dispatch `is_none` on `None`. A standalone probe reproduces that
limitation with the unchanged stdlib too. It is separate from regex.

All existing regex fixtures pass on the bootstrap backends. The generic
self-host fixture lane requires a native x86-64 host and skips on this
arm64 machine. A dedicated primary-CLI test runs the regex corpus on each
target and compares each fixture's expected result inside the program,
so WASI's process exit-code limit cannot hide a failing bitmask assertion.
The expanded primary corpus passes. A standalone current-compiler probe
also runs all 16 programs on the four targets and the primary interpreter,
with balanced semantic allocation counts on the compiled core targets.
Template-reference parsing and byte assembly are separate to stay within
the existing complexity limit. The integrated full unit suite, all lint
gates, existing regex fixtures and VCL caller checks pass. The primary
Darwin compiler also runs all 42 VCL backend TAP tests successfully.

## Native measurements

Measured on arm64 macOS on 2026-10-01 with strict semantic lowering and
the same compiler/runtime selection for both versions. Each process runs
20 checked replacements. A 16-repeat pilot precedes 4,096 repeats; only
the repeat count changes. Two warmups precede seven samples, alternating
version order. Other validation jobs were active.

| Workload | Before median | After median | Before range | After range |
| --- | ---: | ---: | ---: | ---: |
| ASCII literal replacement | 11.370 ms | 8.762 ms | 10.394-18.870 ms | 8.484-10.081 ms |
| Unicode literal replacement | 18.811 ms | 12.588 ms | 18.327-19.480 ms | 12.400-13.941 ms |
| Unicode capture templates | 93.179 ms | 79.421 ms | 90.776-95.120 ms | 78.540-112.019 ms |
| No match | 10.092 ms | 9.977 ms | 9.659-10.152 ms | 9.870-11.602 ms |

The ASCII and Unicode literal replacements have nonoverlapping ranges.
Capture-template and no-match ranges overlap, so this run does not establish
a speed change for those workloads.

Native text falls from 58,432 to 57,784 bytes. Object text falls from
58,388 to 57,736 bytes, with all 652 bytes attributed to symbol changes.
Direct span assembly removes temporary captured strings, replacement arrays
and the old join helper; this more than pays for UTF-8 validation and the
checked-result handling. Object constants shrink by one byte; compact unwind
grows by 128 bytes and EH unwind by 392. Object data/BSS and linked data are
unchanged. No size baseline was raised.
