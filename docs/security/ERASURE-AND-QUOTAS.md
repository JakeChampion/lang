# Initialization, erasure, and heap quotas

Status: partial implementation for [SEC-025](https://github.com/JakeChampion/lang/issues/13527).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).
The allocator itself is recorded in [ALLOCATOR-AUDIT.md](ALLOCATOR-AUDIT.md).

## Initialization

A program can observe these rules about fresh memory on both native targets.

| Memory | Rule | Evidence |
|---|---|---|
| A typed array box from `__alloc_u8`, `__alloc_i64`, a literal, or a push | Every payload word is zero at allocation, so a fresh array never carries a previous owner's bytes | `rep stosq` in `__fern_alloc_u8` and `__fern_alloc_bytes`, and the arm64 store loop; a push copies its source |
| A struct | Every field is written by the literal that builds it, because the checker refuses a missing field | `E005` |
| A raw block from `__alloc` | Not filled. It can carry the bytes of a released block of the same size class | The control of `TestSelfHostEraseZeroesStorage` reads 48 secret bytes back through `__alloc(72)` |
| A released block | Not zeroed. Only its first word changes, to the free list link | [ALLOCATOR-AUDIT.md](ALLOCATOR-AUDIT.md) |
| A stack frame | Spill slots are not zeroed when a frame is released, and the next frame overwrites them as it uses them | the prologue stores no fill |

A secret therefore stays in the arena from the release of its array until the allocator hands the block out again.
Safe code cannot read it, because a fresh typed box is zero-filled before the program sees it.
The raw floor can, and so can a core dump, a debugger, or a memory disclosure bug.

## Erasure

`std/crypto` gains `erase(own bytes: u8[]): void`.
It takes the owned array and writes zero into every element with `with`, which stores in place when no other reference shares the array.
The block therefore holds zeros when it returns to the allocator.
The checker accepts an owned argument only at its last use, so a program cannot read the array after the call.

The stores cannot be elided, because no pass of the compiler removes a store to an array.
`TestSelfHostEraseZeroesStorage` pins that on both targets.
Its control releases a 48-byte secret without erasure and reads at least 48 secret bytes back through eight raw allocations of the released class.
Its erase path must read none.

`erase` covers one array.
It does not erase a copy that the program made before the call.
It does not erase a register or a spill slot that held a word of the secret, or a kernel buffer that carried it.
A process that must not leave a secret in a core dump calls `disable_core_dumps`.

## Heap quotas

`-heap-limit SIZE` sets the arena reservation of a native executable.
The size is digits with an optional `K`, `M` or `G` suffix, from `1M` to `16G`, and the default stays `16G`.
The reservation is the quota: when the bump pointer reaches its end, the runtime prints `fern: out of memory (heap arena exhausted)` and exits 125, as it does at the default size.
The flag changes only the reservation, so the size classes, the free lists, and the box ceiling are unchanged.

The flag is refused for a target without the native arena, and a size outside the range or with other text is refused.
Each refusal prints its reason and exits 2.
Wasm grows linear memory instead, and its trap is unchanged.

The quota bounds the arena alone.
The 1 GiB stack reservation, the signal stack, the kernel's own buffers, and memory that a foreign library maps are outside it.

## Verification

- `TestSelfHostEraseZeroesStorage` compiles one program for x86-64-linux and arm64-linux. The control run must read at least 48 secret bytes back, and the erase run must read 0. Both targets pass, the arm64 one under QEMU.
- `TestSelfHostHeapLimit` keeps 256 one-megabyte arrays live. Under `-heap-limit 64M` the program must exit 125 with the cause after its 32nd array and before its 64th, on both targets. Without the flag every array fits. Seven refusals cover `0`, `512K`, `32G`, a 20-digit size, `64MB`, a bare `M`, and `wasm32-wasi`. Five accepted sizes cover both ends of the range in bytes and with suffixes.
- The `std/crypto` suite calls `erase` on a digest, on an empty array, and on a literal. It passes natively on x86-64, on arm64 under QEMU, and under the Go oracle.

```sh
go test ./internal/testing/e2ecompiler -run 'TestSelfHostEraseZeroesStorage|TestSelfHostHeapLimit' -count=1 -timeout 0
```

## Remaining limits

- `erase` exists for `u8[]` alone. A secret held in another element type, in a string, or in a struct field has no erasure call.
- A quota per allocation site, per package, or per request is not defined. The arena quota is one number per executable.
- The sanitizer's quarantine keeps every released block for the life of the process, so under `-sanitize` an erased block stays mapped. That is SEC-024.
- The Go oracle and the self-host interpreter run `erase` on their own heaps and ignore `-heap-limit`, which is a compile flag of the native backends.
