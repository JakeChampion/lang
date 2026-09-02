# 2026-09-02 — a reused box's old `Cell` field: still leaking, and the freeing fix is unsound

The residual left by `2026-09-02-cell-container-child-drop.md`, at the fourth
and last of the sites that route a child drop. The first three are the
generated drop fns (`appendChildDrop`), the function-exit sweep
(`dropStructField`), and a slot reinit. This one is Perceus REUSE.

**Status: open.** The fix described below landed as 6e0c2bd and was reverted —
it miscompiles the self-hosted compiler. Read "Why the fix is unsound" before
attempting it again.

## What is leaking

When reuse hands a dead value's box to the next construction, the reuse branch
first releases the previous occupant's pointer fields, so the new stores do not
strand them. All three reuse paths — struct overwrite, enum overwrite, and the
cross-value `emitReuseOldFieldDrops` — do that through
`emitFieldDropOnStack`, which delegates an ARRAY field to `dropStructField`'s
ladder and leaves everything else to `dropFnNameFor`. That declines `Cell` (no
generated drop fn exists for it), so a cell field falls to the flat
`__fern_rc_dec`: decrement, return, box stranded.

## Why it looked like it scaled with the number of containers

The earlier entry recorded this as "N containers strand N − 1 slot buffers"
and blamed slot accounting. Both halves were wrong, and the shape of the
measurement is what misled: only the FIRST container in a function allocates a
fresh box, and every one after it reuses a dead predecessor's. So the leak
count tracks reuse sites, not cells, not accumulation, and not string slots.

The discriminator that settles it is that each container in isolation is
already clean, and any PAIR of them leaks exactly 1 — regardless of which two:

| blocks from `cell_in_container` in one `main` | unpaired |
|-----------------------------------------------|----------|
| tuple + scalar cell, alone                     | 0        |
| enum + string cell payload, alone              | 0        |
| one cell shared by two tuples, alone           | 0        |
| any two of the three                           | 1        |
| all three                                      | 2        |

That measurement stands; it is the accounting of the leak, not of the fix.

## Why the fix is unsound

The obvious routing — let a `Cell` take `dropStructField` outright, "exactly as
an array already does, because a cell IS a one-element array box" — frees the
box at the cell's own rc == 1.

The array arm can do that because of a premise stated in its own comment: a
struct-field READ leaves the buffer at rc >= 2, which forces `cow_inplace`'s
copy branch. So for an array, rc == 1 at the replacement genuinely means "no
alias".

**That premise does not hold for a `Cell`.** `emitCellGet` loads slot 0 and
retains only the ELEMENT, and only when the element is a string; it never incs
the cell box's own rc. A cell field can therefore sit at rc == 1 while a live
alias still holds its data pointer, and freeing it there is a use-after-free.

`emitFieldDropOnStack`'s contract says as much directly: a value with a live
alias "is only dec'd, never freed".

## What it broke, and how to see it

`TestSelfHostAuditStdArrayX86_64` — `sum`, `product`, `sorted_asc`,
`sorted_desc` — on a clean tree, bisected to the commit:

| commit | gate |
| --- | --- |
| `eb44d80` | green |
| `10f0379` | green |
| `6e0c2bd` | **red** |

```
FERN_STRICT_IR: num__sum     (body lowered, but references unknown function value "T")
FERN_STRICT_IR: num__product (body lowered, but references unknown function value "T")
```

`T.zero()` / `T.one()` never receive `T := i32`: the compiled driver loses a
monomorphisation. The same self-host sources on the same input are GREEN under
the interpreter (`TestSelfHostInterpStdlibModload`), which is what places the
defect in native codegen rather than in the self-host's own logic — the driver
is compiled by `internal/ir` and then misbehaves at runtime.

A direct alias does not reproduce it (`var c: Cell[string] = a.1;` before a
second container retains the box, so the free is declined, and x86-64 matches
the interpreter oracle). The self-host shape that does is not yet reduced.

## What has to be true before trying again

The cause is the missing retain on a cell-field READ, not the drop. Either give
a `Cell` field read the same rc >= 2 invariant the array arm relies on — at
which point freeing at rc == 1 becomes sound — or reclaim the box through a
path that does not free while an alias can exist. Reverting to the flat
`__fern_rc_dec` trades the use-after-free back for the leak, which is where
this stands.

Whatever the next attempt is, run `internal/e2eselfhost` on it. The gates the
reverted commit ran — `internal/ir`, the conformance corpus and its leak
census, the x86-64/arm64 rc leak gates, the native IR verifiers, and
`TestSelfHostStage2FixpointArm64` — were all green while the self-host driver
was being miscompiled. The fixpoint is self-referential and structurally blind
to a stable miscompile; `docs/TEST-GATES.md` says so, and this is the case in
point.
