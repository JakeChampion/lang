# Allocation core audit

Status: [reference] audit record for [SEC-022](https://github.com/JakeChampion/lang/issues/13524) and the metadata part of [SEC-023](https://github.com/JakeChampion/lang/issues/13525), with the facts [SEC-025](https://github.com/JakeChampion/lang/issues/13527) starts from.
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).

The record reads the native allocator as it stands: the arena, the size classes, the free lists, the size checks at each entry, and the failure paths.
It names the tests that pin each rule and the gaps.
It changes two things, which the last section records.

## The arena

`__fern_alloc` maps one 16 GiB arena on its first call, with `MAP_NORESERVE`, so the kernel backs a page on first touch.
A failed map prints `fern: could not map the heap arena (errno N)` and exits 123.
The bump pointer advances through the arena, and a request that reaches the end prints `fern: out of memory (heap arena exhausted)` and exits 125.
`docs/LOCAL-DEV-LOOP.md` records why those two statuses are distinct from a kernel kill, and `TestArenaExhaustedExitCodeIsNot137` and `TestArenaExhaustedExitCodeSelfHostLockstep` pin them on both backends.
Wasm grows linear memory instead, and `TestWASMHeapExhaustionTrapsInTheAllocator` and `TestWasmAbortOutOfMemory` pin its trap.

## Size classes and free lists

A request is rounded up to 8 bytes.
A block of at most 2 KiB takes its exact class from a direct-mapped free list.
A block of at most 512 KiB takes a class rounded by `__fern_capw`, from the same list.
A larger block takes one of four sub-classes per power of two from the large free list.
The class math derives the index from the two leading bits of the rounded size.

The free list link is the first word of the freed block.
For a typed box that word is the capacity field of the header, 16 bytes before the payload.
For a raw block from `__alloc` it is the first payload word.
A byte array is zero-filled on allocation (`rep stosq`), so a fresh block never carries a previous owner's bytes.
A freed block is not zeroed, and only its first word changes.

## Metadata and misuse

A typed box carries its capacity, its reference count and its length in the 24 bytes before and at the payload start.
Safe code reaches the payload through checked indexing, so it cannot write the header of its own box or the header of the next block.
The raw floor, `__store_*` and `__free`, can.

A second release of a typed box reads a reference count of zero.
The release helper then counts an underflow and returns without a second free, so the block is not linked to itself.
`__rc_underflow_count()` reports the count, and `TestX86_64MapHandleOverReleaseCounted` pins it.
A dangling release after the block was recycled reads the new owner's count and frees the new owner's block.
Only `-sanitize` detects that, through its quarantine, and `docs/SANITIZER.md` records the two reports.

A raw free of an address outside the arena linked that address into a free list, so a later allocation of that class returned it.
An unmapped address ended the program by signal, and a mapped one gave the caller a block on the stack or in the data segment.
`__free` now compares the address with the arena bounds and aborts with `fern: release of memory outside the heap`, status 134.
`TestSelfHostRawFreeOutsideHeap` pins the abort and the control on both targets.

A write through a dangling raw pointer to the header word of a freed box forges the free list link.
The next allocation of that class takes the freed box, and the one after it returns the forged address.
A probe that wrote a non-address there ended by signal at that allocation.
A forged address inside a mapping comes back without a report.

A safe-linking encoding of the link, keyed by the process canary and checked for alignment and arena bounds on every pop, closes that path.
That encoding touches every free list push and pop on both backends.
It is a `-harden` design of its own and is not in this record.

## Size checks at each entry

| Entry | Check |
|---|---|
| `__alloc_u8`, `__alloc_bytes`, a string growth | a negative count aborts with `fern: allocation size out of range`, status 134 |
| `arr_reserve` and the array push slow path | a capacity above the representable bound aborts the same way |
| string concatenation and `repeat` | a combined length that wraps aborts the same way (`TestAllocSizeOverflowAborts`, `TestAllocSizeWrappingToZeroStillAborts`); the native byte-copy capacity bound is #13647 |
| `__raw_alloc`, the runtime's own entry | not reachable from a program, because the checker refuses the name; a negative count aborts with the size cause |
| `__fern_alloc`, the bump | a sum that wraps the address space now aborts with the size cause, which only a corrupted length can reach; a request past the arena end exits 125 |

## What the tests pin

| Rule | Test |
|---|---|
| Negative and wrapped counts abort before allocation | `TestAllocSizeOverflowAborts`, `TestAllocSizeWrappingToZeroStillAborts`, `TestSelfHostAbortReports*` (`alloc_size_negative`), `TestSelfHostAllocSizeAbortIR*` |
| A representable large allocation succeeds | `TestLargeButRepresentableAllocationStillWorks` |
| The arena's exit statuses and their lockstep on both backends | `TestArenaExhaustedExitCodeIsNot137`, `TestArenaExhaustedExitCodeSelfHostLockstep`, `TestX86_64ArenaCheckpoint`, `TestArm64ArenaCheckpoint` |
| A freed large block is a donor for the next request of its class | `TestSelfHostAllocReuseLargeDonor` |
| A freed block above one gigabyte is reused, on both backends | `TestSelfHostAllocReuseAboveOneGigabyte` |
| A request whose bump wraps the address space aborts with the size cause, on both backends; the probe calls the allocator from patched assembly, because no checked entry passes such a size | `TestSelfHostAllocRefusesWrappedBump` |
| Every rc corpus case reclaims everything on three targets | `TestX86_64RcCorpusLeakGate`, `TestArm64RcCorpusLeakGate`, `TestWASMRcCorpusLeakGate` |
| Over-release, use after free, and leaks under `-sanitize` | `docs/SANITIZER.md` |

## Gaps

| Gap | Consequence |
|---|---|
| Free list link inside the freed block | A raw write through a dangling pointer to that word forges the next link, and the allocation after the next returns the forged address; `-sanitize` quarantines freed blocks and detects the touch, the release build does not (SEC-023) |
| A raw double free | `__free` twice on one block links it to itself, because a raw block has no count to read; the typed release helpers count the second release instead (SEC-023) |
| No zeroing on free | A secret stays in the arena until the block is reused and zero-filled. `crypto.erase` zeroes an owned byte array before its release, and `-heap-limit` bounds the arena ([ERASURE-AND-QUOTAS.md](ERASURE-AND-QUOTAS.md)) |
| A runtime helper that computes a size | The helper is responsible for its arithmetic; the bump's wrap check is the last line, and a wrapped positive size below the arena end is not caught there |
| Alignment above 8 bytes | No entry takes an alignment; lane values and SIMD kernels use unaligned loads and stores by design |
| `munmap` | The arena is never unmapped; a freed block returns to a list, never to the kernel |

## Changes in this record

`__free` of an address outside the arena linked that address into a free list.
It now aborts with the out-of-heap cause before it touches the address, on both backends.

A freed block above one gigabyte was dropped at the free: both the lookup and the free push stopped at that bound, so the block was lost for the life of the process.
A program that allocated a 1.1 GB buffer twice advanced the bump by 2.2 GB on both targets.
Twenty such allocations exhausted the host before the arena.
The bound is now the arena size, so the class math, which already covered that range, reaches every block the arena can hold.

The bump added the request to the heap pointer and compared the sum with the arena end.
A request large enough to wrap the sum passed that comparison.
Both backends now abort with the allocation size cause when the sum wraps.
Before the change, the wrapped-bump probe ended with exit status 55 on x86-64, and the arm64 probe did not end within 15 minutes under QEMU.
No program reaches that path through a checked entry, and every runtime entry refuses a negative count before the bump.
The check guards against a corrupted length that reaches the bump through the runtime's own arithmetic.

## Commands

```
go test ./internal/testing/e2ecompiler -run 'TestSelfHostAllocReuseAboveOneGigabyte$|TestSelfHostAllocRefusesWrappedBump$|TestSelfHostAllocReuseLargeDonor$'
go test ./internal/testing/e2e -run 'TestAllocSize|TestLargeButRepresentable|ArenaCheckpoint|TestArenaExhausted|RcCorpusLeakGate'
```

Host: Linux 6.18.44-fc-v165, Intel(R) Xeon(R) Processor @ 2.80GHz, Go go1.27.3, QEMU 8.2.2, wasmtime 46.0.1.
