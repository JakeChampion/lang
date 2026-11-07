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
`__free` now compares the address with the arena bounds and aborts with `fern: release of memory outside the heap`, status 134. A worker child keeps the bounds of the parent's image, and a raw free of a block in the image does nothing: the kernel reclaims the image at exit (`docs/WORKERS.md`).
`TestSelfHostRawFreeOutsideHeap` pins the abort and the control on both targets.

A raw block has no count. A second `__free` of it linked the block to itself, and the next two allocations of its class returned one address.
`__free` now compares the block with the head of its free list before the push, on the small lists and on the large lists.
A block that is the head aborts with `fern: release of memory that is already free`, status 134.

The check costs one load and one compare on the raw free path. The typed releases keep their count and do not pay it.
`TestSelfHostRawDoubleFree` pins the abort and a control that frees two blocks of one class in turn, on both targets.

The head compare does not see a second free when a free of another block of the class comes between the two.
Under `-harden` the raw free walks the first 64 blocks of its list instead, and a match aborts with the same cause.
The walk decodes each link with the check that `__fern_freelist_take` makes, so a corrupt link on the way aborts as a corrupt link.
A large free walks its list the same way, whether the block is raw or typed.
The release build keeps the head compare.
`TestSelfHostHardenRawInterleavedDoubleFree` pins the abort after three frees of one class, for a small class and a large class on both targets.

The WebAssembly runtime makes the same two checks in `$__fern_raw_free`.
The two interpreters keep a table of the raw blocks a program took, and a release is checked against that table.
The `raw_free_outside_heap`, `raw_double_free` and `raw_double_free_large` rows of the abort report suite pin the cause line on every target and in both interpreters.

Under `-sanitize` the raw free quarantines the block instead.
A second raw free of a block in the quarantine aborts with the double-free cause, with or without other frees between the two.
The free compares the first word of the block with the quarantine's poison word, and on a match it walks the quarantine list.
So a live block whose first word holds the poison value is still freed.
A block that the quarantine has already evicted to a free list is not found.

A block of one or two words has no room for the quarantine's three words, so the free keeps it out of every list.
A second free of such a block is not found, and the block is not used again.
`__fern_box_free` releases small typed boxes through the same path.
The poison in the count word of such a box reports a later use, so the free cannot give the block to its free list.

Before this change, a second raw free quarantined the block again without a report, and the leak census showed one more free than allocations.

A write through a dangling raw pointer to the header word of a freed box forges the free list link.
In the release build the next allocation of that class takes the freed box, and the one after it returns the forged address.
A probe that wrote a non-address there ended by signal at that allocation.
A forged address inside a mapping comes back without a report.

Under `-harden` the link inside a free block is encoded: the stored word is the next address XOR the process canary XOR the block's page number.
The list heads in `__fern_freelist` and `__fern_large_freelist` stay plain.
Every push encodes the link with the key of the block it lands in, and `__fern_freelist_take` decodes each link it follows.
A decoded link must be zero, or 8-byte aligned and inside the arena. Any other value aborts with `fern: corrupt free list link`, status 134.

A forged word therefore aborts at the next allocation of its class, and a link copied from another block fails too.
The cost is one load of the canary and three logical operations per push and per link followed. The release build keeps plain links.
`TestSelfHostFreeListLinkForgery` pins the abort for a small class and a large class on both targets.
`TestSelfHostHardenFreeListRoundTrip` recycles twelve blocks of each class through the encoded lists.

The take under `-harden` writes zero over the link word of the block it hands out.
Before that change, a raw allocation from a free list read the encoded link.
The link of the last block of a list is the key itself.
With the key, a program can forge a link that decodes to a valid address.
The encoding therefore did not hold against the raw floor.
`TestSelfHostHardenScrubsReleasedBlocks` pins a zero link word on both targets.

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
| A second raw free of the same block in a row aborts, small and large, on both backends | `TestSelfHostRawDoubleFree`, `TestSelfHostAbortReports*` (`raw_double_free`, `raw_double_free_large`) |
| Under `-harden` a second raw free after other frees of the class aborts, small and large, on both backends | `TestSelfHostHardenRawInterleavedDoubleFree` |
| Under `-sanitize` a second raw free of a quarantined block aborts, small and large, on both backends, and a live block that holds the poison value frees | `TestSelfHostSanitizeRawDoubleFree` |
| Under `-harden` a released block holds zeros and a taken block has no link word, small and large, on both backends | `TestSelfHostHardenScrubsReleasedBlocks` |
| A freed block above one gigabyte is reused, on both backends | `TestSelfHostAllocReuseAboveOneGigabyte` |
| A request whose bump wraps the address space aborts with the size cause, on both backends; the probe calls the allocator from patched assembly, because no checked entry passes such a size | `TestSelfHostAllocRefusesWrappedBump` |
| Every rc corpus case reclaims everything on three targets | `TestX86_64RcCorpusLeakGate`, `TestArm64RcCorpusLeakGate`, `TestWASMRcCorpusLeakGate` |
| Over-release, use after free, and leaks under `-sanitize` | `docs/SANITIZER.md` |

## Gaps

| Gap | Consequence |
|---|---|
| Free list link inside the freed block | In the release build a raw write through a dangling pointer to that word forges the next link, and the allocation after the next returns the forged address. `-harden` encodes the link and aborts on a bad one, and `-sanitize` quarantines freed blocks and detects the touch (SEC-023) |
| A raw double free with another free of the same class between the two | In the release build `__free` compares the block with the head of its list alone, so a free of another block of the class between the two frees hides the second. `-harden` walks the first 64 blocks of the list, so only a second free with 64 or more frees of the class between them links the block twice. `-sanitize` aborts while the block is in its quarantine (SEC-023) |
| No zeroing on free in the release build | A secret stays in the arena until the block is reused and zero-filled. `-harden` zeroes each block before its push and clears the link word at the take. `crypto.erase` zeroes an owned byte array before its release, and `-heap-limit` bounds the arena ([ERASURE-AND-QUOTAS.md](ERASURE-AND-QUOTAS.md)) |
| A runtime helper that computes a size | The helper is responsible for its arithmetic; the bump's wrap check is the last line, and a wrapped positive size below the arena end is not caught there |
| Alignment above 8 bytes | No entry takes an alignment; lane values and SIMD kernels use unaligned loads and stores by design |
| `munmap` | The arena is never unmapped; a freed block returns to a list, never to the kernel |

## Changes in this record

`__free` of an address outside the arena linked that address into a free list.
It now aborts with the out-of-heap cause before it touches the address, on both backends.

A freed block above one gigabyte was dropped at the free: both the lookup and the free push stopped at that bound, so the block was lost for the life of the process.
A program that allocated a 1.1 GB buffer twice advanced the bump by 2.2 GB on both targets.
Twenty such allocations exhausted the host before the arena.

With the lists in reach, a block above one gigabyte rounds up to its size class like every large block.
A 1.2 GB request therefore bumps 1342177280 bytes, and `TestX86_64HeapBumpAbove2GiB` pins that.
The bound is now the arena size, so the class math, which already covered that range, reaches every block the arena can hold.

The bump added the request to the heap pointer and compared the sum with the arena end.
A request large enough to wrap the sum passed that comparison.
Both backends now abort with the allocation size cause when the sum wraps.
Before the change, the wrapped-bump probe ended with exit status 55 on x86-64, and the arm64 probe did not end within 15 minutes under QEMU.
No program reaches that path through a checked entry, and every runtime entry refuses a negative count before the bump.
The check guards against a corrupted length that reaches the bump through the runtime's own arithmetic.

The free list link inside a released block was plain on every build.
Under `-harden` it is now encoded with the process canary and the block's page. A decoded link that is not aligned or not inside the arena aborts.
The forgery probe ended by signal before the change, and ends with the corrupt-link cause after it.

## Commands

```
go test ./internal/testing/e2ecompiler -run 'TestSelfHostAllocReuseAboveOneGigabyte$|TestSelfHostAllocRefusesWrappedBump$|TestSelfHostAllocReuseLargeDonor$'
go test ./internal/testing/e2ecompiler -run 'TestSelfHostRawFreeOutsideHeap$|TestSelfHostRawDoubleFree$|TestSelfHostFreeListLinkForgery$|TestSelfHostHardenFreeListRoundTrip$'
go test ./internal/testing/e2e -run 'TestAllocSize|TestLargeButRepresentable|ArenaCheckpoint|TestArenaExhausted|RcCorpusLeakGate'
```

Host: Linux 6.18.44-fc-v165, Intel(R) Xeon(R) Processor @ 2.80GHz, Go go1.27.3, QEMU 8.2.2, wasmtime 46.0.1.
