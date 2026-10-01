# 2026-10-01 — a string append grows its left operand in place

`ssaunits.string_append`, `ssarc.grow_or_concat`, `__fern_str_grow` on
x86-64, arm64 and wasm. Refs #9077.

## The shape

`s = s + piece` on a string the frame owns. The typed lowering emitted a
fresh `__fern_str_concat` per append, which copies the whole accumulator, so
the loop was quadratic: 20,000 / 80,000 / 320,000 eight-byte appends took
0.047 s / 0.753 s / 27.4 s on x86-64.

## What changed

A string `+` now takes a unit of its left operand (`operation_supplies`), the
way an array append takes its receiver's. Where the plan MOVES that unit in —
the operand's last use, owned by the frame — `grow_or_concat` asks
`__fern_str_grow(a, b)` first. The helper appends in place and answers 1 when
`a` is a heap box at rc 1 whose block has room in its size class; otherwise it
writes nothing and answers 0, and the concatenation runs and the operand's unit
is released. Where the plan would RETAIN the operand (the frame still reads
it), the retain is held back (`deferred_retain`) and the plain concatenation
lends it, so a string the frame still reads costs nothing extra. A literal
operand skips the helper: its immortal count can never be one.

The room test is the free path read backwards. On the register backends a
fused box (`[rc][fused][data][len]` in one block) is freed at
`words = ceil(len/8) + 3`, classed by `__fern_capw` below 65,536 words and
by `large_class` above; growth is in place only when the grown words still fit
the capacity the ORIGINAL words were classed at, in the same tier, and at most
1 GiB, where the large tier stops rounding. An unfused box (separate data
buffer) and a view (immortal count) never pass. On wasm a heap string is one
`[rc][bsz][len][bytes]` block freed at the class of `bsz`, so growth leaves
`bsz` alone and needs only `len + lb + 12`, rounded to 8, within that class's
capacity.

It pays only because both tiers now have slack: `__fern_capw`'s
three-significant-bit classes above 2 KiB, and the large tier's (#9077 step
1). The 2026-09-18 null result (`the-self-host-heap-has-no-slack-to-append-into`)
measured an in-place helper against the exact-fit heap and lost.

## Measured

x86-64, same container, the loop in a function returning the accumulator:

| appends | before | after |
|---|---|---|
| 20,000 | 0.047 s | 0.003 s |
| 80,000 | 0.753 s | 0.005 s |
| 320,000 | 27.4 s | 0.015 s |
| 1,280,000 | — | 0.054 s |

arm64 under qemu: 0.019 / 0.019 / 0.038 s for the first three rows; wasm
under wasmtime 0.023 / 0.021 / 0.035 s. 20,000 appends allocate about 290
times rather than 20,000 (`TestSelfHostStringAppendInPlace`, which exits 3 on
the compiler before this change).

`fern.fern/stage2` is 13,265,672 bytes against the banked 13,040,616 (+1.7%,
including main's drift since the bank). Stage2 recompiles the compiler to a
byte-identical stage3.

## Diagnostic builds

Under `FERN_RC_TRACE` / `FERN_LEAKCHECK` an in-place growth reports a free at
the old size and an alloc at the new, so the trace still pairs every block's
alloc with its free by size and `live_bytes` stays the demand. The production
row `string-append-grows-in-place` runs the alias, parameter, view, `d + d`,
field and element shapes under the sanitizer with nothing held at exit.
