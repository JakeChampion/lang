# 2026-10-12 — a match loads the shape once

`ssa.share_shape_loads`. Refs #8171. Changes emitted code.

## The shape

A `match` on an enum lowers to a chain of variant tests, one block per arm,
each entered from the test before it. Each test loads the box's word 0, its
shape, materialises the variant's shape address and compares:

```
movq 0(%rsi), %rdi
leaq .S0(%rip), %r8
cmpq %r8, %rdi
jne  .next
```

On a stage-2 compile of `checker.fern`, these four-instruction tests were
5.4% of all instructions executed, across 6,402 sites. The load in every test
after the first reads the word the first one read.

## What changed

`share_shape_loads`, in `register_form` after `merge_duplicates`, maps a load
of a box's word 0 onto an earlier load of the same word that reaches it along
blocks each entered from one predecessor. The later tests then compare against
the register the first test loaded. From each shape load it walks back through
its block and then up the chain of single predecessors, and stops at an
earlier load of the same box or at anything that may write memory: a call, a
store, an allocation. The pass leaves alone any box whose word 0 or elements
the function stores, and any function with a raw store.

A first version kept the available loads across calls, on the reasoning that
a box updated in place is consumed and is not read again through its old
value. That is false for a map iterator, whose `advance` updates the cursor
that later `has_next` and `key` calls read through the same value.
`TestSelfHostSSABackendAgreesWithNative/maps` hung on x86-64 and crashed on
arm64, and the compiler built with it crashed compiling a wasi-http handler.

## Measured

The whole compiler under callgrind. Both compilers compiled the same frozen
copy of the tree, each on a warm memo of its own, and both were built from
the same main.

| | Ir |
|---|--:|
| main (6ca24c5) | 71.464 G |
| shape loads shared | 71.213 G (−0.35%) |

The compiled code runs faster, `astwalk` −182 M and `checker` −119 M among
them. A first version carried each block's available loads forward to its
successors in lists, which every block paid for. It cost about 250 M and
measured −0.27%. Walking back from the loads touches only the blocks on a
path to one. Both versions compile the compiler to the same bytes.

`TestSelfHostOptimisationShapes/match_loads_the_shape_once` pins the shape
on x86-64 and arm64: one shape load in a four-arm match.
