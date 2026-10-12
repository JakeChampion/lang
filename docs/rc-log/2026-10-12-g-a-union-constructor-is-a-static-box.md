# 2026-10-12 — a union constructor is a static box, and a straight splice needs no split

`seminline`. Refs #8171. Changes emitted code.

## The shape

`seminline.constructor_leaves` splices a function that only builds its
result from its parameters and literals into the calls whose arguments are
all literals, so the construction there is a static box. It required the
result to be a construction, and a record returned as a member of a union of
records is not one: the semantic graph builds the record (`record_new`) and
then takes it up into the union (`variant_up`). That is the checker's whole
type representation. `checker.t_unknown(reason)` returns
`typeinfo.TypeUnknown { reason }` as a `typeinfo.Type`, so each of its 173
call sites, nearly all with a string literal, called it and allocated a box:
2.3 M calls on the whole compiler's self-compile. `t_string`, `t_bool` and
`t_i32` had the same shape.

Fixing that alone measured +2.5%, because it made many more splices and each
splice was expensive:

- `literal_args` rebuilt the definitions of the whole caller for every call it
  looked at.
- `splice` rebuilt every block of the caller and split the block at the call,
  so a function with dozens of `t_unknown("…")` calls grew a chain of blocks
  that every later pass walked.
- A splice whose callee has one return renames the call's result to the
  returned value in every use, over the whole caller, each time.

## What changed

- `constructor_body` accepts `variant_up`, in the body and as the result.
- A callee that is one block ending in a return (`straight`) is spliced in
  place (`splice_in_place`): its instructions take the call's place in the
  caller's block, with no split and no new blocks. The walk then continues at
  the first spliced instruction, so a constructor the body calls folds in turn.
  This covers most scalar leaves, not only constructors.
- `splice_calls` gathers the renames the in-place splices leave and makes them
  together, at the end, or before a splice that splits a block or an analysis
  that reads the whole function (`sempair.whole_values`).
- `literal_args` looks for each argument's definition earlier in the call's
  own block. A literal defined in another block no longer counts.
- The inner walk stops at `max_splices` too, since an in-place splice does not
  leave it.

## Measured

The whole compiler under callgrind. All three compilers were built from the
same tree and compiled the same frozen copy of it, each on a warm memo of its
own.

| | Ir |
|---|--:|
| before | 70.706 G |
| straight splices in place, renames gathered | 69.866 G (−1.19%) |
| and union constructors spliced | 69.894 G (−1.15%) |

The in-place splice took `splice_calls` −158 M, `defs_of` −100 M, and the
passes that walked the extra blocks: `ssalive` −109 M, `ssarc` −89 M and
`ssa_lift` −56 M. The renames still cost 59 M (`swapped_blocks`).

On the compiler's own compile the union constructors are about even. The
checker runs faster: `__sem_drop_typeinfo__Type` −151 M, `t_unknown` −69 M,
`Par.peek` −51 M. The extra splicing costs about as much: `seminline` +120 M,
`ssaunits` +68 M, `ir` +48 M. A program that builds a union member from
literals in a loop no longer allocates at all.

The compiler with both changes rebuilds itself byte for byte.
`TestSelfHostConstructorLeafUnionMember` checks, on all four targets, that a
union constructor called with a literal allocates nothing and one called with
a computed reason allocates once a round. Its `Off` twin runs the same
program with the pass turned off.
