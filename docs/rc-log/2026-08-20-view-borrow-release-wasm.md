# A slice temp at a string borrow position leaked on wasm

`base[a:b].len()`, `base[a:b] == x`, `base[a:b] + y`, `base[a:b][0]`,
`base[a:b][c:d]`, `len(base[a:b])` — every position `lower_view_borrowed`
serves — stranded a payload-sized heap block on wasm. 400 rounds, two compilers
from the same commit:

| position | x86-64 | wasm |
| --- | --- | --- |
| `.len()` receiver | 0 | 48000 → **0** |
| `==` comparison | 0 | 48000 → **0** |
| concat operand | 0 | 48000 → **0** |
| index base | 0 | 48000 → **0** |
| slice source | 0 | 54400 → **0** |
| `len(x)` builtin | 0 | 48000 → **0** |

Six shapes, one number, one cause. That uniformity is what said it was not six
bugs: a nesting bug or a per-op bug would not produce 48000 six times.

## Cause

The two backends reach "this temp costs nothing" by different routes and only
one route existed. `view_frame_temp_ok` sends a borrow-position slice to
`lower_str_slice_frame`, whose 24-byte box lives in three reserved frame slots
and never reaches the heap — nothing to release. It opens with
`if (s.for_wasm()) { return false; }`, and that bail is right: wasm has no such
storage. `op_str_slice_frame`'s own comment says so — it "ignores it, its
str_slice copies into a fresh inline block". What was missing is the other half:
having declined the frame form, nothing released the copy wasm makes instead.

## Fix

`lower_view_borrowed_parked` parks the copy on wasm and the caller drains after
the consuming op — the park/drain pair `stash_fresh_str_arg` already uses. The
warrant is the one `lower_view_borrowed` asserts in its own name: the value is
borrowed for the duration of ONE EXPRESSION, which is the same proof the frame
form relies on to reuse its slots.

Wired at the six borrow positions. A parked slot is `0 - 1` on every non-wasm
target, so `free_parked_view_after` emits nothing there.

## Blast radius, checked not asserted

The compiler's OWN x86-64 emission is byte-identical between the two compilers —
1.7M lines. A change that only fires under `s.for_wasm()` should prove it rather
than claim it, and that is the cheapest available proof.

## What the tests witness, and what they do not

The byte gates witness that the release HAPPENS: all six fail with 98 on the
parent, on the wasm leg.

They do NOT witness WHERE. A build that frees the copy BEFORE the consuming op
passes every probe here, because the free and the use are adjacent with no
allocation able to intervene, so the read finds stale-but-intact memory. The
placement is correct by construction and contract-only by measurement. Recording
that rather than letting the green gates imply the ordering was tested: an
early-free build is a latent use-after-free that any change to allocator reuse
or op ordering would expose, and no gate here would catch it.

## Rejected design

A pending-view slot list on `LowerState` with one drain at statement end — 1
stash point and 1 drain instead of 6 — is cleaner in principle. `LowerState` has
206 literal constructions and already folds four booleans into `ret_arrdyn`
specifically to avoid new fields, so a new field is a 206-site mechanical edit.
That is the exact shape that produced the #7167 parameter crossing, fixed in
#7178 + #7189. Per-site looks riskier and is not.

Regression test:
`internal/e2eselfhost/self_host_str_view_borrow_release_test.go` (7 cases x 3
backends; the six byte gates fail on the parent on the wasm leg, the register
legs pass either way and guard against the frame path regressing, and the
all-positions liveness case passes either way).

Refs #6544 #4451.
