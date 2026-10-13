# 2026-10-13 — the array box pops its own class

`asm_ir.emit_ir_runtime` and `asm_arm64_ir`: `__fern_arr_box`. Refs #8171.
Emitted bytes change in the runtime helper only.

## The shape

`__fern_arr_box` is the shim behind nearly every allocation: a record, a
variant, an array literal, an append's growth. On the stage-2 compile of
`checker.fern` it runs 21.2 M times, and each time it pushed its capacity,
called `__fern_alloc`, and popped it back: a push, a call, a return and a
pop around an eleven-instruction fast path that rounds a byte count the shim
had just produced as a multiple of eight.

## What changed

The shim pops the small-class freelist itself, in the two registers it may
touch (`%rax` and `%rdi`; `x0`–`x3` on arm64, which every caller sets or
reloads after the call), and calls the allocator only on an empty class or
for a block over 256 words. Under the heap-event diagnostics or the
allocation counter the shim still calls, since the allocator is where those
hooks live. On arm64 the shim had a frame of its own around the call, and
the allocator another; the fast path has neither.

## Measured

The stage-2 compile of `checker.fern` to a binary under callgrind, each
compiler built from its own tree by itself, both compiling one frozen copy of
main at 6fdb1671, warm certifier memo. The base is the first-append entry
before this one.

| | Ir |
|---|--:|
| the first append pops its own block | 14.413 G |
| the shim pops its class | 14.370 G (−0.30%) |

`TestSelfHostAllocFastPath` now pins the shim's fast path as frameless and
call-free, with the allocator call kept for a miss.

## The machine

The array-box text machine (`compiler/ssaarrayboxmachine.fern`, run by
`TestSelfHostRetainedNativeBodies`) followed one straight path through one
allocator call. It now runs each capacity twice, with the class's slot
holding a recycled block and holding zero, carries the zero and carry flags
of an unsigned compare or a self test, decides the two branches from them,
and knows the list base, the selected slot, the recycled block and its next
head as values of their own. The pop path derives the same box header and
`AX=B+16` from the recycled block, the next head moved into the slot and
left at `S-16`, and no allocator call; the empty slot derives what the
machine derived before. Its decoder reads the index-only `leaq`, the
free-list `leaq`, `testq`, and a memory `pushq`/`popq`. The controls that
mutated the old `addq $3`/`shlq $3` request now mutate the `leaq`, and a new
group refuses a bound either capacity takes the call for, a flag the compare
did not set, a stale next head, a slot written twice or through the wrong
register, and the list base reached any other way.
