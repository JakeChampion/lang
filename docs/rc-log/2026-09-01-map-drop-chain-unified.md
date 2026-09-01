# One map-drop chain for every site (#7914 item 2, first slice)

Three sites reclaimed a Map reached through a container, and each had
its own copy of the chain:

| site | chain it emitted |
|---|---|
| bare local exit sweep (`emitMapSlotDrop`) | full dispatch: value column by kind, string keys, buf + handle |
| struct field (`dropStructField`) | `__map_drop_values` + `__fern_map_drop` only |
| generated struct/tuple drops (`appendChildDrop`) | the same partial pair |
| closure capture drop | the same partial pair, a third copy |

The measurement that found it: `Map[string, i32]` built into a struct
field strands every key (20/12, 512 B on one round of 8 inserts) while
the identical map as a bare local balances 19/19. The key column was
never walked from any container site; a string-valued or struct-valued
map lost its value column from them too.

`appendMapDropChain` is now the single dispatch — struct/enum values via
the generated `__drop_map_via_<perValueDrop>`, string values via
`__drop_map_str_values`, array values via the generic
`__map_drop_values`, then `__drop_map_str_keys` for a string key column,
then `__fern_map_drop` — and all four sites route through it, so the
coverage cannot drift again. Each helper self-guards on the map's own
rc==1 and returns the handle, so the calls chain on the operand stack
and a shared map only dec's, whichever owner drops last.

## Measured

x86-64, answers equal to interp everywhere, zero rc underflows: field
scalar values 20/12 live 512 → 20/20 live 0; `Map[string, string]`
37/37; struct values (`__drop_map_via___drop_struct_Pt` from a field
drop) 40/40; map still read through the live local after the struct
binds it, two holders of one map, field read back out — all balance.
Three corpus cases pin the shapes at zero in the leak gate; on the
parent commit each fails at 1,536 B. The census, certifier, existing
corpus and leak-gate pins are unchanged — no existing fixture covered
the container-held map, which is why the class survived.

## What this does NOT move: the driver

The self-host driver's retained bytes are identical before and after
(478,064 on the 4-loop probe): its pipeline roots never reach an
eligible deep drop at all, so completing the drop changes nothing until
the ROOT release exists. And the obvious upstream suspect is now
FALSIFIED by experiment: patching `consumedDropWired` to accept every
Map (so Map-carrying params can promote to callee-owned) rebuilds the
driver to the same 478,064 — the promotion gate is not where the roots
die. That leaves `computeFreeEligible`'s taint on the LOCALS themselves,
the issue's original suspicion: `run()`'s roots are passed to callees
whose parameters are not (all) counted, and the blanket taint strands
them before any drop-completeness question arises. The next slice is the
issue's own directive — take one root, find the exact tainting use, and
measure the tree that one release frees.

## A trap, re-learned

`git checkout HEAD -- .` in the worktree, meant to undo a two-file
non-vacuity revert, discarded every uncommitted edit — the fix and the
new tests. Everything was re-applied from context, but the docs' stash
warning generalises: name the files you are reverting, never `.`, and
commit before running a non-vacuity check.
