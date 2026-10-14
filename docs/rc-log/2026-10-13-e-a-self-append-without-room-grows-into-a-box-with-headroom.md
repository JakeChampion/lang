# 2026-10-13 — a self-append without room grows into a box with headroom

`__fern_str_grow_fresh` and `__fern_str_grow_fresh_range` on x86-64, arm64
and wasm, the grown marker and the capacity word on the register backends,
`ssarc.grow_or_concat` and `grow_field_or_concat`. Closes #8770.

## The shape

A self-append (`s = s + piece`) tries `__fern_str_grow`, which takes the
piece in place when the accumulator's block has room, and otherwise fell
back to the concatenation: an exact-size fused box of both operands. A
fused box's block is sized from its length, and the small tier's classes
are exact to the word below 2 KiB, so the fallback ran at every word
boundary and each run copied the whole prefix. #8770 measured 135
instructions for a two-byte literal append and 418 for an eight-byte
range, most of them in `__fern_str_concat`.

## What changed

The fallback builds a **grown box**: a fused box under a 32-byte header
whose first word is the block's capacity in words, with the GROW marker
where the fused marker sits. The block is twice the words the
accumulator's length takes or what the grown string needs, whichever is
more, rounded to its allocator class (`__fern_capw`, or the large tier's), and allocated at that size, so
the trace and the census pair its allocation and its free. `__fern_str_grow`
takes the next appends into a grown box while the capacity holds them, with
no size change for the census to see, and `__fern_str_free` returns the
block at the recorded capacity. The range form reads the source's bytes
straight into the new block and traps on a range outside the source, where
the concatenation path built a frame view for the slice to raise it.

On wasm every heap string carries its block size, so the fallback records
the headroom there and the existing `$__fern_str_grow_bytes` and
`$__fern_arr_dec` honour it unchanged.

The length, not the accumulator's block, is the measure: a shared
accumulator is copied on every append, and sizing from the capacity of the
box it copies doubled the capacity each time, out of memory within a few
dozen appends. The gate's program shares its accumulator every seventh
round to hold that.

The exact concatenation stays for a range join of a string the frame keeps
(`ssarc.concatenation`): only the shape that appends again carries slack.
`rcsigs` reads the new helpers as inert with a fresh result, as the
concatenation was.

## Measured

x86-64, `-O`, callgrind, one million iterations of #8770's probes with the
accumulator reset at 8 KiB, both compilers from the same tree:

| probe | before | after |
|---|--:|--:|
| `out = out + "$\n"` | 134,926,239 (135 an append) | 51,208,270 (51) |
| `out = out + slice_unchecked(src, 0, 8)` | 418,568,174 (419) | 77,928,355 (78) |

Both exits identical. `bench/string_build.ir` falls 37.5% (98,161,013 to
61,394,539). The gate's program, 3000 one-byte appends, 1000 three-byte
range appends and an append to a shared accumulator, takes 26 blocks where
it took 523, on x86-64 and arm64 alike; wasm takes 35. The alloc-count
matrix's `str_self_append_chain` cell falls from 6 blocks a round to 2.

The `.text` rows grow by the two helpers and the two marker arms, 50 to
160 instructions a program on the rows that carry the `str_grow` need,
which is why nine x86-64 and seven aarch64 rows are re-pinned upward.

## Gates

`TestSelfHostStrGrowFreshX86_64` runs the program, its census with a block
ceiling, and its quarantine build; `Arm64` the program and the census under
qemu; `Wasm` the program and the census under wasmtime.
`TestSelfHostAllocCountMatrixX86_64` carries the new row.

`__fn___fern_str_free`'s grown arm is a path the fused-string text machine
walks over the separate-buffer word intervals: it reads the capacity at
P-16, returns the block based there at that capacity, and links it through
the block's first word. Its mutation controls refuse a wrong capacity
offset, base, branch, marker or exit, and the separate-string machine
treats the GROW marker as a known tag. With that walk the retained-bodies
driver outgrew a 16 GB runner under the sanitizer, whose quarantine keeps
every freed block, so the text machines run in a driver of their own,
`ssaretainedmachines_run.fern`: 9.3 GB and 4.4 GB at peak, where the one
process took 13.4 GB.

Three e2e gates pinned the exact boxes. `TestX86_64AllocScaling`'s
left-fold calibration case is now linear (1.97x as n doubles), so it joins
the corpus as an ordinary case and a prepend fold, which copies the whole
string on every `+`, is the quadratic control (3.75x).
`StrAppendClassBoundary` re-banks its allocation count from 258 to 8 (262
to 11 on wasm), one block per doubling. `OwnStringParamBalanced` bounded
its 800-round fresh bytes by twice its 200-round ones; a doubling
accumulator's fresh bytes follow its length, 1.6x to 2.4x what it holds, so
it now bounds each segment at eight times the string it builds.
