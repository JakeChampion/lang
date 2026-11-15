# The narrow fields of a variant share a word

2026-11-15. x86-64 and arm64. Follows
`2026-11-15-d-a-load-moves-to-the-side-that-reads-it.md`.

## The shape

Each field of a box had a word. A red-black `Node` of rbtree has a colour, a
key of 32 bits, a boolean and two children: five words, and 48 bytes with its
header. The Koka node has 32 bytes, because the key, the colour and the
boolean share one word. In rbtree-ck the key has 64 bits, and the Koka node
has 40 bytes.

rbtree-ck 4,200,000 is bound by memory. Fern ran 11.1 billion instructions
and Koka ran 12.8 billion. Fern took 48.8 million last-level cache misses and
Koka took 41.7 million. Fern took 2978 ms and Koka took 2140 ms.

## The layout

On a native target, the narrow fields of a variant share one word when two
or more of them fit in 64 bits (`semsource.packing`).

- A narrow field is a boolean, a `u8`, an `i32`, a `u32`, or an enum whose
  value is an index that fits one byte (`narrow_bits`, `index_enum`).
- The fields of one byte are at the low bits. A field of 32 bits starts at
  bit 0 or at bit 32, so a load of 32 bits reads it.
- The word takes the place of the first narrow field. Each other field keeps
  its order (`held_fields`).

The front end builds the layout, not a later pass:

- The schema of the variant has the word as one field of 64 bits with the
  name `$packed` (`laid_variants`). Each function gets the same schema from
  the declaration.
- A constructor makes the word and gives it to `variant_new`
  (`variant_built`). A constant variant gets a constant word.
- A pattern reads a narrow field with a shift, a mask and a cast
  (`payload_read`).

The declaration follows the schema. `semlower.packed_decls` gives the
declaration of the variant the same fields, at its index, and
`irtables.replaced_decls` matches a variant by its shape.

Thus the tail call holes, the reuse, the release helpers and the proofs see a
box of whole words. A record and a tuple do not change. The wasm target does
not pack (`BuildOpts.pack_fields`): its import bridge reads one word for each
field.

## What a field access costs

The first version ran 36% more instructions on rbtree. Five changes took most
of that back.

### A kept field stays in its word

A read notes the word and the bits of its value (`State.word_reads`). Some
constructors get fields of one box at their own places. Such a constructor
masks that word and adds only the other fields (`kept_bits`).
`Node(Black, l, k, v, r)` from a matched node is one `and` and one `or`.

### The steps of a field do not count for the inliner

The shift, the mask and the cast have the mark `ssasem.field_step()` in
`imm`. `seminline.count_insts` skips them and each constant that only they
read. Without that, `balance_left` was too large to inline, and the insert
lost its reuse across the call. It made 3,850,587 allocations in place of
420,004.

### A cast to an enum is a source of each variant

A constant colour in a word has no `variant_new`. `semreach` then removed the
arms of a variant that no instruction built, and the program stopped with
exit code 134. `semantic_arrives` now counts the cast.

### A part of a loaded word is a narrow load

`ssa.narrow_field_loads` reads `(w >> 32)` as a load of 32 bits with the sign
at byte 4. It reads `(w >> 8) & 255` as a load of the byte at 1. x86-64
writes `movslq` and `movzbl`, and arm64 writes `ldrsw` and `ldrb`.

A compare of a byte with a constant reads the byte from memory (`cmpb`). The
pass that moves a load to its reader then moves these loads too.

### Parts of one word go back as a mask of the word

After the inliner joins a reader and a constructor, the code shifted each
part back to its place. `ssa.rejoined_words` writes that as `and` of the
loaded word with the mask of those parts.

## Measured

x86-64, this container. Peak resident memory, and Cachegrind with the small
inputs:

| workload | memory before | after | Koka | LL misses before | after | Koka |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| rbtree | 194 MB | 130 MB | 133 MB | 598,079 | 387,902 | 396,409 |
| rbtree-ck | 1376 MB | 1150 MB | 1151 MB | 4,258,324 | 3,588,783 | 3,666,235 |

| workload | Ir before | Ir after |
| --- | ---: | ---: |
| rbtree 420,000 | 574,315,108 | 624,055,258 |
| rbtree-ck 420,000 | 940,355,820 | 1,003,422,820 |

deriv, cfold and nqueens have no variant with two narrow fields, and their
counts do not change.

Best CPU time of the runs, with no stack limit: rbtree-ck 4,200,000 goes from
about 3060 ms to 2630 ms, and Koka takes 2170 ms. rbtree 4,200,000 takes 884
to 892 ms, and Koka takes 856 to 863 ms in the same set.

The compiler compiles itself in 70.6 s, down from 72.6 s, with the same peak
memory. Stage 2 and stage 3 are the same file. Each of the 626 programs of
`tests/stdlib` gives the same output.

Four programs of `bench/` run more instructions at their small sizes, where
the memory does not matter: `perceus_rbtree` 7.0%, `perceus_rbtree_ck` 5.7%,
`pmap_insert` 5.3% and `ordmap_insert` 1.4%.

## Traps

- A wide constant must keep 0 in `imm`. The verifier refuses another value
  with `constant needs its literal text`, so the mark of a field step is only
  on operations.
- The mask that keeps fields is the complement of the fields that change.
  The bits between the bytes and the half are zero, and the complement of one
  byte is a small constant.
- `map_string` is not a measure of a change of this size. See
  `2026-11-15-d-a-load-moves-to-the-side-that-reads-it.md`.

What stays open:

- A record keeps one word for each field.
- A store of one byte can change one field of a unique box. The code writes
  the whole word.
- The wasm target does not pack.
- rbtree-ck is 21% behind Koka in time, with the same memory and fewer
  instructions.
