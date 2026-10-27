# Memory, integer, alignment and leak sanitizers

Status: audit for [SEC-041](https://github.com/JakeChampion/lang/issues/13543).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).
The mode itself is described in `docs/SANITIZER.md`, the allocator in [ALLOCATOR-AUDIT.md](ALLOCATOR-AUDIT.md), and the quarantine policy in [QUARANTINE.md](QUARANTINE.md).

## What exists

`-sanitize` turns on three detectors on x86-64-linux and arm64-linux, and the leak census alone on wasm.

| Detector | Fires when | Report |
|---|---|---|
| reference-count over-release | a decrement sees a count at or below zero | `fern-sanitizer: rc over-release (double free)`, a backtrace, exit 124 |
| use after free | an increment or a decrement touches a quarantined block | `fern-sanitizer: use-after-free (touched a quarantined block)`, a backtrace, exit 124 |
| leak census | the process exits with live bytes | `fern-sanitizer: leak <K> bytes in <N> blocks` |

A clean run prints nothing, keeps its exit code and its output, and is the whole pass condition.
Each fatal report goes through `__fern_report`, the same path as a bounds or arena abort. The cause and the frame-pointer backtrace locate the fault, and `-g` resolves the addresses to functions.
Without the flag the emitted code is byte-identical to a compiler that never had the feature.

Integers need no sanitizer.
Every integer operation is total, so there is no undefined arithmetic to detect, and the wrapped-index rules are pinned separately ([ARITHMETIC-AUDIT.md](ARITHMETIC-AUDIT.md)).
A typed box is zero-filled at allocation. A read of an unwritten typed slot is a read of zero, not of a previous owner's data.

The suites that pin the detectors run on both native targets and under the sanitizer differential. They cover the clean run, the double free, the use after free, the quarantine, the leak verdict and the map handle cases. The 60 leak-check and sanitized-construction tests of `internal/testing/e2e` cover the ownership lowering.
`TestSelfHostSanitizeQuarantineGrowth` pins the cost of the quarantine, and `TestSelfHostSanitizeReportCarriesBacktrace` pins the report shape.

## What the detectors do not see

- A raw read after free. `__load_*` through a dangling raw pointer touches no count, so the quarantine never sees it. The block keeps its bytes, and the read returns them.
- A raw write past a block. There are no redzones, so the write lands in the next block's header or payload. The next release or increment of that block then reports an over-release or nothing at all.
- An uninitialised raw read. `__alloc` does not zero its block, and the sanitizer has no shadow memory to tell a written byte from an unwritten one.
- A misaligned raw access. The native targets tolerate a misaligned ordinary load and store, and no check exists for the atomic operations that require alignment.
- A reference cycle. The census reports it as live bytes without attribution.
- A stale reference that is only read through raw builtins, which is the first case again.

Each of these needs shadow memory, which tracks a state per byte of the arena, or redzones, which cost space per block.
Both are the AddressSanitizer design, and neither is built.
Shadow memory costs one bit to one byte per arena byte and a check on every raw access, which the raw builtins emit. Redzones cost a guard region per block in every size class.

## Remaining limits

- The three detectors cover reference-counted misuse only. Raw memory misuse through the raw builtins is the unsafe surface of [UNSAFE-INVENTORY.md](UNSAFE-INVENTORY.md). The only detector for it is the arena-bounds check of `__free`.
- The quarantine is unbounded, which [QUARANTINE.md](QUARANTINE.md) leaves as the owner's decision.
- There is no source-span attribution beyond the backtrace. A report names the function, not the line, unless `-g` and `addr2line` resolve it.
