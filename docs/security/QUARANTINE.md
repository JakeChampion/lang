# Quarantine, placement, and guard pages

Status: audit for [SEC-024](https://github.com/JakeChampion/lang/issues/13526), with the policy decision open.
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).
The allocator is recorded in [ALLOCATOR-AUDIT.md](ALLOCATOR-AUDIT.md) and the sanitizer in `docs/SANITIZER.md`.

## What exists

The release build recycles a released block at once.
The block goes to the free list of its size class, and the next request of that class receives it.
There is no quarantine, no randomized placement, and no guard page in the release build.

Under `-sanitize` a released block never returns to a free list.
Its reference count becomes a poison value, and every count operation runs through a helper that checks the poison.
A touch reports `fern-sanitizer: use-after-free` with exit 124.
The quarantine is unbounded, so the arena grows by one block per release for the life of the process.
That is what makes every use after free detectable, because a recycled block overwrites its own poison.

## Measured growth

`TestSelfHostSanitizeQuarantineGrowth` releases one 64-byte array 100000 times and prints the bump of the arena.

| Build | x86-64-linux | arm64-linux under QEMU |
|---|---|---|
| release | 152 bytes, one block recycled | 152 bytes |
| `-sanitize` | 8800064 bytes, 88 per release | 8800064 bytes |

A 64-byte payload is an 88-byte box with its three header words.
The test pins both numbers, so a bounded quarantine moves the second row and a regression of the free list moves the first.

## Policy options

The acceptance asks for a bounded quarantine, randomized placement, sampled guard pages, and tests of the configurable overhead.
Each option has a cost that a policy must set, and none is implemented until the owner approves one.

| Option | Shape | Cost | What it loses |
|---|---|---|---|
| Bounded quarantine | A byte budget per process. A release above the budget evicts the oldest block to its free list after a poison check | A queue of block addresses and a counter per release | A use after free older than the budget is a silent reuse again |
| Randomized placement | A free list that hands out one of its first N blocks at random, seeded by `getrandom` at startup | One random word per allocation, and the entropy read | Predictable reuse, which is what a heap-spray needs |
| Sampled guard pages | Every k-th large block gets a `PROT_NONE` page after it | An `mmap` and an `mprotect` per sample, and a page per sampled block | Nothing in the release build when k is large |
| Release-build quarantine | The bounded quarantine on by default | The budget in memory for every program | The exact-reuse behaviour the leak census and the allocation lane pin today |

The first three options compose.
A policy needs three numbers: the quarantine budget, the placement window, and the sampling rate.
It also needs the rule for release builds, because the perf lane pins the allocator's cost today.

## Remaining limits

- No option is implemented. The quarantine stays unbounded under `-sanitize` and absent in the release build.
- The budget, the window, and the rate are the owner's decision, and the record above is the proposal.
- `-heap-limit` bounds the arena, so a quarantined program under a limit exits 125 when the quarantine fills the arena ([ERASURE-AND-QUOTAS.md](ERASURE-AND-QUOTAS.md)).
