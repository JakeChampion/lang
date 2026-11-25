# Memory, integer, alignment and leak sanitizers

Status: audit for [SEC-041](https://github.com/JakeChampion/lang/issues/13543).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).
The mode itself is described in `docs/SANITIZER.md`, the allocator in [ALLOCATOR-AUDIT.md](ALLOCATOR-AUDIT.md), and the quarantine policy in [QUARANTINE.md](QUARANTINE.md).

## What exists

`-sanitize` turns on these detectors on x86-64-linux and arm64-linux. The wasm backend has each of them but two. A wasm module has one thread and no atomics, so it has no second-context check and no atomic check. `docs/SANITIZER.md` has the backend table.

| Detector | Fires when | Report |
|---|---|---|
| reference-count over-release | a decrement sees a count at or below zero | `fern-sanitizer: rc over-release (double free)`, a backtrace, exit 124 |
| use after free | an increment or a decrement touches a quarantined block | `fern-sanitizer: use-after-free (touched a quarantined block)`, a backtrace, exit 124 |
| write after free | the eviction or the exit scan finds a word of a quarantined block without the poison | `fern-sanitizer: write-after-free (a quarantined block was written)`, a backtrace, exit 124 |
| heap buffer overflow | a release finds that the redzone after a block was written | `fern-sanitizer: heap-buffer-overflow (a block was written past its end)`, a backtrace, exit 124 |
| misaligned atomic | an `__atomic_*` builtin gets an address that is not a multiple of 8 | `fern-sanitizer: misaligned atomic (an atomic access of an address that is not a multiple of 8)`, a backtrace, exit 124 |
| second context | a context that does not own the heap allocates, releases or counts a block | `fern-sanitizer: second context (a thread or stack that does not own the heap used it)`, a backtrace, exit 124 |
| leak census | `main` returns with live bytes | `fern-sanitizer: leak <K> bytes in <N> blocks`. A run that `exit()` ends gets no verdict, because the frames above the call are never unwound. |

A clean run prints nothing, keeps its exit code and its output, and is the whole pass condition.
Each fatal report goes through `__fern_report`, the same path as a bounds or arena abort. The cause and the frame-pointer backtrace locate the fault, and `-g` resolves the addresses to functions.
Without the flag the emitted code is byte-identical to a compiler that never had the feature.

Integers need no sanitizer.
Every integer operation is total, so there is no undefined arithmetic to detect, and the wrapped-index rules are pinned separately ([ARITHMETIC-AUDIT.md](ARITHMETIC-AUDIT.md)).
A typed box is zero-filled at allocation. A read of an unwritten typed slot is a read of zero, not of a previous owner's data.

The suites that pin the detectors run on both native targets and under the sanitizer differential. They cover the clean run, the double free, the use after free, the quarantine, the leak verdict and the map handle cases. The 60 leak-check and sanitized-construction tests of `internal/testing/e2e` cover the ownership lowering.
`TestSelfHostSanitizeQuarantineGrowth` pins the cost of the quarantine, and `TestSelfHostSanitizeReportCarriesBacktrace` pins the report shape.

## What the detectors do not see

- A raw read after free. `__load_*` through a dangling raw pointer touches no count, so the quarantine never sees it. The quarantine fills the payload with the poison pattern, so the read returns the pattern and not the bytes the block held ([QUARANTINE.md](QUARANTINE.md)).
- A write more than 16 bytes past a block. The redzone after each block is 16 bytes, and the release of the block reads it. A write beyond it lands in the next block's header or payload. The next release or increment of that block then reports an over-release or nothing at all.
- A write in the class past the request of a block that is never released. The leak census at exit reads the redzone after the class of each live block, on the native targets and on wasm. It does not read the words of the class past the request, because a string grows into those words.
- An uninitialised raw read. `__alloc` does not zero its block, and the sanitizer has no shadow memory to tell a written byte from an unwritten one.
- A misaligned ordinary raw access. The native targets accept a misaligned `__load_*` and `__store_*`. Under `-sanitize` on a hosted target, each `__atomic_*` builtin checks that its address is a multiple of 8. A misaligned address reports `fern-sanitizer: misaligned atomic` with exit 124 (`TestSelfHostSanitizeMisalignedAtomic`). A kernel image has no check, because the report needs a host.
- A reference cycle. The census reports it as live bytes without attribution.
- A stale reference that is only read through raw builtins, which is the first case again.

Each of these needs shadow memory, which tracks a state per byte of the arena, or redzones, which cost space per block.
Both are the AddressSanitizer design. The redzone exists: each block has 16 bytes of poison after its size class, and its release checks them. Shadow memory is not built.
Shadow memory costs one bit to one byte per arena byte and a check on every raw access, which the raw builtins emit.

## Remaining limits

- The count detectors cover reference-counted misuse only. Raw memory misuse through the raw builtins is the unsafe surface of [UNSAFE-INVENTORY.md](UNSAFE-INVENTORY.md). Its detectors are the arena-bounds check of `__free` and, under `-sanitize`, three more. The first is the abort on a second `__free` of a quarantined block. The second is the redzone check at every `__free` ([ALLOCATOR-AUDIT.md](ALLOCATOR-AUDIT.md)). The third is the alignment check of each atomic.
- The quarantine holds a byte budget, 64 MiB unless `-quarantine SIZE` gives another. A use after free older than the budget is a silent reuse again ([QUARANTINE.md](QUARANTINE.md)).
- `docs/SANITIZER.md` ("Combinations with other switches") lists the switches that change a sanitized build and the targets that refuse one. A kernel image or a freestanding library has no host for a report, so the compiler refuses `-sanitize` there.
- There is no source-span attribution beyond the backtrace. A report names the function, not the line, unless `-g` and `addr2line` resolve it.
