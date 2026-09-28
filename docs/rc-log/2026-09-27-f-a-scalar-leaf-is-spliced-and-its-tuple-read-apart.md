# A scalar leaf is spliced into its callers, and its tuple is read apart

Part of #8920, option 1 of #10498.

## What changed

`semsource.build_module` now runs a pass, `seminline`, over the produced
graphs before they are planned. It does two things:

- **Splices leaves into their callers.** A leaf is a declaration that computes
  only on scalars and tuples of scalars, and calls nothing. It has at most 40
  instructions and 16 blocks, and its entry block is not a branch target. A
  call to a leaf is replaced by a copy of the leaf's blocks. Each copied
  return branches to a new block. That block defines the call's result (a
  phi when there are several returns) and continues with the rest of the
  caller's block. Splicing moves no reference count, because every value a
  leaf makes is a word or a tuple of words.
- **Reads tuples apart.** A `tuple_get` of a scalar element whose tuple is a
  construction, through copies, becomes a copy of that element. A
  `tuple_get` through a phi of constructions becomes a per-element phi. A
  scalar-tuple construction, phi or copy that nothing reads is dropped. This
  runs on every produced body, spliced or not, so a local `(ok, value)` pair
  is never allocated either.

These are not spliced:

- a declaration marked `@noinline`;
- a template, or an instance the semantic source produces from one;
- a recursive call.

Nothing is spliced into a caller already past 1,500 instructions, and a
caller takes at most 64 splices. `FERN_SEM_INLINE=` (set but empty) turns
the pass off, for comparing a build against itself.

## Measured

`TestSelfHostSemanticInline`: 100 rounds per probe, heap allocations. The
results are identical on all four targets:

| probe | pass off | pass on |
|---|---|---|
| `divmod`, one tuple return | 100 | 0 |
| `scan`, two tuple returns joined by a phi | 100 | 0 |
| `clamp`, a scalar through three blocks | 0 | 0 |
| `kept`, `divmod` under `@noinline` | 100 | 100 |

`nl` over 100,000 lines, built by the self-host compiler:

- heap allocations: 105,140 → 5,140;
- instructions: 44.3M → 33.2M;
- binary size: +312 bytes;
- output: identical.

The self-host compiler compiling `coreutils/tsort.fern` (callgrind, x86-64).
Both compilers are built by the same stage 2 from the same source, one with
the pass off:

| compiler | pass while compiling | instructions |
|---|---|---|
| built with the pass off | off | 4,422,957,367 |
| built with the pass on | off | 4,413,456,633 (−0.2%) |
| built with the pass on | on | 4,446,136,342 (+0.5%) |

The compiler's own code is 0.2% faster spliced. Running the pass costs 0.7%
of a compile: 2.7M instructions in the pass itself, and the rest in planning
and lowering the larger graphs it leaves. The net cost is +0.5% compile time,
paid for code that allocates less. The spliced compiler is 12,059,704 bytes,
against 12,037,688 bytes (+0.2%).

Stage 2 → stage 3 → stage 4 is byte-identical.

`testdata/semsource_print.golden` now shows the spliced graphs. The calls to
`twice_it`, `add_at`, `join_at`, `wide_low`, `loop_phi`, `i32.doubled` and
`LIMIT` became their bodies, and single-construction tuple reads became copies.

## Next

Only scalar leaves are spliced. A leaf that reads a field of a borrowed record
is the next widening. It moves no count either, but the splice has to carry
the callee's borrow of the argument into the caller's plan.
