# The emitted-RC certifier

Refs #11908. `ssaunits.verify_planned` replays the abstract unit plan and says
every unit is moved, dropped or returned once. ssarc then lowers that plan to
physical RC — retains, releases, reuse tokens, takes, shell releases, grow
brackets — and nothing read the ops it emitted. `compiler/rccertify.fern` does
now: it takes the lifted SSA of each lowered function (`ssa_lift`, in the form
the emitter allocates, after `ssaprepare.finish`) and nothing of the plan, and
accounts per path the units the frame holds. A finding is a compile refusal
under `FERN_IR_VERIFY` (exit 5, naming the function and the value), a report
line under `FERN_RC_CERTIFY=report`.

## The model

- A value's definition says what the frame holds on it: a fresh box or an
  owned call result is one unit; a counted parameter is one unit handed in; a
  borrowed parameter, a marked load and a contract borrow are zero; a phi
  takes what its edges bring. An indirect call, an unknown op or a callee with
  no contract makes a value unproved, never a finding.
- A release, a move, a store into a box, a push and a return of a unit each
  spend one; a retain adds one; a store of null into a slot gives the loads it
  matched their unit back (a take). Spending at zero is "spent without a
  unit"; a unit still held at a return is "leaked at return".
- A marked load is the frame's own unit — a child — inside a region a
  uniqueness test opened (`__fern_rc_is_unique`, a reuse token's null test,
  or either carried through phis of phis with `false`, together with the
  phis beside them that select the same box on the same edges), in a
  children-only drop (`__sem_drop_<T>`), before an overwrite of the same
  slot, or beside a shell release (`ir.shell`). Elsewhere it is a borrow,
  and releasing one is the finding this exists for.
- A retain of a value read out of a box the frame owns, where neither the
  retain's result nor the value is read again, hands the unit to that box:
  the lowering filling an option it made with a payload it borrowed. Read
  again, the value is the frame's binding and the unit is the frame's.
- A body that re-enters through the task stack (`__fern_task_mode`) is not
  certified: its boxes and reuse tokens come back as popped words, and
  which path a child release sits on is decided by a word of the saved
  frame. The census reports it as skipped. This is the one hole worth
  closing next; std/serve and std/stream are what it covers.
- A branch settles a word on its edges: `x == 0` makes `x` absent on that
  arm, and a paired call's second word (sempair) holds a unit only where the
  first word is settled to a variant whose payload is a reference
  (`Abi.pair_second`, one bit per tag).
- `p + c` names the object `p` does for a spend, and carries nothing into a
  phi: a cursor over a box is not a second unit of it.
- Rows meet with a three-point lattice: a count, absent, unproved. A count
  the paths disagree on is unproved. That was tried as a finding and it fires
  6560 times over the compiler, every one a retain on one arm paired with a
  consume on a later arm of the same test — the lowering's normal shape — so
  it stays unproved, and the census says how much.

## What the lowering tells it

The certifier cannot type a word, so the lowering marks what it knows
(`ir.counted`, `i64_imm` 1 on an op): every load of a reference at a typed
site (struct, tuple, option payload, element), every builtin call and
container op whose result is a reference, and core/map's `get_or` where the
runtime retained the value (a box column) — `ir.lent` (3) where it did not (a
text column; the lowering retains after). The lift carries the mark in `str`
for loads (`rc`), in `imm` for runtime calls (bit 2) and calls (1 and 3), and
the certifier reads a flat op's mark off the op its `imm` indexes. Emitted
bytes are unchanged: `scripts/selfhost-emit-hashes` over the fixture corpus
on all three backends is identical before and after.

`compiler/rcsigs.fern` is the one table of what a runtime helper or IR op
does to a count — the releases, the moves, the retains, the inspections, the
producers — and `irverifyrc.fern` reads its release set from it now rather
than carrying a copy (`TestSelfHostRcSigsReleaseSetCoversOracle` pins it
against `internal/oracle/ir/rcsigs.go`). A generated helper gets its contract
from its name (`rccertify.Abis.lookup`): a children-only drop lends, a
whole-value release consumes, a map column's closure takes its value owned
except the scalar and cell columns, which take a word.

## What it found

Nothing in the compiler, which is the result. The census over
`compiler/fern.fern` on x86-64 is 6781 functions, 153,097 tracked values,
29,235 unproved, 0 findings; arm64 the same; the checker driver on wasm 0.
Every one of the 570 fixtures of the conformance corpus compiles under it on
all three targets, and so does every e2ecompiler driver (the suite sets
`FERN_IR_VERIFY=quiet`).

The Go-side e2e suite (`internal/testing/e2e`, which also compiles under
`FERN_IR_VERIFY=quiet`) compiles 325 more programs; the first CI round
refused 26 of them and every one was a model gap, fixed here. Two of its
tests compile deliberately broken programs for the runtime sanitizer to
catch (a double free, a corrupted count); those opt out with
`FERN_RC_CERTIFY=0`, since the certifier now refuses them at compile time.

Getting there was a sequence of model corrections, not lowering fixes, each
surfaced by a finding over the corpus: the one-sided retain whose "unique"
arm was the join block (a region needs an arm with one predecessor); the
builtin result lists, replaced by the lowering's marks after `sleep_ms` and
`reactor_ctl` read as fresh references; `__map_get_or_impl`, fresh on a box
column and a word on a scalar one; `free` and `__map_cell_free`, which free a
raw cell no count covers; the pair word of a `Result` whose `Err` payload is
the reference; a cursor's map read to be dropped inside the cursor's unique
region; the retain that fills an option's payload; the uniqueness test
hoisted across a resume and the array phi beside it; the empty static box
and null, which two constants named; a reuse token carried round a loop
and across a suspension. Two test scaffolds were unbalanced on their error
exits and were fixed rather than exempted (`physicalRCHelpers`,
`byteViewRCDriver`).

The planted bug the issue asks for is `TestSelfHostSSAPhysicalRC`'s
`omit-retains` mutation: with every physical retain stripped from `produce`,
the compile is refused naming `produce` on all four targets, and the same
lowering without the mutation certifies. `TestSelfHostRcCertifyCases` pins
thirteen hand-built shapes: balanced, leak, double release, borrow released,
counted parameter kept, a retain paying for a store, a child in and out of
its unique region, a null-guarded release, and a count the arms disagree on
(unproved, not a finding).

## Cost

Callgrind over `compiler/drivers/checker_run.fern` on x86-64, instructions:

| Configuration | Instructions |
|---|---:|
| plain compile | 15.76 G |
| `FERN_RC_CERTIFY=1`, nested row matrices | 22.31 G |
| `FERN_RC_CERTIFY=1`, flat dense rows | 20.45 G |
| flat rows, one round on an acyclic graph | 20.57 G |
| flat rows, block-local slots kept out of the rows | 19.58 G |
| sparse rows (slot, count) with a key list | 27.25 G |
| rows and retirement by per-block liveness | 22.66 G |

The budget is 1% of the plain compile for the replay and the certifier
together, so this runs under `FERN_IR_VERIFY` for now and the default compile
pays nothing. The profile of the nested form spent a third of the certifier
in `__sem_drop_rccertify__Rows` and `__fern_arr_inc_elems`: a row-of-rows
update copied and retained the outer array on every edge. Flattening took
that out. The profile of the flat form puts 60% of the certifier in the
dense rows themselves — the entry meet, `put_row`, `row_equal`, the per-block
`filled` — and the rest in `walk_of` (regions, origins, touches) and the
per-instruction scans (`taken_at`, `reloaded`, `mark_taken_loads`).

The four variants after that are the shapes tried against the dense rows
and the numbers they measured; none is kept. Keeping a block's own slots
out of the rows saves little because most slots are read across blocks. A
sparse row costs more than it saves: the per-slot writes rebuild the state
struct and the rows are not sparse in a long function, where a value is
live across most of it. Liveness-driven rows (only the live-in slots read,
only the live-out written, a slot retired on the edge where it dies, which
also reports a leak where the value dies rather than at the return) cost
their fixpoint — several passes over `blocks × tracked` bits — more than the
rows they save. A second round on an acyclic graph is nearly free, since it
only compares. What would move this is a representation whose cost is the
live set at a block rather than the tracked set, computed in one pass, and
that is a different walk, not a tuning of this one. The fixpoint's share of
a verifying compile is accepted for now; the budget question for the
default compile is open.

A profile of a compiler built without semantic inlining (`FERN_SEM_INLINE=`),
so that callgrind attributes cost per function, puts the 4.64 G of the
certifier as:

| Function | Instructions | Share |
|---|---:|---:|
| `settle`, the fixpoint | 3.54 G | 76% |
| of which `entry_row` (the meet over predecessors) | 1.69 G | 36% |
| of which `feed_successors` (`put_row`, `row_equal`, `filled`) | 1.37 G | 30% |
| of which `apply`, the accounting itself | 0.22 G | 5% |
| `walk_of` (regions, origins, touches, takes) | 0.67 G | 14% |

The accounting is 5% of the certifier; the rows are three quarters. A row
restricted to the values live at the block, with liveness taken from
`ssalive` (the register allocator computes it for the same SSA and the
liveness variant above paid for computing it again), takes the rows'
share down to the size of the live sets. That is the next step.
