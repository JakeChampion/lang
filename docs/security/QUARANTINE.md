# Quarantine, placement, and guard pages

Status: implemented for [SEC-024](https://github.com/JakeChampion/lang/issues/13526) under the policy the owner chose on 2026-10-28.
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).
The allocator is recorded in [ALLOCATOR-AUDIT.md](ALLOCATOR-AUDIT.md) and the sanitizer in `docs/SANITIZER.md`.

## The policy

The owner chose all three options, with these numbers:

| Mechanism | Build | Number |
|---|---|---|
| Bounded quarantine | `-sanitize` | a byte budget, 64 MiB unless `-quarantine SIZE` gives another, `0` for no bound |
| Randomized placement | `-harden` | a free list hands out one of its first eight blocks |
| Sampled guard pages | `-harden` on Linux | every 64th large block the bump hands out gets a page after it that the process cannot touch |

The release build is unchanged. Its allocator recycles a released block at once, hands out the head of a free list, and maps no guard page.
The leak census, the allocation lane and the perf lane pin that behaviour, and this change moves none of their rows.

## The quarantine

Under `-sanitize` a released block does not return to a free list at once.
Its count word becomes the poison value, and every count operation runs through a helper that checks the poison.
A touch reports `fern-sanitizer: use-after-free` with exit 124, as before.

The block then joins a FIFO that is threaded through the released blocks themselves.
Word 2 of the block links the next block.
The count word keeps its poison.
The other of words 0 and 1 holds the block's size in words.
A block with no count word, such as the bytes of a string, gets the poison in word 0 when it enters.
Three counters hold the head, the tail and the bytes in the queue.

A release that takes the queue past its budget evicts the oldest block until the queue fits.
Before the evicted block returns to its free list, the helper reads its poison word again.
A block whose poison is gone was written after its release, and the eviction reports `fern-sanitizer: write-after-free (a quarantined block was written)` with exit 124.
A large block, 512 KiB and up, returns to the large free list through the same path.
A raw block below three words cannot carry the link. It stays out of the queue, as every released block did before.

`-quarantine SIZE` sets the budget from 1M to 16G with a K, M or G suffix.
`-quarantine 0` removes the bound.
The flag needs `-sanitize`, and the build manifest carries the budget as `quarantine=`.

The accounting for the leak census happens at the release, as before.
An eviction moves a block the census has already counted as freed.

## Randomized placement

Under `-harden` the startup reads 16 bytes from `getrandom`: eight for the stack canary and eight for the placement seed.
A read of fewer bytes is the same startup failure as a short canary read.

Every take from a small or large free list goes through `__fern_freelist_take`.
It steps a xorshift generator over the seed, walks up to seven links from the list head, and unlinks the block it lands on.
The cost is one generator step and at most eight loads per allocation that a free list serves.
A bump allocation pays nothing.

## Sampled guard pages

Under `-harden` on Linux the bump counts the large blocks it hands out, 512 KiB and up.
Every 64th block starts on a page boundary and ends on one, because every large class is a multiple of 128 KiB.
The bump skips the page after the block and makes it `PROT_NONE` with `mprotect`.
A read or write one byte past the block faults.
A failed `mprotect` leaves the block without its guard, and the bump moves on.

The arm64 Darwin target does not map guard pages.
Its `-harden` keeps the canary, the indirect-call check, the signed return addresses and the landing pads.

## Measured

`TestSelfHostSanitizeQuarantineGrowth` releases one 64-byte array 100000 times and prints the bump of the arena.

| Build | x86-64-linux | arm64-linux under QEMU |
|---|---|---|
| release | 152 bytes, one block recycled | 152 bytes |
| `-sanitize`, the 64 MiB default | 8800064 bytes, 88 per release | 8800064 bytes |
| `-sanitize -quarantine 0` | 8800064 bytes | 8800064 bytes |
| `-sanitize -quarantine 1M` | 1048672 bytes, the budget plus one block | 1048672 bytes |

`TestSelfHostQuarantinePolicy` runs on both targets:

- `write-after-free`: a raw block is released and then written. The next 40000 releases under `-quarantine 1M` evict it, and the run exits 124 with the report. The release build prints `survived`.
- `placement`: 16 blocks of one class are released and taken again 32 times. The release build takes the last released block first on all 32 rounds. The `-harden` build did so on 4 rounds on x86-64 and 2 on arm64 in one run, and the test requires fewer than 32.
- `guard-page`: 70 one-MiB raw blocks come from the bump. Under `-harden` a read one byte past the 64th block faults, and the same read past the 63rd and the 65th does not. The release build reads past the 64th.
- `string-blocks`: 64 rounds of a split, a join and a grown string release every string shape under `-sanitize`, `-sanitize -quarantine 1M` and `-harden`. The census balances, 1218 allocations and 1218 frees.
- `large-growth`: an i32 array and a u8 array grow past the large tier under `-sanitize`, `-harden` and the release build. The push-owned helpers return the grown array and release the old block, and the census balances.
- `large-churn`: 200 one-MiB strings are built and released under `-sanitize -quarantine 4M`. The arena grows by 5242960 bytes and the census balances, 204 allocations and 204 frees.

`TestSelfHostQuarantineFlag` pins the refusals: `-quarantine` without `-sanitize`, and a size below 1M, above 16G or that is not a size.

## Remaining limits

- A use after free older than the budget is a silent reuse again. The budget is the trade.
- The write-after-free check reads one word. A write that puts the poison value back, or that touches only the block's payload, is not seen.
- The placement window is eight. A heap spray that fills a class with more than eight blocks still gets predictable reuse past the window.
- The guard sample is one block in 64. A write past the end of the other 63 lands in the next block.
- `-heap-limit` bounds the arena, so a quarantined program under a limit exits 125 when the quarantine fills the arena ([ERASURE-AND-QUOTAS.md](ERASURE-AND-QUOTAS.md)). The guard pages count against the arena as well.
