# 2026-10-13 — the certifier walks under each tested reuse token's value

`compiler/rccertify.fern`, `rcmemo.version` 4 → 5. Refs #12181: its first two
acceptance items, the cost item stays open. No emitted byte changes.

## The shape

The emitted-RC certifier (`2026-10-11-emitted-rc-certifier.md`) meets the
counts of a value at a join, and a disagreement is unproved rather than a
finding. The lowering's reuse tokens make that disagreement routine: a
token is the box where the box was unique and null where it was not, and
the lowering pairs a retain on the null arm of one test of the token with a
consume after the join and a release on the null arm of a later test. Every
such join was unproved, 32,277 of them over the compiler, and a lowering
bug of exactly that shape, #12135's missing retain of a field kept in place
by the construction that reused the box, was invisible to the certifier.

## What changed

For each token a function branches on, the certifier now walks the
function twice more: once under the token holding the box and once under
it null.

- `prune_token` is the function with every branch on the token taking the
  arm its value selects and every edge that would give the token the other
  value removed, at the branch that opened the box's unique region where
  the input comes out of it. The token's phi, the token phis it is built
  from, and every phi the pruning leaves the box or null (`fixed_phis`, to
  a fixpoint with the branches on them) become the value they have: a copy
  of the box where the token holds, null otherwise. A branch losing an arm
  is a jump; a jump losing its target loses its block.
- Where the token holds, the box's reuse (`__fern_alloc_reuse`) is the box
  itself, and its shallow release where the box was proved unique is a
  shell release (`box_ops`).
- The walk covers the token's region alone: the blocks reached from the
  token's block, extracted as a function of their own (`extracted`) with
  a stub per block outside it that has an edge in. The rows the walk
  without an assumption settled on those edges seed the region (`seeded`),
  with a phi the pruning made a copy of the box adding the unit the base
  handed it to the box's count. The tables of the values the region uses
  but does not define (roots, origins, children, regions, the takes of the
  loads outside the region, and the box each outside phi carries) are the
  base walk's.
- A load off the held box is the frame's where a path from it cannot
  return without the box's shell release or its reuse, and the reuse only
  where the construction stores into the slot the load read: a slot it
  keeps, and one it stores the same value back into, stays the box's. A
  reuse through a phi of the box and null counts where every edge the
  paths take into the phi gives it the box (`escapes`, assumed of every
  such phi and withdrawn until the paths refute none).
- A join a token's walks settle is proved; what no tested token decides
  stays unproved. A finding either walk makes is a finding: a path under
  one value of the token is a path.

## What it found

The first census of the walks over the compiler reported 429 findings, all
false; each was a rule the walk under an assumption lacked, and the last
three stood for shapes the compiler has in a handful of places:

- A second phi of the box, null where the token is, tested where the
  shell release sits (`asmcore.unit_leaves`): without its branches pruned,
  the walk saw a path past the shell. `fixed_phis`.
- A construction reusing the box through a phi three arms feed, each with
  its own token of the box (`semfuse.fuse_pipelines`): the box's unit
  leaves through the phi only on the path from the arm that holds it.
- A construction that stores the field it read back into the slot it read
  it from (#12135, and `seminline.paired_all`, which releases that field
  on the other path): a store back keeps the box's unit in the slot; a
  store of a unit retained of it is a payload of its own.

With the three, the census is 0 findings over the compiler and 24,713
unproved joins (from 32,277; both on the tree at 611e9054). With the
second round's rules below it is 0 findings and 24,787 unproved, and 0
findings over the 106 coreutils and 245 standard-library test programs.

The first CI round found two more in the standard library, which the
compiler's own sources do not exercise:

- `std/tls`'s `__server_drain` reads a field off the message its loop
  carries round, under the message's token, and reuses the message for
  the next time round through a phi of that token. The message is itself
  a phi of the fourteen arms that produce one, so the walk's notion of
  "the box a phi carries" found no single box and the reuse went
  unrecognised. The notion is gone: a phi carries the box the walk asks
  about (`carries`) when each input is that box, null, or such a phi, and
  the reuse's phi is matched against the load's box, not resolved on its
  own.
- `std/serve`'s `__supervisor_reap` reads the fields of the supervisor on
  the arm where its token is null, to retain them into the copy, and the
  copy's reuse through that token is at a position before the load in the
  load's own block, reached round the loop. A reuse through a phi now
  counts only where the paths from the load enter the phi's block and
  every edge they take gives it the box; a phi given its value before the
  load, which may be null, is not the box. And a release position before
  the load in its block is reached round a loop, so `between` walks the
  paths rather than reading the block straight through. 

The second round found two in the coreutils, both from the walk's region
being cut at the token's block:

- `seq`'s `main` makes the number's text with a paired call before the
  token's block, and the arm that drops the second word is inside it. The
  word is a value the region uses but does not define, so the walk took
  its origin from the base walk, but not the tag its unit hangs on, and
  the arm leaked it. The tag is inherited with the origin.
- `tail`'s `tail_forever` makes a `File` each time round a loop that
  never leaves, tests it unique, and where it is not, copies its fields
  into a fresh box and releases its own unit of it. The region's entry is
  the token's block, so the copy's block dominates the construction next
  time round, and the construction's store of each field read looked like
  an overwrite of the slot, on every path. The box's own construction is
  now an event on the paths `between` walks: past it the slot is another
  box's.

Reverting the `built_kept` restriction of
`supplies` in ssarc, which is #12135's fix, makes the compile of #12135's
program refuse: `run: spent without a unit of v59 (borrow ld_at) in b20:
moved into wrap`, exit 5. The phi that meets a disagreement now records
the join (`maybe_at`) as a block's entry does, so a token can decide it.

## Cost

Wall time of the compiler compiling itself on the 4-core container, x86-64:

| | first round | second round |
|---|---:|---:|
| certifier off (`FERN_RC_CERTIFY=0`) | 19.9 s | 19.6 s |
| cold memo (`FERN_RC_CERTIFY_MEMO=0`) | 30.1 s | 35.8 s |
| warm memo | 21.0 s | 19.6 s |

The second round's rules (`carries` per base, the escape fixpoint, the
construction as an event) add about 6 s to the cold compile; it is not
yet broken down by phase. In the first round the walks were about 9 s of
the 10 s over the certifier off: 4,673 walks over 1,905 functions. Timed
per phase inside `walk_assuming`, `walk_of` was
3.9 s (its null-store took 1.2 s and origins 1.7 s), `settle` 2.4 s,
`live_of` 1.5 s, `prune_token` 1.2 s. Before the walks the certifier's
whole cost was about 1 s. Three things were taken out of the first
version, which cost 22 s: `pick_loads` computed the blocks reached from
a null store avoiding each dominating block before looking for a load
there (callgrind had `reached_in` at 21% of the compile), `escapes` scanned
every phi of the function per load for the phis that carry the box, and
`shell_taken` ran `escapes` once per shell of the box rather than once.

Two cuts were tried and dropped. Walking only to the horizon where the
token's effect ends (the blocks where a root the value changes, or a root
of a join it decides, is still live, with a stub reading what is live at
each edge out) halved the instructions walked but, once a loop-carried phi
of the box kept the loop inside the horizon, saved under 15% of the time,
and its exits reported deaths the base walk owns. Inheriting the base
walk's origin for every root the token cannot affect skipped most of
`origins` but classified phis of those roots differently from the base
and made 15 findings. The memo makes a steady compile pay nothing for the
walks; the cold compile pays them in full.

## Gates

`TestSelfHostRcCertifyCases` gains six cases with the two of the first
round: a derived token's test pruned past the shell, a reuse through a
carried token that writes the slot, a reuse that writes the slot a moved
field was read from, beside the one where the construction keeps that
slot and the move is the finding, a loop's box reused for the next time
round through its token, a paired call's word settled past the token's
test, and a loop's box copied under its null token. `TestSelfHostRcCertifyMemo`,
`TestSelfHostRcCertifyMemoKey`, `TestSelfHostRcCertifyInPlaceScale`, the
physical-RC driver's `certifier-refuses-missing-retain`, and the lint
ratchet.
