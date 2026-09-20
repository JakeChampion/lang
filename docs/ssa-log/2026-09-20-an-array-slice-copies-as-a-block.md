# 2026-09-20 — an array slice copies as a block

`pvec_with` is the worst remaining gap on the bench table. Profiled with
symbols after the rc-primitives entry, its largest single function was the
slice helper:

| function | instructions | share |
|---|---|---|
| `__fern_arr_slice` | 178,321,656 | **24.1%** |
| `__fern_arr_dec` | 129,356,711 | 17.5% |
| `pvec.__pv_with_in` (four shards) | 211,286,334 | 28.6% |
| `__fern_rc_inc` | 72,445,009 | 9.8% |
| `__fern_arr_inc_elems` | 51,252,194 | 6.9% |

A persistent-vector write descends a 32-way trie and rebuilds its path, so
each write copies several 32-element nodes, and every node copy went through
that helper one element at a time.

## What changed

The helper is generated Fern source (`asmcore.rt_src_arr_slice`), compiled by
the register path for x86-64 and arm64; wasm has its own hand-written
`$__fern_arr_slice` and is untouched. Its loop body was thirteen
instructions to move eight bytes:

```
    movq %r9, %rdi
    addq $1, %rdi
    movslq %edi, %rdi
    movq %r12, %r8
    addq %r9, %r8
    movslq %r8d, %r8
    cmpq (%rbx), %r8
    jae __fern_oob_abort
    movq 8(%rbx,%r8,8), %rsi
    movq %rsi, (%rax,%rdi,8)
    addq $1, %r9
    movslq %r9d, %r9
    jmp .Lssa___fern_arr_slice_17
```

A bounds check and two sign extensions per element, for a copy whose range
the helper's own four guards had already established. It now moves the
elements with `__memcpy`, which x86-64 lowers to `rep movsb` and arm64 to its
size-classed eight-byte loop.

An array value is already its data pointer, so `__raw_addr(a, off)` takes one
with no new intrinsic and no checker change: `irlower`'s note on `__raw_array`
says it "emits NOTHING: the data pointer already IS the array value".

## The cap, and why there are two loops

`__memcpy` takes an i32 byte count and `__raw_addr` an i32 byte offset, so a
word array of 2^28 elements or more would overflow either one — its byte
count is 2^31 exactly, a silent 2 GiB cliff where the element loop was good
to 2^31 elements. Strings do not have this
problem because their length is a byte count already, so the cap coincides
with the type's own limit; a word array's does not.

So both the skip to `start` and the copy itself step in chunks of 2^27
elements, one GiB, advancing the pointers rather than indexing from the base,
which is what keeps every offset in range. A chunk of 2^28 would be the
broken size: 2^31 bytes, the very overflow the paragraph above is about. For
every array anyone has this is one iteration and one compare.

## Measured

x86-64, retired instructions under callgrind, against the shift entry
immediately before it.

| bench | before | after | Δ | after / native ssa |
|---|---|---|---|---|
| `pvec_with` | 737,874,440 | 680,946,612 | **−7.7%** | 3.69x |
| `record_update` | 332,888,005 | 332,888,005 | 0.0% | 1.93x |
| `array_with` | 65,828,289 | 65,828,289 | 0.0% | 0.89x |
| `ordmap_insert` | 320,027,237 | 320,027,237 | 0.0% | 0.74x |
| `string_slice` | 70,920,088 | 70,920,088 | 0.0% | 0.50x |
| `tokenize` | 128,700,088 | 128,700,088 | 0.0% | 1.44x |

Every exit status is unchanged. The flat rows never reach the helper: their
updates take the unique in-place path, and a user-written slice is a view
rather than a copy.

## What is left in that gap

Removing the whole helper would still leave `pvec_with` at about 3x. The rest
is not codegen: `__fern_arr_dec` at 17.5%, `__fern_rc_inc` and
`__fern_arr_inc_elems` at another 16.7%, which is the self-host doing rc work
per node that native's reuse analysis avoids. That is goal-2 reuse work, not
another instruction-selection slice, and it is where this benchmark's
remaining factor of three lives.
