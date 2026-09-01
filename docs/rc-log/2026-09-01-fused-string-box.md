# One block per heap string: fusing the self-host's string box into its buffer

#7351 measured the self-host allocating **exactly twice** what native did for
every heap string, on both register backends, and named two independent causes.
This entry is the second one closed. Nothing gated either, which is why the
issue exists at all: the leak matrix classifies a cell clean-or-leak and is
layout-free by design, so 200 allocs against 200 frees at `live_bytes 0` reads
`clean` no matter what the other compiler did with the same source.

## The shape

A self-host heap string was **two** allocations:

    __raw_alloc(n)  ->  [ bytes … ]                    the data buffer
    __raw_string    ->  [ rc | data | len ]            a fresh 24-byte box

Native's x86-64 string is one word pointing at the bytes with the length in
front of them, so the same value costs it one block. wasm has been at one block
the whole time — `$__fern_str_box` there is a single inline `[rc][bsz][len][bytes]`
— so this was a register-backend divergence from the project's own third
backend, not a design the compiler had settled on.

## The fix

`__raw_alloc(n)` now allocates `n + 24` and returns `base + 24`. The pointer
points PAST a reserved string-box header, so a buffer that later becomes a
string already has the box's three words in front of it, and `__raw_string`
stamps them in place instead of allocating:

    base      base+8    base+16   base+24 = data = box+16
    [ rc | fused ][ data ][ len ][ bytes … ]
                  ^ box = base+8

`data@box+0` and `len@box+8` are exactly where they were, so no string CONSUMER
moved — len, index, slice, concat, eq, cmp, split, print are all untouched.

Two constructors now exist and `__fern_str_free` has to tell them apart, because
the separate-buffer form is still needed for data the box does not own the block
of (argv entries, the strbuf drain, the subprocess pipe buffers):

- `__fern_str_box(data, len)` — allocates the 24-byte box, marker 0.
- `__fern_str_box_hdr(data, len)` — the fused form, allocates nothing, marker set.

`fused` is a 32-bit word at `box-4`, in the four bytes the rc word's block has
always had spare (rc is `movl -8(ptr)`, and the allocating form's 8-byte rc
store zeroes them). It holds a magic value, `0x734F800D`, not 1.

Two weaker tests were rejected for the same reason. A **positional** one —
`data == box + 16` — reads as fused whenever the allocator happens to hand a
separate buffer the block immediately after its box. A **1** reads as fused
whenever a recycled block's spare word still holds one. Neither misread is a
fault: the blocks involved are contiguous, so freeing them as one never
overruns. What happens instead is that the block is returned to the freelist
ONCE where it was taken twice — a phantom leak, standing in front of whatever
real defect produced the stale read. A magic word makes the test say what it
means.

`__fern_str_free` at `rc == 1` therefore branches on that word: fused returns
ONE block of `3 + ceil(len/8)` words at `base = box-8`; non-fused keeps the
heap-range-guarded buffer free plus the class-3 box free it always did. The
immortal (`rc < 0`) forms — literals, `s[a:b]` views, the frame-form box, the
`.data` const-aggregates — return before either, unchanged.

`len` is the LOGICAL length, so a producer that over-allocated (`read_all_stdin`
reserves 32 MiB for what may be a short read) hands the freelist a block bigger
than the class it lands in. That wastes the slack and never overruns, and it is
the same trade the separate-buffer path has always made by freeing the data
buffer at `ceil(len/8)`.

## Measured

100 rounds, `FERN_LEAKCHECK=1` at compile time, blocks per round:

| probe | native x86-64 | self-host before | self-host after |
|---|---|---|---|
| `struct N { n: i32 }` | 1 | 1 | 1 |
| `var xs: i32[] = [i, i+1]` | 1 | 1 | 1 |
| `struct A { xs: i32[] }` | 2 | 2 | 2 |
| 21-char concat result | 1 | **2** | **1** |
| `struct S { s: string }`, 21-char | 2 | **3** | **2** |
| `struct P { xs: i32[], s: string }` | 3 | **4** | **3** |
| 2-char concat result | **0** | **2** | **1** |
| `struct S { s: string }`, 2-char | **1** | **3** | **2** |

arm64 self-host matches arm64 native exactly on all of these, including the two
short-string rows: the arm64 native string does not keep this shape inline
either, so the remaining divergence is against **x86-64 native only**.

## What is left of #7351

Effect 1, the inline form. Native x86-64 keeps a string of 7 bytes or fewer in
the value word (`ssoTagBit`, single-word LSB-tagged) and allocates nothing; the
self-host has no inline form at all and allocates one block for every string
however short. That is an ABI change of the same size as the native flip
`docs/SSO-NATIVE-FLIP-STATUS.md` records — every string op needs an inline arm
— and it is the ONLY row where the two compilers still disagree.

## The gate

`TestSelfHostAllocCountMatrixX86_64` +
`internal/e2eselfhost/testdata/selfhost-alloc-count-matrix.txt`: the same corpus
compiled by both compilers, pinning blocks-per-round for each side. Counts, not
bytes — one block per array, one per struct box, one per heap string is a
property of the value graph, so it survives layout and capacity changes where a
byte total does not. `TestX86_64AllocScaling` bounds a ratio INSIDE one
compiler and cannot see a constant factor between two; this is the missing half.
The two SSO rows are the file's only disagreement, each with the reason in its
note.

## The x86-64 whole-compiler self-compile is not a control here

`gen2` — the compiler compiled by the self-host-built compiler — dies on this
16 GB box, on `main` and with this change alike, and the two die differently:
across the conformance corpus every disagreement between them is one failing
case where BOTH fail (125 arena-exhausted / 137 OOM-killed / 139 SIGSEGV, in
either order), and there is no case `main`'s gen2 compiles that this one does
not. Changing how much a string allocates moves which wall it hits first, and
nothing more; the same holds for the arm64 `self` case behind
`FERN_STAGE2_SELF=1`, which is OOM-killed under qemu either way.

Worth recording because the first reading of that SIGSEGV was "the fused free
path corrupts the heap". It does not — but the route to knowing is not the
symptom. `gen2` built from `-emit asm` through gcc gives a SYMBOLISED binary of
the same code, and gdb then names the frame in one step
(`irlower.bytes_at` ← `tagged_value_start` ← `tagged_value_of`, reading a
`reclaimable_names` element whose data word is 1). Reach for that before
theorising about a stripped address.

The gates that DO speak for this change are `internal/e2eselfhost` and, for the
whole compiler, `TestSelfHostPerModuleEmitAllFixpointX86_64`,
`TestSelfHostStage2FixpointArm64`, `TestSelfHostStage2Compiler`,
`TestSelfHostStage2Bootstrap` and `TestSelfHostHeapBumpFixpointX86_64` — all
green.

## The trap this set, and the assembler bug under it

The first version of the free-side branch was one instruction, `cmpl $1,
-4(%rax)`, and it silently never fired. `x86_gas_alu32` — the GAS front end for
addl/subl/andl/orl/xorl/cmpl — passes both operands through `x86_gas_reg32`,
which answers **-1** for anything that is not a 32-bit register name, and -1
encodes as a perfectly well-formed instruction naming the wrong register. The
memory operand assembled as `cmp $1, %eax`. That violates the file's own SH-005
contract ("anything not recognised is RECORDED, not dropped — a dropped
instruction is a corrupt byte stream") for the whole 32-bit ALU group; every
other operand shape in that assembler refuses.

Nothing in the tree emitted such an operand before, so the bug had never fired.
It now refuses, with checks 41-46 of `x86GasSelfTestMain` pinning both halves
(three memory shapes recorded and encoding nothing; the register forms still
encoding their GNU-as bytes). The emitter uses the two-instruction load-then-
compare form, which was always encodable.

The symptom to recognise: `allocs` halved as intended while `frees` did not
move, at `live_bytes 0`. Both free events were still the old path's — the
branch simply never taken.
