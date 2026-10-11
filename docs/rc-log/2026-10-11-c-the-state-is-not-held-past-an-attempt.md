# 2026-10-11 — the state is not held past an attempt

`seminline.resolve`, `seminline.part_of`, `ssarc.carried_pairs`,
`semsource`. Refs #11913, the attempt-and-rollback copies. The append cliff
compiling `checker.fern` (`scripts/cliff-bench`, x86-64): 66,812,280 bytes
over 222,628 crossings on main to 30,008,120 over 198,714.

## The shape the issue named

A step that may fail kept the state it started from, so it could hand it
back untouched on failure. The copy of that state was then held across the
step, and the step's first append into any of its arrays copied the array
whole. Two sites, attributed with gdb on the production binary (the method
in `docs/LOCAL-DEV-LOOP.md`):

| caller | bytes | copies |
|---|--:|--:|
| `seminline.part_of` (three appends) | 32.4 MB | 3,650 |
| `semsource.define` (`values.append`) | 10.6 MB | 12,265 |

`seminline.resolve` walked a read's operand down through constructions and
phis, appending part values and stand-ins to the edits as it went, and
returned the edits it started from when the walk failed. The decision is
now a walk of its own (`resolvable`), making the same tests as `part_of` and
nothing else, and the walk that writes takes the edits `own`. It answers in
a pair: a record per step cost the compiler a measurable share of its
allocations.

`semsource` had the same shape in `method_call` (a builtin tried and, on
refusal, the declared method from the state the refusal handed back, where
it had been tried from the state before the attempt), `match_chain` (a
`Value` kept across the arms for its id), `iterate_array`, `variant_arm` and
`wildcard_arm` (the gathered arms held while the arm ran) and `narrowed`
(the state's names read after the define that consumed it). Each now reads
what it needs before the attempt or threads the attempt's state on.

## The holders those left

With the two sites rewritten, `define` still copied 6.8 MB. Scanning the
frames between a copy and its caller for the box's address found only dead
spills; the holder was found by replaying the compile under gdb with a
watchpoint on the box's count word, which logs every retain and release with
the pc that made it (`docs/LOCAL-DEV-LOOP.md`). Two holders, one mechanism
each:

**A part phi per read.** `seminline.part_of` made a phi part per read of a
phi of constructions. The inlined `guard_gate` returned `Gate { s, from }`
from four paths, and `variant_arm` read `gate.s` three times (the `ok` test,
the failure return, the arm body), so the join held three phis of the arm's
state, each with a unit; the one for the test died at the test, the one for
the body moved into it, and the one for the failure path lived to the join
after the body. One part phi now serves every read of the same part of the
same phi (`made_part`). `TestSelfHostSemanticSplit`'s `gate` row pins it: a
record read three times through one field rebuilds in place, 200
allocations a hundred rounds where a phi per read made 300.

**A carried donor held past the arm.** The dying copy above was a reuse
donor: `ssarc.carried_pairs` held it in its local from the holding block to
the construction in a later block, which tested its uniqueness there. A box
that was shared when it died stayed shared through everything between, here
the whole arm body, so every `define` in the arm copied the state's arrays.
The holding block now takes the token where the donor dies (`hold_token`):
the box with its children released when it is unique there, else null once
the donor's unit is released. The construction moves the token in and an
exit edge frees an unspent one (`free_token`). The certifier reads the
token as it read an async frame's: a phi whose non-null inputs are the box
in a block that proved it unique. `TestSelfHostCrossBlockReuse`'s
`shared-donor-released-where-it-dies` row pins it: a donor shared with a
container is released where it dies, and the record taken out of the
container appends in place where it copied.

## Measured

The cliff compiling `checker.fern`, bytes copied by appends onto shared
arrays with room, as `scripts/cliff-bench` reads it:

| | bytes | crossings |
|---|--:|--:|
| main (181c613) | 66,812,280 | 222,628 |
| `seminline.resolve` decides first | 37,863,424 | 220,205 |
| `semsource` threads the attempt's state | 33,477,808 | 208,430 |
| one part phi per read, the token taken at the death | 30,008,120 | 198,714 |

The bench corpus's emitted sizes fall on every row with a split or a carry
(x86-64 1-7%, arm64 2-16%, wasm 2-7%); `pmap_insert.ir` falls 3.9% and
`ordmap_insert.ir` rises 1.2%, the token taken at each arm's exit where the
construction past the join tested it once. The compiler compiling
`checker.fern` allocates 22,941,440 times against main's 22,989,069 on the
same host. The certifier census over `compiler/fern.fern`: 6,882 functions,
153,123 tracked, 28,026 unproved, 0 findings.

## What is left

`define` still copies 4.7 MB and `part_of` 2.6 MB, so the issue stays open.
The remaining `define` copies are under `method_call` (2.4 MB at its
`ssasem.call()` define) and `produce`. The forward watch from such a copy
shows the state's last count released by the `Value` that `expr(fa.obj, s,
want)` returned: `s = produced.s` is a borrowed read, `produced` lives as
long as the plan's dependency says `s` does, and `Value { s: s, id: recv }`
then retains the state into the new record while the old one still holds
it. Where the record's death is placed in that function is the next lead;
the probes `a`-`d` in the session's scratch, the same head in a function of
fifteen lines, release `produced` at its last read and copy nothing, so the
difference is in what keeps `s` live along `method_call`'s paths. Two more
passes were tried and measured: reading a call result's fields apart where
it is produced (every use a field read) was neutral, 30.25 MB, and with a
copy per read it was 619 MB, a copy counting as an owner; moving a record's
scalar reads ahead of its last counted read was neutral too, 30.03 MB, the
plan already taking the field at the record's death. Neither is in the tree.

Outside the issue, `checker.inst_stmt`'s closure copies 3.9 MB,
`lift.lift_lambdas_view_typed` 3.8 MB and `seminline.splice_calls` 2.1 MB.
