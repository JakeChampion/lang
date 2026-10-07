# 2026-10-07 — an append onto a sole box asks nothing

`ssaunits.sole_boxes` and `ssarc.append_push`. Refs #8171. Builds on the
previous entry, which proved a loop's `with` writes sole.

## What changed

An `append` on a receiver the frame holds tests the box's count at run time
before it pushes: the consuming push when the count is 1, which grows the
buffer in place and frees the old one on a realloc, and otherwise the
non-consuming push, which copies, then a release of the receiver. Every list
the compiler builds, `out = out.append(x)` in a loop over a box that started
as `[]`, asked once per element.

The sole-box proof now covers appends and more seeds:

- An `append` is a use that keeps its receiver sole, as a `with` is.
- An `append`'s result is sole when its receiver is sole and moved into it.
  Both pushes hand back a box only the frame holds. Requiring the receiver
  keeps out the appends threaded through a borrowed parameter (`Link`),
  whose first box is the caller's: a parameter is never sole, so nothing
  rooted in one is either.
- An array literal is a seed when it is built at run time, or when it is
  empty. A non-empty constant literal is one static box every evaluation
  shares, and a `with` on it must copy. The empty one is shared too, but no
  write lands in it: a `with` on it fails its bounds check, and a static
  array's capacity is its length, so a push onto it always takes the
  runtime's grow path, which copies and frees nothing whose count is
  negative.
- `array_reserve`, a fresh empty array with capacity, is a seed.

An `append` whose receiver is sole and moved emits the consuming push alone,
marked as tested unique so the inline push drops its own count test.

## Measured

`checker.fern` (at 2e084b79) built for x86-64-linux under callgrind by
production compilers, no `-g`: each side's stage 3, built by its own stage
2, built from the side's sources by the stage0-built stage 1. The baseline is
the previous entry's branch at ef66a3aab. Both stage 3s rebuild themselves
byte for byte.

| | before | this change |
|---|--:|--:|
| total Ir | 15.853 G | 15.544 G (−1.95%) |
| stage 3 size | 11,157,952 | 10,932,872 (−2.0%) |

## What is left

The inlined uniqueness test costs 703 M on the same compile, down from 876 M,
summed per instruction as the previous entry did, on a `-g` stage 2 built by
each side's production stage 2.

The largest remaining sites are `with` writes in `ssa_lift.lift_impl` on
tables a helper call makes (`filled(nslots, 0 - 1)`, and `util.minus_ones`
elsewhere). A call's result is never a seed, because the proof does not see
into the callee. A summary per function, recording that its result is always
a box no other holder has, would let those tables start sole; a helper that
fills a fresh array and returns it is exactly the shape this proof already
takes inside the helper. That is the next lead.

The release paths' count tests (the `__sem_drop_*` functions, about 30 M)
stay: the frame cannot know the count where it drops a unit it was handed.

The test is `TestSelfHostSoleLoopWithX86_64`. Two append shapes join the
proved ones (an empty literal grown in a loop, and a run-time literal grown
and then written over). Three join the refused ones: an append with a second
name held across it, an append onto a parameter the caller still reads, and a
`with` on a constant literal called twice, which must not change the
constant the second call reads.
