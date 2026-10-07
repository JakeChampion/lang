# 2026-10-07 — an update keeps its scalar fields in place

`ssarc.keep_slots` and `ssarc.reuse_construct`, with the verifier's
`irverifyrc.read_keep_site`. Refs #9083, #8171. Extends
2026-09-28-an-update-keeps-its-fields-in-place.

## Before

`s = S { ...s, f: v }` with `s` dying kept only its COUNTED carried-over fields
in place. Every scalar field the update carried over was still read from the
donor and stored back, so a unique update of one field of nine read eight
slots and wrote nine. Even with the donor's box reused, the construction still
called `__fern_alloc_reuse` and wrote the shape word, both of which the
donor's box already satisfies.

`coreutils/ptx.fern` threads a nine-field `Line` through `put` once per field
of every output line. Under callgrind, `put` was the largest function at 23%
of the run (1.23 M calls, about 105 instructions each), and the reload and
restore of `Line` were most of it.

## Change

- **A scalar field is kept** under the same rule as a counted one: the
  construction stores it back into the donor's slot of the same index, the
  operand is the donor's own `record_get` of that slot, and it appears once.
  It holds no unit, so there is no take, retain or release to drop. While the
  donor is unique, only the store goes. A late scalar read (one the
  construction alone uses) is not emitted at all, and on the fresh path it is
  copied from the donor without the `__fern_rc_inc` a counted field gets.
- **The uniqueness read for scalars waits.** A counted keep needs the donor's
  uniqueness at its read, so it sets `start`. A keep with only scalar fields
  reads uniqueness at the donor's drop instead. This keeps the token interval,
  and so `token_busy`, as narrow as it was.
- **A kept token is the result.** A kept donor has the construction's own
  type, so on the token arm the result is the token as it stands.
  `__fern_alloc_reuse` and the shape store run only on the null arm, where
  the box is fresh. The kept fields are stored in that same arm.

`irverifyrc.read_keep_site` reads the new shape: the allocation sits in the
`token == 0` arm, whose other arm stores the token as the result. The
`keep_reuse` fixture behind `irverify_run` checks 218 to 220 builds that
shape.

## Measured

`coreutils/ptx.fern`, x86-64, built by the self-host compiler, linked from
`-emit asm` so callgrind sees symbols. Output is byte-identical to GNU ptx
9.4 for the default format, `-G`, `-O`, `-T` and `-A`.

| workload | before Ir | after Ir | |
|---|--:|--:|--:|
| 120k words, default | 552,472,228 | 496,150,780 | −10.2% |
| 348 KB prose, `-A` | 404,199,875 | 373,460,896 | −7.6% |

`put` went from 129.7 M to 100.8 M Ir with the scalar keep alone, and lower
again with the token taken as the result.

The compiler gains less. Its updates mostly carry counted fields, which the
earlier keep already left in place. A stage 2 of each side, built by its own
stage 1, compiling `checker.fern` for x86-64: 14,805,664,837 Ir before,
14,691,227,236 after (−0.77%).

## Tests

`TestSelfHostSemanticReuseDifferentialX86_64` gains four cases:

- a unique update over one field of each scalar width beside a counted one;
- the same over a shared base, where the fresh box must receive every kept
  scalar from the donor;
- a kept scalar that is also read after the update;
- a record of scalars alone, whose uniqueness is read at the drop.

Dropping the fresh path's scalar copy makes the shared-base case exit 123
with reuse on and 130 with it off.
