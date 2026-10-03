# A small freelist hit allocates without a frame

The x86-64 `__fern_alloc` that `asm_ir.fern` emits set up a frame and saved
`%rcx` and `%rdx` on every call, then derived the class, then popped the
freelist. On a stage-2 compile most calls end at that pop, so the frame and
the two saves were most of the work.

Below 257 words a size class is its word count, so the rounded byte count is
already the byte offset of the class's freelist head. The runtime now tests
the rounded size against 2048 bytes and, when it fits and the head is
non-empty, pops it with `%rax` and `%rdi` alone and returns. Both are the
registers the allocator's contract already clobbers. A miss subtracts the
freelist base back out of `%rdi` and falls into the full path unchanged; a
larger request skips straight to it. The alloc counter and the heap-event
wrapper sit ahead of the split, so both still see every call.

arm64 is unchanged. Its small hit already works in the four scratch registers
the contract clobbers and saves only the `x29`/`x30` pair; there is no
instruction counter under qemu here to measure moving that pair.

Stage-2 (self-host-built) x86-64 compilers, compiling `checker.fern` to a
binary, under callgrind, on main 7ddc44f9:

| | Ir |
|---|--:|
| before | 26.832 G |
| after | 26.495 G (−1.26%) |

`__fern_alloc` itself falls from 865 M to 528 M self cost.
