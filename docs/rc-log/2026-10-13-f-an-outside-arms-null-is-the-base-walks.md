# 2026-10-13 — an outside arm's null is the base walk's

`compiler/rccertify.fern`: `carries_at`. Fixes #12334, against
2026-10-13-d (the certifier walks under each tested reuse token's value).
No emitted byte changes; `rcmemo.version` stays 5, since the change only
certifies what was refused, and a refusal is never memoised.

## The shape

```fern
enum E { Wrap(i32[]), Two(i32, i32) }
let a: E = Wrap(id(xs)); let sa: i32 = match (a) { Wrap(x) => x[0], Two(p, q) => p + q };
let b: E = Wrap(id(xs)); ...
```

With the semantic inliner off, as `e2eharness.BoxedProbes` runs
`TestWASMBoxPreciseDrop`, the `Wrap` arm tests the box unique, reads the
payload after the token's join, and the next `Wrap` reuses the box through
a phi of the token and the null the `Two` arm gives it. The certifier
refused it on every target: `main: spent without a unit of v464 (borrow
ld_at) in b29: released by __fern_rc_dec`, once per match whose box a
later construction reuses.

## The cause

The walk under the token holding the box covers the token's region, the
blocks reached from the token's block, and reads a value the region uses
but does not define off the base walk's tables. The `Two` arm is reached
from the shape test before the token's block, so the null it hands the
reuse's phi is such a value. `carries_at` asked whether a phi input is
null against the region's own function, which does not define it, then
followed it into the base walk's function as a phi, which it is not, and
answered that the phi does not carry the box. The reuse went
unrecognised, so a path from the read seemed to return without the box's
release or reuse, and the payload taken out of a box proved unique read
as a borrow.

## The fix

An input the region does not define is null where the base walk's
function defines it as one. With that, the phi carries the box where the
token holds, the reuse writes the slot the payload was read from, and the
read is the frame's.

## Witnessed

`TestSelfHostRcCertifyCases` gains
`token-reuse-through-a-phi-an-outside-arm-nulls`, the shape in eleven
blocks: no finding, one join unproved (the reuse's phi meets the other
arm, which no tested token decides). On main without the fix it reports
`spent without a unit of v5 (borrow ld_at) in b9`, the same finding.
`TestWASMBoxPreciseDrop` passes again, and the compiler reproduces itself
with the certifier on.

## Every null test a walk makes

The fix read the outside null in `carries_at` alone. `carry_candidates`,
which finds the boxes a phi can carry for the per-walk index, follows
the first input that is not null, and asked the region's function too,
so with the phi's inputs the other way round, the other arm's null
first, it stopped at the null and the index missed the box. The same
refusal followed. `zero_at` now answers every null test a walk makes:
`carries_at`, `carry_candidates`, `box_on_edges`, `tested`,
`unique_word`, `token_inputs`, `is_take`, `apply_store` and `stores_of`.
`TestSelfHostRcCertifyCases` gains `reuse-past-a-join-null-first`, which
reports `spent without a unit of v5 (borrow ld_at) in b10` with the
`carries_at` fix alone and nothing with `zero_at`.
